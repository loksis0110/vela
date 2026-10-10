package core

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"testing"

	"encoding/json"
	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/experimental/deprecated"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	sjson "github.com/sagernet/sing/common/json"
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
	res := ParseText(strings.Join(sampleLinks, "\n") + "\nvless://x@h:1?type=quantum#bad\n")
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
	opts, err := sjson.UnmarshalExtendedContext[option.Options](ctx, []byte(content))
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
		for _, routing := range []string{"smart", "global", "blocked"} {
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
	if !strings.Contains(cfg, "__VELA_DATA__/rules/geoip-ru.srs") {
		t.Fatal("placeholder paths missing")
	}
	dir := t.TempDir()
	if err := InstallRules(dir + "/rules"); err != nil {
		t.Fatal(err)
	}
	checkConfig(t, strings.ReplaceAll(cfg, "__VELA_DATA__", dir))
}

func TestAdvancedSettings(t *testing.T) {
	res := ParseText(strings.Join(sampleLinks, "\n"))
	ids := []string{}
	for _, n := range res.Nodes {
		ids = append(ids, n.ID)
	}
	for _, platform := range []string{PlatformWindows, PlatformAndroid, PlatformIOS} {
		for _, frag := range []string{"", "record", "full"} {
			for _, mode := range []string{"", "exclude", "only"} {
				s := DefaultSettings()
				s.TLSFragment = frag
				s.AppMode = mode
				if platform == PlatformAndroid {
					s.Apps = []string{"ru.sberbankmobile", "com.android.chrome"}
				} else {
					s.Apps = []string{"chrome.exe"}
				}
				s.Rules = []RoutingRule{
					{Match: "https://Example.com/path", Action: "proxy"},
					{Match: "*torrent*", Action: "block"},
					{Match: "10.1.2.3", Action: "direct"},
					{Match: "2001:db8::/32", Action: "direct"},
					{Match: "bad/cidr/x", Action: "direct"},
				}
				cfg, err := BuildConfig(BuildRequest{Platform: platform, Nodes: res.Nodes, Selected: "auto",
					Settings: s, DataDir: t.TempDir(), Favorites: ids[:2], Ranked: ids})
				if err != nil {
					t.Fatal(err)
				}
				if platform == PlatformAndroid {
					cfg = strings.Replace(cfg, `"override_android_vpn": true`, `"override_android_vpn": false`, 1)
					// package filters need the Android package manager
					cfg = strings.Replace(cfg, `"include_package"`, `"x_include_package"`, 1)
					cfg = strings.Replace(cfg, `"exclude_package"`, `"x_exclude_package"`, 1)
					var m map[string]any
					json.Unmarshal([]byte(cfg), &m)
					for _, ib := range m["inbounds"].([]any) {
						delete(ib.(map[string]any), "x_include_package")
						delete(ib.(map[string]any), "x_exclude_package")
					}
					b, _ := json.Marshal(m)
					cfg = string(b)
				}
				checkConfig(t, cfg)
			}
		}
	}
}

func TestAutoCandidates(t *testing.T) {
	var all []string
	var ids []string
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("n%03d", i)
		ids = append(ids, id)
		all = append(all, NodeTag(Node{ID: id}))
	}
	got := autoCandidates(BuildRequest{}, all)
	if len(got) != MaxAutoCandidates || got[0] != "node-n000" {
		t.Fatalf("default %v", got[:3])
	}
	got = autoCandidates(BuildRequest{Ranked: []string{"n050", "n051"}}, all)
	if got[0] != "node-n050" || len(got) != MaxAutoCandidates {
		t.Fatalf("ranked %v", got[:3])
	}
	// one favorite: it goes first, the rest fills in so failover still works
	got = autoCandidates(BuildRequest{Favorites: []string{"n099", "zzz"}, Ranked: ids}, all)
	if len(got) != MaxAutoCandidates || got[0] != "node-n099" {
		t.Fatalf("fav %v", got[:3])
	}
	// two working favorites: only them
	got = autoCandidates(BuildRequest{Favorites: []string{"n099", "n098"}, Ranked: ids}, all)
	if len(got) != 2 {
		t.Fatalf("favs %v", got)
	}
	// dead favorites are skipped
	got = autoCandidates(BuildRequest{Favorites: []string{"n099", "n098", "n097"}, Failed: []string{"n098", "n097"}, Ranked: ids}, all)
	for _, g := range got {
		if g == "node-n098" || g == "node-n097" {
			t.Fatalf("failed favorite kept: %v", got)
		}
	}
	if got[0] != "node-n099" {
		t.Fatalf("fav first %v", got[:3])
	}
}

func TestDuplicatesAndOddLinks(t *testing.T) {
	link := sampleLinks[0]
	res := ParseText(link + "\n" + link)
	if len(res.Nodes) != 1 {
		t.Fatalf("duplicates kept: %d", len(res.Nodes))
	}
	// duplicates saved by older versions must not break the config
	nodes := append(res.Nodes, res.Nodes[0])
	cfg, err := BuildConfig(BuildRequest{Platform: PlatformWindows, Nodes: nodes, Selected: "auto", Settings: DefaultSettings(), DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	checkConfig(t, cfg)
	// percent-encoded base64 in ss://
	if r := ParseText("ss://YWVzLTI1Ni1nY206cGFzczE%3D@1.2.3.4:8388#x"); len(r.Nodes) != 1 {
		t.Fatalf("ss %v", r.Errors)
	}
	// BOM inside a base64 body
	body := base64.StdEncoding.EncodeToString([]byte("\ufeff" + link + "\n"))
	if r := ParseText(body); len(r.Nodes) != 1 {
		t.Fatalf("bom %v", r.Errors)
	}
	if got := ExplainError("lookup auth.example.com: no such host"); got != "не удалось найти адрес сервера" {
		t.Fatal(got)
	}
	if !strings.Contains(processRegex([]string{"discord.exe"})[0], "(?i)") {
		t.Fatal("regex")
	}
}

func TestIOSKeepsConfigSmall(t *testing.T) {
	var nodes []Node
	base := ParseText(sampleLinks[0]).Nodes[0]
	for i := 0; i < 200; i++ {
		n := base
		n.ID = fmt.Sprintf("x%03d", i)
		n.Link = ""
		nodes = append(nodes, n)
	}
	cfg, err := BuildConfig(BuildRequest{Platform: PlatformIOS, Nodes: nodes, Selected: "x150", Settings: DefaultSettings(),
		DataDir: t.TempDir(), Favorites: []string{"x199"}})
	if err != nil {
		t.Fatal(err)
	}
	n := strings.Count(cfg, `"tag": "node-`)
	if n > MaxAutoCandidates+2 || !strings.Contains(cfg, "node-x150") || !strings.Contains(cfg, "node-x199") {
		t.Fatalf("ios config has %d servers", n)
	}
}
