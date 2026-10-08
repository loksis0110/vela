package core

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/experimental/deprecated"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/service"
)

func TestStartProxyMode(t *testing.T) {
	res := ParseText(strings.Join(sampleLinks, "\n"))
	s := DefaultSettings()
	s.Mode = "proxy"
	s.BlockAds = true
	cfg, err := BuildConfig(BuildRequest{Platform: PlatformLinux, Nodes: res.Nodes, Selected: "auto",
		Settings: s, DataDir: t.TempDir(), ControllerPort: 39191, ControllerSecret: "sec"})
	if err != nil {
		t.Fatal(err)
	}
	cfg = strings.Replace(cfg, `"set_system_proxy": true`, `"set_system_proxy": false`, 1)
	ctx := include.Context(service.ContextWith(context.Background(), deprecated.NewStderrManager(log.StdLogger())))
	opts, err := json.UnmarshalExtendedContext[option.Options](ctx, []byte(cfg))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(service.ExtendContext(ctx))
	defer cancel()
	instance, err := box.New(box.Options{Context: ctx, Options: opts})
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Start(); err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	time.Sleep(300 * time.Millisecond)
	req, _ := http.NewRequest("GET", "http://127.0.0.1:39191/proxies/proxy", nil)
	req.Header.Set("Authorization", "Bearer sec")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	t.Logf("selector: %s", body)
	if !strings.Contains(strings.ReplaceAll(string(body), " ", ""), `"now":"auto"`) {
		t.Fatalf("unexpected: %s", body)
	}
	req, _ = http.NewRequest("PUT", "http://127.0.0.1:39191/proxies/proxy", strings.NewReader(`{"name":"`+NodeTag(res.Nodes[1])+`"}`))
	req.Header.Set("Authorization", "Bearer sec")
	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 204 {
		t.Fatalf("switch failed: %v %v", err, resp.StatusCode)
	}
	req, _ = http.NewRequest("GET", "http://127.0.0.1:39191/connections", nil)
	req.Header.Set("Authorization", "Bearer sec")
	resp, _ = http.DefaultClient.Do(req)
	body, _ = io.ReadAll(resp.Body)
	t.Logf("connections: %.120s", body)
}
