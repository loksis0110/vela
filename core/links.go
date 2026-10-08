// Package core parses share links and subscriptions and builds sing-box
// configurations. It is shared by the desktop app and the mobile apps
// (through gomobile), so every platform behaves the same way.
package core

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Node is one server parsed from a share link.
type Node struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Protocol string         `json:"protocol"`
	Server   string         `json:"server"`
	Port     int            `json:"port"`
	Security string         `json:"security,omitempty"`
	Network  string         `json:"network,omitempty"`
	Link     string         `json:"link"`
	Outbound map[string]any `json:"outbound"`
}

// ParseResult is returned by ParseText.
type ParseResult struct {
	Nodes  []Node   `json:"nodes"`
	Errors []string `json:"errors"`
}

// ParseText accepts any text: a single link, many links separated by new
// lines, or a base64-encoded subscription body.
func ParseText(text string) ParseResult {
	text = strings.TrimSpace(strings.TrimPrefix(text, "\ufeff"))
	if !strings.Contains(text, "://") {
		if decoded, ok := tryBase64(text); ok && strings.Contains(decoded, "://") {
			text = decoded
		}
	}
	var res ParseResult
	seen := map[string]int{}
	for _, line := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' || r == ' ' }) {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "://") {
			continue
		}
		node, err := ParseLink(line)
		if err != nil {
			res.Errors = append(res.Errors, err.Error())
			continue
		}
		// keep names unique, they are shown in UI lists
		if n := seen[node.Name]; n > 0 {
			seen[node.Name] = n + 1
			node.Name = fmt.Sprintf("%s (%d)", node.Name, n+1)
		} else {
			seen[node.Name] = 1
		}
		res.Nodes = append(res.Nodes, node)
	}
	if res.Nodes == nil {
		res.Nodes = []Node{}
	}
	if res.Errors == nil {
		res.Errors = []string{}
	}
	return res
}

// ParseLink parses a single share link.
func ParseLink(link string) (Node, error) {
	scheme, _, ok := strings.Cut(link, "://")
	if !ok {
		return Node{}, fmt.Errorf("не ссылка: %s", short(link))
	}
	var (
		node Node
		err  error
	)
	switch strings.ToLower(scheme) {
	case "vless":
		node, err = parseVLESS(link)
	case "vmess":
		node, err = parseVMess(link)
	case "trojan":
		node, err = parseTrojan(link)
	case "ss":
		node, err = parseShadowsocks(link)
	case "hysteria2", "hy2":
		node, err = parseHysteria2(link)
	case "tuic":
		node, err = parseTUIC(link)
	default:
		return Node{}, fmt.Errorf("протокол %s не поддерживается", scheme)
	}
	if err != nil {
		return Node{}, fmt.Errorf("%s: %w", short(link), err)
	}
	node.Link = link
	node.ID = shortHash(link)
	if node.Name == "" {
		node.Name = net.JoinHostPort(node.Server, strconv.Itoa(node.Port))
	}
	return node, nil
}

func parseURL(link string) (*url.URL, string, int, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, "", 0, err
	}
	host := u.Hostname()
	if host == "" {
		return nil, "", 0, fmt.Errorf("нет адреса сервера")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port <= 0 || port > 65535 {
		return nil, "", 0, fmt.Errorf("неверный порт")
	}
	return u, host, port, nil
}

func fragmentName(u *url.URL) string {
	name, err := url.PathUnescape(u.Fragment)
	if err != nil {
		return u.Fragment
	}
	return strings.TrimSpace(name)
}

func truthy(v string) bool {
	switch strings.ToLower(v) {
	case "1", "true", "yes":
		return true
	}
	return false
}

func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// tlsOptions builds the sing-box "tls" object from common query parameters.
func tlsOptions(q url.Values, security, host string) (map[string]any, error) {
	switch security {
	case "", "none":
		return nil, nil
	case "tls", "xtls", "reality":
	default:
		return nil, fmt.Errorf("security=%s не поддерживается", security)
	}
	sni := firstNonEmpty(q.Get("sni"), q.Get("peer"), q.Get("host"))
	if sni == "" && net.ParseIP(host) == nil {
		sni = host
	}
	t := map[string]any{"enabled": true}
	if sni != "" {
		t["server_name"] = sni
	}
	if pins := pinnedHashes(q); len(pins) > 0 {
		// pinned certificate: verify by fingerprint instead of a CA chain
		t["certificate_public_key_sha256"] = pins
	} else if truthy(q.Get("allowInsecure")) || truthy(q.Get("insecure")) {
		t["insecure"] = true
	}
	if alpn := splitList(q.Get("alpn")); len(alpn) > 0 {
		t["alpn"] = alpn
	}
	fp := strings.ToLower(q.Get("fp"))
	if security == "reality" {
		pbk := q.Get("pbk")
		if pbk == "" {
			return nil, fmt.Errorf("в ссылке Reality нет pbk")
		}
		r := map[string]any{"enabled": true, "public_key": pbk}
		if sid := q.Get("sid"); sid != "" {
			r["short_id"] = sid
		}
		t["reality"] = r
		if fp == "" {
			fp = "chrome"
		}
	}
	if fp != "" && fp != "none" {
		if fp == "random" || fp == "randomized" {
			fp = "randomized"
		}
		t["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
	}
	return t, nil
}

// transportOptions builds the sing-box "transport" object (V2Ray transports).
func transportOptions(q url.Values, network string) (map[string]any, error) {
	path := q.Get("path")
	host := q.Get("host")
	switch network {
	case "", "tcp", "raw":
		if strings.EqualFold(q.Get("headerType"), "http") {
			t := map[string]any{"type": "http", "method": "GET"}
			if host != "" {
				t["host"] = splitList(host)
			}
			if path != "" {
				t["path"] = path
			}
			return t, nil
		}
		return nil, nil
	case "ws":
		t := map[string]any{"type": "ws"}
		if path != "" {
			// "?ed=2048" early data used by Xray
			p, rawQuery, _ := strings.Cut(path, "?")
			t["path"] = p
			if ed, err := url.ParseQuery(rawQuery); err == nil && ed.Get("ed") != "" {
				if n, err := strconv.Atoi(ed.Get("ed")); err == nil {
					t["max_early_data"] = n
					t["early_data_header_name"] = "Sec-WebSocket-Protocol"
				}
			}
		}
		if host != "" {
			t["headers"] = map[string]any{"Host": host}
		}
		return t, nil
	case "httpupgrade":
		t := map[string]any{"type": "httpupgrade"}
		if path != "" {
			t["path"] = path
		}
		if host != "" {
			t["host"] = host
		}
		return t, nil
	case "grpc":
		t := map[string]any{"type": "grpc"}
		if sn := firstNonEmpty(q.Get("serviceName"), path); sn != "" {
			t["service_name"] = sn
		}
		return t, nil
	case "http", "h2":
		t := map[string]any{"type": "http"}
		if host != "" {
			t["host"] = splitList(host)
		}
		if path != "" {
			t["path"] = path
		}
		return t, nil
	case "quic":
		return map[string]any{"type": "quic"}, nil
	case "xhttp", "splithttp":
		return nil, fmt.Errorf("транспорт XHTTP пока не поддерживается ядром")
	case "kcp", "mkcp":
		return nil, fmt.Errorf("транспорт mKCP не поддерживается")
	}
	return nil, fmt.Errorf("транспорт %s не поддерживается", network)
}

func parseVLESS(link string) (Node, error) {
	u, host, port, err := parseURL(link)
	if err != nil {
		return Node{}, err
	}
	q := u.Query()
	uuid := u.User.Username()
	if uuid == "" {
		return Node{}, fmt.Errorf("нет UUID")
	}
	security := strings.ToLower(q.Get("security"))
	network := strings.ToLower(firstNonEmpty(q.Get("type"), "tcp"))
	out := map[string]any{"type": "vless", "server": host, "server_port": port, "uuid": uuid}
	if flow := q.Get("flow"); flow != "" {
		flow = strings.TrimSuffix(flow, "-udp443")
		if flow != "xtls-rprx-vision" {
			return Node{}, fmt.Errorf("flow %s не поддерживается", flow)
		}
		out["flow"] = flow
	}
	tls, err := tlsOptions(q, security, host)
	if err != nil {
		return Node{}, err
	}
	if tls != nil {
		out["tls"] = tls
	}
	tr, err := transportOptions(q, network)
	if err != nil {
		return Node{}, err
	}
	if tr != nil {
		out["transport"] = tr
	}
	out["packet_encoding"] = "xudp"
	return Node{Name: fragmentName(u), Protocol: "VLESS", Server: host, Port: port,
		Security: securityLabel(security), Network: network, Outbound: out}, nil
}

type vmessJSON struct {
	PS   string          `json:"ps"`
	Add  string          `json:"add"`
	Port json.RawMessage `json:"port"`
	ID   string          `json:"id"`
	Aid  json.RawMessage `json:"aid"`
	Scy  string          `json:"scy"`
	Net  string          `json:"net"`
	Type string          `json:"type"`
	Host string          `json:"host"`
	Path string          `json:"path"`
	TLS  string          `json:"tls"`
	SNI  string          `json:"sni"`
	ALPN string          `json:"alpn"`
	FP   string          `json:"fp"`
}

func rawNumber(r json.RawMessage) int {
	s := strings.Trim(string(r), `" `)
	n, _ := strconv.Atoi(s)
	return n
}

func parseVMess(link string) (Node, error) {
	body := strings.TrimPrefix(link, link[:strings.Index(link, "://")+3])
	body, _, _ = strings.Cut(body, "#")
	decoded, ok := tryBase64(body)
	if !ok {
		return Node{}, fmt.Errorf("не удалось декодировать vmess")
	}
	var v vmessJSON
	if err := json.Unmarshal([]byte(decoded), &v); err != nil {
		return Node{}, fmt.Errorf("неверный vmess JSON")
	}
	port := rawNumber(v.Port)
	if v.Add == "" || port <= 0 || v.ID == "" {
		return Node{}, fmt.Errorf("в vmess нет адреса, порта или id")
	}
	q := url.Values{}
	q.Set("path", v.Path)
	q.Set("host", v.Host)
	q.Set("headerType", v.Type)
	q.Set("sni", v.SNI)
	q.Set("alpn", v.ALPN)
	q.Set("fp", v.FP)
	if v.Net == "grpc" {
		q.Set("serviceName", v.Path)
	}
	security := strings.ToLower(v.TLS)
	out := map[string]any{"type": "vmess", "server": v.Add, "server_port": port, "uuid": v.ID,
		"security": firstNonEmpty(v.Scy, "auto"), "alter_id": rawNumber(v.Aid), "packet_encoding": "xudp"}
	tls, err := tlsOptions(q, security, v.Add)
	if err != nil {
		return Node{}, err
	}
	if tls != nil {
		out["tls"] = tls
	}
	network := strings.ToLower(firstNonEmpty(v.Net, "tcp"))
	tr, err := transportOptions(q, network)
	if err != nil {
		return Node{}, err
	}
	if tr != nil {
		out["transport"] = tr
	}
	return Node{Name: strings.TrimSpace(v.PS), Protocol: "VMess", Server: v.Add, Port: port,
		Security: securityLabel(security), Network: network, Outbound: out}, nil
}

func parseTrojan(link string) (Node, error) {
	u, host, port, err := parseURL(link)
	if err != nil {
		return Node{}, err
	}
	q := u.Query()
	password, _ := url.PathUnescape(u.User.Username())
	if password == "" {
		return Node{}, fmt.Errorf("нет пароля")
	}
	security := strings.ToLower(firstNonEmpty(q.Get("security"), "tls"))
	network := strings.ToLower(firstNonEmpty(q.Get("type"), "tcp"))
	out := map[string]any{"type": "trojan", "server": host, "server_port": port, "password": password}
	tls, err := tlsOptions(q, security, host)
	if err != nil {
		return Node{}, err
	}
	if tls != nil {
		out["tls"] = tls
	}
	tr, err := transportOptions(q, network)
	if err != nil {
		return Node{}, err
	}
	if tr != nil {
		out["transport"] = tr
	}
	return Node{Name: fragmentName(u), Protocol: "Trojan", Server: host, Port: port,
		Security: securityLabel(security), Network: network, Outbound: out}, nil
}

func parseShadowsocks(link string) (Node, error) {
	rest := link[len("ss://"):]
	rest, frag, _ := strings.Cut(rest, "#")
	name, _ := url.PathUnescape(frag)
	rest, rawQuery, _ := strings.Cut(rest, "?")
	if !strings.Contains(rest, "@") {
		// SIP002 legacy: base64(method:password@host:port)
		decoded, ok := tryBase64(rest)
		if !ok {
			return Node{}, fmt.Errorf("неверная ss-ссылка")
		}
		rest = decoded
	}
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return Node{}, fmt.Errorf("неверная ss-ссылка")
	}
	userinfo, hostport := rest[:at], rest[at+1:]
	if decoded, ok := tryBase64(userinfo); ok && strings.Contains(decoded, ":") {
		userinfo = decoded
	} else if unescaped, err := url.PathUnescape(userinfo); err == nil {
		userinfo = unescaped
	}
	method, password, ok := strings.Cut(userinfo, ":")
	if !ok {
		return Node{}, fmt.Errorf("нет метода или пароля")
	}
	host, portStr, err := net.SplitHostPort(strings.TrimRight(hostport, "/"))
	if err != nil {
		return Node{}, fmt.Errorf("неверный адрес")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return Node{}, fmt.Errorf("неверный порт")
	}
	q, _ := url.ParseQuery(rawQuery)
	if plugin := q.Get("plugin"); plugin != "" && !strings.HasPrefix(plugin, "none") {
		return Node{}, fmt.Errorf("плагины Shadowsocks не поддерживаются")
	}
	out := map[string]any{"type": "shadowsocks", "server": host, "server_port": port,
		"method": method, "password": password}
	return Node{Name: strings.TrimSpace(name), Protocol: "Shadowsocks", Server: host, Port: port,
		Security: method, Outbound: out}, nil
}

func parseHysteria2(link string) (Node, error) {
	// port hopping "host:443,20000-30000" is not a valid URL port; keep first
	if i := strings.Index(link, "@"); i > 0 {
		hp := link[i+1:]
		end := strings.IndexAny(hp, "/?#")
		if end < 0 {
			end = len(hp)
		}
		if c := strings.LastIndex(hp[:end], ":"); c >= 0 {
			ports := hp[c+1 : end]
			if j := strings.IndexAny(ports, ",-"); j > 0 {
				link = link[:i+1] + hp[:c+1] + ports[:j] + hp[end:]
			}
		}
	}
	u, host, port, err := parseURL(link)
	if err != nil {
		return Node{}, err
	}
	q := u.Query()
	auth := u.User.String()
	if p, ok := u.User.Password(); ok {
		auth = u.User.Username() + ":" + p
	} else {
		auth, _ = url.PathUnescape(u.User.Username())
	}
	out := map[string]any{"type": "hysteria2", "server": host, "server_port": port}
	if auth != "" {
		out["password"] = auth
	}
	if obfs := q.Get("obfs"); obfs != "" && obfs != "none" {
		out["obfs"] = map[string]any{"type": obfs, "password": q.Get("obfs-password")}
	}
	tls := map[string]any{"enabled": true, "alpn": []string{"h3"}}
	sni := q.Get("sni")
	if sni == "" && net.ParseIP(host) == nil {
		sni = host
	}
	if sni != "" {
		tls["server_name"] = sni
	}
	if pins := pinnedHashes(q); len(pins) > 0 {
		tls["certificate_public_key_sha256"] = pins
	} else if truthy(q.Get("insecure")) {
		tls["insecure"] = true
	}
	out["tls"] = tls
	return Node{Name: fragmentName(u), Protocol: "Hysteria2", Server: host, Port: port,
		Security: "QUIC", Outbound: out}, nil
}

func parseTUIC(link string) (Node, error) {
	u, host, port, err := parseURL(link)
	if err != nil {
		return Node{}, err
	}
	q := u.Query()
	uuid := u.User.Username()
	password, _ := u.User.Password()
	if uuid == "" {
		return Node{}, fmt.Errorf("нет UUID")
	}
	out := map[string]any{"type": "tuic", "server": host, "server_port": port, "uuid": uuid, "password": password}
	if cc := q.Get("congestion_control"); cc != "" {
		out["congestion_control"] = cc
	}
	if m := q.Get("udp_relay_mode"); m != "" {
		out["udp_relay_mode"] = m
	}
	tls := map[string]any{"enabled": true}
	alpn := splitList(q.Get("alpn"))
	if len(alpn) == 0 {
		alpn = []string{"h3"}
	}
	tls["alpn"] = alpn
	sni := q.Get("sni")
	if sni == "" && net.ParseIP(host) == nil {
		sni = host
	}
	if sni != "" {
		tls["server_name"] = sni
	}
	if pins := pinnedHashes(q); len(pins) > 0 {
		tls["certificate_public_key_sha256"] = pins
	} else if truthy(q.Get("allow_insecure")) || truthy(q.Get("insecure")) {
		tls["insecure"] = true
	}
	out["tls"] = tls
	return Node{Name: fragmentName(u), Protocol: "TUIC", Server: host, Port: port,
		Security: "QUIC", Outbound: out}, nil
}

// pinnedHashes reads Xray's `pcs` (pinnedPeerCertSha256): SHA-256 hashes of
// the server certificate as hex (with or without colons) or base64, separated
// by commas or "~". Returned as base64 for sing-box.
func pinnedHashes(q url.Values) []string {
	raw := firstNonEmpty(q.Get("pcs"), q.Get("pinnedPeerCertSha256"), q.Get("pinSHA256"))
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '~' || r == ' ' }) {
		h := strings.ReplaceAll(part, ":", "")
		if b, err := hex.DecodeString(h); err == nil && len(b) == 32 {
			out = append(out, base64.StdEncoding.EncodeToString(b))
			continue
		}
		if b, ok := tryBase64(part); ok && len(b) == 32 {
			out = append(out, base64.StdEncoding.EncodeToString([]byte(b)))
		}
	}
	return out
}

func securityLabel(s string) string {
	switch s {
	case "reality":
		return "Reality"
	case "tls", "xtls":
		return "TLS"
	case "", "none":
		return ""
	}
	return s
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func tryBase64(s string) (string, bool) {
	s = strings.TrimSpace(s)
	s = strings.NewReplacer("\n", "", "\r", "", " ", "").Replace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return string(b), true
		}
	}
	return "", false
}

func short(s string) string {
	if len(s) > 48 {
		return s[:48] + "…"
	}
	return s
}
