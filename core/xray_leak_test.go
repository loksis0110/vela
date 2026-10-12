package core

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/velaapp/vela/xrayout"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// waitFor polls cond for up to d.
func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return cond()
}

// TestXrayLifecycleNoLeaks starts and closes cores with Xray servers again
// and again (like connecting, switching and latency checks do) and checks
// that Xray handlers, pooled transport clients and goroutines don't pile up.
func TestXrayLifecycleNoLeaks(t *testing.T) {
	const uuid = "11111111-2222-3333-4444-555555555555"
	server := freePort(t) // nothing listens here: dials fail, as with a dead server
	target := M.ParseSocksaddr(fmt.Sprintf("127.0.0.1:%d", freePort(t)))
	links := []string{
		fmt.Sprintf("vless://%s@127.0.0.1:%d?type=xhttp&path=%%2Fx&security=none#xhttp", uuid, server),
		fmt.Sprintf("vless://%s@127.0.0.1:%d?type=xhttp&path=%%2Fy&security=tls&sni=localhost&alpn=h3#h3", uuid, server),
		fmt.Sprintf("vless://%s@127.0.0.1:%d?type=grpc&serviceName=g&security=tls&sni=localhost&encryption=mlkem768x25519plus.native.0rtt.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA#grpc", uuid, server),
		fmt.Sprintf("vless://%s@127.0.0.1:%d?type=kcp&seed=s&security=none#kcp", uuid, server),
	}
	res := ParseText(strings.Join(links, "\n"))
	if len(res.Nodes) != len(links) {
		t.Fatalf("nodes: %v", res.Errors)
	}
	s := DefaultSettings()
	s.Mode = "local"
	cycle := func(i int) {
		s.ProxyPort = freePort(t)
		cfg, err := BuildConfig(BuildRequest{Platform: PlatformLinux, Nodes: res.Nodes, Selected: "auto",
			Settings: s, DataDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		instance, cancel, err := StartBox(cfg, "")
		if err != nil {
			t.Fatal(err)
		}
		if st := xrayout.CurrentStats(); st.Outbounds != len(links) {
			t.Fatalf("cycle %d: %d xray outbounds started", i, st.Outbounds)
		}
		for _, n := range res.Nodes {
			ob, ok := instance.Outbound().Outbound(NodeTag(n))
			if !ok {
				t.Fatalf("%s: no outbound", n.Name)
			}
			ctx, done := context.WithTimeout(context.Background(), 2*time.Second)
			if conn, err := ob.DialContext(ctx, N.NetworkTCP, target); err == nil {
				conn.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
				conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
				conn.Read(make([]byte, 16))
				conn.Close()
			}
			if pc, err := ob.ListenPacket(ctx, target); err == nil {
				pc.WriteTo([]byte("x"), target)
				pc.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
				if _, _, err := pc.ReadFrom(make([]byte, 16)); err == nil {
					t.Errorf("%s: unexpected UDP answer", n.Name)
				}
				pc.Close()
			}
			done()
		}
		if i == 0 && !waitFor(3*time.Second, func() bool { return xrayout.CurrentStats().TransportClients > 0 }) {
			t.Fatal("dials created no pooled XHTTP/gRPC clients: the test does not test anything")
		}
		instance.Close()
		cancel()
		instance.Close() // closing twice is harmless
		var st xrayout.Stats
		waitFor(6*time.Second, func() bool {
			st = xrayout.CurrentStats()
			return st.Outbounds == 0 && st.Handlers == 0 && st.TransportClients == 0
		})
		if st.Outbounds != 0 || st.Handlers != 0 || st.TransportClients != 0 {
			t.Fatalf("cycle %d: left after close: %+v", i, st)
		}
	}
	cycle(0)
	time.Sleep(500 * time.Millisecond)
	base := runtime.NumGoroutine()
	for i := 1; i <= 4; i++ {
		cycle(i)
	}
	if !waitFor(45*time.Second, func() bool { return runtime.NumGoroutine() <= base+8 }) {
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		t.Fatalf("goroutines grew from %d to %d\n%s", base, runtime.NumGoroutine(), buf[:n])
	}
}
