package core

import (
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
// through onResult as soon as it is known. When instance is nil, a temporary
// core without any inbounds is started, so this works while disconnected.
func ProbeNodes(ctx context.Context, instance *box.Box, nodes []Node, timeout time.Duration, parallel int, onResult func(ProbeResult)) error {
	if len(nodes) == 0 {
		return nil
	}
	// servers the core refused to create: reported without a check
	rejected := map[string]error{}
	if instance == nil {
		var outbounds []map[string]any
		seen := map[string]bool{}
		for _, n := range nodes {
			if n.Link != "" {
				if fresh, err := ParseLink(n.Link); err == nil {
					n.Outbound = fresh.Outbound
				}
			}
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
		instance = probe
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if parallel <= 0 {
		parallel = 8
	}
	return probeScheduled(ctx, instance, nodes, rejected, timeout, parallel, onResult)
}

// Probe scheduling. Checking hundreds of servers used to take many minutes:
// a dead host with 100 entries cost 100 timeouts, and every failure was
// retried slowly. Now:
//  1. identical servers are checked once;
//  2. every TCP endpoint (host:port) gets a quick connect check first, so
//     closed or unreachable servers fail in about two seconds;
//  3. real checks run with a short timeout, at most probePerHost at a time
//     per host; after probeGiveUp timeouts without a single success the rest
//     of an endpoint's entries are not tested one by one;
//  4. only failures that look like overload (timeouts, dropped connections)
//     get a second attempt, one entry per silent endpoint; if that one works
//     the others are checked too.
const (
	probePerHost      = 3
	probeGiveUp       = 3
	probeDialTimeout  = 2500 * time.Millisecond
	probeFirstTimeout = 6 * time.Second
)

type probeItem struct {
	node     Node // representative of identical servers
	ids      []string
	endpoint string // host:port for the connect check, "" for UDP-based servers
	result   ProbeResult
	tested   bool // a real check ran (not given up)
	done     bool // final result reported
}

func probeScheduled(ctx context.Context, instance *box.Box, nodes []Node, rejected map[string]error,
	timeout time.Duration, parallel int, onResult func(ProbeResult)) error {
	firstTimeout := min(timeout, probeFirstTimeout)
	workers := parallel
	if len(nodes) > 40 {
		workers = max(workers, 24)
	}

	// 1. identical servers
	var items []*probeItem
	byKey := map[string]*probeItem{}
	for _, n := range nodes {
		ob := n.Outbound
		if n.Link != "" {
			if fresh, err := ParseLink(n.Link); err == nil {
				ob = fresh.Outbound
			}
		}
		key := n.ID
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
		byKey[key] = it
		items = append(items, it)
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

	// 2. quick connect check per endpoint
	direct, hasDirect := instance.Outbound().Outbound(TagDirect)
	if hasDirect {
		endpoints := map[string]bool{}
		for _, it := range items {
			if it.endpoint != "" {
				endpoints[it.endpoint] = true
			}
		}
		dead := map[string]string{}
		var deadMu sync.Mutex
		sem := make(chan struct{}, 48)
		var wg sync.WaitGroup
		for ep := range endpoints {
			if stopped() {
				break
			}
			sem <- struct{}{}
			wg.Add(1)
			go func(ep string) {
				defer wg.Done()
				defer func() { <-sem }()
				host, portStr, _ := net.SplitHostPort(ep)
				port, _ := strconv.Atoi(portStr)
				dctx, cancel := context.WithTimeout(ctx, probeDialTimeout)
				defer cancel()
				conn, err := direct.DialContext(dctx, N.NetworkTCP, M.ParseSocksaddrHostPort(host, uint16(port)))
				if err == nil {
					conn.Close()
					return
				}
				if stopped() {
					return
				}
				msg := err.Error()
				if errors.Is(dctx.Err(), context.DeadlineExceeded) {
					msg = "connect timeout: " + msg
				}
				deadMu.Lock()
				dead[ep] = msg
				deadMu.Unlock()
			}(ep)
		}
		wg.Wait()
		if stopped() {
			return ctx.Err()
		}
		for _, it := range items {
			if msg, ok := dead[it.endpoint]; ok && it.endpoint != "" {
				reason := ExplainError(msg)
				if strings.HasPrefix(msg, "connect timeout") {
					reason = T("сервер недоступен из этой сети")
				}
				finish(it, ProbeResult{Error: msg, Reason: reason})
			}
		}
	}

	// per-host limit and per-endpoint statistics
	hostSem := map[string]chan struct{}{}
	type epStat struct{ timeouts, ok int }
	stats := map[string]*epStat{}
	gate := func(host string, size int) chan struct{} {
		mu.Lock()
		defer mu.Unlock()
		k := fmt.Sprintf("%s#%d", host, size)
		g := hostSem[k]
		if g == nil {
			g = make(chan struct{}, size)
			hostSem[k] = g
		}
		return g
	}
	stat := func(ep string) *epStat {
		st := stats[ep]
		if st == nil {
			st = &epStat{}
			stats[ep] = st
		}
		return st
	}
	epKey := func(it *probeItem) string {
		if it.endpoint != "" {
			return it.endpoint
		}
		return it.node.Server
	}

	test := func(it *probeItem, limit time.Duration) ProbeResult {
		res := ProbeResult{}
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
		delay, err := urltest.URLTest(tctx, TestURL, outbound)
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

	// run checks items with the given limits; decide is called with each
	// result and may report it
	run := func(list []*probeItem, workers, perHost int, limit time.Duration, giveUp bool, decide func(*probeItem, ProbeResult)) {
		sem := make(chan struct{}, workers)
		var wg sync.WaitGroup
		for _, it := range list {
			if stopped() {
				break
			}
			sem <- struct{}{}
			wg.Add(1)
			go func(it *probeItem) {
				defer wg.Done()
				defer func() { <-sem }()
				g := gate(it.node.Server, perHost)
				select {
				case g <- struct{}{}:
				case <-ctx.Done():
					return
				}
				defer func() { <-g }()
				ep := epKey(it)
				if giveUp {
					mu.Lock()
					st := stat(ep)
					silent := st.ok == 0 && st.timeouts >= probeGiveUp
					mu.Unlock()
					if silent {
						return // not tested: decided after the second pass
					}
				}
				r := test(it, limit)
				if stopped() && r.Delay == 0 {
					return // stopped by the user: not a server failure
				}
				mu.Lock()
				it.tested = true
				it.result = r
				st := stat(ep)
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
	var pending []*probeItem
	for _, it := range items {
		if !it.done {
			pending = append(pending, it)
		}
	}
	run(pending, workers, probePerHost, firstTimeout, true, func(it *probeItem, r ProbeResult) {
		if r.Delay > 0 || !transientError(r.Error) {
			finish(it, r)
		}
	})
	if stopped() {
		return ctx.Err()
	}

	// 4. second pass: overload-looking failures
	mu.Lock()
	byEp := map[string][]*probeItem{}
	for _, it := range items {
		if !it.done {
			byEp[epKey(it)] = append(byEp[epKey(it)], it)
		}
	}
	var retry, later []*probeItem
	for ep, list := range byEp {
		if stats[ep] != nil && stats[ep].ok > 0 {
			retry = append(retry, list...) // the host works: these were unlucky
			continue
		}
		// a silent endpoint: one more try for one of its entries
		rep := 0
		for i, it := range list {
			if it.tested {
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
	run(retry, 12, 2, timeout, false, func(it *probeItem, r ProbeResult) {
		finish(it, r)
	})
	if stopped() {
		return ctx.Err()
	}

	// 5. entries of endpoints that answered the second attempt
	var third []*probeItem
	mu.Lock()
	for _, it := range later {
		if st := stats[epKey(it)]; st != nil && st.ok > 0 {
			third = append(third, it)
		}
	}
	mu.Unlock()
	run(third, 12, 2, firstTimeout, false, func(it *probeItem, r ProbeResult) {
		finish(it, r)
	})
	if stopped() {
		return ctx.Err()
	}
	// everything else on silent endpoints fails like its tested neighbours
	mu.Lock()
	failed := map[string]ProbeResult{}
	for _, it := range items {
		if it.done || it.tested {
			if it.result.Delay == 0 && it.result.Error != "" {
				failed[epKey(it)] = it.result
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
			r = failed[epKey(it)]
		}
		if r.Error == "" {
			r = ProbeResult{Error: "timeout", Reason: ExplainError("timeout")}
		}
		finish(it, r)
	}
	return ctx.Err()
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

// tcpCheckable reports whether a server is reached over TCP, so a plain
// connect to its port says whether it is up at all.
func tcpCheckable(ob map[string]any) bool {
	typ, _ := ob["type"].(string)
	switch typ {
	case "hysteria", "hysteria2", "tuic", "wireguard":
		return false
	case EngineXray:
		outs, _ := ob["outbounds"].([]any)
		if len(outs) == 0 {
			return false
		}
		first, _ := outs[0].(map[string]any)
		if p, _ := first["protocol"].(string); p == "hysteria" || p == "wireguard" {
			return false
		}
		ss, _ := first["streamSettings"].(map[string]any)
		network, _ := ss["network"].(string)
		if network == "kcp" || network == "mkcp" || network == "quic" {
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
	delay, err := urltest.URLTest(tctx, TestURL, outbound)
	if err != nil {
		return 0, err
	}
	if delay == 0 {
		delay = 1
	}
	return int(delay), nil
}
