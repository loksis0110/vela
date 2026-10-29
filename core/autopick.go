package core

import (
	"context"
	"errors"
	"net"
	"net/url"
	"sort"
	"sync"
	"time"

	mDNS "github.com/miekg/dns"
	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/trafficcontrol"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/log"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/observable"
	"github.com/sagernet/sing/service"
)

// The automatic group of sing-box ("urltest") picks its server only after
// it has checked all of its candidates: up to 15 s per silent server, ten
// at a time, so 30-45 s when the best servers of the last check have since
// been blocked. Until then every connection goes to the first candidate,
// working or not. And when the chosen server stops accepting connections
// the group stays on it until its next check, minutes later.
//
// The "vela-autopick" helper fixes both from inside the core, on every
// platform (it is registered like the Xray outbound, so the mobile
// libraries have it too): right after start it tries the first candidates
// in order, a few at once and more every moment while none has answered,
// and hands the group the first one that works; later, when the group's
// server fails a connection or a check, it does the same among the others
// at once. It carries no traffic itself.

// TypeAutoPick is the type of the helper outbound, TagAutoPick its tag.
const (
	TypeAutoPick = "vela-autopick"
	TagAutoPick  = "auto-pick"
)

type autoPickOptions struct {
	Group    string `json:"group"`
	Selector string `json:"selector,omitempty"`
	URL      string `json:"url"`
	// WarmDNS is a DNS server reached through the tunnel (DoH): its
	// connection is opened as soon as a server is picked, so the first
	// page doesn't wait for it.
	WarmDNS string `json:"warm_dns,omitempty"`
}

// Tuning of the race between candidates.
var (
	pickTimeout     = 5 * time.Second        // one candidate
	pickStagger     = 250 * time.Millisecond // more candidates while none answered
	pickFirst       = 4                      // tried at once
	pickStep        = 3                      // added every stagger
	pickMaxInFlight = 16                     // memory of the iOS extension
	repickDebounce  = 300 * time.Millisecond
	repickBackoff   = 5 * time.Second
	repickBackoffTo = time.Minute
)

func init() {
	include.ExtraOutbounds = append(include.ExtraOutbounds, func(registry *outbound.Registry) {
		outbound.Register[autoPickOptions](registry, TypeAutoPick, newAutoPick)
	})
}

// urlTestGroup is what the helper needs from sing-box's urltest group.
type urlTestGroup interface {
	adapter.OutboundGroup
	PerformUpdateCheck()
}

type pickResult struct {
	tag string
	err error
}

type autoPick struct {
	outbound.Adapter
	ctx     context.Context
	cancel  context.CancelFunc
	logger  log.ContextLogger
	manager adapter.OutboundManager
	history *urltest.HistoryStorage
	options autoPickOptions
	group   urlTestGroup
	sel     adapter.OutboundGroup
	events  *observable.Subscriber[struct{}]

	raceMu sync.Mutex // one race at a time

	mu        sync.Mutex
	first     pickResult
	firstDone chan struct{}
	backoff   time.Duration
	nextTry   time.Time
}

func newAutoPick(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options autoPickOptions) (adapter.Outbound, error) {
	if options.Group == "" || options.URL == "" {
		return nil, E.New("autopick: missing group or url")
	}
	deps := []string{options.Group}
	if options.Selector != "" {
		deps = append(deps, options.Selector)
	}
	pctx, cancel := context.WithCancel(ctx)
	return &autoPick{
		Adapter:   outbound.NewAdapter(TypeAutoPick, tag, nil, deps),
		ctx:       pctx,
		cancel:    cancel,
		logger:    logger,
		manager:   service.FromContext[adapter.OutboundManager](ctx),
		history:   service.PtrFromContext[urltest.HistoryStorage](ctx),
		options:   options,
		firstDone: make(chan struct{}),
	}, nil
}

func (p *autoPick) Start(stage adapter.StartStage) error {
	switch stage {
	case adapter.StartStateStart:
		ob, ok := p.manager.Outbound(p.options.Group)
		if !ok {
			return E.New("autopick: group not found: ", p.options.Group)
		}
		g, ok := ob.(urlTestGroup)
		if !ok {
			return E.New("autopick: not an automatic group: ", p.options.Group)
		}
		p.group = g
		if p.options.Selector != "" {
			if ob, ok := p.manager.Outbound(p.options.Selector); ok {
				p.sel, _ = ob.(adapter.OutboundGroup)
			}
		}
	case adapter.StartStateStarted:
		if p.group == nil || p.history == nil {
			close(p.firstDone)
			return nil
		}
		p.events = observable.NewSubscriber[struct{}](1)
		p.history.AddUpdateHook(p.events)
		go p.loop()
	}
	return nil
}

// InterfaceUpdated: the network changed (Wi-Fi, cable, wake from sleep).
// Failures seen before say nothing about the new network: the next failure
// gets a new race at once instead of waiting out the back-off.
func (p *autoPick) InterfaceUpdated(ctx context.Context) {
	p.mu.Lock()
	p.backoff, p.nextTry = 0, time.Time{}
	p.mu.Unlock()
	if p.events != nil {
		p.events.Emit(struct{}{})
	}
}

func (p *autoPick) Close() error {
	p.cancel()
	if p.events != nil {
		p.events.Close()
	}
	return nil
}

func (p *autoPick) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return nil, E.New("autopick carries no traffic")
}

func (p *autoPick) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, E.New("autopick carries no traffic")
}

// inUse reports whether traffic goes through the automatic group now.
func (p *autoPick) inUse() bool {
	return p.sel == nil || p.sel.Now() == p.group.Tag()
}

func (p *autoPick) loop() {
	tag, err := "", error(nil)
	if p.inUse() {
		tag, err = p.race(p.ctx, false)
	}
	p.mu.Lock()
	p.first = pickResult{tag, err}
	p.mu.Unlock()
	close(p.firstDone)
	if err == nil && tag != "" {
		go p.dropStuck(tag)
	}
	if err == nil && p.options.WarmDNS != "" {
		// also with a server chosen by hand
		go p.warmDNS()
	}
	sub, done := p.events.Subscription()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-done:
			return
		case <-sub:
		}
		// a burst of results comes in at once (the group's own checks)
		select {
		case <-p.ctx.Done():
			return
		case <-time.After(repickDebounce):
		}
		for len(sub) > 0 {
			<-sub
		}
		if p.needsRepick() {
			p.race(p.ctx, true)
		}
	}
}

// stuckGrace: how long (from its start) a connection opened before the
// first pick may stay without a single byte from the server.
var stuckGrace = 1500 * time.Millisecond

// dropStuck closes connections opened before the group had a server (they
// went to its first candidate, which may be the silent one) that still got
// nothing back a moment after the pick. Apps then retry, through the
// server that answered, instead of waiting on a dead one for minutes.
func (p *autoPick) dropStuck(picked string) {
	m := service.PtrFromContext[trafficcontrol.Manager](p.ctx)
	if m == nil {
		return
	}
	// without results the group sends everything to its first candidate:
	// when that one won, the early connections are on a working server
	if all := p.group.All(); len(all) > 0 && all[0] == picked {
		return
	}
	var early []*trafficcontrol.TrackerMetadata
	for _, c := range m.Connections() {
		// the chain stops at the group when it had no server yet
		if c.Outbound == p.group.Tag() {
			early = append(early, c)
		}
	}
	// each one gets stuckGrace from its start, the oldest are closed first
	sort.Slice(early, func(i, j int) bool { return early[i].CreatedAt.Before(early[j].CreatedAt) })
	closed := 0
	for len(early) > 0 {
		if wait := time.Until(early[0].CreatedAt.Add(stuckGrace)); wait > 0 {
			select {
			case <-p.ctx.Done():
				return
			case <-time.After(wait):
			}
		}
		now := time.Now()
		for len(early) > 0 && now.Sub(early[0].CreatedAt) >= stuckGrace {
			c := early[0]
			early = early[1:]
			if c.Download != nil && c.Download.Load() > 0 {
				continue
			}
			if t := m.Connection(c.ID); t != nil {
				t.Close()
				closed++
			}
		}
	}
	if closed > 0 {
		p.logger.Info("closed ", closed, " connections that waited on a server before the pick")
	}
}

// warmDNS sends one query to the tunnel's DNS server; the answer is cached
// and the (DoH) connection stays open for the queries of the first page.
func (p *autoPick) warmDNS() {
	transports := service.FromContext[adapter.DNSTransportManager](p.ctx)
	router := service.FromContext[adapter.DNSRouter](p.ctx)
	if transports == nil || router == nil {
		return
	}
	transport, ok := transports.Transport(p.options.WarmDNS)
	if !ok {
		return
	}
	host := "www.gstatic.com"
	if u, err := url.Parse(p.options.URL); err == nil && u.Hostname() != "" && net.ParseIP(u.Hostname()) == nil {
		host = u.Hostname()
	}
	ctx, cancel := context.WithTimeout(p.ctx, 8*time.Second)
	defer cancel()
	msg := new(mDNS.Msg)
	msg.SetQuestion(mDNS.Fqdn(host), mDNS.TypeA)
	start := time.Now()
	if _, err := router.Exchange(ctx, msg, adapter.DNSQueryOptions{Transport: transport}); err != nil {
		p.logger.Debug("dns warm-up: ", err)
		return
	}
	p.logger.Debug("dns warm-up: ", time.Since(start).Round(time.Millisecond))
}

// needsRepick: the group's server failed a connection or a check (sing-box
// then forgets its result) and nothing replaced it, or the group still has
// no server at all (the first race found none: the network was down).
func (p *autoPick) needsRepick() bool {
	if !p.inUse() {
		return false
	}
	if now := p.group.Now(); now != "" && p.history.LoadURLTestHistory(now) != nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return !time.Now().Before(p.nextTry)
}

// candidates: servers with a result first (fastest first), then the rest
// in the group's order (best of the last check first); the server that
// just failed goes last.
func (p *autoPick) candidates(failed string) []adapter.Outbound {
	var known, rest []adapter.Outbound
	var last adapter.Outbound
	delays := map[string]uint16{}
	for _, tag := range p.group.All() {
		ob, ok := p.manager.Outbound(tag)
		if !ok {
			continue
		}
		if tag == failed {
			last = ob
			continue
		}
		if h := p.history.LoadURLTestHistory(tag); h != nil {
			delays[tag] = h.Delay
			known = append(known, ob)
		} else {
			rest = append(rest, ob)
		}
	}
	sort.SliceStable(known, func(i, j int) bool { return delays[known[i].Tag()] < delays[known[j].Tag()] })
	out := append(known, rest...)
	if last != nil {
		out = append(out, last)
	}
	return out
}

// race tries the candidates and hands the first one that answers to the
// group. repick: the group's current server is known to fail.
func (p *autoPick) race(ctx context.Context, repick bool) (string, error) {
	p.raceMu.Lock()
	defer p.raceMu.Unlock()
	failed := ""
	if repick {
		failed = p.group.Now()
		if failed != "" && p.history.LoadURLTestHistory(failed) != nil {
			// answered again, or replaced while this call waited for
			// another race
			return failed, nil
		}
	}
	cands := p.candidates(failed)
	if len(cands) == 0 {
		return "", E.New("autopick: no servers")
	}
	start := time.Now()
	tag, delay, err := raceOutbounds(ctx, cands, p.options.URL, p.history)
	p.mu.Lock()
	if err != nil {
		if p.backoff == 0 {
			p.backoff = repickBackoff
		} else if p.backoff *= 2; p.backoff > repickBackoffTo {
			p.backoff = repickBackoffTo
		}
		p.nextTry = time.Now().Add(p.backoff)
	} else {
		p.backoff, p.nextTry = 0, time.Time{}
	}
	p.mu.Unlock()
	if err != nil {
		if ctx.Err() == nil {
			p.logger.Warn("no server answered (", len(cands), " tried): ", err)
		}
		return "", err
	}
	p.group.PerformUpdateCheck()
	p.logger.Info("picked ", tag, " (", delay, " ms) in ", time.Since(start).Round(time.Millisecond))
	return p.group.Now(), nil
}

// raceOutbounds requests link through the candidates in order: pickFirst at
// once, pickStep more every pickStagger (or as soon as all started ones have
// failed). The first answer wins and is stored in history; failed
// candidates lose their stored result, and so do the ones still on their
// way when the winner answered (unless the group's own check stored a new
// one meanwhile): an older, better result of a server that is slower now (or
// silent) would otherwise make the group prefer it to the server that just
// answered.
func raceOutbounds(ctx context.Context, cands []adapter.Outbound, link string, history *urltest.HistoryStorage) (string, uint16, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		tag   string
		delay uint16
		err   error
	}
	results := make(chan result, len(cands))
	next, received := 0, 0
	pending := map[string]bool{}
	start := time.Now()
	launch := func(k int) {
		for ; k > 0 && next < len(cands) && next-received < pickMaxInFlight; k-- {
			ob := cands[next]
			next++
			pending[ob.Tag()] = true
			go func() {
				tctx, done := context.WithTimeout(ctx, pickTimeout)
				delay, err := urltest.URLTest(tctx, link, ob)
				done()
				results <- result{ob.Tag(), delay, err}
			}()
		}
	}
	launch(pickFirst)
	ticker := time.NewTicker(pickStagger)
	defer ticker.Stop()
	var firstErr error
	for received < len(cands) {
		select {
		case <-ctx.Done():
			return "", 0, ctx.Err()
		case <-ticker.C:
			launch(pickStep)
		case r := <-results:
			received++
			delete(pending, r.tag)
			if r.err == nil {
				if r.delay == 0 {
					r.delay = 1 // 0 means "no result" to the group
				}
				for tag := range pending {
					// a result stored meanwhile (the group's own check) is fresh
					if h := history.LoadURLTestHistory(tag); h != nil && h.Time.Before(start) {
						history.DeleteURLTestHistory(tag)
					}
				}
				history.StoreURLTestHistory(r.tag, &adapter.URLTestHistory{Time: time.Now(), Delay: r.delay})
				return r.tag, r.delay, nil
			}
			if ctx.Err() != nil {
				return "", 0, ctx.Err()
			}
			history.DeleteURLTestHistory(r.tag)
			if firstErr == nil {
				firstErr = r.err
			}
			if received == next {
				launch(pickStep)
			}
		}
	}
	if firstErr == nil {
		firstErr = errors.New("no servers")
	}
	return "", 0, firstErr
}

func findAutoPick(instance *box.Box) *autoPick {
	if instance == nil {
		return nil
	}
	ob, ok := instance.Outbound().Outbound(TagAutoPick)
	if !ok {
		return nil
	}
	p, _ := ob.(*autoPick)
	return p
}

// WaitAutoPick waits until the automatic group has its first server after
// the core started and returns its tag: "" when the group is not in use
// (a server was chosen by hand) or the config has no helper, an error when
// none of the servers answered.
func WaitAutoPick(ctx context.Context, instance *box.Box) (string, error) {
	p := findAutoPick(instance)
	if p == nil {
		return "", nil
	}
	select {
	case <-p.firstDone:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.first.tag, p.first.err
}

// RepickAuto moves the automatic group to a working server now, when its
// server stopped answering (found by the app's own check): the others are
// tried in order of their last results and the first that answers is used.
// It returns the tag of the server in use.
func RepickAuto(ctx context.Context, instance *box.Box) (string, error) {
	p := findAutoPick(instance)
	if p == nil {
		if instance == nil {
			return "", errors.New("not running")
		}
		ob, ok := instance.Outbound().Outbound(TagAuto)
		if !ok {
			return "", errors.New("no automatic group")
		}
		g, ok := ob.(adapter.URLTestGroup)
		if !ok {
			return "", errors.New("no automatic group")
		}
		_, err := g.URLTest(ctx)
		if og, ok := ob.(adapter.OutboundGroup); ok {
			return og.Now(), err
		}
		return "", err
	}
	if p.group == nil {
		return "", errors.New("not started")
	}
	if now := p.group.Now(); now != "" {
		p.history.DeleteURLTestHistory(now)
	}
	return p.race(ctx, true)
}

var (
	_ N.Dialer                        = (*autoPick)(nil)
	_ adapter.Lifecycle               = (*autoPick)(nil)
	_ adapter.InterfaceUpdateListener = (*autoPick)(nil)
)
