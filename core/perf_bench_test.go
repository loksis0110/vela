package core

// Connection-quality bench. Not part of the normal test run:
//
//	VELA_PERF=1 VELA_XRAY_BIN=/path/to/xray go test -run TestPerf -v ./core/
//
// Local servers (Shadowsocks, VLESS, Trojan+TLS on sing-box, VLESS+XHTTP on
// Xray) sit behind forwarders that add a network delay (VELA_PERF_RTT ms,
// default 100), next to dead (closed port) and silent (accepts, never
// answers) servers, a local web target and a local DoH resolver.

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
)

type perfEnv struct {
	ip                string
	target            string // http://ip:port
	doh               string // https://ip:port/dns-query
	ok, xrayOK        []Node
	dead, silent      []Node
	xrayDead          []Node
	dohQueries        int64
	dohMu             sync.Mutex
	silentConns       []net.Conn
	silentMu          sync.Mutex
	rtt               time.Duration
	targetConnections int64
	victims           []Node
	kill              []func(mode int)
}

func perfIP() string {
	if l, err := net.Listen("tcp", "192.0.2.2:0"); err == nil {
		l.Close()
		return "192.0.2.2"
	}
	return "127.0.0.1"
}

// delayPipe copies src to dst, delivering every chunk oneWay later; the
// first chunk waits one more round trip (the TCP handshake of a real link).
func delayPipe(dst, src net.Conn, oneWay, first time.Duration) {
	type chunk struct {
		b  []byte
		at time.Time
	}
	ch := make(chan chunk, 4096)
	go func() {
		defer close(ch)
		extra := first
		for {
			buf := make([]byte, 32<<10)
			n, err := src.Read(buf)
			if n > 0 {
				ch <- chunk{buf[:n], time.Now().Add(oneWay + extra)}
				extra = 0
			}
			if err != nil {
				return
			}
		}
	}()
	for c := range ch {
		if w := time.Until(c.at); w > 0 {
			time.Sleep(w)
		}
		if _, err := dst.Write(c.b); err != nil {
			break
		}
	}
	for range ch {
	}
	if tc, ok := dst.(*net.TCPConn); ok {
		tc.CloseWrite()
	} else {
		dst.Close()
	}
}

func (e *perfEnv) delayProxy(t *testing.T, backend string) int {
	port, _ := e.killableProxy(t, backend, e.rtt)
	return port
}

// killableProxy is a delayProxy whose server can be made silent (mode 1:
// connections are accepted and never answered) or dead (mode 2: refused).
func (e *perfEnv) killableProxy(t *testing.T, backend string, rtt time.Duration) (int, func(mode int)) {
	l, err := net.Listen("tcp", e.ip+":0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	oneWay := rtt / 2
	var mode int
	var modeMu sync.Mutex
	var held []net.Conn
	setMode := func(m int) {
		modeMu.Lock()
		mode = m
		for _, c := range held {
			c.Close()
		}
		held = nil
		modeMu.Unlock()
		if m == 2 {
			l.Close()
		}
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			modeMu.Lock()
			m := mode
			held = append(held, c)
			modeMu.Unlock()
			if m != 0 {
				continue
			}
			go func() {
				b, err := net.Dial("tcp", backend)
				if err != nil {
					c.Close()
					return
				}
				done := make(chan struct{}, 2)
				go func() { delayPipe(b, c, oneWay, rtt); done <- struct{}{} }()
				go func() { delayPipe(c, b, oneWay, 0); done <- struct{}{} }()
				<-done
				<-done
				c.Close()
				b.Close()
			}()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port, setMode
}

func newPerfEnv(t *testing.T) *perfEnv {
	e := &perfEnv{ip: perfIP(), rtt: 100 * time.Millisecond}
	if v, err := strconv.Atoi(os.Getenv("VELA_PERF_RTT")); err == nil {
		e.rtt = time.Duration(v) * time.Millisecond
	}
	dir := t.TempDir()
	// web target
	wl, err := net.Listen("tcp", e.ip+":0")
	if err != nil {
		t.Fatal(err)
	}
	web := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/generate_204" {
			w.WriteHeader(204)
			return
		}
		io.WriteString(w, "hello")
	})}
	go web.Serve(wl)
	t.Cleanup(func() { web.Close() })
	e.target = "http://" + wl.Addr().String()

	// DoH resolver
	certFile, keyFile, _ := xraySelfSigned(t, dir)
	dl, err := net.Listen("tcp", e.ip+":0")
	if err != nil {
		t.Fatal(err)
	}
	doh := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		if r.Method == http.MethodGet {
			body, _ = base64.RawURLEncoding.DecodeString(r.URL.Query().Get("dns"))
		} else {
			body, _ = io.ReadAll(r.Body)
		}
		var m dns.Msg
		if m.Unpack(body) != nil || len(m.Question) == 0 {
			w.WriteHeader(400)
			return
		}
		e.dohMu.Lock()
		e.dohQueries++
		e.dohMu.Unlock()
		resp := new(dns.Msg)
		resp.SetReply(&m)
		if m.Question[0].Qtype == dns.TypeA {
			rr, _ := dns.NewRR(m.Question[0].Name + " 300 IN A " + e.ip)
			resp.Answer = append(resp.Answer, rr)
		}
		out, _ := resp.Pack()
		w.Header().Set("Content-Type", "application/dns-message")
		w.Write(out)
	})}
	go doh.ServeTLS(dl, certFile, keyFile)
	t.Cleanup(func() { doh.Close() })
	e.doh = "https://" + dl.Addr().String() + "/dns-query"

	// sing-box servers
	const uuid = "b831381d-6324-4d53-ad4f-8cda48b30811"
	ssPort, vlessPort, trojanPort := freeTCP(t), freeTCP(t), freeTCP(t)
	serverCfg, _ := json.Marshal(map[string]any{
		"log": map[string]any{"level": "error"},
		"inbounds": []any{
			map[string]any{"type": "shadowsocks", "listen": "127.0.0.1", "listen_port": ssPort, "method": "aes-128-gcm", "password": "secret"},
			map[string]any{"type": "vless", "listen": "127.0.0.1", "listen_port": vlessPort, "users": []any{map[string]any{"uuid": uuid}}},
			map[string]any{"type": "trojan", "listen": "127.0.0.1", "listen_port": trojanPort, "users": []any{map[string]any{"password": "secret"}},
				"tls": map[string]any{"enabled": true, "certificate_path": certFile, "key_path": keyFile}},
		},
		"outbounds": []any{map[string]any{"type": "direct"}},
	})
	startBoxJSON(t, string(serverCfg))
	addr := func(p int) string { return "127.0.0.1:" + strconv.Itoa(p) }
	ss, vl, tj := e.delayProxy(t, addr(ssPort)), e.delayProxy(t, addr(vlessPort)), e.delayProxy(t, addr(trojanPort))
	parse := func(link string) Node {
		n, err := ParseLink(link)
		if err != nil {
			t.Fatalf("%s: %v", link, err)
		}
		return n
	}
	// servers that die while connected (one per recovery scenario): the
	// fastest ones, so that the automatic group (old and new) settles on them
	for i := 0; i < 8; i++ {
		port, kill := e.killableProxy(t, addr(vlessPort), e.rtt/4)
		e.victims = append(e.victims, parse(fmt.Sprintf("vless://%s@%s:%d?type=tcp&security=none#victim-%d", uuid, e.ip, port, i)))
		e.kill = append(e.kill, kill)
	}
	for i := 0; i < 4; i++ {
		e.ok = append(e.ok,
			parse(fmt.Sprintf("ss://YWVzLTEyOC1nY206c2VjcmV0@%s:%d#ok-ss-%d", e.ip, ss, i)),
			parse(fmt.Sprintf("vless://%s@%s:%d?type=tcp&security=none#ok-vless-%d", uuid, e.ip, vl, i)),
			parse(fmt.Sprintf("trojan://secret@%s:%d?security=tls&sni=localhost&allowInsecure=1#ok-trojan-%d", e.ip, tj, i)))
	}
	// Xray server (XHTTP)
	if bin := os.Getenv("VELA_XRAY_BIN"); bin != "" {
		xp := freeTCP(t)
		server := map[string]any{
			"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": xp, "protocol": "vless",
				"settings":       map[string]any{"clients": []any{map[string]any{"id": uuid}}, "decryption": "none"},
				"streamSettings": map[string]any{"network": "xhttp", "xhttpSettings": map[string]any{"path": "/xh"}}}},
			"outbounds": []any{map[string]any{"protocol": "freedom"}},
		}
		data, _ := json.Marshal(server)
		cfgFile := filepath.Join(dir, "xray.json")
		os.WriteFile(cfgFile, data, 0o600)
		cmd := exec.Command(bin, "run", "-c", cfgFile)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
		waitFor(3*time.Second, func() bool {
			c, err := net.Dial("tcp", addr(xp))
			if err == nil {
				c.Close()
			}
			return err == nil
		})
		xh := e.delayProxy(t, addr(xp))
		for i := 0; i < 4; i++ {
			e.xrayOK = append(e.xrayOK, parse(fmt.Sprintf("vless://%s@%s:%d?type=xhttp&path=%%2Fxh&security=none#ok-xhttp-%d", uuid, e.ip, xh, i)))
		}
	}
	// dead and silent servers
	closed := freeTCP(t)
	silent, _ := net.Listen("tcp", "127.0.0.1:0")
	t.Cleanup(func() {
		silent.Close()
		e.silentMu.Lock()
		for _, c := range e.silentConns {
			c.Close()
		}
		e.silentMu.Unlock()
	})
	go func() {
		for {
			c, err := silent.Accept()
			if err != nil {
				return
			}
			e.silentMu.Lock()
			e.silentConns = append(e.silentConns, c)
			e.silentMu.Unlock()
		}
	}()
	sp := silent.Addr().(*net.TCPAddr).Port
	for i := 0; i < 300; i++ {
		e.dead = append(e.dead, parse(fmt.Sprintf("vless://%s@127.0.0.1:%d?type=tcp&security=none#dead-%d", uuid, closed, i)))
	}
	for i := 0; i < 120; i++ {
		e.silent = append(e.silent,
			parse(fmt.Sprintf("vless://%s@127.0.0.1:%d?type=tcp&security=none#silent-%d", uuid, sp, i)),
			parse(fmt.Sprintf("trojan://secret@127.0.0.1:%d?security=tls&sni=x.example#silent-tls-%d", sp, i)))
	}
	for i := 0; i < 40; i++ {
		e.xrayDead = append(e.xrayDead, parse(fmt.Sprintf("vless://%s@127.0.0.1:%d?type=xhttp&path=%%2Fxh&security=none#xdead-%d", uuid, closed, i)))
	}
	return e
}

// subscription returns n servers: the working ones spread over the list.
func (e *perfEnv) subscription(n int) []Node {
	var bad []Node
	bad = append(bad, e.dead...)
	bad = append(bad, e.silent...)
	bad = append(bad, e.xrayDead...)
	good := append(append([]Node{}, e.ok...), e.xrayOK...)
	var out []Node
	gi, bi := 0, 0
	for len(out) < n {
		if gi < len(good) && len(out)%(n/len(good)+1) == 5 {
			out = append(out, good[gi])
			gi++
			continue
		}
		if bi >= len(bad) {
			bi = 0
		}
		b := bad[bi]
		bi++
		out = append(out, b)
	}
	// unique names/ids (bad entries repeat when n is large)
	seen := map[string]int{}
	for i := range out {
		if k := seen[out[i].ID]; k > 0 {
			l := out[i].Link + "-" + strconv.Itoa(k)
			nn, _ := ParseLink(l)
			out[i] = nn
		}
		seen[out[i].ID]++
	}
	return out
}

func ids(nodes []Node) []string {
	var out []string
	for _, n := range nodes {
		out = append(out, n.ID)
	}
	return out
}

// config builds a "local" mode config the way the desktop app does and
// points the checks and DNS at the local servers.
func (e *perfEnv) config(t *testing.T, nodes []Node, ranked, failed []string, ctrl int, mixed int) string {
	return e.configWarm(t, nodes, ranked, failed, ctrl, mixed, false)
}

func (e *perfEnv) configWarm(t *testing.T, nodes []Node, ranked, failed []string, ctrl int, mixed int, warmDNS bool) string {
	s := DefaultSettings()
	s.Mode = "local"
	s.Routing = "global"
	s.ProxyPort = mixed
	s.RemoteDNS = e.doh
	s.LogLevel = "error"
	cfg, err := BuildConfig(BuildRequest{Platform: PlatformLinux, Nodes: nodes, Selected: "auto", Settings: s,
		DataDir: t.TempDir(), ControllerPort: ctrl, ControllerSecret: "sec", Ranked: ranked, Failed: failed})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal([]byte(cfg), &m)
	for _, ob := range m["outbounds"].([]any) {
		o := ob.(map[string]any)
		if o["url"] != nil {
			o["url"] = e.target + "/generate_204"
		}
		if o["type"] == TypeAutoPick && warmDNS {
			o["warm_dns"] = "dns-remote"
		}
	}
	for _, sv := range m["dns"].(map[string]any)["servers"].([]any) {
		s := sv.(map[string]any)
		if s["tag"] == "dns-remote" {
			s["tls"] = map[string]any{"enabled": true, "insecure": true}
		}
		if s["tag"] == "dns-direct" {
			s["type"] = "local"
			delete(s, "server")
		}
	}
	route := m["route"].(map[string]any)
	var rules []any
	for _, r := range route["rules"].([]any) {
		if r.(map[string]any)["ip_is_private"] == nil {
			rules = append(rules, r)
		}
	}
	route["rules"] = rules
	out, _ := json.Marshal(m)
	return string(out)
}

func proxyClient(mixed int, timeout time.Duration) *http.Client {
	pu, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", mixed))
	return &http.Client{Timeout: timeout, Transport: &http.Transport{Proxy: http.ProxyURL(pu), DisableKeepAlives: true}}
}

func ctrlGet(port int, path string, out any) error {
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
	req.Header.Set("Authorization", "Bearer sec")
	resp, err := (&http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil}}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%d %s", resp.StatusCode, b)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

type firstResult struct {
	start, firstOK, selected, pick time.Duration
	attempts                       int
	now                            string
}

// firstRequest starts the core and requests the target through the local
// proxy until it works, like a browser the user reloads.
func (e *perfEnv) firstRequest(t *testing.T, cfg string, mixed int, limit time.Duration, waitPick bool) (firstResult, *box.Box, func()) {
	var r firstResult
	t0 := time.Now()
	instance, cancel, err := StartBox(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	r.start = time.Since(t0)
	if waitPick {
		ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
		WaitAutoPick(ctx, instance)
		done()
		r.pick = time.Since(t0)
	}
	stopWatch := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		auto, _ := instance.Outbound().Outbound(TagAuto)
		g, _ := auto.(adapter.OutboundGroup)
		for {
			if g != nil && g.Now() != "" && r.selected == 0 {
				r.selected = time.Since(t0)
				r.now = g.Now()
			}
			select {
			case <-stopWatch:
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}()
	client := proxyClient(mixed, 5*time.Second)
	for time.Since(t0) < limit {
		r.attempts++
		resp, err := client.Get(e.target + "/")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(body) == "hello" {
				r.firstOK = time.Since(t0)
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if r.firstOK == 0 {
		r.firstOK = -1
	}
	// how the group settles: wait for its selection (up to the limit)
	waitFor(limit-time.Since(t0), func() bool { return r.selected > 0 })
	close(stopWatch)
	<-watchDone
	return r, instance, func() { instance.Close(); cancel() }
}

func TestPerf(t *testing.T) {
	if os.Getenv("VELA_PERF") == "" {
		t.Skip("VELA_PERF not set")
	}
	e := newPerfEnv(t)
	t.Logf("env: ip %s, rtt %s, working %d (+%d xray), dead %d, silent %d", e.ip, e.rtt, len(e.ok), len(e.xrayOK), len(e.dead), len(e.silent))
	sub := e.subscription(600)

	// 1. BuildConfig for 600 servers
	{
		dir := t.TempDir()
		s := DefaultSettings()
		const runs = 10
		var size int
		t0 := time.Now()
		for i := 0; i < runs; i++ {
			cfg, err := BuildConfig(BuildRequest{Platform: PlatformWindows, Nodes: sub, Selected: "auto", Settings: s, DataDir: dir, ControllerPort: 1, ControllerSecret: "x"})
			if err != nil {
				t.Fatal(err)
			}
			size = len(cfg)
		}
		t.Logf("BuildConfig 600 servers: %s per call, %d KB", time.Since(t0)/runs, size/1024)
	}

	// 2. core start time with 30 and 600 outbounds (Xray ones included)
	for _, n := range []int{30, 600} {
		nodes := sub[:n]
		if n == 30 {
			nodes = append(append(append([]Node{}, e.dead[:14]...), e.xrayDead[:6]...), append(e.ok[:6], e.xrayOK...)...)
		}
		xr := 0
		for _, nd := range nodes {
			if nd.Engine == "xray" {
				xr++
			}
		}
		var total, closeTotal time.Duration
		const runs = 4
		for i := 0; i < runs; i++ {
			cfg := e.config(t, nodes, nil, nil, freeTCP(t), freeTCP(t))
			t0 := time.Now()
			instance, cancel, err := StartBox(cfg, "")
			if err != nil {
				t.Fatal(err)
			}
			total += time.Since(t0)
			t1 := time.Now()
			instance.Close()
			cancel()
			closeTotal += time.Since(t1)
		}
		t.Logf("start %d outbounds (%d xray): box.New+Start %s, Close %s", len(nodes), xr, total/runs, closeTotal/runs)
	}

	// 3. auto mode: start -> first working request
	scen := []struct {
		name   string
		ranked []string
	}{
		{"alive first", ids(e.ok)},
		{"5 dead first", append(ids(e.dead[:5]), ids(e.ok)...)},
		{"5 silent first", append(ids(e.silent[:5]), ids(e.ok)...)},
		{"15 silent first", append(ids(e.silent[:15]), ids(e.ok)...)},
		{"no ranking (subscription order)", nil},
	}
	for _, waitPick := range []bool{false, true} {
		for _, sc := range scen {
			ctrl, mixed := freeTCP(t), freeTCP(t)
			cfg := e.config(t, sub, sc.ranked, nil, ctrl, mixed)
			r, _, stop := e.firstRequest(t, cfg, mixed, 60*time.Second, waitPick)
			stop()
			how := "request at once"
			if waitPick {
				how = fmt.Sprintf("after pick %s", r.pick.Round(time.Millisecond))
			}
			t.Logf("auto, %-32s %-20s start %6s  first OK %7s (%d tries)  group selected at %7s",
				sc.name+":", how, r.start.Round(time.Millisecond), r.firstOK.Round(time.Millisecond), r.attempts, r.selected.Round(time.Millisecond))
		}
	}

	// 4. DNS through the tunnel (DoH via the selected server)
	for _, warm := range []bool{false, true} {
		ctrl, mixed := freeTCP(t), freeTCP(t)
		cfg := e.configWarm(t, sub, ids(e.ok), nil, ctrl, mixed, warm)
		_, _, stop := e.firstRequest(t, cfg, mixed, 30*time.Second, true)
		time.Sleep(time.Second) // the user opens the first page
		for i, name := range []string{"first.example", "second.example", "first.example"} {
			t0 := time.Now()
			err := ctrlGet(ctrl, "/dns/query?name="+name, nil)
			t.Logf("DNS (warm-up %v) query %d (%s) via DoH: %s err=%v", warm, i+1, name, time.Since(t0).Round(time.Millisecond), err)
		}
		stop()
	}

	// 4b. the server in use dies while connected (auto mode): how long until
	// requests work again, with the core alone and with an app check that
	// asks for another server after a failed request
	for i, mode := range []int{2, 1} {
		ctrl, mixed := freeTCP(t), freeTCP(t)
		victim := e.victims[i]
		cands := append([]Node{victim}, e.silent[20:22]...)
		cands = append(cands, e.ok...)
		cands = append(cands, e.silent[:10]...)
		cfg := e.config(t, cands, ids(cands), nil, ctrl, mixed)
		_, instance, stop := e.firstRequest(t, cfg, mixed, 30*time.Second, true)
		auto, _ := instance.Outbound().Outbound(TagAuto)
		g := auto.(adapter.OutboundGroup)
		// the group settles on the victim (the old core: after its first check)
		waitFor(25*time.Second, func() bool { return g.Now() != "" })
		if g.Now() != NodeTag(victim) {
			t.Logf("recovery: group is on %s, not the victim", g.Now())
		}
		e.kill[i](mode)
		killed := time.Now()
		client := proxyClient(mixed, 5*time.Second)
		var recovered time.Duration = -1
		tries := 0
		appChecked := false
		for time.Since(killed) < 40*time.Second {
			tries++
			resp, err := client.Get(e.target + "/")
			if err == nil {
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if string(body) == "hello" {
					recovered = time.Since(killed)
					break
				}
			}
			if mode == 1 && !appChecked && time.Since(killed) > 10*time.Second {
				// the app's watchdog: the check through the server failed
				appChecked = true
				ctx, done := context.WithTimeout(context.Background(), 20*time.Second)
				perfAppRepick(ctx, instance)
				done()
			}
			time.Sleep(500 * time.Millisecond)
		}
		name := map[int]string{1: "silent", 2: "dead (refused)"}[mode]
		after := "never (40 s)"
		if recovered >= 0 {
			after = "after " + recovered.Round(time.Millisecond).String()
		}
		t.Logf("recovery, server became %-15s requests work again %s (%d tries), group now %s", name+":", after, tries, g.Now())
		stop()
	}

	// 5. memory and goroutines over connect/disconnect cycles
	{
		runtime.GC()
		g0 := runtime.NumGoroutine()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		h0 := ms.HeapInuse
		for i := 1; i <= 12; i++ {
			ctrl, mixed := freeTCP(t), freeTCP(t)
			cfg := e.config(t, sub, ids(e.ok), nil, ctrl, mixed)
			instance, cancel, err := StartBox(cfg, "")
			ctx, done := context.WithTimeout(context.Background(), 5*time.Second)
			WaitAutoPick(ctx, instance)
			done()
			if err != nil {
				t.Fatal(err)
			}
			client := proxyClient(mixed, 5*time.Second)
			for k := 0; k < 3; k++ {
				if resp, err := client.Get(e.target + "/"); err == nil {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
			}
			instance.Close()
			cancel()
			if i == 1 || i == 6 || i == 12 {
				time.Sleep(time.Second)
				runtime.GC()
				runtime.ReadMemStats(&ms)
				t.Logf("after %2d connect/disconnect cycles: goroutines %d (start %d), heap in use %d MB (start %d MB)",
					i, runtime.NumGoroutine(), g0, ms.HeapInuse>>20, h0>>20)
			}
		}
	}
	_ = tls.VersionTLS13
	_ = strings.TrimSpace
	_ = context.Background
}

// TestPerfServe keeps the local servers running for a desktop run:
// VELA_PERF_SERVE=<dir> writes <dir>/sub.txt (600 links, the working ones
// listed after 15 silent ones), <dir>/env.json and waits for <dir>/stop.
func TestPerfServe(t *testing.T) {
	dir := os.Getenv("VELA_PERF_SERVE")
	if dir == "" {
		t.Skip("VELA_PERF_SERVE not set")
	}
	e := newPerfEnv(t)
	var links []string
	for _, n := range e.silent[:15] {
		links = append(links, n.Link)
	}
	for _, n := range e.victims {
		links = append(links, n.Link)
	}
	for _, n := range e.subscription(600 - len(links)) {
		links = append(links, n.Link)
	}
	os.WriteFile(filepath.Join(dir, "sub.txt"), []byte(strings.Join(links, "\n")), 0o644)
	info, _ := json.Marshal(map[string]any{"target": e.target, "doh": e.doh,
		"victims": ids(e.victims), "silentFirst": ids(e.silent[:15]), "ok": ids(e.ok)})
	os.WriteFile(filepath.Join(dir, "env.json"), info, 0o644)
	// control: <dir>/kill-<i>-<mode> files
	for {
		if _, err := os.Stat(filepath.Join(dir, "stop")); err == nil {
			return
		}
		for i := range e.victims {
			for _, m := range []int{1, 2} {
				f := filepath.Join(dir, fmt.Sprintf("kill-%d-%d", i, m))
				if _, err := os.Stat(f); err == nil {
					os.Remove(f)
					e.kill[i](m)
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestPerfStartMemory: start time and memory of a running core with 30 and
// 600 servers, without and with the rule sets of each routing mode.
func TestPerfStartMemory(t *testing.T) {
	if os.Getenv("VELA_PERF") == "" {
		t.Skip("VELA_PERF not set")
	}
	e := newPerfEnv(t)
	sub := e.subscription(600)
	for _, routing := range []string{"global", "smart", "blocked"} {
		for _, n := range []int{30, 600} {
			s := DefaultSettings()
			s.Mode, s.Routing, s.ProxyPort, s.LogLevel = "local", routing, freeTCP(t), "error"
			cfg, err := BuildConfig(BuildRequest{Platform: PlatformLinux, Nodes: sub[:n], Selected: "auto", Settings: s,
				DataDir: t.TempDir(), ControllerPort: freeTCP(t), ControllerSecret: "s"})
			if err != nil {
				t.Fatal(err)
			}
			const runs = 3
			var took time.Duration
			var heap int64
			var gor int
			for i := 0; i < runs; i++ {
				var ms runtime.MemStats
				runtime.GC()
				runtime.GC()
				runtime.ReadMemStats(&ms)
				h0, g0 := int64(ms.HeapAlloc), runtime.NumGoroutine()
				t0 := time.Now()
				instance, cancel, err := StartBox(cfg, "")
				if err != nil {
					t.Fatal(err)
				}
				took += time.Since(t0)
				time.Sleep(500 * time.Millisecond)
				runtime.GC()
				runtime.GC()
				runtime.ReadMemStats(&ms)
				heap += int64(ms.HeapAlloc) - h0
				gor += runtime.NumGoroutine() - g0
				instance.Close()
				cancel()
			}
			t.Logf("routing %-7s %3d servers: start %6s, live heap +%.1f MB, goroutines +%d, config %d KB",
				routing, n, (took / runs).Round(time.Millisecond), float64(heap/runs)/(1<<20), gor/runs, len(cfg)>>10)
		}
	}
}
