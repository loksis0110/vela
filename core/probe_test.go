package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProbeReportsRealReasons(t *testing.T) {
	certPEM, keyPEM, der := selfSigned(t, "lk.x5.ru")
	sum := sha256.Sum256(der)
	pcs := fmt.Sprintf("%X", sum[:])

	web := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })}
	wl, _ := net.Listen("tcp", "127.0.0.1:0")
	go web.Serve(wl)
	defer web.Close()
	old := TestURL
	TestURL = "http://" + wl.Addr().String() + "/generate_204"
	defer func() { TestURL = old }()

	port := freeTCP(t)
	uuid := "11111111-2222-3333-4444-555555555555"
	serverCfg, _ := json.Marshal(map[string]any{
		"log": map[string]any{"level": "error"},
		"inbounds": []any{map[string]any{
			"type": "vless", "listen": "127.0.0.1", "listen_port": port,
			"users": []any{map[string]any{"uuid": uuid}},
			"tls": map[string]any{"enabled": true, "server_name": "lk.x5.ru",
				"certificate": strings.Split(strings.TrimSpace(certPEM), "\n"),
				"key":         strings.Split(strings.TrimSpace(keyPEM), "\n")},
			"transport": map[string]any{"type": "grpc", "service_name": "svc"},
		}},
		"outbounds": []any{map[string]any{"type": "direct"}},
	})
	startBoxJSON(t, string(serverCfg))
	closed := freeTCP(t)

	mk := func(id, u string, p int, pin string) Node {
		n, err := ParseLink(fmt.Sprintf("vless://%s@127.0.0.1:%d?encryption=none&type=grpc&serviceName=svc&security=tls&sni=lk.x5.ru&fp=edge&pcs=%s#%s", u, p, pin, id))
		if err != nil {
			t.Fatal(err)
		}
		n.ID = id
		return n
	}
	nodes := []Node{
		mk("good", uuid, port, pcs),
		mk("wrongpin", uuid, port, strings.Repeat("AB", 32)),
		mk("closed", uuid, closed, pcs),
	}
	var mu sync.Mutex
	res := map[string]ProbeResult{}
	err := ProbeNodes(context.Background(), nil, nodes, 4*time.Second, 4, func(r ProbeResult) {
		mu.Lock()
		res[r.ID] = r
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	for id, r := range res {
		t.Logf("%s: delay=%d reason=%q err=%q", id, r.Delay, r.Reason, r.Error)
	}
	if res["good"].Delay <= 0 {
		t.Fatalf("good node failed: %+v", res["good"])
	}
	if res["wrongpin"].Delay > 0 || !strings.Contains(res["wrongpin"].Reason, "отпечаток") {
		t.Fatalf("wrong pin not explained: %+v", res["wrongpin"])
	}
	if res["closed"].Delay > 0 || res["closed"].Reason != "порт сервера закрыт" {
		t.Fatalf("closed port not explained: %+v", res["closed"])
	}
	if err := WriteReport(t.TempDir()+"/r.txt", nodes, res); err != nil {
		t.Fatal(err)
	}
}
