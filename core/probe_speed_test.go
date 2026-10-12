package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
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
