package core

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/velaapp/vela/xrayout"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func xraySelfSigned(t *testing.T, dir string) (certFile, keyFile, pin string) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"},
		DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600)
	sum := sha256.Sum256(der)
	return certFile, keyFile, hex.EncodeToString(sum[:])
}

// TestXrayEndToEnd runs a real Xray server (VELA_XRAY_BIN) and sends TCP and
// UDP through XHTTP servers handled by the embedded Xray engine.
func TestXrayEndToEnd(t *testing.T) {
	bin := os.Getenv("VELA_XRAY_BIN")
	if bin == "" {
		t.Skip("VELA_XRAY_BIN not set")
	}
	dir := t.TempDir()
	const uuid = "b831381d-6324-4d53-ad4f-8cda48b30811"
	plainPort, tlsPort, kcpPort := freePort(t), freePort(t), freePort(t)
	certFile, keyFile, pin := xraySelfSigned(t, dir)
	server := map[string]any{
		"log": map[string]any{"loglevel": os.Getenv("VELA_XRAY_SERVER_LOG")},
		"inbounds": []any{
			map[string]any{"listen": "127.0.0.1", "port": plainPort, "protocol": "vless",
				"settings":       map[string]any{"clients": []any{map[string]any{"id": uuid}}, "decryption": "none"},
				"streamSettings": map[string]any{"network": "xhttp", "xhttpSettings": map[string]any{"path": "/xh"}}},
			map[string]any{"listen": "127.0.0.1", "port": tlsPort, "protocol": "vless",
				"settings": map[string]any{"clients": []any{map[string]any{"id": uuid}}, "decryption": "none"},
				"streamSettings": map[string]any{"network": "xhttp", "security": "tls",
					"tlsSettings":   map[string]any{"certificates": []any{map[string]any{"certificateFile": certFile, "keyFile": keyFile}}},
					"xhttpSettings": map[string]any{"path": "/xt"}}},
			map[string]any{"listen": "127.0.0.1", "port": kcpPort, "protocol": "vless",
				"settings": map[string]any{"clients": []any{map[string]any{"id": uuid}}, "decryption": "none"},
				"streamSettings": map[string]any{"network": "kcp", "kcpSettings": map[string]any{},
					"finalmask": map[string]any{"udp": []any{
						map[string]any{"type": "mkcp-legacy", "settings": map[string]any{"value": "s33d"}},
						map[string]any{"type": "mkcp-legacy", "settings": map[string]any{"header": "wechat"}}}}}},
		},
		"outbounds": []any{map[string]any{"protocol": "freedom", "settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow"}}}}},
	}
	data, _ := json.Marshal(server)
	cfgFile := filepath.Join(dir, "server.json")
	os.WriteFile(cfgFile, data, 0o600)
	cmd := exec.Command(bin, "run", "-c", cfgFile)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	// targets: an HTTP server and a UDP echo server
	httpLn, _ := net.Listen("tcp", "127.0.0.1:0")
	go http.Serve(httpLn, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hello via xray") }))
	udpLn, _ := net.ListenPacket("udp", "127.0.0.1:0")
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := udpLn.ReadFrom(buf)
			if err != nil {
				return
			}
			udpLn.WriteTo(buf[:n], addr)
		}
	}()
	time.Sleep(800 * time.Millisecond)

	links := []string{
		fmt.Sprintf("vless://%s@127.0.0.1:%d?type=xhttp&path=%%2Fxh&security=none#plain", uuid, plainPort),
		fmt.Sprintf("vless://%s@127.0.0.1:%d?type=xhttp&path=%%2Fxt&security=tls&sni=localhost&alpn=h2&pcs=%s#tls", uuid, tlsPort, pin),
		fmt.Sprintf("vless://%s@127.0.0.1:%d?type=kcp&seed=s33d&headerType=wechat-video&security=none#kcp", uuid, kcpPort),
	}
	res := ParseText(strings.Join(links, "\n"))
	if len(res.Nodes) != 3 {
		t.Fatalf("nodes: %v", res.Errors)
	}
	// "wechat-video" was the old name of the header
	res.Nodes[2].Link = strings.Replace(res.Nodes[2].Link, "wechat-video", "wechat", 1)
	var outbounds []any
	for _, n := range res.Nodes {
		fresh, err := ParseLink(n.Link)
		if err != nil {
			t.Fatal(err)
		}
		ob := fresh.Outbound
		ob["tag"] = NodeTag(n)
		outbounds = append(outbounds, ob)
	}
	cfg, _ := json.Marshal(map[string]any{"log": map[string]any{"level": "warn"}, "outbounds": outbounds})
	instance, cancel, err := StartBox(string(cfg), "")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	defer instance.Close()

	for _, n := range res.Nodes {
		ob, ok := instance.Outbound().Outbound(NodeTag(n))
		if !ok {
			t.Fatalf("%s: no outbound", n.Name)
		}
		ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
		conn, err := ob.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr(httpLn.Addr().String()))
		if err != nil {
			t.Fatalf("%s: dial: %v", n.Name, err)
		}
		timer := time.AfterFunc(10*time.Second, func() { conn.Close() })
		io.WriteString(conn, "GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
		body, _ := io.ReadAll(conn)
		timer.Stop()
		conn.Close()
		if !strings.Contains(string(body), "hello via xray") {
			t.Fatalf("%s: TCP response %q", n.Name, body)
		}
		pc, err := ob.ListenPacket(ctx, M.ParseSocksaddr(udpLn.LocalAddr().String()))
		if err != nil {
			t.Fatalf("%s: listen: %v", n.Name, err)
		}
		pc.SetReadDeadline(time.Now().Add(10 * time.Second))
		udpTimer := time.AfterFunc(12*time.Second, func() { pc.Close() })
		defer udpTimer.Stop()
		if _, err := pc.WriteTo([]byte("ping-"+n.Name), udpLn.LocalAddr()); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 100)
		k, from, err := pc.ReadFrom(buf)
		if err != nil || string(buf[:k]) != "ping-"+n.Name {
			t.Fatalf("%s: UDP echo %q from %v: %v", n.Name, buf[:k], from, err)
		}
		pc.Close()
		done()
		t.Logf("%s: TCP and UDP ok", n.Name)
	}
	// live XHTTP/gRPC connections and Xray handlers go away with the core
	instance.Close()
	if !waitFor(6*time.Second, func() bool { return xrayout.CurrentStats() == (xrayout.Stats{}) }) {
		t.Fatalf("left after close: %+v", xrayout.CurrentStats())
	}
}
