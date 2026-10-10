package core

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

const xhttpLink = "vless://11111111-2222-3333-4444-555555555555@x.example.com:443?type=xhttp&security=reality&pbk=Q1H2rOvf0Rc3uLYZKYDkX0aWbUoT5r5JbQf0Yb7cS2c&sid=6ba8&sni=www.microsoft.com&fp=chrome&path=%2Fxh&mode=packet-up&extra=%7B%22xPaddingBytes%22%3A%22100-1000%22%7D#XHTTP"

func TestXHTTPRunsOnXray(t *testing.T) {
	res := ParseText(xhttpLink + "\nvless://11111111-2222-3333-4444-555555555555@k.example.com:1000?type=kcp&seed=abc&security=none#KCP\nvless://11111111-2222-3333-4444-555555555555@e.example.com:443?type=tcp&security=tls&encryption=mlkem768x25519plus.native.0rtt.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA#ENC")
	if len(res.Nodes) != 3 {
		t.Fatalf("nodes %d errors %v", len(res.Nodes), res.Errors)
	}
	n := res.Nodes[0]
	if n.Engine != EngineXray || n.Outbound["type"] != "xray" || n.Network != "xhttp" || n.Security != "Reality" {
		t.Fatalf("bad node %+v", n)
	}
	data, _ := json.Marshal(n.Outbound)
	for _, want := range []string{`"network":"xhttp"`, `"mode":"packet-up"`, `"publicKey":"Q1H2`, `"xPaddingBytes":"100-1000"`, `"serverName":"www.microsoft.com"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing %s in %s", want, data)
		}
	}
	cfg, err := BuildConfig(BuildRequest{Platform: PlatformLinux, Nodes: append(res.Nodes, ParseText(sampleLinks[0]).Nodes...),
		Selected: n.ID, Settings: DefaultSettings(), DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	checkConfig(t, cfg)
}

func TestClashYAML(t *testing.T) {
	yamlText := `
mixed-port: 7890
proxies:
  - name: "RU Reality"
    type: vless
    server: r.example.com
    port: 443
    uuid: 11111111-2222-3333-4444-555555555555
    network: tcp
    tls: true
    flow: xtls-rprx-vision
    servername: www.microsoft.com
    client-fingerprint: chrome
    reality-opts:
      public-key: Q1H2rOvf0Rc3uLYZKYDkX0aWbUoT5r5JbQf0Yb7cS2c
      short-id: 6ba8
  - name: VMess WS
    type: vmess
    server: v.example.com
    port: 443
    uuid: 11111111-2222-3333-4444-555555555555
    alterId: 0
    cipher: auto
    tls: true
    network: ws
    ws-opts:
      path: /vm
      headers:
        Host: v.example.com
  - {name: Trojan, type: trojan, server: t.example.com, port: 443, password: "p@ss", sni: t.example.com}
  - {name: SS, type: ss, server: 5.6.7.8, port: 8388, cipher: aes-256-gcm, password: secret}
  - {name: Hy2, type: hysteria2, server: h.example.com, port: 443, password: pw, obfs: salamander, obfs-password: x}
  - {name: Obfs, type: ss, server: 5.6.7.8, port: 1, cipher: aes-256-gcm, password: s, plugin: obfs}
proxy-groups:
  - {name: PROXY, type: select, proxies: [RU Reality]}
`
	res := ParseText(yamlText)
	if len(res.Nodes) != 5 || len(res.Errors) != 1 {
		t.Fatalf("nodes %d errors %v", len(res.Nodes), res.Errors)
	}
	if res.Nodes[0].Name != "RU Reality" || res.Nodes[0].Security != "Reality" || res.Nodes[1].Network != "ws" {
		t.Fatalf("bad nodes %+v", res.Nodes[:2])
	}
	cfg, err := BuildConfig(BuildRequest{Platform: PlatformLinux, Nodes: res.Nodes, Selected: "auto",
		Settings: DefaultSettings(), DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	checkConfig(t, cfg)
}

func TestSingBoxJSON(t *testing.T) {
	body := `{"log":{},"outbounds":[
	 {"type":"selector","tag":"select","outbounds":["a","b"]},
	 {"type":"vless","tag":"a","server":"a.example.com","server_port":443,"uuid":"11111111-2222-3333-4444-555555555555","tls":{"enabled":true,"server_name":"a.example.com"},"transport":{"type":"ws","path":"/w"}},
	 {"type":"shadowsocks","tag":"b","server":"1.2.3.4","server_port":8388,"method":"aes-128-gcm","password":"x"},
	 {"type":"direct","tag":"direct"}]}`
	res := ParseText(body)
	if len(res.Nodes) != 2 {
		t.Fatalf("nodes %d errors %v", len(res.Nodes), res.Errors)
	}
	a := res.Nodes[0]
	if a.Name != "a" || a.Network != "ws" || a.Security != "TLS" || !strings.HasPrefix(a.Link, "velajson://") {
		t.Fatalf("bad node %+v", a)
	}
	// the stored line reads back the same server
	again, err := ParseLink(a.Link)
	if err != nil || again.ID != a.ID || again.Outbound["server"] != "a.example.com" {
		t.Fatalf("roundtrip %v %+v", err, again)
	}
	cfg, err := BuildConfig(BuildRequest{Platform: PlatformLinux, Nodes: res.Nodes, Selected: a.ID,
		Settings: DefaultSettings(), DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	checkConfig(t, cfg)
}

func TestXrayJSON(t *testing.T) {
	body := `[{"remarks":"NL XHTTP","outbounds":[
	  {"tag":"proxy","protocol":"vless","settings":{"vnext":[{"address":"nl.example.com","port":443,"users":[{"id":"11111111-2222-3333-4444-555555555555","encryption":"none"}]}]},
	   "streamSettings":{"network":"xhttp","security":"tls","tlsSettings":{"serverName":"nl.example.com"},"xhttpSettings":{"path":"/x"},"sockopt":{"dialerProxy":"fragment"}}},
	  {"tag":"fragment","protocol":"freedom","settings":{"fragment":{"packets":"tlshello","length":"100-200","interval":"10-20"}}},
	  {"tag":"direct","protocol":"freedom"},
	  {"tag":"block","protocol":"blackhole"}],
	 "routing":{"rules":[]}},
	 {"remarks":"Only direct","outbounds":[{"tag":"direct","protocol":"freedom"}]}]`
	res := ParseText(body)
	if len(res.Nodes) != 1 || len(res.Errors) != 1 {
		t.Fatalf("nodes %d errors %v", len(res.Nodes), res.Errors)
	}
	n := res.Nodes[0]
	if n.Name != "NL XHTTP" || n.Server != "nl.example.com" || n.Port != 443 || n.Engine != EngineXray || n.Protocol != "VLESS" {
		t.Fatalf("bad node %+v", n)
	}
	cfg, err := BuildConfig(BuildRequest{Platform: PlatformLinux, Nodes: res.Nodes, Selected: n.ID,
		Settings: DefaultSettings(), DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	checkConfig(t, cfg)
}

func TestDeepLinks(t *testing.T) {
	sub := "https://panel.example.com/sub/AbCdEf123456?x=1"
	enc := url.QueryEscape(sub)
	cases := map[string]string{
		"vela://import/" + sub + "#My%20VPN":                      sub,
		"happ://add/" + sub:                                       sub,
		"v2rayng://install-config?url=" + enc + "&name=My":        sub,
		"v2rayn://install-config?url=" + enc:                      sub,
		"hiddify://import/" + sub + "#Name":                       sub,
		"sing-box://import-remote-profile?url=" + enc + "#Name":   sub,
		"clash://install-config?url=" + enc + "&name=X":           sub,
		"streisand://import/" + sub:                               sub,
		"v2box://install-sub?url=" + enc + "&name=Y":              sub,
		"sub://" + base64.StdEncoding.EncodeToString([]byte(sub)): sub,
	}
	for link, want := range cases {
		req, err := ParseDeepLink(link)
		if err != nil {
			t.Fatalf("%s: %v", link, err)
		}
		if len(req.Subscriptions) != 1 || req.Subscriptions[0] != want {
			t.Fatalf("%s: got %+v", link, req)
		}
	}
	req, _ := ParseDeepLink("vela://import/https://a.example/s#My%20VPN")
	if req.Name != "My VPN" {
		t.Fatalf("name %q", req.Name)
	}
	req, err := ParseDeepLink("hiddify://import/" + url.QueryEscape(sampleLinks[3]))
	if err != nil || req.LinkCount != 1 {
		t.Fatalf("single server: %v %+v", err, req)
	}
	if _, err := ParseDeepLink("happ://crypt3/abcdef"); err == nil {
		t.Fatal("crypt link accepted")
	}
}

func TestDetectImport(t *testing.T) {
	if r := DetectImport("https://www.google.com/"); !r.Empty() {
		t.Fatalf("plain site detected: %+v", r)
	}
	if r := DetectImport("just some text"); !r.Empty() {
		t.Fatal("text detected")
	}
	if r := DetectImport("https://panel.example.com/api/sub/AbCdEfGh12345"); len(r.Subscriptions) != 1 {
		t.Fatalf("subscription missed: %+v", r)
	}
	if r := DetectImport(strings.Join(sampleLinks[:3], "\n")); r.LinkCount != 3 {
		t.Fatalf("links missed: %+v", r)
	}
	if r := DetectImport("happ://add/https://panel.example.com/s/abc"); len(r.Subscriptions) != 1 {
		t.Fatalf("app link missed: %+v", r)
	}
	b64 := base64.StdEncoding.EncodeToString([]byte(strings.Join(sampleLinks[:2], "\n")))
	if r := DetectImport(b64); r.LinkCount != 2 {
		t.Fatalf("base64 missed: %+v", r)
	}
}

func TestDirectives(t *testing.T) {
	body := "#profile-title: base64:" + base64.StdEncoding.EncodeToString([]byte("Мой VPN")) +
		"\n#announce: Оплатите до 1 ноября\n#support-url: https://t.me/support\n" + strings.Join(sampleLinks[:2], "\n")
	text, d := extractDirectives(body)
	if d["announce"] != "Оплатите до 1 ноября" || d["support-url"] != "https://t.me/support" || strings.Contains(text, "#announce") {
		t.Fatalf("directives %v text %q", d, text)
	}
	if decodeHeaderText(d["profile-title"]) != "Мой VPN" {
		t.Fatal("title")
	}
	// inside a base64 body too
	_, d = extractDirectives(base64.StdEncoding.EncodeToString([]byte(body)))
	if d["announce"] == "" {
		t.Fatal("base64 directives")
	}
	res := ParseText(body)
	if len(res.Nodes) != 2 || len(res.Errors) != 0 {
		t.Fatalf("comment lines broke parsing: %v", res.Errors)
	}
}

func TestEnglish(t *testing.T) {
	defer SetLanguage("ru")
	SetLanguage("en")
	_, err := ParseLink("vless://@h:1")
	if err == nil || !strings.Contains(err.Error(), "no UUID") {
		t.Fatalf("got %v", err)
	}
	SetLanguage("ru-RU")
	if T("нет UUID") != "нет UUID" {
		t.Fatal("ru")
	}
}
