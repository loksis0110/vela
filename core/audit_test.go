package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/experimental/deprecated"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	sjson "github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/service"
)

const testUUID = "11111111-2222-3333-4444-555555555555"

func mustParse(t *testing.T, link string) Node {
	t.Helper()
	n, err := ParseLink(link)
	if err != nil {
		t.Fatalf("%s: %v", link, err)
	}
	return n
}

func outboundJSON(n Node) string {
	b, _ := json.Marshal(n.Outbound)
	return string(b)
}

// buildAndCheck builds a config for the nodes and lets sing-box create it.
func buildAndCheck(t *testing.T, nodes []Node, s Settings) string {
	t.Helper()
	cfg, err := BuildConfig(BuildRequest{Platform: PlatformLinux, Nodes: nodes, Selected: "auto", Settings: s, DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	checkConfig(t, cfg)
	return cfg
}

func TestOddLinks(t *testing.T) {
	// a literal "%" in a password is sent as %25: it must not be decoded twice
	if n := mustParse(t, "trojan://50%25off@t.example.com:443#t"); n.Outbound["password"] != "50%off" {
		t.Fatalf("trojan password %q", n.Outbound["password"])
	}
	if n := mustParse(t, "hysteria2://pa%25ss@h.example.com:443#h"); n.Outbound["password"] != "pa%ss" {
		t.Fatalf("hy2 password %q", n.Outbound["password"])
	}
	// characters providers forget to encode
	if n := mustParse(t, "trojan://p^a|ss%zz@t.example.com:443?sni=t.example.com#raw"); n.Outbound["password"] != "p^a|ss%zz" || n.Name != "raw" {
		t.Fatalf("raw password %q %q", n.Outbound["password"], n.Name)
	}
	// IPv6 servers
	n := mustParse(t, "vless://"+testUUID+"@[2001:db8::1]:443?security=tls&sni=x.example.com&type=ws&path=%2Fws#v6")
	if n.Server != "2001:db8::1" || n.Port != 443 || n.Network != "ws" {
		t.Fatalf("ipv6 %+v", n)
	}
	if n := mustParse(t, "vless://"+testUUID+"@[2001:db8::1]:443?type=tcp"); n.Name != "[2001:db8::1]:443" {
		t.Fatalf("ipv6 name %q", n.Name)
	}
	// raw spaces in names, several links on one line
	res := ParseText("vless://" + testUUID + "@a.example.com:443?type=tcp#My Server Name vless://" + testUUID + "@b.example.com:443?type=tcp#Second one")
	if len(res.Nodes) != 2 || res.Nodes[0].Name != "My Server Name" || res.Nodes[1].Name != "Second one" {
		t.Fatalf("spaces %+v %v", res.Nodes, res.Errors)
	}
	// unknown fingerprints must not stop the whole core
	for fp, want := range map[string]string{"hellochrome_120": "chrome", "firefox_99": "firefox", "random": "randomized", "ios": "ios", "qq": "qq"} {
		n := mustParse(t, "vless://"+testUUID+"@r.example.com:443?type=tcp&security=reality&pbk=Q1H2rOvf0Rc3uLYZKYDkX0aWbUoT5r5JbQf0Yb7cS2c&sni=www.microsoft.com&fp="+fp)
		if got := n.Outbound["tls"].(map[string]any)["utls"].(map[string]any)["fingerprint"]; got != want {
			t.Fatalf("fp %s -> %v", fp, got)
		}
	}
	// Shadowsocks: aliases, unknown ciphers, plugins
	ss := func(method, query string) string {
		return "ss://" + base64.RawURLEncoding.EncodeToString([]byte(method+":pw")) + "@5.6.7.8:8388" + query + "#ss"
	}
	if n := mustParse(t, ss("chacha20-poly1305", "")); n.Outbound["method"] != "chacha20-ietf-poly1305" {
		t.Fatalf("alias %v", n.Outbound["method"])
	}
	if _, err := ParseLink(ss("rc4", "")); err == nil {
		t.Fatal("unknown cipher accepted")
	}
	obfs := mustParse(t, ss("aes-256-gcm", "/?plugin="+strings.ReplaceAll("obfs-local;obfs=http;obfs-host=www.bing.com", ";", "%3B")))
	if obfs.Outbound["plugin"] != "obfs-local" || obfs.Outbound["plugin_opts"] != "obfs=http;obfs-host=www.bing.com" {
		t.Fatalf("plugin %v", outboundJSON(obfs))
	}
	if _, err := ParseLink(ss("aes-256-gcm", "?plugin=kcptun%3Bkey%3Dx")); err == nil {
		t.Fatal("unsupported plugin accepted")
	}
	// Hysteria2 port hopping
	hy := mustParse(t, "hysteria2://pw@h.example.com:443,20000-30000/?sni=h.example.com#Hop")
	if hy.Port != 443 || hy.Name != "Hop" || fmt.Sprint(hy.Outbound["server_ports"]) != "[443:443 20000:30000]" {
		t.Fatalf("hopping %s", outboundJSON(hy))
	}
	hy6 := mustParse(t, "hy2://pw@[2001:db8::2]:20000-30000?obfs=salamander&obfs-password=x&mport=40000-40010")
	if hy6.Server != "2001:db8::2" || hy6.Port != 20000 || fmt.Sprint(hy6.Outbound["server_ports"]) != "[20000:20000 20000:30000 40000:40010]" {
		t.Fatalf("hopping v6 %s", outboundJSON(hy6))
	}
	if _, err := ParseLink("hysteria2://pw@h.example.com:443?obfs=gost"); err == nil {
		t.Fatal("unknown obfs accepted")
	}
	// TUIC: unknown congestion control is dropped
	tu := mustParse(t, "tuic://"+testUUID+":pw@q.example.com:443?congestion_control=hybla&udp_relay_mode=weird")
	if _, ok := tu.Outbound["congestion_control"]; ok {
		t.Fatalf("tuic %s", outboundJSON(tu))
	}
	buildAndCheck(t, []Node{n, obfs, hy, hy6, tu, mustParse(t, ss("plain", ""))}, DefaultSettings())
}

func vmessLink(v map[string]any) string {
	b, _ := json.Marshal(v)
	return "vmess://" + base64.StdEncoding.EncodeToString(b)
}

func TestVMessVariants(t *testing.T) {
	// mKCP on the Xray engine keeps its header and seed (v2rayN: type, path)
	kcp := mustParse(t, vmessLink(map[string]any{"v": "2", "ps": "KCP", "add": "k.example.com", "port": 1000, "id": testUUID,
		"aid": 0, "net": "kcp", "type": "wechat-video", "path": "s33d"}))
	js := outboundJSON(kcp)
	if kcp.Engine != EngineXray || !strings.Contains(js, `"value":"s33d"`) || !strings.Contains(js, `"header":"wechat"`) {
		t.Fatalf("kcp %s", js)
	}
	// the URL form of the share-link standard
	u := mustParse(t, "vmess://"+testUUID+"@v.example.com:443?type=ws&security=tls&path=%2Fvm&host=v.example.com&encryption=auto#URL%20form")
	if u.Protocol != "VMess" || u.Network != "ws" || u.Security != "TLS" || u.Name != "URL form" {
		t.Fatalf("url form %+v", u)
	}
	// Shadowrocket
	sr := mustParse(t, "vmess://"+base64.StdEncoding.EncodeToString([]byte("auto:"+testUUID+"@s.example.com:443"))+"?remarks=SR&obfs=websocket&tls=1&path=/ws&obfsParam=s.example.com")
	if sr.Name != "SR" || sr.Network != "ws" || sr.Security != "TLS" {
		t.Fatalf("shadowrocket %+v", sr)
	}
	// odd values from generators
	odd := mustParse(t, vmessLink(map[string]any{"add": "o.example.com", "port": "443.0", "id": testUUID, "scy": "aes-256-cfb", "tls": "tls", "allowInsecure": true}))
	if odd.Port != 443 || odd.Outbound["security"] != "auto" || odd.Outbound["tls"].(map[string]any)["insecure"] != true {
		t.Fatalf("odd %s", outboundJSON(odd))
	}
	buildAndCheck(t, []Node{kcp, u, sr, odd}, DefaultSettings())
}

func TestXrayImportsAreChecked(t *testing.T) {
	// "allowInsecure" was removed from Xray and an unknown fingerprint is
	// rejected: imported configs are fixed instead of breaking the core
	body := `{"remarks":"Old","outbounds":[{"tag":"proxy","protocol":"vless","settings":{"vnext":[{"address":"o.example.com","port":443,"users":[{"id":"` + testUUID + `","encryption":"none"}]}]},
	 "streamSettings":{"network":"xhttp","security":"tls","tlsSettings":{"serverName":"o.example.com","allowInsecure":true,"fingerprint":"hellofoo"},"xhttpSettings":{"path":"/x"}}}]}`
	res := ParseText(body)
	if len(res.Nodes) != 1 {
		t.Fatalf("old config: %v", res.Errors)
	}
	if js := outboundJSON(res.Nodes[0]); strings.Contains(js, "allowInsecure") || !strings.Contains(js, `"fingerprint":"chrome"`) {
		t.Fatalf("not sanitized: %s", js)
	}
	// a broken config is reported, not stored
	broken := `{"remarks":"Broken","outbounds":[{"tag":"proxy","protocol":"vless","settings":{"vnext":[{"address":"b.example.com","port":443,"users":[{"id":"` + testUUID + `"}]}]},
	 "streamSettings":{"network":"tcp","sockopt":{"dialerProxy":"missing"}}}]}`
	if res := ParseText(broken); len(res.Nodes) != 0 || len(res.Errors) != 1 {
		t.Fatalf("broken config: %+v", res)
	}
	x := mustParse(t, "vless://"+testUUID+"@x.example.com:443?type=xhttp&security=tls&sni=x.example.com&fp=hellofoo")
	if !strings.Contains(outboundJSON(x), `"fingerprint":"chrome"`) {
		t.Fatalf("xray fp %s", outboundJSON(x))
	}
	// sing-box JSON with a field sing-box doesn't know
	if res := ParseText(`{"outbounds":[{"type":"vless","tag":"a","server":"a.example.com","server_port":443,"uuid":"` + testUUID + `","bogus_field":1}]}`); len(res.Nodes) != 0 {
		t.Fatalf("invalid sing-box outbound stored: %+v", res.Nodes)
	}
	buildAndCheck(t, append(res.Nodes, x), DefaultSettings())
}

func TestClashMore(t *testing.T) {
	yamlText := `proxies:
  - {name: Socks, type: socks5, server: 1.2.3.4, port: 1080, username: u, password: p}
  - {name: HTTPS, type: http, server: h.example.com, port: 443, tls: true, skip-cert-verify: true}
  - {name: AnyTLS, type: anytls, server: a.example.com, port: 443, password: pw, client-fingerprint: chrome}
  - {name: Hop, type: hysteria2, server: h.example.com, port: 443, ports: "20000-30000", password: pw}
  - {name: V2, type: ss, server: 5.6.7.8, port: 443, cipher: aes-128-gcm, password: p, plugin: v2ray-plugin, plugin-opts: {mode: websocket, tls: true, host: cdn.example.com, path: /ws}}
  - {name: VM, type: vmess, server: v.example.com, port: 443, uuid: ` + testUUID + `, alterId: 0, cipher: auto, tls: true, skip-cert-verify: true, network: ws, ws-opts: {path: /v, headers: {host: v.example.com}}}
`
	res := ParseText(yamlText)
	if len(res.Nodes) != 6 || len(res.Errors) != 0 {
		t.Fatalf("nodes %d errors %v", len(res.Nodes), res.Errors)
	}
	byName := map[string]Node{}
	for _, n := range res.Nodes {
		byName[n.Name] = n
	}
	if byName["Socks"].Protocol != "SOCKS" || byName["HTTPS"].Security != "TLS" || byName["AnyTLS"].Protocol != "AnyTLS" {
		t.Fatalf("plain proxies %+v", res.Nodes[:3])
	}
	if fmt.Sprint(byName["Hop"].Outbound["server_ports"]) != "[443:443 20000:30000]" {
		t.Fatalf("hop %s", outboundJSON(byName["Hop"]))
	}
	if byName["V2"].Outbound["plugin"] != "v2ray-plugin" || !strings.Contains(byName["V2"].Outbound["plugin_opts"].(string), "host=cdn.example.com") {
		t.Fatalf("v2ray-plugin %s", outboundJSON(byName["V2"]))
	}
	if !strings.Contains(outboundJSON(byName["VM"]), `"Host":"v.example.com"`) {
		t.Fatalf("vmess host %s", outboundJSON(byName["VM"]))
	}
	buildAndCheck(t, res.Nodes, DefaultSettings())
}

func TestDetectImportHeuristics(t *testing.T) {
	yes := []string{
		"https://panel.example.com/sub/AbCdEf123456",
		"https://example.com:2096/sub/k3n2l1m0p9o8i7u6",
		"https://vpn.example.com/AbCdEf12345GhIjK",
		"https://host.example/api/v1/client/subscribe?token=0123456789abcdef0123456789abcdef",
		"https://h.example.com/9f8a1b2c-3d4e-5f60-7182-93a4b5c6d7e8/",
		"«https://panel.example.com/sub/AbCdEf123456»",
		"Ваша подписка: https://sub.example.net/s1/xyz12345.",
		"https://raw.githubusercontent.com/someone/free/main/v2ray/configs.txt",
	}
	for _, s := range yes {
		if r := DetectImport(s); len(r.Subscriptions) != 1 || strings.ContainsAny(r.Subscriptions[0], "«».") && strings.HasSuffix(r.Subscriptions[0], ".") {
			t.Errorf("missed %q: %+v", s, r)
		}
	}
	no := []string{
		"https://www.google.com/",
		"https://example.substack.com/p/my-article-title",
		"https://www.youtube.com/feed/subscriptions",
		"https://github.com/user/repo/commit/0123abcd4567ef890123abcd4567ef8901234567",
		"https://example.com/blog/post-2024-01-15-something-long",
		"https://shop.example.com/s/Summer2024",
		"https://news.example.com/article?utm_campaign=summer_sale_2026_x1",
		"https://youtu.be/dQw4w9WgXcQ?si=AbCdEfGh12345678",
		"https://docs.example.com/guides/getting-started",
		"https://raw.githubusercontent.com/someone/project/main/README.md",
	}
	for _, s := range no {
		if r := DetectImport(s); !r.Empty() {
			t.Errorf("false positive %q: %+v", s, r)
		}
	}
	// app links keep the server name and accept base64 bodies
	req, err := ParseDeepLink("vela://import/vless://" + testUUID + "@a.example.com:443?type=tcp#My%20Server")
	if err != nil || req.LinkCount != 1 || !strings.HasSuffix(req.Links, "#My%20Server") {
		t.Fatalf("server name lost: %v %+v", err, req)
	}
	sub := "https://panel.example.com/sub/AbCdEf123456"
	if req, err := ParseDeepLink("streisand://" + base64.StdEncoding.EncodeToString([]byte(sub))); err != nil || len(req.Subscriptions) != 1 || req.Subscriptions[0] != sub {
		t.Fatalf("base64 app link: %v %+v", err, req)
	}
	if r := DetectImport(" \"happ://add/" + sub + "\" "); len(r.Subscriptions) != 1 {
		t.Fatalf("quoted app link: %+v", r)
	}
	subs, links := SplitImport("(" + sub + "), vless://" + testUUID + "@a.example.com:443?type=tcp#Name With Spaces")
	if len(subs) != 1 || subs[0] != sub || !strings.HasSuffix(links, "#Name With Spaces") {
		t.Fatalf("split %v %q", subs, links)
	}
}

func TestFetchSubscriptionDetails(t *testing.T) {
	links := strings.Join(sampleLinks[:2], "\n")
	var open atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big":
			w.Write([]byte(strings.Repeat("#", maxSubscriptionSize+10)))
			return
		case "/gone":
			w.WriteHeader(404)
			return
		}
		w.Header().Set("profile-title", "C++ VPN")
		w.Header().Set("announce", "base64:"+base64.RawStdEncoding.EncodeToString([]byte("Привет")))
		w.Header().Set("support-url", "javascript:alert(1)")
		w.Header().Set("profile-web-page-url", "https://example.com/cabinet")
		w.Header().Set("subscription-userinfo", "upload = 10; download=20; total=1e3; expire=1767225600000")
		w.Header().Set("profile-update-interval", "-5")
		w.WriteHeader(http.StatusNonAuthoritativeInfo)
		w.Write([]byte(links))
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		switch s {
		case http.StateNew:
			open.Add(1)
		case http.StateClosed, http.StateHijacked:
			open.Add(-1)
		}
	}
	srv.Start()
	defer srv.Close()
	sub, err := FetchSubscription(context.Background(), srv.URL+"/sub")
	if err != nil {
		t.Fatal(err)
	}
	if sub.Title != "C++ VPN" || sub.Announce != "Привет" || sub.SupportURL != "" || sub.WebPageURL == "" {
		t.Fatalf("headers %+v", sub)
	}
	if sub.Upload != 10 || sub.Total != 1000 || sub.Expire != 1767225600 || sub.UpdateInterval != 0 {
		t.Fatalf("userinfo %+v", sub)
	}
	// the connection is not left open after the download
	if !waitFor(3*time.Second, func() bool { return open.Load() == 0 }) {
		t.Fatalf("%d connections left open", open.Load())
	}
	if _, err := FetchSubscription(context.Background(), srv.URL+"/big"); err == nil || !strings.Contains(err.Error(), "слишком большая") {
		t.Fatalf("big: %v", err)
	}
	if _, err := FetchSubscription(context.Background(), srv.URL+"/gone"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("404: %v", err)
	}
	// the message explains the failure and doesn't repeat the secret URL
	_, err = FetchSubscription(context.Background(), fmt.Sprintf("http://127.0.0.1:%d/sub/SECRETTOKEN", freePort(t)))
	if err == nil || strings.Contains(err.Error(), "SECRETTOKEN") || !strings.Contains(err.Error(), "порт сервера закрыт") {
		t.Fatalf("network error: %v", err)
	}
}

func TestEncryptionRequirement(t *testing.T) {
	cases := map[string]bool{
		"vless://" + testUUID + "@a.example.com:443?type=tcp&security=none":                 false,
		"vless://" + testUUID + "@a.example.com:443?type=ws&security=tls&sni=a.example.com": true,
		sampleLinks[0]: true, // Reality
		"trojan://pw@t.example.com:443?security=none":                                                                                                             false,
		"trojan://pw@t.example.com:443":                                                                                                                           true,
		vmessLink(map[string]any{"add": "v.example.com", "port": 443, "id": testUUID, "scy": "none"}):                                                             false,
		vmessLink(map[string]any{"add": "v.example.com", "port": 443, "id": testUUID, "scy": "zero"}):                                                             false,
		vmessLink(map[string]any{"add": "v.example.com", "port": 443, "id": testUUID}):                                                                            true,
		vmessLink(map[string]any{"add": "v.example.com", "port": 443, "id": testUUID, "scy": "none", "tls": "tls"}):                                               true,
		"ss://" + base64.RawURLEncoding.EncodeToString([]byte("none:x")) + "@5.6.7.8:1":                                                                           false,
		"ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:x")) + "@5.6.7.8:1":                                                                    true,
		"hysteria2://pw@h.example.com:443":                                                                                                                        true,
		"vless://" + testUUID + "@x.example.com:443?type=xhttp&security=none":                                                                                     false,
		"vless://" + testUUID + "@x.example.com:443?type=xhttp&security=tls&sni=x.example.com":                                                                    true,
		"vless://" + testUUID + "@e.example.com:443?type=tcp&security=none&encryption=mlkem768x25519plus.native.0rtt.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA": true,
	}
	var nodes []Node
	encrypted := 0
	for link, want := range cases {
		n := mustParse(t, link)
		if got := IsEncrypted(n); got != want {
			t.Errorf("%s: encrypted=%v, want %v", link, got, want)
		}
		nodes = append(nodes, n)
		if want {
			encrypted++
		}
	}
	socks := ParseText("proxies:\n  - {name: S, type: socks5, server: 1.2.3.4, port: 1080}\n").Nodes
	if len(socks) != 1 || IsEncrypted(socks[0]) {
		t.Fatalf("socks %+v", socks)
	}
	nodes = append(nodes, socks...)
	s := DefaultSettings()
	s.RequireEncryption = true
	cfg := buildAndCheck(t, nodes, s)
	if got := strings.Count(cfg, `"tag": "node-`); got != encrypted {
		t.Fatalf("%d servers in config, want %d", got, encrypted)
	}
	_, err := BuildConfig(BuildRequest{Platform: PlatformAndroid, Nodes: socks, Settings: s, DataDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "шифрованием") {
		t.Fatalf("all unencrypted: %v", err)
	}
}

func TestConfigDetails(t *testing.T) {
	nodes := ParseText(strings.Join(sampleLinks, "\n")).Nodes
	// a stored server whose link no longer parses is left out, not used with
	// its old outbound (which could break the whole core)
	stale := Node{ID: "stale", Link: "vless://x@h.example.com:1?type=quantum", Outbound: map[string]any{"type": "vless", "bogus": true}}
	s := DefaultSettings()
	s.IPv6 = false
	s.RemoteDNS = "tls://[2606:4700:4700::1111]:853"
	s.DirectDNS = "udp://77.88.8.8:53"
	s.ProxyPort = 99999
	s.Rules = []RoutingRule{{Match: "Example.com:443", Action: "proxy"}, {Match: "10.0.0.0/8", Action: "proxy"}, {Match: "local.example", Action: "direct"}}
	cfg, err := BuildConfig(BuildRequest{Platform: PlatformAndroid, Nodes: append([]Node{stale}, nodes...), Selected: "stale",
		Settings: s, DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cfg, "node-stale") || strings.Contains(cfg, "bogus") {
		t.Fatal("stale server kept")
	}
	var c struct {
		DNS struct {
			Servers []map[string]any `json:"servers"`
			Rules   []map[string]any `json:"rules"`
		} `json:"dns"`
		Inbounds []map[string]any `json:"inbounds"`
		Route    struct {
			Rules []map[string]any `json:"rules"`
		} `json:"route"`
	}
	json.Unmarshal([]byte(cfg), &c)
	if c.DNS.Servers[0]["type"] != "tls" || c.DNS.Servers[0]["server"] != "2606:4700:4700::1111" || c.DNS.Servers[0]["server_port"] != float64(853) {
		t.Fatalf("remote dns %v", c.DNS.Servers[0])
	}
	if c.DNS.Servers[1]["type"] != "udp" || c.DNS.Servers[1]["server"] != "77.88.8.8" {
		t.Fatalf("direct dns %v", c.DNS.Servers[1])
	}
	if c.DNS.Rules[0]["server"] != "dns-remote" || fmt.Sprint(c.DNS.Rules[0]["domain_suffix"]) != "[example.com]" || c.DNS.Rules[1]["server"] != "dns-direct" {
		t.Fatalf("user dns rules %v", c.DNS.Rules)
	}
	// IPv6 off on a phone: captured and refused, not sent past the VPN
	if fmt.Sprint(c.Inbounds[0]["address"]) != "[172.19.0.1/30 fdfe:dcba:9876::1/126]" || c.Route.Rules[2]["ip_version"] != float64(6) {
		t.Fatalf("ipv6 off: %v %v", c.Inbounds[0]["address"], c.Route.Rules[2])
	}
	// user rules come before the local network rule
	if fmt.Sprint(c.Route.Rules[4]["ip_cidr"]) != "[10.0.0.0/8]" || c.Route.Rules[6]["ip_is_private"] != true {
		t.Fatalf("rule order %v", c.Route.Rules)
	}
	checkConfig(t, strings.Replace(cfg, `"override_android_vpn": true`, `"override_android_vpn": false`, 1))
	// broken DNS settings fall back to the defaults instead of breaking the core
	for _, bad := range []string{"ftp://x", "https://", "1.1.1.1:99999", "local"} {
		s := DefaultSettings()
		s.RemoteDNS, s.DirectDNS = bad, bad
		cfg := buildAndCheck(t, nodes, s)
		if bad != "local" && !strings.Contains(cfg, "77.88.8.8") {
			t.Fatalf("%s: no fallback", bad)
		}
	}
	if _, ok := dnsServer("x", "h3://dns.google", "udp"); !ok {
		t.Fatal("h3")
	}
	if srv, _ := dnsServer("x", "https://dns.example/custom/path", "udp"); srv["path"] != "/custom/path" {
		t.Fatalf("path %v", srv)
	}
}

// startBoxCtx is StartBox that also returns the context with the services.
func startBoxCtx(t *testing.T, cfg string) (*box.Box, context.Context) {
	t.Helper()
	ctx := include.Context(service.ContextWith(context.Background(), deprecated.NewStderrManager(log.StdLogger())))
	opts, err := sjson.UnmarshalExtendedContext[option.Options](ctx, []byte(cfg))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(service.ExtendContext(ctx))
	t.Cleanup(cancel)
	b, err := box.New(box.Options{Context: ctx, Options: opts})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b, ctx
}

// countingDNS answers every A query with 10.9.8.7 and counts the names asked.
func countingDNS(t *testing.T) (addr string, asked func() []string) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var names []string
	srv := &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		for _, q := range r.Question {
			mu.Lock()
			names = append(names, strings.TrimSuffix(q.Name, "."))
			mu.Unlock()
			if q.Qtype == dns.TypeA {
				rr, _ := dns.NewRR(q.Name + " 60 IN A 10.9.8.7")
				m.Answer = append(m.Answer, rr)
			}
		}
		w.WriteMsg(m)
	})}
	go srv.ActivateAndServe()
	t.Cleanup(func() { srv.Shutdown() })
	return pc.LocalAddr().String(), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string{}, names...)
	}
}

// TestBlockedModeDNS: in "blocked" mode a blocked site is resolved through
// the tunnel (remote DNS), anything else only by the local DNS server.
func TestBlockedModeDNS(t *testing.T) {
	remoteAddr, remoteAsked := countingDNS(t)
	directAddr, directAsked := countingDNS(t)
	// the "VPN server": a local Shadowsocks server whose traffic leaves directly
	port := freePort(t)
	server, _ := json.Marshal(map[string]any{
		"log":       map[string]any{"level": "error"},
		"inbounds":  []any{map[string]any{"type": "shadowsocks", "listen": "127.0.0.1", "listen_port": port, "method": "aes-128-gcm", "password": "pw"}},
		"outbounds": []any{map[string]any{"type": "direct"}},
	})
	startBoxJSON(t, string(server))
	node := mustParse(t, fmt.Sprintf("ss://%s@127.0.0.1:%d#local", base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:pw")), port))
	s := DefaultSettings()
	s.Mode = "local"
	s.ProxyPort = freePort(t)
	s.Routing = "blocked"
	s.RemoteDNS = "udp://" + remoteAddr
	s.DirectDNS = directAddr
	s.Rules = []RoutingRule{{Match: "my-blocked.example", Action: "proxy"}}
	cfg, err := BuildConfig(BuildRequest{Platform: PlatformLinux, Nodes: []Node{node}, Selected: node.ID, Settings: s, DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	b, ctx := startBoxCtx(t, cfg)
	_ = b
	router := service.FromContext[adapter.DNSRouter](ctx)
	lookup := func(domain string) []netip.Addr {
		qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		qctx, _ = adapter.ExtendContext(qctx)
		addrs, err := router.Lookup(qctx, domain, adapter.DNSQueryOptions{})
		if err != nil {
			t.Fatalf("%s: %v", domain, err)
		}
		return addrs
	}
	has := func(list []string, name string) bool {
		for _, n := range list {
			if n == name {
				return true
			}
		}
		return false
	}
	for _, d := range []string{"rutracker.org", "chatgpt.com", "my-blocked.example"} {
		lookup(d)
		if !has(remoteAsked(), d) || has(directAsked(), d) {
			t.Errorf("%s: remote %v direct %v", d, remoteAsked(), directAsked())
		}
	}
	for _, d := range []string{"example.org", "yandex.ru"} {
		lookup(d)
		if has(remoteAsked(), d) || !has(directAsked(), d) {
			t.Errorf("%s must be resolved locally only: remote %v direct %v", d, remoteAsked(), directAsked())
		}
	}
}

func TestProbeSurvivesBrokenServers(t *testing.T) {
	closed := freePort(t)
	good := mustParse(t, fmt.Sprintf("vless://%s@127.0.0.1:%d?type=tcp&security=none#closed", testUUID, closed))
	dup := good // the same id twice (saved by an older version)
	broken := Node{ID: "broken", Name: "broken", Server: "127.0.0.1", Outbound: map[string]any{
		"type": "vless", "server": "127.0.0.1", "server_port": closed, "uuid": testUUID,
		"tls": map[string]any{"enabled": true, "utls": map[string]any{"enabled": true, "fingerprint": "bogus"}}}}
	base := runtime.NumGoroutine()
	var mu sync.Mutex
	got := map[string]ProbeResult{}
	err := ProbeNodes(context.Background(), nil, []Node{good, dup, broken}, 2*time.Second, 4, func(r ProbeResult) {
		mu.Lock()
		got[r.ID] = r
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	if got[good.ID].Reason != "порт сервера закрыт" || got["broken"].Reason != "сервер не удалось разобрать" {
		t.Fatalf("results %+v", got)
	}
	// the temporary core is gone with its goroutines
	if !waitFor(5*time.Second, func() bool { return runtime.NumGoroutine() <= base+3 }) {
		t.Fatalf("goroutines %d -> %d", base, runtime.NumGoroutine())
	}
	// stopping reports nothing more and returns quickly
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if err := ProbeNodes(ctx, nil, []Node{good}, time.Second, 1, func(r ProbeResult) { t.Errorf("result after stop: %+v", r) }); err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("stop: %v after %v", err, time.Since(start))
	}
}

// fakeController serves /connections like the core's Clash API.
func fakeController(t *testing.T, body func() string) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body())) })}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return l.Addr().(*net.TCPAddr).Port
}

func TestTrafficRecorderAndFile(t *testing.T) {
	var up, down atomic.Int64
	up.Store(1000)
	down.Store(2000)
	port := fakeController(t, func() string {
		return fmt.Sprintf(`{"uploadTotal":%d,"downloadTotal":%d,"connections":[{"id":"1","metadata":{"network":"tcp","destinationIP":"2001:db8::1","destinationPort":"443","host":""},"chains":["node-x","auto","proxy"],"start":"2026-10-11T10:00:00Z"}]}`, up.Load(), down.Load())
	})
	path := filepath.Join(t.TempDir(), "traffic.json")
	// started while the core already ran: earlier traffic is not counted again
	r := StartTrafficRecorder(port, "", path)
	time.Sleep(300 * time.Millisecond)
	up.Add(500)
	down.Add(600)
	r.Stop()
	r.Stop()
	if s := LoadTrafficSummary(path, time.Now()); s.Today.Up != 500 || s.Today.Down != 600 {
		t.Fatalf("today %+v", s.Today)
	}
	list, err := ListConnections(port, "")
	if err != nil || len(list) != 1 || list[0].Destination != "[2001:db8::1]:443" || list[0].Host != "2001:db8::1" || list[0].Outbound != "node-x" {
		t.Fatalf("connections %+v %v", list, err)
	}
	// a damaged file is kept aside, statistics continue
	os.WriteFile(path, []byte("{garbage"), 0o644)
	if err := AddTraffic(path, 1, 2, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".damaged"); err != nil {
		t.Fatal("damaged file not kept")
	}
	if s := LoadTrafficSummary(path, time.Now()); s.Today.Down != 2 {
		t.Fatalf("after damage %+v", s.Today)
	}
	// concurrent writers don't lose updates
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			AddTraffic(path, 1, 1, time.Now())
		}()
	}
	wg.Wait()
	if s := LoadTrafficSummary(path, time.Now()); s.Today.Up != 21 {
		t.Fatalf("concurrent %+v", s.Today)
	}
}

func TestToolsDetails(t *testing.T) {
	if _, err := EncodeQR(strings.Repeat("x", 5000), 256); err == nil || !strings.Contains(err.Error(), "QR") {
		t.Fatalf("long QR: %v", err)
	}
	// a backup pasted from a messenger with line breaks
	text, _ := EncodeBackup(Backup{Links: []string{sampleLinks[0]}})
	broken := text[:40] + "\n" + text[40:80] + " \r\n" + text[80:]
	if b, ok, err := DecodeBackup(broken); !ok || err != nil || len(b.Links) != 1 {
		t.Fatalf("backup %v %v", ok, err)
	}
	// the cut first line of a long log is skipped
	path := filepath.Join(t.TempDir(), "log.txt")
	line := "+0300 2026-10-08 22:27:49 INFO router: " + strings.Repeat("y", 100) + "\n"
	os.WriteFile(path, []byte(strings.Repeat(line, 6000)), 0o644)
	lines := ReadLog(path, 0)
	if len(lines) != 300 || !strings.HasPrefix(lines[0], "22:27:49 INFO") {
		t.Fatalf("log %d %q", len(lines), lines[0])
	}
}
