package core

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/experimental/deprecated"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	sjson "github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/service"
)

func selfSigned(t *testing.T, name string) (certPEM, keyPEM string, der []byte) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "self-signed"},
		DNSNames: []string{"not-" + name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})), der
}

func startBoxJSON(t *testing.T, cfg string) *box.Box {
	t.Helper()
	ctx := include.Context(service.ContextWith(context.Background(), deprecated.NewStderrManager(log.StdLogger())))
	opts, err := sjson.UnmarshalExtendedContext[option.Options](ctx, []byte(cfg))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	ctx, cancel := context.WithCancel(service.ExtendContext(ctx))
	t.Cleanup(cancel)
	b, err := box.New(box.Options{Context: ctx, Options: opts})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := b.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

func freeTCP(t *testing.T) int {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// Reproduces subscriptions where gRPC+TLS servers use a self-signed
// certificate pinned with `pcs` (Xray pinnedPeerCertSha256).
func TestPinnedCertificateVLESSgRPC(t *testing.T) {
	certPEM, keyPEM, der := selfSigned(t, "lk.x5.ru")
	sum := sha256.Sum256(der)
	var hexParts []string
	for _, b := range sum {
		hexParts = append(hexParts, fmt.Sprintf("%02X", b))
	}
	pcs := strings.Join(hexParts, ":")

	// target web server
	web := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "vela-ok") })}
	wl, _ := net.Listen("tcp", "127.0.0.1:0")
	go web.Serve(wl)
	defer web.Close()

	serverPort := freeTCP(t)
	uuid := "11111111-2222-3333-4444-555555555555"
	serverCfg, _ := json.Marshal(map[string]any{
		"log": map[string]any{"level": "error"},
		"inbounds": []any{map[string]any{
			"type": "vless", "listen": "127.0.0.1", "listen_port": serverPort,
			"users": []any{map[string]any{"uuid": uuid}},
			"tls": map[string]any{"enabled": true, "server_name": "lk.x5.ru",
				"certificate": strings.Split(strings.TrimSpace(certPEM), "\n"),
				"key":         strings.Split(strings.TrimSpace(keyPEM), "\n")},
			"transport": map[string]any{"type": "grpc", "service_name": "x5.lk.v1.AccountService"},
		}},
		"outbounds": []any{map[string]any{"type": "direct"}},
	})
	startBoxJSON(t, string(serverCfg))

	try := func(pin string) error {
		link := fmt.Sprintf("vless://%s@127.0.0.1:%d?encryption=none&type=grpc&mode=gun&serviceName=x5.lk.v1.AccountService&security=tls&sni=lk.x5.ru&fp=edge&pcs=%s#test",
			uuid, serverPort, url.QueryEscape(pin))
		node, err := ParseLink(link)
		if err != nil {
			return err
		}
		proxyPort := freeTCP(t)
		ob := node.Outbound
		ob["tag"] = "out"
		ob["server"] = "127.0.0.1"
		clientCfg, _ := json.Marshal(map[string]any{
			"log":       map[string]any{"level": "error"},
			"inbounds":  []any{map[string]any{"type": "mixed", "listen": "127.0.0.1", "listen_port": proxyPort}},
			"outbounds": []any{ob},
		})
		b := func() *box.Box {
			ctx := include.Context(service.ContextWith(context.Background(), deprecated.NewStderrManager(log.StdLogger())))
			opts, err := sjson.UnmarshalExtendedContext[option.Options](ctx, clientCfg)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(service.ExtendContext(ctx))
			t.Cleanup(cancel)
			bb, err := box.New(box.Options{Context: ctx, Options: opts})
			if err != nil {
				t.Fatal(err)
			}
			if err := bb.Start(); err != nil {
				t.Fatal(err)
			}
			return bb
		}()
		defer b.Close()
		pu, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", proxyPort))
		client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}
		resp, err := client.Get("http://" + wl.Addr().String())
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "vela-ok" {
			return fmt.Errorf("unexpected body %q (status %d)", body, resp.StatusCode)
		}
		return nil
	}

	if err := try(pcs); err != nil {
		t.Fatalf("pinned connection failed: %v", err)
	}
	wrong := strings.Repeat("AB:", 31) + "AB"
	if err := try(wrong); err == nil {
		t.Fatalf("connection with a wrong pin must fail")
	}
}
