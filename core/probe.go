package core

import (
	"container/heap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/velaapp/vela/xrayout"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/experimental/deprecated"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	sjson "github.com/sagernet/sing/common/json"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

// TestURL is fetched through each server to measure real latency.
var TestURL = func() string {
	if u := os.Getenv("VELA_TEST_URL"); u != "" {
		return u
	}
	return "https://www.gstatic.com/generate_204"
}()

// ProbeResult is the outcome of a real connection test through one server.
type ProbeResult struct {
	ID    string `json:"id"`
	Delay int    `json:"delay"` // ms, 0 when failed
	Error string `json:"error,omitempty"`
	// Human readable reason in the language set by SetLanguage.
	Reason string `json:"reason,omitempty"`
}

// StartBox creates and starts a sing-box instance from a JSON config.
func StartBox(config string, logPath string) (*box.Box, context.CancelFunc, error) {
	ctx := include.Context(service.ContextWith(context.Background(), deprecated.NewStderrManager(log.StdLogger())))
	opts, err := sjson.UnmarshalExtendedContext[option.Options](ctx, []byte(config))
	if err != nil {
		return nil, nil, err
	}
	if logPath != "" {
		if opts.Log == nil {
			opts.Log = &option.LogOptions{}
		}
		opts.Log.Output = logPath
		opts.Log.DisableColor = true
	}
	ctx, cancel := context.WithCancel(service.ExtendContext(ctx))
	instance, err := box.New(box.Options{Context: ctx, Options: opts})
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if err = instance.Start(); err != nil {
		instance.Close()
		cancel()
		return nil, nil, err
	}
	return instance, cancel, nil
}

// ProbeNodes makes a real request through every node and reports each result
// through onResult as soon as it is known; onResult may be called from
// several goroutines at once, never after ProbeNodes returns. When ctx is
// cancelled the servers not checked yet get no result. When instance is nil,
// a temporary core without any inbounds is started, so this works while
// disconnected; servers the running instance doesn't have (added after it
// started, left out by the settings) are checked with a temporary core too.
func ProbeNodes(ctx context.Context, instance *box.Box, nodes []Node, timeout time.Duration, parallel int, onResult func(ProbeResult)) error {
	if len(nodes) == 0 {
		return nil
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if parallel <= 0 {
		parallel = 8
	}
	// links are read once more, so parser fixes apply to saved servers
	fresh := make([]Node, len(nodes))
	for i, n := range nodes {
		if n.Link != "" {
			if f, err := ParseLink(n.Link); err == nil {
				n.Outbound = f.Outbound
				if f.Server != "" && f.Port > 0 {
					n.Server, n.Port = f.Server, f.Port
				}
			}
		}
		fresh[i] = n
	}
	if instance == nil {
		return probeTemporary(ctx, fresh, timeout, parallel, onResult)
	}
	var here, missing []Node
	for _, n := range fresh {
		if _, ok := instance.Outbound().Outbound(NodeTag(n)); ok || n.Outbound == nil {
			here = append(here, n)
		} else {
			missing = append(missing, n)
		}
	}
	if len(missing) == 0 {
		return probeScheduled(ctx, instance, here, nil, timeout, parallel, onResult)
	}
	var wg sync.WaitGroup
	var tempErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		tempErr = probeTemporary(ctx, missing, timeout, parallel, onResult)
	}()
	var err error
	if len(here) > 0 {
		err = probeScheduled(ctx, instance, here, nil, timeout, parallel, onResult)
	}
	wg.Wait()
	if err == nil {
		err = tempErr
	}
	return err
}

// probeTemporary checks nodes with a core started just for that.
func probeTemporary(ctx context.Context, nodes []Node, timeout time.Duration, parallel int, onResult func(ProbeResult)) error {
	// servers the core refused to create: reported without a check
	rejected := map[string]error{}
	var outbounds []map[string]any
	seen := map[string]bool{}
	for _, n := range nodes {
		tag := NodeTag(n)
		if n.Outbound == nil || seen[tag] {
			continue // duplicate tags would fail the whole core
		}
		seen[tag] = true
		ob := map[string]any{}
		for k, v := range n.Outbound {
			ob[k] = v
		}
		ob["tag"] = tag
		outbounds = append(outbounds, ob)
	}
	probe, cancel, err := startProbeBox(outbounds)
	if err != nil {
		// one broken server must not fail all checks: find the ones the
		// core rejects by halving the list, report them, check the rest
		var good []map[string]any
		var split func(list []map[string]any)
		split = func(list []map[string]any) {
			if len(list) == 0 || ctx.Err() != nil {
				return
			}
			part, partCancel, partErr := startProbeBox(list)
			if partErr == nil {
				part.Close()
				partCancel()
				good = append(good, list...)
				return
			}
			if len(list) == 1 {
				rejected[list[0]["tag"].(string)] = partErr
				return
			}
			split(list[:len(list)/2])
			split(list[len(list)/2:])
		}
		split(outbounds)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		probe, cancel, err = startProbeBox(good)
		if err != nil {
			return err
		}
	}
	defer func() {
		probe.Close()
		cancel()
	}()
	return probeScheduled(ctx, probe, nodes, rejected, timeout, parallel, onResult)
}

// Probe scheduling. Checking hundreds of servers used to take many minutes:
// a dead host with 100 entries cost 100 timeouts, and every failure was
// retried slowly. Now:
//  1. identical servers are checked once;
//  2. every TCP endpoint (host:port) gets a quick connect check, so closed
//     or unreachable servers fail in about three seconds (a lost SYN gets a
//     second connect attempt, so a working server is not hidden). Real
//     checks of an endpoint start as soon as it answered: the servers that
//     answer first (usually the nearest) are checked first;
//  3. real checks run with a short timeout, a few at a time per backend
//     (host:port plus SNI, Host header and path: behind one CDN address
//     there are many unrelated servers) and per host; after probeGiveUp
//     timeouts of a backend without a single success its other entries
//     (differing only in keys/passwords) are not tested one by one;
//  4. a second attempt with the whole timeout goes to failures on backends
//     that work for other entries (overload) and to one entry of a backend
//     that dropped connections at once (EOF, reset); if that one works the
//     others are checked too. A connected but silent server is not retried:
//     the first attempt already waited for it long enough.
const (
	probePerBackend   = 3
	probePerHost      = 8
	probeGiveUp       = 3
	probeDialTimeout  = 3 * time.Second
	probeRedial       = time.Second
	probeFirstTimeout = 6 * time.Second
	probeDialParallel = 128
)

type probeItem struct {
	node     Node // representative of identical servers
	ids      []string
	endpoint string // host:port for the connect check, "" for UDP-based servers
	group    string // backend: endpoint (or host) plus what selects a server behind it
	result   ProbeResult
	tested   bool // a real check ran (not given up)
	done     bool // final result reported
}

// endpointCheck is the connect check of one host:port.
type endpointCheck struct {
	done chan struct{} // closed when err and rtt are set
	err  string        // "" when the endpoint accepted a connection
	rtt  time.Duration // how long the connect took
}

func probeScheduled(ctx context.Context, instance *box.Box, nodes []Node, rejected map[string]error,
	timeout time.Duration, parallel int, onResult func(ProbeResult)) error {
	// 1. identical servers
	var items []*probeItem
	byKey := map[string]*probeItem{}
	for _, n := range nodes {
		ob := n.Outbound
		key := "id:" + n.ID
		if ob != nil {
			if data, err := json.Marshal(ob); err == nil {
				key = string(data)
			}
		}
		if it := byKey[key]; it != nil {
			it.ids = append(it.ids, n.ID)
			continue
		}
		it := &probeItem{node: n, ids: []string{n.ID}}
		if ob != nil && tcpCheckable(ob) && n.Server != "" && n.Port > 0 {
			it.endpoint = net.JoinHostPort(n.Server, strconv.Itoa(n.Port))
		}
		it.group = firstNonEmpty(it.endpoint, n.Server, n.ID) + "|" + backendSignature(ob)
		byKey[key] = it
		items = append(items, it)
	}
	if len(items) == 0 {
		return nil
	}
	items = interleave(items)
	groupSize := map[string]int{}
	for _, it := range items {
		groupSize[it.group]++
	}
	// a single server is checked once with the whole timeout (a second
	// attempt only after a quick failure such as a dropped connection)
	firstTimeout := min(timeout, probeFirstTimeout)
	if len(items) == 1 {
		firstTimeout = timeout
	}
	// dead servers take the whole timeout: many checks run at once, the
	// limits per backend and per host keep single servers from overload
	active := parallel
	if len(items) > 40 {
		active = max(active, 64)
	}

	var mu sync.Mutex
	finish := func(it *probeItem, r ProbeResult) {
		mu.Lock()
		if it.done {
			mu.Unlock()
			return
		}
		it.done = true
		it.result = r
		mu.Unlock()
		for _, id := range it.ids {
			out := r
			out.ID = id
			onResult(out)
		}
	}
	stopped := func() bool { return ctx.Err() != nil }
	deadResult := func(msg string) ProbeResult {
		reason := ExplainError(msg)
		if strings.HasPrefix(msg, "connect timeout") {
			reason = T("сервер недоступен из этой сети")
		}
		return ProbeResult{Error: msg, Reason: reason}
	}

	// 2. connect checks, in the background: each endpoint's servers are
	// checked as soon as it answered
	checks := map[string]*endpointCheck{}
	var endpoints []string
	reachDone := make(chan struct{})
	reachable := true
	var bg sync.WaitGroup
	defer bg.Wait() // nothing keeps running after the return
	if _, ok := instance.Outbound().Outbound(TagDirect); ok {
		for _, it := range items {
			if it.endpoint != "" && checks[it.endpoint] == nil {
				checks[it.endpoint] = &endpointCheck{done: make(chan struct{})}
				endpoints = append(endpoints, it.endpoint)
			}
		}
	}
	if len(endpoints) > 0 {
		direct, _ := instance.Outbound().Outbound(TagDirect)
		// meanwhile: is there internet at all? (dead servers are reported
		// once this is known)
		bg.Add(1)
		go func() {
			defer bg.Done()
			reachable = dialerReachable(ctx, direct)
			close(reachDone)
		}()
		bg.Add(1)
		go func() {
			defer bg.Done()
			sem := make(chan struct{}, probeDialParallel)
			var wg sync.WaitGroup
			for _, ep := range endpoints {
				c := checks[ep]
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					c.err = "context canceled"
					close(c.done)
					continue
				}
				wg.Add(1)
				go func(ep string, c *endpointCheck) {
					defer wg.Done()
					defer func() { <-sem }()
					host, portStr, _ := net.SplitHostPort(ep)
					port, _ := strconv.Atoi(portStr)
					// a name that didn't resolve (maybe a resolver hit by
					// the burst of lookups) is left to the real check
					start := time.Now()
					if err := dialCheck(ctx, direct, M.ParseSocksaddrHostPort(host, uint16(port))); err != nil && !dnsError(err.Error()) {
						c.err = err.Error()
					}
					c.rtt = time.Since(start)
					close(c.done)
				}(ep, c)
			}
			wg.Wait()
		}()
	} else {
		close(reachDone)
	}

	// per-backend and per-host limits, per-backend statistics
	gates := map[string]chan struct{}{}
	type groupStat struct{ timeouts, ok, inflight int }
	stats := map[string]*groupStat{}
	gate := func(key string, size int) chan struct{} {
		mu.Lock()
		defer mu.Unlock()
		k := fmt.Sprintf("%s#%d", key, size)
		g := gates[k]
		if g == nil {
			g = make(chan struct{}, size)
			gates[k] = g
		}
		return g
	}
	stat := func(group string) *groupStat {
		st := stats[group]
		if st == nil {
			st = &groupStat{}
			stats[group] = st
		}
		return st
	}

	test := func(it *probeItem, limit time.Duration) (res ProbeResult) {
		defer func() {
			// a panic in a protocol implementation must not take the app down
			if r := recover(); r != nil {
				res = ProbeResult{Error: fmt.Sprint("internal error: ", r), Reason: T("ошибка соединения")}
			}
		}()
		if err := rejected[NodeTag(it.node)]; err != nil {
			res.Error = err.Error()
			res.Reason = T("сервер не удалось разобрать")
			return res
		}
		outbound, ok := instance.Outbound().Outbound(NodeTag(it.node))
		if !ok {
			res.Error = "outbound not found"
			res.Reason = T("сервер не удалось разобрать")
			return res
		}
		tctx, cancel := context.WithTimeout(ctx, limit)
		delay, err := urltest.URLTest(tctx, TestURL, boundedDialer{outbound})
		timedOut := errors.Is(tctx.Err(), context.DeadlineExceeded)
		cancel()
		if err != nil {
			if timedOut {
				err = fmt.Errorf("timeout after %s: %w", limit, err)
			}
			res.Error = err.Error()
			res.Reason = ExplainError(res.Error)
			return res
		}
		res.Delay = max(int(delay), 1)
		return res
	}

	// run checks items, at most active at a time; before (optional) runs
	// first and may settle an item; decide is called with each result and
	// may report it. Items waiting for their turn don't take a slot.
	run := func(list []*probeItem, active, perBackend, perHost int, limit time.Duration, giveUp bool,
		before func(*probeItem) bool, decide func(*probeItem, ProbeResult)) {
		// servers that answered the connect check sooner go first; UDP-based
		// ones (no connect check) have slots of their own, so a list of
		// silent ones can't hold back the others
		tcpSlots := newSlotPool(active)
		udpSlots := newSlotPool(active)
		spawn := make(chan struct{}, 4096) // bounds the waiting goroutines
		var wg sync.WaitGroup
		for _, it := range list {
			if stopped() {
				break
			}
			spawn <- struct{}{}
			wg.Add(1)
			go func(it *probeItem) {
				defer wg.Done()
				defer func() { <-spawn }()
				if before != nil && !before(it) {
					return
				}
				// always in this order (backend, host, slot): no deadlock
				for _, g := range []chan struct{}{gate("b:"+it.group, perBackend), gate("h:"+it.node.Server, perHost)} {
					select {
					case g <- struct{}{}:
						defer func(g chan struct{}) { <-g }(g)
					case <-ctx.Done():
						return
					}
				}
				counted := false
				for giveUp {
					mu.Lock()
					st := stat(it.group)
					if st.ok == 0 && st.timeouts >= probeGiveUp {
						mu.Unlock()
						return // not tested: decided after the second pass
					}
					// a backend that timed out and never answered: wait for
					// the checks under way before starting another one
					if st.ok > 0 || st.timeouts == 0 || st.inflight == 0 {
						st.inflight++
						counted = true
						mu.Unlock()
						break
					}
					mu.Unlock()
					select {
					case <-time.After(50 * time.Millisecond):
					case <-ctx.Done():
						return
					}
				}
				release := func() {
					if counted {
						mu.Lock()
						stat(it.group).inflight--
						mu.Unlock()
					}
				}
				pool, prio := udpSlots, time.Duration(0)
				if c := checks[it.endpoint]; c != nil {
					pool = tcpSlots
					select {
					case <-c.done:
						prio = c.rtt
					default:
					}
				} else if it.endpoint != "" {
					pool = tcpSlots
				}
				if !pool.acquire(ctx, prio) {
					release()
					return
				}
				r := test(it, limit)
				pool.release()
				mu.Lock()
				if counted {
					stat(it.group).inflight--
				}
				if stopped() && r.Delay == 0 {
					mu.Unlock()
					return // stopped by the user: not a server failure
				}
				it.tested = true
				it.result = r
				st := stat(it.group)
				if r.Delay > 0 {
					st.ok++
				} else if transientError(r.Error) {
					st.timeouts++
				}
				mu.Unlock()
				decide(it, r)
			}(it)
		}
		wg.Wait()
	}

	// 3. first pass
	waitEndpoint := func(it *probeItem) bool {
		c := checks[it.endpoint]
		if c == nil {
			return true
		}
		select {
		case <-c.done:
		case <-ctx.Done():
			return false
		}
		if c.err == "" {
			return true
		}
		select {
		case <-reachDone:
		case <-ctx.Done():
			return false
		}
		if reachable && !stopped() {
			finish(it, deadResult(c.err))
		}
		return false // without internet: decided after this pass
	}
	run(items, active, probePerBackend, probePerHost, firstTimeout, true, waitEndpoint, func(it *probeItem, r ProbeResult) {
		// a silent server alone on its backend gets no second attempt
		// (step 4): its result is final right away
		if r.Delay > 0 || !transientError(r.Error) || groupSize[it.group] == 1 && timeoutError(r.Error) {
			finish(it, r)
		}
	})
	bg.Wait()
	if stopped() {
		return ctx.Err()
	}
	if !reachable {
		// the well-known addresses didn't answer either: when no server
		// accepted a connection, there is no internet at all
		dead := 0
		for _, ep := range endpoints {
			if checks[ep].err != "" {
				dead++
			}
		}
		offline := len(endpoints) >= 2 && dead == len(endpoints)
		for _, it := range items {
			if c := checks[it.endpoint]; c != nil && c.err != "" {
				if offline {
					finish(it, ProbeResult{Error: "network is unreachable", Reason: T("нет подключения к интернету")})
				} else {
					finish(it, deadResult(c.err))
				}
			}
		}
	}

	// 4. second pass: overload-looking failures
	mu.Lock()
	byGroup := map[string][]*probeItem{}
	var groups []string // in list order
	for _, it := range items {
		if !it.done {
			if byGroup[it.group] == nil {
				groups = append(groups, it.group)
			}
			byGroup[it.group] = append(byGroup[it.group], it)
		}
	}
	var retry, later []*probeItem
	for _, group := range groups {
		list := byGroup[group]
		if st := stats[group]; st != nil && st.ok > 0 {
			retry = append(retry, list...) // the backend works: these were unlucky
			continue
		}
		// a backend that never answered: running into the whole limit is
		// final (the connect worked, the server is silent: blocked or dead),
		// a quick drop (EOF, reset) gets one more try for one entry
		rep := -1
		for i, it := range list {
			if it.tested && !timeoutError(it.result.Error) {
				rep = i
				break
			}
		}
		for i, it := range list {
			if i == rep {
				retry = append(retry, it)
			} else {
				later = append(later, it)
			}
		}
	}
	mu.Unlock()
	run(retry, 24, probePerBackend, probePerHost/2, timeout, false, nil, func(it *probeItem, r ProbeResult) {
		finish(it, r)
	})
	if stopped() {
		return ctx.Err()
	}

	// 5. entries of backends that answered the second attempt
	var third []*probeItem
	mu.Lock()
	for _, it := range later {
		if st := stats[it.group]; st != nil && st.ok > 0 {
			third = append(third, it)
		}
	}
	mu.Unlock()
	run(third, 24, probePerBackend, probePerHost/2, firstTimeout, false, nil, func(it *probeItem, r ProbeResult) {
		finish(it, r)
	})
	if stopped() {
		return ctx.Err()
	}
	// everything else on silent backends fails like its tested neighbours
	mu.Lock()
	failed := map[string]ProbeResult{}
	for _, it := range items {
		if it.done || it.tested {
			if it.result.Delay == 0 && it.result.Error != "" {
				failed[it.group] = it.result
			}
		}
	}
	var rest []*probeItem
	for _, it := range items {
		if !it.done {
			rest = append(rest, it)
		}
	}
	mu.Unlock()
	for _, it := range rest {
		r := it.result
		if !it.tested || r.Error == "" {
			r = failed[it.group]
		}
		if r.Error == "" {
			r = ProbeResult{Error: "timeout", Reason: ExplainError("timeout")}
		}
		finish(it, r)
	}
	return ctx.Err()
}

// slotPool hands out a fixed number of slots, to the waiter with the lowest
// priority value first (in arrival order among equals).
type slotPool struct {
	mu      sync.Mutex
	free    int
	seq     int
	waiters slotWaiters
}

type slotWaiter struct {
	prio    time.Duration
	seq     int
	index   int
	granted bool
	ready   chan struct{}
}

type slotWaiters []*slotWaiter

func (w slotWaiters) Len() int { return len(w) }
func (w slotWaiters) Less(i, j int) bool {
	return w[i].prio < w[j].prio || w[i].prio == w[j].prio && w[i].seq < w[j].seq
}
func (w slotWaiters) Swap(i, j int) {
	w[i], w[j] = w[j], w[i]
	w[i].index, w[j].index = i, j
}
func (w *slotWaiters) Push(x any) {
	sw := x.(*slotWaiter)
	sw.index = len(*w)
	*w = append(*w, sw)
}
func (w *slotWaiters) Pop() any {
	old := *w
	sw := old[len(old)-1]
	*w = old[:len(old)-1]
	return sw
}

func newSlotPool(size int) *slotPool { return &slotPool{free: size} }

// acquire waits for a slot; false when ctx ended first.
func (p *slotPool) acquire(ctx context.Context, prio time.Duration) bool {
	p.mu.Lock()
	if p.free > 0 && len(p.waiters) == 0 {
		p.free--
		p.mu.Unlock()
		return true
	}
	p.seq++
	w := &slotWaiter{prio: prio, seq: p.seq, ready: make(chan struct{})}
	heap.Push(&p.waiters, w)
	p.mu.Unlock()
	select {
	case <-w.ready:
		return true
	case <-ctx.Done():
		p.mu.Lock()
		granted := w.granted
		if !granted {
			heap.Remove(&p.waiters, w.index)
		}
		p.mu.Unlock()
		if granted {
			p.release() // handed over just now: pass it on
		}
		return false
	}
}

func (p *slotPool) release() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.waiters) > 0 {
		w := heap.Pop(&p.waiters).(*slotWaiter)
		w.granted = true
		close(w.ready)
		return
	}
	p.free++
}

// boundedDialer returns from DialContext when ctx ends even if the
// outbound doesn't watch it: a WebSocket, gRPC or TLS handshake with a
// server that accepted the connection and went silent (typical for blocked
// servers) would wait for its own 15-second timeout. The late connection
// is closed.
type boundedDialer struct{ N.Dialer }

func (d boundedDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	done := make(chan result, 1)
	go func() {
		conn, err := d.Dialer.DialContext(ctx, network, destination)
		done <- result{conn, err}
	}()
	select {
	case r := <-done:
		return r.conn, r.err
	case <-ctx.Done():
		go func() {
			if r := <-done; r.conn != nil {
				r.conn.Close()
			}
		}()
		return nil, ctx.Err()
	}
}

// Upstream lets sing-box see the outbound (multiplex settings).
func (d boundedDialer) Upstream() any { return d.Dialer }

// interleave orders items so that consecutive ones belong to different
// backends: lists grouped by server would otherwise keep all workers
// waiting for the limit of one backend.
func interleave(items []*probeItem) []*probeItem {
	byGroup := map[string][]*probeItem{}
	var groups []string
	for _, it := range items {
		if byGroup[it.group] == nil {
			groups = append(groups, it.group)
		}
		byGroup[it.group] = append(byGroup[it.group], it)
	}
	out := make([]*probeItem, 0, len(items))
	for round := 0; len(out) < len(items); round++ {
		for _, g := range groups {
			if list := byGroup[g]; round < len(list) {
				out = append(out, list[round])
			}
		}
	}
	return out
}

// dialCheck opens (and closes) a TCP connection. A second attempt starts
// after probeRedial if the first one hasn't finished: a lost SYN is only
// retransmitted after a second or three, which would look like a dead host.
func dialCheck(ctx context.Context, dialer N.Dialer, dest M.Socksaddr) error {
	dctx, cancel := context.WithTimeout(ctx, probeDialTimeout)
	defer cancel()
	type result struct {
		conn net.Conn
		err  error
	}
	results := make(chan result, 2)
	dial := func() {
		conn, err := dialer.DialContext(dctx, N.NetworkTCP, dest)
		results <- result{conn, err}
	}
	go dial()
	started, got := 1, 0
	timer := time.NewTimer(probeRedial)
	defer timer.Stop()
	defer func() {
		// an attempt still under way ends with dctx; close what it opens
		if pending := started - got; pending > 0 {
			go func() {
				for i := 0; i < pending; i++ {
					if r := <-results; r.conn != nil {
						r.conn.Close()
					}
				}
			}()
		}
	}()
	var firstErr error
	for got < started {
		select {
		case <-timer.C:
			if started == 1 && got == 0 {
				started++
				go dial()
			}
		case r := <-results:
			got++
			if r.err == nil {
				r.conn.Close()
				return nil
			}
			if firstErr == nil {
				firstErr = r.err
			}
			if dctx.Err() == nil {
				if !transientError(r.err.Error()) {
					return r.err // refused, no such host: no second try
				}
				if started == 1 {
					started++ // dropped at once: try again right away
					go dial()
				}
			}
		}
	}
	if errors.Is(dctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("connect timeout: %w", firstErr)
	}
	return firstErr
}

// reachabilityTargets are well-known addresses (no DNS needed) that answer
// TCP connects from almost anywhere.
var reachabilityTargets = []string{"77.88.8.8:53", "1.1.1.1:443", "8.8.8.8:443", "9.9.9.9:443"}

// dialerReachable reports whether any well-known address accepts a TCP
// connection through the dialer.
func dialerReachable(ctx context.Context, dialer N.Dialer) bool {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	ok := make(chan bool, len(reachabilityTargets))
	for _, target := range reachabilityTargets {
		go func(target string) {
			conn, err := dialer.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr(target))
			if err == nil {
				conn.Close()
			}
			ok <- err == nil
		}(target)
	}
	for range reachabilityTargets {
		if <-ok {
			return true
		}
	}
	return false
}

// NetworkReachable reports whether the internet works without the VPN: a
// TCP connect to well-known addresses through the "direct" outbound of the
// running core (instance), or of this process when instance is nil. Apps
// use it to tell a dead server from a dead network.
func NetworkReachable(ctx context.Context, instance *box.Box) bool {
	if instance != nil {
		if direct, ok := instance.Outbound().Outbound(TagDirect); ok {
			return dialerReachable(ctx, direct)
		}
	}
	return dialerReachable(ctx, systemDialer{})
}

type systemDialer struct{}

func (systemDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, destination.String())
}

func (systemDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	var lc net.ListenConfig
	return lc.ListenPacket(ctx, "udp", "")
}

// credentialKeys are settings that differ between users of one server.
var credentialKeys = map[string]bool{
	"uuid": true, "password": true, "id": true, "users": true, "username": true, "user": true,
	"alter_id": true, "alterId": true, "flow": true, "email": true, "level": true, "auth": true, "auth_str": true,
}

// backendSignature describes a server without its credentials: entries
// with the same signature on one address are the same server for different
// users, entries with another SNI, Host header or path may be other servers.
func backendSignature(ob map[string]any) string {
	if ob == nil {
		return ""
	}
	data, err := json.Marshal(withoutCredentials(ob))
	if err != nil {
		return ""
	}
	return string(data)
}

func withoutCredentials(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if !credentialKeys[k] {
				out[k] = withoutCredentials(val)
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = withoutCredentials(val)
		}
		return out
	}
	return v
}

// dnsError reports a failed name lookup.
func dnsError(e string) bool {
	s := strings.ToLower(e)
	return strings.Contains(s, "no such host") || strings.Contains(s, "lookup ") || strings.Contains(s, "dns")
}

// timeoutError reports a check that ran into its time limit.
func timeoutError(e string) bool {
	s := strings.ToLower(e)
	return strings.Contains(s, "timeout") || strings.Contains(s, "deadline exceeded")
}

// transientError tells failures that may pass on a second try (overloaded
// server, dropped connection) from definite ones (wrong key, certificate,
// closed port).
func transientError(e string) bool {
	s := strings.ToLower(e)
	for _, k := range []string{"timeout", "deadline exceeded", "eof", "reset by peer", "closed pipe",
		"broken pipe", "forcibly closed", "connection closed", "use of closed"} {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

// tcpCheckable reports whether a server is reached over TCP directly, so a
// plain connect to its address and port says whether it is up at all.
// Servers reached through another server (detour, Xray dialerProxy) are
// not: their own address may be closed to everyone but the relay.
func tcpCheckable(ob map[string]any) bool {
	typ, _ := ob["type"].(string)
	if d, _ := ob["detour"].(string); d != "" {
		return false
	}
	switch typ {
	case "hysteria", "hysteria2", "tuic", "wireguard":
		return false
	case "naive":
		if q, _ := ob["quic"].(bool); q {
			return false
		}
	case EngineXray:
		outs, _ := ob["outbounds"].([]any)
		if len(outs) == 0 {
			return false
		}
		first, _ := outs[0].(map[string]any)
		if p, _ := first["protocol"].(string); p == "hysteria" || p == "wireguard" {
			return false
		}
		if ps, _ := first["proxySettings"].(map[string]any); ps != nil {
			return false
		}
		ss, _ := first["streamSettings"].(map[string]any)
		if so, _ := ss["sockopt"].(map[string]any); so != nil {
			if d, _ := so["dialerProxy"].(string); d != "" {
				return false
			}
		}
		network, _ := ss["network"].(string)
		if network == "kcp" || network == "mkcp" || network == "quic" || network == "hysteria" {
			return false
		}
		if network == "xhttp" || network == "splithttp" {
			if tls, ok := ss["tlsSettings"].(map[string]any); ok {
				for _, a := range strList(tls["alpn"]) {
					if a == "h3" {
						return false
					}
				}
			}
		}
		return true
	}
	if tr, ok := ob["transport"].(map[string]any); ok && tr["type"] == "quic" {
		return false
	}
	if opts, _ := ob["plugin_opts"].(string); strings.Contains(opts, "mode=quic") {
		return false
	}
	return true
}

// startProbeBox starts a core without inbounds for latency checks.
func startProbeBox(outbounds []map[string]any) (*box.Box, context.CancelFunc, error) {
	list := make([]any, 0, len(outbounds)+1)
	for _, ob := range outbounds {
		list = append(list, ob)
	}
	list = append(list, map[string]any{"type": "direct", "tag": TagDirect})
	cfg, err := json.Marshal(map[string]any{
		"log":       map[string]any{"disabled": true},
		"dns":       map[string]any{"servers": []any{probeDNS()}},
		"outbounds": list,
		"route":     map[string]any{"final": TagDirect, "default_domain_resolver": "local"},
	})
	if err != nil {
		return nil, nil, err
	}
	return StartBox(string(cfg), "")
}

// probeDNS resolves server domain names for the temporary core. Mobile apps
// can't rely on the system resolver from Go, so a public resolver is used.
func probeDNS() map[string]any {
	if runtime.GOOS == "android" || runtime.GOOS == "ios" {
		return map[string]any{"type": "udp", "tag": "local", "server": "77.88.8.8"}
	}
	return map[string]any{"type": "local", "tag": "local"}
}

// ExplainError turns a core error into a short explanation in the language
// set by SetLanguage.
func ExplainError(e string) string {
	s := strings.ToLower(e)
	switch {
	case strings.Contains(s, "context canceled"), strings.Contains(s, "operation was canceled"):
		return T("операция отменена")
	case strings.Contains(s, "no such host"):
		return T("не удалось найти адрес сервера")
	case strings.Contains(s, "unrecognized remote public key"):
		return T("отпечаток сертификата сервера не совпал со ссылкой")
	case strings.Contains(s, "reality verification failed"), strings.Contains(s, "reality: "):
		return T("Reality: сервер не принял ключ (pbk/sid) или SNI")
	case strings.Contains(s, "x509"), strings.Contains(s, "certificate"):
		return T("сертификат сервера не прошёл проверку")
	case strings.Contains(s, "connection refused"):
		return T("порт сервера закрыт")
	case strings.Contains(s, "no route to host"), strings.Contains(s, "network is unreachable"):
		return T("сервер недоступен из этой сети")
	case strings.Contains(s, "authentication failed"), strings.Contains(s, "invalid user"), strings.Contains(s, "unauthorized"):
		return T("сервер отклонил ключ доступа (UUID/пароль)")
	case strings.Contains(s, "timeout"), strings.Contains(s, "deadline exceeded"):
		return T("сервер не ответил вовремя")
	case strings.Contains(s, "handshake failure"), strings.Contains(s, "protocol version"), strings.Contains(s, "unrecognized name"):
		return T("сервер не принял TLS-соединение (проверьте SNI)")
	case strings.Contains(s, "status code"), strings.Contains(s, "unexpected status"):
		return T("сервер ответил ошибкой")
	case strings.Contains(s, "eof"), strings.Contains(s, "reset by peer"), strings.Contains(s, "closed pipe"), strings.Contains(s, "forcibly closed"):
		return T("сервер разорвал соединение")
	}
	return T("ошибка соединения")
}

// WriteReport saves a plain-text diagnostics report.
func WriteReport(path string, nodes []Node, results map[string]ProbeResult) error {
	var b strings.Builder
	ok := 0
	for _, n := range nodes {
		if r, found := results[n.ID]; found && r.Delay > 0 {
			ok++
		}
	}
	fmt.Fprintf(&b, T("Vela: проверка серверов %s\nработают: %d из %d\n\n"), time.Now().Format("2006-01-02 15:04:05"), ok, len(nodes))
	for _, n := range nodes {
		r, found := results[n.ID]
		status := T("не проверен")
		if found {
			if r.Delay > 0 {
				status = fmt.Sprintf(T("OK %d мс"), r.Delay)
			} else {
				status = T("ОШИБКА: ") + r.Reason + " | " + r.Error
			}
		}
		fmt.Fprintf(&b, "%-48s %-10s %-8s %-6s %s\n", n.Name, n.Protocol, n.Security, n.Network, status)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// ProbeTag makes one real request through an outbound of a running core.
func ProbeTag(ctx context.Context, instance *box.Box, tag string, timeout time.Duration) (int, error) {
	outbound, ok := instance.Outbound().Outbound(tag)
	if !ok {
		return 0, fmt.Errorf("outbound %s not found", tag)
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	delay, err := urltest.URLTest(tctx, TestURL, boundedDialer{outbound})
	if err != nil {
		return 0, err
	}
	if delay == 0 {
		delay = 1
	}
	return int(delay), nil
}
