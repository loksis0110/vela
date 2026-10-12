package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
)

// TestProbeIsFast checks a subscription the way real ones look: a few
// working servers, a dead host with many entries and a host that accepts
// connections but never answers. The whole check must not take minutes.
func TestProbeIsFast(t *testing.T) {
	web := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })}
	wl, _ := net.Listen("tcp", "127.0.0.1:0")
	go web.Serve(wl)
	defer web.Close()
	old := TestURL
	TestURL = "http://" + wl.Addr().String() + "/generate_204"
	defer func() { TestURL = old }()

	// a working Shadowsocks server
	ssPort := freeTCP(t)
	serverCfg, _ := json.Marshal(map[string]any{
		"log": map[string]any{"level": "error"},
		"inbounds": []any{map[string]any{"type": "shadowsocks", "listen": "127.0.0.1", "listen_port": ssPort,
			"method": "aes-128-gcm", "password": "secret"}},
		"outbounds": []any{map[string]any{"type": "direct"}},
	})
	startBoxJSON(t, string(serverCfg))

	// a host that accepts connections and stays silent
	silent, _ := net.Listen("tcp", "127.0.0.1:0")
	defer silent.Close()
	var conns []net.Conn
	var connMu sync.Mutex
	go func() {
		for {
			c, err := silent.Accept()
			if err != nil {
				return
			}
			connMu.Lock()
			conns = append(conns, c)
			connMu.Unlock()
		}
	}()
	defer func() {
		connMu.Lock()
		for _, c := range conns {
			c.Close()
		}
		connMu.Unlock()
	}()
	silentPort := silent.Addr().(*net.TCPAddr).Port
	closed := freeTCP(t)

	var nodes []Node
	add := func(link string) {
		n, err := ParseLink(link)
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, n)
	}
	for i := 0; i < 6; i++ {
		add(fmt.Sprintf("ss://YWVzLTEyOC1nY206c2VjcmV0@127.0.0.1:%d#ok-%d", ssPort, i))
	}
	for i := 0; i < 60; i++ {
		add(fmt.Sprintf("vless://11111111-2222-3333-4444-%012d@127.0.0.1:%d?type=tcp&security=none#dead-%d", i, closed, i))
	}
	for i := 0; i < 40; i++ {
		add(fmt.Sprintf("vless://11111111-2222-3333-4444-%012d@127.0.0.1:%d?type=tcp&security=none#silent-%d", i, silentPort, i))
	}
	var mu sync.Mutex
	got := map[string]ProbeResult{}
	calls := 0
	start := time.Now()
	err := ProbeNodes(context.Background(), nil, nodes, 2*time.Second, 12, func(r ProbeResult) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		got[r.ID] = r
	})
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d servers checked in %s", len(nodes), took)
	if calls != len(nodes) || len(got) != len(nodes) {
		t.Fatalf("%d results for %d servers (%d unique)", calls, len(nodes), len(got))
	}
	for _, n := range nodes {
		r := got[n.ID]
		ok := r.Delay > 0
		if want := n.Name[:2] == "ok"; ok != want {
			t.Fatalf("%s: %+v", n.Name, r)
		}
		if !ok && r.Reason == "" {
			t.Fatalf("%s: no reason", n.Name)
		}
	}
	// was: 40 silent entries x timeout / 2 per host + a slow second pass
	if took > 9*time.Second {
		t.Fatalf("too slow: %s", took)
	}
}

// probeEnv starts a test web server (TestURL) and a working Shadowsocks
// server; it returns a link to it.
func probeEnv(t *testing.T) string {
	web := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })}
	wl, _ := net.Listen("tcp", "127.0.0.1:0")
	go web.Serve(wl)
	t.Cleanup(func() { web.Close() })
	old := TestURL
	TestURL = "http://" + wl.Addr().String() + "/generate_204"
	t.Cleanup(func() { TestURL = old })
	ssPort := freeTCP(t)
	serverCfg, _ := json.Marshal(map[string]any{
		"log": map[string]any{"level": "error"},
		"inbounds": []any{map[string]any{"type": "shadowsocks", "listen": "127.0.0.1", "listen_port": ssPort,
			"method": "aes-128-gcm", "password": "secret"}},
		"outbounds": []any{map[string]any{"type": "direct"}},
	})
	startBoxJSON(t, string(serverCfg))
	return fmt.Sprintf("ss://YWVzLTEyOC1nY206c2VjcmV0@127.0.0.1:%d", ssPort)
}

func collectProbe(t *testing.T, instance *box.Box, nodes []Node, timeout time.Duration) map[string]ProbeResult {
	t.Helper()
	var mu sync.Mutex
	got := map[string]ProbeResult{}
	err := ProbeNodes(context.Background(), instance, nodes, timeout, 12, func(r ProbeResult) {
		mu.Lock()
		defer mu.Unlock()
		if _, dup := got[r.ID]; dup {
			t.Errorf("%s reported twice", r.ID)
		}
		got[r.ID] = r
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(nodes) {
		t.Fatalf("%d results for %d servers", len(got), len(nodes))
	}
	return got
}

// Behind one address (a CDN, a web server routing by path) there may be
// many unrelated servers: silent ones must not hide a working one.
func TestProbeFindsWorkingServerBehindSharedAddress(t *testing.T) {
	probeEnv(t)
	const uuid = "b831381d-6324-4d53-ad4f-8cda48b30811"
	backPort := freeTCP(t)
	serverCfg, _ := json.Marshal(map[string]any{
		"log": map[string]any{"level": "error"},
		"inbounds": []any{map[string]any{"type": "vless", "listen": "127.0.0.1", "listen_port": backPort,
			"users": []any{map[string]any{"uuid": uuid}}, "transport": map[string]any{"type": "ws", "path": "/good"}}},
		"outbounds": []any{map[string]any{"type": "direct"}},
	})
	startBoxJSON(t, string(serverCfg))
	// the shared front: /good goes to the server, other paths hang
	front, _ := net.Listen("tcp", "127.0.0.1:0")
	t.Cleanup(func() { front.Close() })
	var held []net.Conn
	var heldMu sync.Mutex
	t.Cleanup(func() {
		heldMu.Lock()
		for _, c := range held {
			c.Close()
		}
		heldMu.Unlock()
	})
	go func() {
		for {
			c, err := front.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				buf := make([]byte, 4096)
				n := 0
				for !strings.Contains(string(buf[:n]), "\r\n\r\n") && n < len(buf) {
					m, err := c.Read(buf[n:])
					if err != nil {
						c.Close()
						return
					}
					n += m
				}
				if !strings.HasPrefix(string(buf[:n]), "GET /good ") {
					heldMu.Lock()
					held = append(held, c)
					heldMu.Unlock()
					return
				}
				back, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", backPort))
				if err != nil {
					c.Close()
					return
				}
				back.Write(buf[:n])
				go func() { io.Copy(back, c); back.Close() }()
				io.Copy(c, back)
				c.Close()
			}(c)
		}
	}()
	frontPort := front.Addr().(*net.TCPAddr).Port
	var nodes []Node
	for i := 0; i < 6; i++ {
		n, err := ParseLink(fmt.Sprintf("vless://%s@127.0.0.1:%d?type=ws&path=%%2Fbad%d&security=none#bad-%d", uuid, frontPort, i, i))
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, n)
	}
	good, err := ParseLink(fmt.Sprintf("vless://%s@127.0.0.1:%d?type=ws&path=%%2Fgood&security=none#good", uuid, frontPort))
	if err != nil {
		t.Fatal(err)
	}
	nodes = append(nodes, good)
	start := time.Now()
	got := collectProbe(t, nil, nodes, 2*time.Second)
	took := time.Since(start)
	t.Logf("checked in %s", took)
	// a WebSocket handshake that never gets an answer is cut at the timeout
	if took > 15*time.Second {
		t.Fatalf("too slow: %s", took)
	}
	for _, n := range nodes {
		if ok := got[n.ID].Delay > 0; ok != (n.Name == "good") {
			t.Fatalf("%s: %+v", n.Name, got[n.ID])
		}
	}
}

// Servers the running core doesn't have (a subscription refreshed while
// connected) are checked with a temporary core instead of failing.
func TestProbeRunningCoreMissingServers(t *testing.T) {
	link := probeEnv(t)
	a, _ := ParseLink(link + "#a")
	b, _ := ParseLink(link + "#b")
	b.ID = "other-" + b.ID
	ob := map[string]any{}
	for k, v := range a.Outbound {
		ob[k] = v
	}
	ob["tag"] = NodeTag(a)
	running, cancel, err := startProbeBox([]map[string]any{ob})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { running.Close(); cancel() }()
	got := collectProbe(t, running, []Node{a, b}, 3*time.Second)
	for _, n := range []Node{a, b} {
		if got[n.ID].Delay <= 0 {
			t.Fatalf("%s: %+v", n.Name, got[n.ID])
		}
	}
}

// Without internet every server fails at once with that reason.
func TestProbeOffline(t *testing.T) {
	probeEnv(t)
	old := reachabilityTargets
	reachabilityTargets = []string{fmt.Sprintf("127.0.0.1:%d", freeTCP(t))}
	defer func() { reachabilityTargets = old }()
	var nodes []Node
	for i := 0; i < 3; i++ {
		n, _ := ParseLink(fmt.Sprintf("vless://11111111-2222-3333-4444-555555555555@127.0.0.1:%d?type=tcp&security=none#n%d", freeTCP(t), i))
		nodes = append(nodes, n)
	}
	got := collectProbe(t, nil, nodes, 2*time.Second)
	for _, n := range nodes {
		if r := got[n.ID]; r.Delay != 0 || r.Reason != T("нет подключения к интернету") {
			t.Fatalf("%s: %+v", n.Name, r)
		}
	}
}

// Stopping returns at once, reports nothing afterwards and leaves nothing
// running.
func TestProbeStop(t *testing.T) {
	link := probeEnv(t)
	silent, _ := net.Listen("tcp", "127.0.0.1:0")
	defer silent.Close()
	go func() {
		for {
			c, err := silent.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	var nodes []Node
	ok, _ := ParseLink(link + "#ok")
	nodes = append(nodes, ok)
	// fewer than the per-host limit: the working server (same host here)
	// must get its turn before the stop
	for i := 0; i < probePerHost-2; i++ {
		n, _ := ParseLink(fmt.Sprintf("vless://11111111-2222-3333-4444-%012d@127.0.0.1:%d?type=ws&path=%%2F%d&security=none#s%d",
			i, silent.Addr().(*net.TCPAddr).Port, i, i))
		nodes = append(nodes, n)
	}
	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	returned := false
	got := 0
	time.AfterFunc(700*time.Millisecond, cancel)
	start := time.Now()
	err := ProbeNodes(ctx, nil, nodes, 5*time.Second, 12, func(r ProbeResult) {
		mu.Lock()
		defer mu.Unlock()
		if returned {
			t.Errorf("result after return: %+v", r)
		}
		got++
	})
	mu.Lock()
	returned = true
	mu.Unlock()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("stop took %s", took)
	}
	if got != 1 {
		t.Fatalf("%d results before the stop, want the working server only", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before+5 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before+5 {
		buf := make([]byte, 1<<20)
		t.Fatalf("%d goroutines left (was %d):\n%s", n, before, buf[:runtime.Stack(buf, true)])
	}
}
