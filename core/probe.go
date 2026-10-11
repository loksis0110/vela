package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	_ "github.com/velaapp/vela/xrayout"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/experimental/deprecated"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	sjson "github.com/sagernet/sing/common/json"
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
			// core rejects, report them and check the rest
			var good []map[string]any
			for _, ob := range outbounds {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				one, oneCancel, oneErr := startProbeBox([]map[string]any{ob})
				if oneErr != nil {
					rejected[ob["tag"].(string)] = oneErr
					continue
				}
				one.Close()
				oneCancel()
				good = append(good, ob)
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
	// Many subscription entries point to the same server. Hitting one host
	// with lots of parallel handshakes makes it drop connections, so checks
	// are limited per host and failures get a calm second attempt.
	const perHost = 2
	var hostMu sync.Mutex
	hostSem := map[string]chan struct{}{}
	hostGate := func(host string) chan struct{} {
		hostMu.Lock()
		defer hostMu.Unlock()
		g, ok := hostSem[host]
		if !ok {
			g = make(chan struct{}, perHost)
			hostSem[host] = g
		}
		return g
	}
	test := func(n Node) ProbeResult {
		res := ProbeResult{ID: n.ID}
		if err := rejected[NodeTag(n)]; err != nil {
			res.Error = err.Error()
			res.Reason = T("сервер не удалось разобрать")
			return res
		}
		outbound, ok := instance.Outbound().Outbound(NodeTag(n))
		if !ok {
			res.Error = "outbound not found"
			res.Reason = T("сервер не удалось разобрать")
			return res
		}
		gate := hostGate(n.Server)
		select {
		case gate <- struct{}{}:
		case <-ctx.Done():
			res.Error = ctx.Err().Error()
			res.Reason = ExplainError(res.Error)
			return res
		}
		defer func() { <-gate }()
		tctx, cancel := context.WithTimeout(ctx, timeout)
		delay, err := urltest.URLTest(tctx, TestURL, outbound)
		timedOut := errors.Is(tctx.Err(), context.DeadlineExceeded)
		cancel()
		if err != nil {
			if timedOut {
				err = fmt.Errorf("timeout after %s: %w", timeout, err)
			}
			res.Error = err.Error()
			res.Reason = ExplainError(res.Error)
			return res
		}
		if delay == 0 {
			delay = 1
		}
		res.Delay = int(delay)
		return res
	}
	run := func(list []Node, workers int, report func(ProbeResult)) {
		sem := make(chan struct{}, workers)
		var wg sync.WaitGroup
		for _, n := range list {
			if ctx.Err() != nil {
				break
			}
			sem <- struct{}{}
			wg.Add(1)
			go func(n Node) {
				defer wg.Done()
				defer func() { <-sem }()
				report(test(n))
			}(n)
		}
		wg.Wait()
	}
	// first pass: results that succeed are reported right away
	var failedMu sync.Mutex
	var failed []Node
	byID := map[string]Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	firstErr := map[string]ProbeResult{}
	run(nodes, parallel, func(r ProbeResult) {
		if r.Delay <= 0 && ctx.Err() != nil {
			return // stopped by the user: not a server failure
		}
		if r.Delay > 0 {
			onResult(r)
			return
		}
		failedMu.Lock()
		failed = append(failed, byID[r.ID])
		firstErr[r.ID] = r
		failedMu.Unlock()
	})
	// second pass for failures, slowly: tells real failures from overload
	run(failed, 2, func(r ProbeResult) {
		if r.Delay <= 0 && ctx.Err() != nil {
			return
		}
		if r.Delay <= 0 && r.Error == "" {
			r = firstErr[r.ID]
		}
		onResult(r)
	})
	return ctx.Err()
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
