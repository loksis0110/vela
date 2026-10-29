package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
)

// autoEnv: a web target (also TestURL), Shadowsocks servers that can be
// stopped, and a host that accepts connections and never answers.
type autoEnv struct {
	target  string
	silent  int
	closed  int
	servers []*switchable
}

// switchable forwards to a Shadowsocks server until it is stopped (then
// connections are refused) or silenced (accepted, never answered).
type switchable struct {
	port    int
	ln      net.Listener
	mu      sync.Mutex
	silent  bool
	held    []net.Conn
	backend string
}

func (s *switchable) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		quiet := s.silent
		s.held = append(s.held, c)
		s.mu.Unlock()
		if quiet {
			continue
		}
		go func() {
			b, err := net.Dial("tcp", s.backend)
			if err != nil {
				c.Close()
				return
			}
			go func() { io.Copy(b, c); b.Close() }()
			io.Copy(c, b)
			c.Close()
		}()
	}
}

func (s *switchable) setSilent() {
	s.mu.Lock()
	s.silent = true
	for _, c := range s.held {
		c.Close()
	}
	s.held = nil
	s.mu.Unlock()
}

func (s *switchable) stop() {
	s.ln.Close()
	s.mu.Lock()
	for _, c := range s.held {
		c.Close()
	}
	s.held = nil
	s.mu.Unlock()
}

func newAutoEnv(t *testing.T, servers int) *autoEnv {
	e := &autoEnv{}
	web := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/generate_204" {
			w.WriteHeader(204)
			return
		}
		io.WriteString(w, "hello")
	})}
	wl, _ := net.Listen("tcp", "127.0.0.1:0")
	go web.Serve(wl)
	t.Cleanup(func() { web.Close() })
	e.target = "http://" + wl.Addr().String()
	old := TestURL
	TestURL = e.target + "/generate_204"
	t.Cleanup(func() { TestURL = old })

	ssPort := freeTCP(t)
	serverCfg, _ := json.Marshal(map[string]any{
		"log": map[string]any{"level": "error"},
		"inbounds": []any{map[string]any{"type": "shadowsocks", "listen": "127.0.0.1", "listen_port": ssPort,
			"method": "aes-128-gcm", "password": "secret"}},
		"outbounds": []any{map[string]any{"type": "direct"}},
	})
	startBoxJSON(t, string(serverCfg))
	for i := 0; i < servers; i++ {
		ln, _ := net.Listen("tcp", "127.0.0.1:0")
		s := &switchable{port: ln.Addr().(*net.TCPAddr).Port, ln: ln, backend: fmt.Sprintf("127.0.0.1:%d", ssPort)}
		go s.serve()
		t.Cleanup(s.stop)
		e.servers = append(e.servers, s)
	}
	silent, _ := net.Listen("tcp", "127.0.0.1:0")
	var held []net.Conn
	var mu sync.Mutex
	go func() {
		for {
			c, err := silent.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		silent.Close()
		mu.Lock()
		for _, c := range held {
			c.Close()
		}
		mu.Unlock()
	})
	e.silent = silent.Addr().(*net.TCPAddr).Port
	e.closed = freeTCP(t)
	return e
}

func (e *autoEnv) working(i int) Node {
	n, _ := ParseLink(fmt.Sprintf("ss://YWVzLTEyOC1nY206c2VjcmV0@127.0.0.1:%d#ok-%d", e.servers[i].port, i))
	return n
}

func (e *autoEnv) silentNode(i int) Node {
	n, _ := ParseLink(fmt.Sprintf("vless://11111111-2222-3333-4444-%012d@127.0.0.1:%d?type=tcp&security=none#silent-%d", i, e.silent, i))
	return n
}

// start runs the config the apps run (local proxy mode), with the checks
// pointed at the local target.
func (e *autoEnv) start(t *testing.T, nodes []Node, selected string) (*box.Box, int) {
	t.Helper()
	mixed := freeTCP(t)
	s := DefaultSettings()
	s.Mode, s.Routing, s.ProxyPort, s.DirectDNS = "local", "global", mixed, "local"
	s.LogLevel = os.Getenv("VELA_TEST_LOG")
	if s.LogLevel == "" {
		s.LogLevel = "error"
	}
	var ranked []string
	for _, n := range nodes {
		ranked = append(ranked, n.ID)
	}
	cfg, err := BuildConfig(BuildRequest{Platform: PlatformLinux, Nodes: nodes, Selected: selected, Settings: s,
		DataDir: t.TempDir(), ControllerPort: freeTCP(t), ControllerSecret: "s", Ranked: ranked})
	if err != nil {
		t.Fatal(err)
	}
	// the targets are on 127.0.0.1: keep them on the proxy
	var m map[string]any
	json.Unmarshal([]byte(cfg), &m)
	route := m["route"].(map[string]any)
	var rules []any
	for _, r := range route["rules"].([]any) {
		if r.(map[string]any)["ip_is_private"] == nil {
			rules = append(rules, r)
		}
	}
	route["rules"] = rules
	data, _ := json.Marshal(m)
	cfg = string(data)
	instance, cancel, err := StartBox(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { instance.Close(); cancel() })
	return instance, mixed
}

func autoNow(t *testing.T, instance *box.Box) string {
	ob, ok := instance.Outbound().Outbound(TagAuto)
	if !ok {
		t.Fatal("no auto group")
	}
	return ob.(adapter.OutboundGroup).Now()
}

func getVia(mixed int, target string) error {
	client := proxyClient(mixed, 3*time.Second)
	resp, err := client.Get(target + "/")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello" {
		return fmt.Errorf("body %q", body)
	}
	return nil
}

// The best servers of the last check went silent: the automatic group must
// not wait for its full check (15 s per silent server, ten at a time).
func TestAutoPickSkipsSilentServers(t *testing.T) {
	e := newAutoEnv(t, 1)
	var nodes []Node
	for i := 0; i < 12; i++ {
		nodes = append(nodes, e.silentNode(i))
	}
	good := e.working(0)
	nodes = append(nodes, good)
	start := time.Now()
	instance, mixed := e.start(t, nodes, "auto")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tag, err := WaitAutoPick(ctx, instance)
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("picked %s in %s", tag, took)
	if tag != NodeTag(good) || autoNow(t, instance) != NodeTag(good) {
		t.Fatalf("picked %s, group on %s, want %s", tag, autoNow(t, instance), NodeTag(good))
	}
	if took > 5*time.Second {
		t.Fatalf("too slow: %s", took)
	}
	if err := getVia(mixed, e.target); err != nil {
		t.Fatal(err)
	}
}

// The server in use stops accepting connections: the group moves to
// another one as soon as a connection fails, not at its next check.
func TestAutoPickLeavesDeadServer(t *testing.T) {
	e := newAutoEnv(t, 2)
	first, second := e.working(0), e.working(1)
	instance, mixed := e.start(t, []Node{first, second}, "auto")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := WaitAutoPick(ctx, instance); err != nil {
		t.Fatal(err)
	}
	victim, other := 0, second
	if autoNow(t, instance) == NodeTag(second) {
		victim, other = 1, first
	}
	e.servers[victim].stop()
	stopped := time.Now()
	getVia(mixed, e.target) // fails: the group's server is gone
	if !waitFor(5*time.Second, func() bool { return autoNow(t, instance) == NodeTag(other) }) {
		t.Fatalf("still on %s", autoNow(t, instance))
	}
	if err := getVia(mixed, e.target); err != nil {
		t.Fatal(err)
	}
	t.Logf("moved in %s", time.Since(stopped))
}

// A server that accepts connections and stays silent is found by the app's
// check; RepickAuto moves the group at once.
func TestRepickAutoLeavesSilentServer(t *testing.T) {
	e := newAutoEnv(t, 2)
	first, second := e.working(0), e.working(1)
	instance, mixed := e.start(t, []Node{first, second, e.silentNode(1)}, "auto")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := WaitAutoPick(ctx, instance); err != nil {
		t.Fatal(err)
	}
	victim, other := 0, second
	if autoNow(t, instance) == NodeTag(second) {
		victim, other = 1, first
	}
	e.servers[victim].setSilent()
	if _, err := ProbeTag(context.Background(), instance, TagSelector, 2*time.Second); err == nil {
		t.Fatal("check through a silent server passed")
	}
	start := time.Now()
	tag, err := RepickAuto(ctx, instance)
	if err != nil {
		t.Fatal(err)
	}
	if tag != NodeTag(other) {
		t.Fatalf("moved to %s", tag)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("too slow: %s", took)
	}
	if err := getVia(mixed, e.target); err != nil {
		t.Fatal(err)
	}
}

// Nothing answers (the network is down): the group stays where it is, no
// endless retries.
func TestAutoPickAllDown(t *testing.T) {
	e := newAutoEnv(t, 0)
	dead, _ := ParseLink(fmt.Sprintf("vless://11111111-2222-3333-4444-000000000001@127.0.0.1:%d?type=tcp&security=none#dead", e.closed))
	instance, _ := e.start(t, []Node{dead, e.silentNode(0)}, "auto")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := WaitAutoPick(ctx, instance); err == nil {
		t.Fatal("no error with all servers down")
	}
	p := findAutoPick(instance)
	p.mu.Lock()
	next := p.nextTry
	p.mu.Unlock()
	if !next.After(time.Now()) {
		t.Fatal("no back-off after a failed pick")
	}
}

// A server chosen by hand: no race, the helper stays out of the way.
func TestAutoPickIdleWhenServerChosen(t *testing.T) {
	e := newAutoEnv(t, 2)
	first, second := e.working(0), e.working(1)
	instance, mixed := e.start(t, []Node{first, second}, second.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tag, err := WaitAutoPick(ctx, instance)
	if err != nil || tag != "" {
		t.Fatalf("tag %q err %v", tag, err)
	}
	if err := getVia(mixed, e.target); err != nil {
		t.Fatal(err)
	}
	up, down, ok := TrafficTotals(instance)
	if !ok || up == 0 || down == 0 {
		t.Fatalf("traffic %d/%d ok=%v", up, down, ok)
	}
}

func TestConfigHasAutoPick(t *testing.T) {
	res := ParseText(strings.Join(sampleLinks, "\n"))
	for _, mode := range []string{"tun", "proxy"} {
		s := DefaultSettings()
		s.Mode = mode
		cfg, err := BuildConfig(BuildRequest{Platform: PlatformWindows, Nodes: res.Nodes, Selected: "auto", Settings: s, DataDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		var m struct {
			DNS       map[string]any   `json:"dns"`
			Outbounds []map[string]any `json:"outbounds"`
		}
		json.Unmarshal([]byte(cfg), &m)
		var pick map[string]any
		for _, ob := range m.Outbounds {
			if ob["type"] == TypeAutoPick {
				pick = ob
			}
		}
		if pick == nil || pick["group"] != TagAuto || pick["selector"] != TagSelector {
			t.Fatalf("%s: helper %v", mode, pick)
		}
		if (pick["warm_dns"] == "dns-remote") != (mode == "tun") {
			t.Fatalf("%s: warm_dns %v", mode, pick["warm_dns"])
		}
		if m.DNS["optimistic"] == nil {
			t.Fatalf("%s: no optimistic DNS cache", mode)
		}
	}
}

// A connection opened before the pick went to the first candidate, a
// silent one: it is closed soon after the pick instead of hanging.
func TestAutoPickDropsStuckConnections(t *testing.T) {
	e := newAutoEnv(t, 1)
	var nodes []Node
	for i := 0; i < 12; i++ {
		nodes = append(nodes, e.silentNode(i))
	}
	nodes = append(nodes, e.working(0))
	instance, mixed := e.start(t, nodes, "auto")
	start := time.Now()
	err := getViaTimeout(mixed, e.target, 20*time.Second)
	took := time.Since(start)
	if err == nil {
		t.Fatal("the early request went through")
	}
	if took > 5*time.Second {
		t.Fatalf("early request hung for %s", took)
	}
	t.Logf("early request ended after %s: %v", took, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := WaitAutoPick(ctx, instance); err != nil {
		t.Fatal(err)
	}
	if err := getVia(mixed, e.target); err != nil {
		t.Fatal(err)
	}
}

func getViaTimeout(mixed int, target string, timeout time.Duration) error {
	client := proxyClient(mixed, timeout)
	resp, err := client.Get(target + "/")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello" {
		return fmt.Errorf("body %q", body)
	}
	return nil
}

// A silent server with an old, better result must not win over the server
// that just answered: the group would go straight back to a dead server.
func TestRepickAutoIgnoresStaleResults(t *testing.T) {
	e := newAutoEnv(t, 2)
	first, second := e.working(0), e.working(1)
	stale := e.silentNode(7)
	instance, mixed := e.start(t, []Node{stale, first, second}, "auto")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := WaitAutoPick(ctx, instance); err != nil {
		t.Fatal(err)
	}
	victim, other := 0, second
	if autoNow(t, instance) == NodeTag(second) {
		victim, other = 1, first
	}
	p := findAutoPick(instance)
	p.history.StoreURLTestHistory(NodeTag(stale), &adapter.URLTestHistory{Time: time.Now(), Delay: 1})
	e.servers[victim].setSilent()
	tag, err := RepickAuto(ctx, instance)
	if err != nil {
		t.Fatal(err)
	}
	if tag != NodeTag(other) || autoNow(t, instance) != NodeTag(other) {
		t.Fatalf("moved to %s (group on %s), want %s", tag, autoNow(t, instance), NodeTag(other))
	}
	if err := getVia(mixed, e.target); err != nil {
		t.Fatal(err)
	}
}
