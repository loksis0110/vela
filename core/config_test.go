package core

import (
	"context"
	"os"
	"strings"
	"testing"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/experimental/deprecated"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/service"
)

var sampleLinks = []string{
	"vless://11111111-2222-3333-4444-555555555555@example.com:443?type=tcp&security=reality&pbk=Q1H2rOvf0Rc3uLYZKYDkX0aWbUoT5r5JbQf0Yb7cS2c&sid=6ba85179e30d4fc2&sni=www.microsoft.com&fp=chrome&flow=xtls-rprx-vision#%D0%93%D0%B5%D1%80%D0%BC%D0%B0%D0%BD%D0%B8%D1%8F%20Reality",
	"vless://11111111-2222-3333-4444-555555555555@1.2.3.4:8443?type=ws&security=tls&sni=cdn.example.com&path=%2Fws%3Fed%3D2048&host=cdn.example.com#WS%20TLS",
	"vless://11111111-2222-3333-4444-555555555555@1.2.3.4:443?type=grpc&security=tls&serviceName=grpc&sni=g.example.com#gRPC",
	"trojan://p%40ss@t.example.com:443?sni=t.example.com#Trojan",
	"ss://MjAyMi1ibGFrZTMtYWVzLTEyOC1nY206WjVMTWYrRUR6Y1RPdk1HcHJDblNpZz09@5.6.7.8:8388#SS2022",
	"hysteria2://secret@h.example.com:443,20000-30000/?sni=h.example.com&obfs=salamander&obfs-password=xyz#Hy2",
	"tuic://11111111-2222-3333-4444-555555555555:pass@q.example.com:443?congestion_control=bbr&alpn=h3#TUIC",
	"vmess://eyJ2IjoiMiIsInBzIjoiVk1lc3MgV1MiLCJhZGQiOiJ2LmV4YW1wbGUuY29tIiwicG9ydCI6IjQ0MyIsImlkIjoiMTExMTExMTEtMjIyMi0zMzMzLTQ0NDQtNTU1NTU1NTU1NTU1IiwiYWlkIjoiMCIsInNjeSI6ImF1dG8iLCJuZXQiOiJ3cyIsInR5cGUiOiJub25lIiwiaG9zdCI6InYuZXhhbXBsZS5jb20iLCJwYXRoIjoiL3ZtIiwidGxzIjoidGxzIiwic25pIjoidi5leGFtcGxlLmNvbSJ9",
}

func TestParse(t *testing.T) {
	res := ParseText(strings.Join(sampleLinks, "\n") + "\nvless://x@h:1?type=xhttp#bad\n")
	if len(res.Nodes) != len(sampleLinks) {
		t.Fatalf("parsed %d nodes, errors: %v", len(res.Nodes), res.Errors)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("expected 1 error, got %v", res.Errors)
	}
	if res.Nodes[0].Name != "Германия Reality" {
		t.Fatalf("bad name %q", res.Nodes[0].Name)
	}
	if res.Nodes[5].Port != 443 {
		t.Fatalf("hy2 port %d", res.Nodes[5].Port)
	}
}

func checkConfig(t *testing.T, content string) {
	t.Helper()
	ctx := include.Context(service.ContextWith(context.Background(), deprecated.NewStderrManager(log.StdLogger())))
	opts, err := json.UnmarshalExtendedContext[option.Options](ctx, []byte(content))
	if err != nil {
		t.Fatalf("decode: %v\n%s", err, content)
	}
	ctx, cancel := context.WithCancel(service.ExtendContext(ctx))
	defer cancel()
	instance, err := box.New(box.Options{Context: ctx, Options: opts})
	if err != nil {
		t.Fatalf("create: %v\n%s", err, content)
	}
	instance.Close()
}

func TestBuildConfigs(t *testing.T) {
	res := ParseText(strings.Join(sampleLinks, "\n"))
	dir := t.TempDir()
	for _, platform := range []string{PlatformWindows, PlatformAndroid, PlatformIOS, PlatformLinux} {
		for _, routing := range []string{"smart", "global"} {
			for _, mode := range []string{"tun", "proxy"} {
				s := DefaultSettings()
				s.Routing = routing
				s.Mode = mode
				s.BlockAds = routing == "smart"
				s.ProxyDomains = []string{"youtube.com"}
				cfg, err := BuildConfig(BuildRequest{
					Platform: platform, Nodes: res.Nodes, Selected: res.Nodes[0].ID,
					Settings: s, DataDir: dir, ControllerPort: 39090, ControllerSecret: "x",
				})
				if err != nil {
					t.Fatal(err)
				}
				if platform == PlatformAndroid {
					// only valid when running on Android itself
					cfg = strings.Replace(cfg, `"override_android_vpn": true`, `"override_android_vpn": false`, 1)
				}
				checkConfig(t, cfg)
				if platform == PlatformWindows && routing == "smart" && mode == "tun" {
					os.WriteFile(dir+"/../sample-config.json", []byte(cfg), 0o644)
				}
			}
		}
	}
}

func TestPortableDataDir(t *testing.T) {
	res := ParseText(strings.Join(sampleLinks, "\n"))
	cfg, err := BuildConfig(BuildRequest{Platform: PlatformIOS, Nodes: res.Nodes, Selected: "auto",
		Settings: DefaultSettings(), DataDir: "__VELA_DATA__", SkipRuleInstall: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg, "__VELA_DATA__/rules/geoip-ru.srs") || !strings.Contains(cfg, "__VELA_DATA__/cache.db") {
		t.Fatal("placeholder paths missing")
	}
	dir := t.TempDir()
	if err := InstallRules(dir + "/rules"); err != nil {
		t.Fatal(err)
	}
	checkConfig(t, strings.ReplaceAll(cfg, "__VELA_DATA__", dir))
}
