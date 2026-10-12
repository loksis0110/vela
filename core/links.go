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
	"regexp"
	"strconv"
	"strings"
)

// Node is one server parsed from a share link.
type Node struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Server   string `json:"server"`
	Port     int    `json:"port"`
	Security string `json:"security,omitempty"`
	Network  string `json:"network,omitempty"`
	// Engine is "xray" for servers run by the embedded Xray core.
	Engine   string         `json:"engine,omitempty"`
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
	if res, ok := parseStructured(text); ok {
		return finishParse(res)
	}
	if !strings.Contains(text, "://") {
		if decoded, ok := tryBase64(text); ok && strings.Contains(decoded, "://") {
			text = strings.TrimPrefix(strings.TrimSpace(decoded), "\ufeff")
		}
	}
	var res ParseResult
	for _, row := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' }) {
		row = strings.TrimSpace(row)
		if row == "" || strings.HasPrefix(row, "#") || strings.HasPrefix(row, "//") {
			continue
		}
		for _, line := range splitLinks(row) {
			node, err := ParseLink(line)
			if err != nil {
				res.Errors = append(res.Errors, err.Error())
				continue
			}
			res.Nodes = append(res.Nodes, node)
		}
	}
	return finishParse(res)
}

// splitLinks splits a line into share links. Several links may share a
// line, and names in the "#fragment" sometimes contain raw spaces: words
// that are not links belong to the name of the link before them.
func splitLinks(row string) []string {
	var links []string
	for _, f := range strings.Fields(row) {
		if strings.Contains(f, "://") {
			links = append(links, f)
			continue
		}
		if n := len(links); n > 0 && strings.Contains(links[n-1], "#") {
			links[n-1] += " " + f
		}
	}
	return links
}

// finishParse drops duplicate servers and keeps names unique.
func finishParse(in ParseResult) ParseResult {
	res := ParseResult{Errors: in.Errors}
	seen := map[string]int{}
	seenLink := map[string]bool{}
	for _, node := range in.Nodes {
		// the same link twice would give two servers with one id
		if seenLink[node.ID] {
			continue
		}
		seenLink[node.ID] = true
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
		return Node{}, errorf("не ссылка: %s", short(link))
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
	case jsonLinkScheme:
		node, err = parseJSONLink(link)
	default:
		return Node{}, errorf("протокол %s не поддерживается", scheme)
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
		// passwords with characters that should have been percent-encoded
		if fixed, ok := escapeUserinfo(link); ok {
			u, err = url.Parse(fixed)
		}
		if err != nil {
			return nil, "", 0, errorf("неверная ссылка")
		}
	}
	host := u.Hostname()
	if host == "" {
		return nil, "", 0, errorf("нет адреса сервера")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port <= 0 || port > 65535 {
		return nil, "", 0, errorf("неверный порт")
	}
	return u, host, port, nil
}

var hostPortStart = regexp.MustCompile(`^(\[[0-9A-Fa-f:.]+\]|[^/?#@\[\]:]+):\d+([/?#]|$)`)

// escapeUserinfo percent-encodes the user and password of a link whose
// provider left special characters (spaces, %, ^, |, ...) unencoded.
func escapeUserinfo(link string) (string, bool) {
	scheme, rest, ok := strings.Cut(link, "://")
	if !ok {
		return "", false
	}
	// the "@" before the server: the last one followed by host:port
	for at := strings.LastIndex(rest, "@"); at > 0; at = strings.LastIndex(rest[:at], "@") {
		if !hostPortStart.MatchString(rest[at+1:]) {
			continue
		}
		var b strings.Builder
		info := rest[:at]
		for i := 0; i < len(info); i++ {
			c := info[i]
			switch {
			case c == '%' && i+2 < len(info) && isHex(info[i+1]) && isHex(info[i+2]):
				b.WriteByte(c)
			case c < 0x80 && (isAlnum(c) || strings.IndexByte("-._~!$&'()*+,;=:", c) >= 0):
				b.WriteByte(c)
			default:
				fmt.Fprintf(&b, "%%%02X", c)
			}
		}
		return scheme + "://" + b.String() + rest[at:], true
	}
	return "", false
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func isAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
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
		return nil, errorf("security=%s не поддерживается", security)
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
			return nil, errorf("в ссылке Reality нет pbk")
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
		t["utls"] = map[string]any{"enabled": true, "fingerprint": utlsFingerprint(fp)}
	}
	return t, nil
}

// utlsFingerprint maps a share-link "fp" to a fingerprint sing-box knows:
// an unknown name would stop the whole connection from starting.
func utlsFingerprint(fp string) string {
	fp = strings.ToLower(strings.TrimSpace(fp))
	switch fp {
	case "chrome", "firefox", "edge", "safari", "360", "qq", "ios", "android", "randomized",
		"chrome_psk", "chrome_psk_shuffle", "chrome_padding_psk_shuffle", "chrome_pq", "chrome_pq_psk":
		return fp
	}
	for _, known := range []string{"firefox", "safari", "edge", "ios", "android", "360", "qq"} {
		if strings.Contains(fp, known) {
			return known
		}
	}
	if strings.HasPrefix(fp, "random") {
		return "randomized"
	}
	return "chrome"
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
		return nil, errorf("транспорт XHTTP пока не поддерживается ядром")
	case "kcp", "mkcp":
		return nil, errorf("транспорт mKCP не поддерживается")
	}
	return nil, errorf("транспорт %s не поддерживается", network)
}

func parseVLESS(link string) (Node, error) {
	u, host, port, err := parseURL(link)
	if err != nil {
		return Node{}, err
	}
	q := u.Query()
	uuid := u.User.Username()
	if uuid == "" {
		return Node{}, errorf("нет UUID")
	}
	security := strings.ToLower(q.Get("security"))
	network := strings.ToLower(firstNonEmpty(q.Get("type"), "tcp"))
	if needsXray(network, q.Get("encryption")) {
		return xrayVLESS(u, host, port, q, network, security)
	}
	out := map[string]any{"type": "vless", "server": host, "server_port": port, "uuid": uuid}
	if flow := q.Get("flow"); flow != "" {
		flow = strings.TrimSuffix(flow, "-udp443")
		if flow != "xtls-rprx-vision" {
			return Node{}, errorf("flow %s не поддерживается", flow)
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
	// not in the v2rayN format, but written by some generators
	Insecure json.RawMessage `json:"allowInsecure"`
}

func rawNumber(r json.RawMessage) int {
	s := strings.Trim(string(r), `" `)
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	f, _ := strconv.ParseFloat(s, 64)
	return int(f)
}

func parseVMess(link string) (Node, error) {
	body := link[strings.Index(link, "://")+3:]
	body, frag, _ := strings.Cut(body, "#")
	b64, rawQuery, _ := strings.Cut(body, "?")
	if decoded, ok := tryBase64(b64); ok {
		decoded = strings.TrimSpace(strings.TrimPrefix(decoded, "\ufeff"))
		if strings.HasPrefix(decoded, "{") {
			var v vmessJSON
			if err := json.Unmarshal([]byte(decoded), &v); err != nil {
				return Node{}, errorf("неверный vmess JSON")
			}
			extra := url.Values{}
			if truthy(strings.Trim(string(v.Insecure), `" `)) {
				extra.Set("allowInsecure", "1")
			}
			return buildVMess(v, extra)
		}
		if strings.Contains(decoded, "@") {
			// Shadowrocket: base64("auto:uuid@host:port")?remarks=...&obfs=websocket
			return parseShadowrocketVMess(decoded, rawQuery, frag)
		}
	}
	if strings.Contains(body, "@") {
		// the URL form of the share-link standard: vmess://uuid@host:port?...
		return parseVMessURL(link)
	}
	return Node{}, errorf("не удалось декодировать vmess")
}

func parseVMessURL(link string) (Node, error) {
	u, host, port, err := parseURL(link)
	if err != nil {
		return Node{}, err
	}
	q := u.Query()
	v := vmessJSON{PS: fragmentName(u), Add: host, Port: json.RawMessage(strconv.Itoa(port)), ID: u.User.Username(),
		Scy: q.Get("encryption"), Net: firstNonEmpty(q.Get("type"), "tcp"), Type: q.Get("headerType"),
		Host: q.Get("host"), Path: firstNonEmpty(q.Get("path"), q.Get("serviceName")), TLS: q.Get("security"),
		SNI: q.Get("sni"), ALPN: q.Get("alpn"), FP: q.Get("fp")}
	if strings.EqualFold(v.Net, "xhttp") || strings.EqualFold(v.Net, "splithttp") {
		v.Type = q.Get("mode")
	}
	if strings.EqualFold(v.Net, "kcp") || strings.EqualFold(v.Net, "mkcp") {
		v.Path = q.Get("seed")
	}
	extra := url.Values{}
	for _, k := range []string{"pbk", "sid", "spx", "pcs", "allowInsecure", "insecure", "extra", "authority", "mode"} {
		if val := q.Get(k); val != "" {
			extra.Set(k, val)
		}
	}
	return buildVMess(v, extra)
}

func parseShadowrocketVMess(decoded, rawQuery, frag string) (Node, error) {
	auth, hostport, _ := strings.Cut(decoded, "@")
	method, id, ok := strings.Cut(auth, ":")
	if !ok {
		method, id = "auto", auth
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(hostport))
	if err != nil {
		return Node{}, errorf("в vmess нет адреса, порта или id")
	}
	q, _ := url.ParseQuery(rawQuery)
	name, _ := url.PathUnescape(frag)
	v := vmessJSON{PS: firstNonEmpty(q.Get("remarks"), name), Add: host, Port: json.RawMessage(port), ID: id,
		Scy: method, Net: "tcp", Host: q.Get("obfsParam"), Path: q.Get("path"), SNI: q.Get("peer"), Aid: json.RawMessage(q.Get("alterId"))}
	switch strings.ToLower(q.Get("obfs")) {
	case "websocket", "ws":
		v.Net = "ws"
	case "http":
		v.Type = "http"
	case "grpc":
		v.Net = "grpc"
	}
	if truthy(q.Get("tls")) {
		v.TLS = "tls"
	}
	extra := url.Values{}
	if truthy(q.Get("allowInsecure")) {
		extra.Set("allowInsecure", "1")
	}
	return buildVMess(v, extra)
}

func buildVMess(v vmessJSON, extra url.Values) (Node, error) {
	port := rawNumber(v.Port)
	if v.Add == "" || port <= 0 || port > 65535 || v.ID == "" {
		return Node{}, errorf("в vmess нет адреса, порта или id")
	}
	network := strings.ToLower(firstNonEmpty(v.Net, "tcp"))
	q := url.Values{}
	for k, vals := range extra {
		q[k] = vals
	}
	q.Set("path", v.Path)
	q.Set("host", v.Host)
	q.Set("headerType", v.Type)
	q.Set("sni", v.SNI)
	q.Set("alpn", v.ALPN)
	q.Set("fp", v.FP)
	if network == "grpc" {
		q.Set("serviceName", v.Path)
	}
	security := strings.ToLower(v.TLS)
	if needsXray(network, "") {
		switch network {
		case "kcp", "mkcp":
			// v2rayN keeps the mKCP seed in "path" and the header in "type"
			q.Set("seed", v.Path)
			q.Del("path")
		default:
			// XHTTP: "type" is the mode
			q.Set("mode", firstNonEmpty(q.Get("mode"), v.Type))
			q.Del("headerType")
		}
		return xrayVMess(v, port, q, network, security)
	}
	out := map[string]any{"type": "vmess", "server": v.Add, "server_port": port, "uuid": v.ID,
		"security": vmessSecurity(v.Scy), "alter_id": rawNumber(v.Aid), "packet_encoding": "xudp"}
	tls, err := tlsOptions(q, security, v.Add)
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
	return Node{Name: strings.TrimSpace(v.PS), Protocol: "VMess", Server: v.Add, Port: port,
		Security: securityLabel(security), Network: network, Outbound: out}, nil
}

// vmessSecurity keeps the VMess cipher to the values sing-box accepts.
func vmessSecurity(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "auto", "none", "zero", "aes-128-gcm", "chacha20-poly1305", "aes-128-ctr":
		return s
	case "chacha20-ietf-poly1305":
		return "chacha20-poly1305"
	}
	return "auto"
}

func parseTrojan(link string) (Node, error) {
	u, host, port, err := parseURL(link)
	if err != nil {
		return Node{}, err
	}
	q := u.Query()
	password := u.User.Username() // already percent-decoded
	if password == "" {
		return Node{}, errorf("нет пароля")
	}
	security := strings.ToLower(firstNonEmpty(q.Get("security"), "tls"))
	network := strings.ToLower(firstNonEmpty(q.Get("type"), "tcp"))
	if needsXray(network, "") {
		return xrayTrojan(u, host, port, password, q, network, security)
	}
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

// shadowsocksMethods are the ciphers sing-box supports, plus common aliases.
var shadowsocksMethods = map[string]string{
	"none": "none", "plain": "none",
	"aes-128-gcm": "aes-128-gcm", "aes-192-gcm": "aes-192-gcm", "aes-256-gcm": "aes-256-gcm",
	"chacha20-ietf-poly1305": "chacha20-ietf-poly1305", "chacha20-poly1305": "chacha20-ietf-poly1305",
	"xchacha20-ietf-poly1305": "xchacha20-ietf-poly1305", "xchacha20-poly1305": "xchacha20-ietf-poly1305",
	"aead_aes_128_gcm": "aes-128-gcm", "aead_aes_256_gcm": "aes-256-gcm", "aead_chacha20_poly1305": "chacha20-ietf-poly1305",
	"2022-blake3-aes-128-gcm": "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm": "2022-blake3-aes-256-gcm",
	"2022-blake3-chacha20-poly1305": "2022-blake3-chacha20-poly1305",
	"aes-128-ctr":                   "aes-128-ctr", "aes-192-ctr": "aes-192-ctr", "aes-256-ctr": "aes-256-ctr",
	"aes-128-cfb": "aes-128-cfb", "aes-192-cfb": "aes-192-cfb", "aes-256-cfb": "aes-256-cfb",
	"rc4-md5": "rc4-md5", "chacha20-ietf": "chacha20-ietf", "xchacha20": "xchacha20",
}

func parseShadowsocks(link string) (Node, error) {
	rest := link[len("ss://"):]
	rest, frag, _ := strings.Cut(rest, "#")
	name, err := url.PathUnescape(frag)
	if err != nil {
		name = frag
	}
	rest, rawQuery, _ := strings.Cut(rest, "?")
	if !strings.Contains(rest, "@") {
		// SIP002 legacy: base64(method:password@host:port)
		decoded, ok := tryBase64(rest)
		if !ok {
			return Node{}, errorf("неверная ss-ссылка")
		}
		rest = decoded
	}
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return Node{}, errorf("неверная ss-ссылка")
	}
	userinfo, hostport := rest[:at], rest[at+1:]
	// base64 is often percent-encoded too ("...%3D")
	if unescaped, err := url.PathUnescape(userinfo); err == nil {
		userinfo = unescaped
	}
	if decoded, ok := tryBase64(userinfo); ok && strings.Contains(decoded, ":") {
		userinfo = decoded
	}
	method, password, ok := strings.Cut(userinfo, ":")
	if !ok {
		return Node{}, errorf("нет метода или пароля")
	}
	m, known := shadowsocksMethods[strings.ToLower(strings.TrimSpace(method))]
	if !known {
		return Node{}, errorf("шифрование %s не поддерживается", method)
	}
	host, portStr, err := net.SplitHostPort(strings.TrimRight(hostport, "/"))
	if err != nil || host == "" {
		return Node{}, errorf("неверный адрес")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return Node{}, errorf("неверный порт")
	}
	out := map[string]any{"type": "shadowsocks", "server": host, "server_port": port,
		"method": m, "password": password}
	q, _ := url.ParseQuery(rawQuery)
	if plugin := q.Get("plugin"); plugin != "" {
		pluginName, opts, err := shadowsocksPlugin(plugin)
		if err != nil {
			return Node{}, err
		}
		if pluginName != "" {
			out["plugin"] = pluginName
			out["plugin_opts"] = opts
		}
	}
	return Node{Name: strings.TrimSpace(name), Protocol: "Shadowsocks", Server: host, Port: port,
		Security: m, Outbound: out}, nil
}

// shadowsocksPlugin converts a SIP003 plugin ("obfs-local;obfs=http;obfs-host=x")
// into the ones built into sing-box.
func shadowsocksPlugin(spec string) (name, opts string, err error) {
	name, opts, _ = strings.Cut(spec, ";")
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "none":
		return "", "", nil
	case "obfs-local", "simple-obfs", "obfs":
		return "obfs-local", opts, nil
	case "v2ray-plugin":
		return "v2ray-plugin", opts, nil
	}
	return "", "", errorf("плагин Shadowsocks %s не поддерживается", name)
}

func parseHysteria2(link string) (Node, error) {
	// port hopping: "host:443,20000-30000" is not a valid URL port; the
	// first port goes into the URL, the others into server_ports
	scheme, rest, _ := strings.Cut(link, "://")
	body, frag, hasFrag := strings.Cut(rest, "#")
	authEnd := strings.IndexAny(body, "/?")
	if authEnd < 0 {
		authEnd = len(body)
	}
	authority := body[:authEnd]
	at := strings.LastIndex(authority, "@")
	hostPorts := authority[at+1:]
	var hopPorts []string
	if c := strings.LastIndex(hostPorts, ":"); c >= 0 && strings.ContainsAny(hostPorts[c+1:], ",-") {
		first, ranges := hysteriaPorts(hostPorts[c+1:])
		if first == "" {
			return Node{}, errorf("неверный порт")
		}
		hopPorts = ranges
		body = authority[:at+1] + hostPorts[:c+1] + first + body[authEnd:]
		link = scheme + "://" + body
		if hasFrag {
			link += "#" + frag
		}
	}
	u, host, port, err := parseURL(link)
	if err != nil {
		return Node{}, err
	}
	q := u.Query()
	if mport := q.Get("mport"); mport != "" {
		_, ranges := hysteriaPorts(mport)
		hopPorts = append(hopPorts, ranges...)
	}
	auth := u.User.Username() // already percent-decoded
	if p, ok := u.User.Password(); ok {
		auth += ":" + p
	}
	out := map[string]any{"type": "hysteria2", "server": host, "server_port": port}
	if len(hopPorts) > 0 {
		out["server_ports"] = append([]string{fmt.Sprintf("%d:%d", port, port)}, hopPorts...)
	}
	if auth != "" {
		out["password"] = auth
	}
	if obfs := strings.ToLower(q.Get("obfs")); obfs != "" && obfs != "none" {
		if obfs != "salamander" {
			return Node{}, errorf("obfs %s не поддерживается", obfs)
		}
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
	} else if truthy(q.Get("insecure")) || truthy(q.Get("allowInsecure")) {
		tls["insecure"] = true
	}
	out["tls"] = tls
	return Node{Name: fragmentName(u), Protocol: "Hysteria2", Server: host, Port: port,
		Security: "QUIC", Outbound: out}, nil
}

// hysteriaPorts reads "443,20000-30000": the first port and sing-box
// server_ports ranges ("20000:30000") for the rest.
func hysteriaPorts(spec string) (first string, ranges []string) {
	valid := func(s string) bool {
		n, err := strconv.Atoi(s)
		return err == nil && n > 0 && n <= 65535
	}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		lo, hi, isRange := strings.Cut(part, "-")
		if !valid(lo) || (isRange && !valid(hi)) {
			continue
		}
		if first == "" {
			first = lo
			if !isRange {
				continue
			}
		}
		if !isRange {
			hi = lo
		}
		ranges = append(ranges, lo+":"+hi)
	}
	return first, ranges
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
		return Node{}, errorf("нет UUID")
	}
	out := map[string]any{"type": "tuic", "server": host, "server_port": port, "uuid": uuid, "password": password}
	// unknown values would stop the whole connection from starting
	switch cc := strings.ToLower(firstNonEmpty(q.Get("congestion_control"), q.Get("congestion"))); cc {
	case "cubic", "new_reno", "bbr":
		out["congestion_control"] = cc
	}
	switch m := strings.ToLower(q.Get("udp_relay_mode")); m {
	case "native", "quic":
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
