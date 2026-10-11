package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	sjson "github.com/sagernet/sing/common/json"
	"gopkg.in/yaml.v3"
)

// jsonLinkScheme stores servers that have no standard share link (they come
// from sing-box or Xray JSON subscriptions) as one line:
// velajson://<base64url of the outbound JSON>#<name>. It keeps the same
// storage format for every server on every platform.
const jsonLinkScheme = "velajson"

// parseStructured recognises Clash/Mihomo YAML, sing-box JSON and Xray JSON
// subscriptions. ok is false for anything else (share links, base64).
func parseStructured(text string) (ParseResult, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ParseResult{}, false
	}
	switch trimmed[0] {
	case '{', '[':
		var v any
		if err := json.Unmarshal([]byte(trimmed), &v); err != nil {
			return ParseResult{}, false
		}
		return parseJSONSubscription(v)
	}
	if looksLikeClash(trimmed) {
		return parseClash(trimmed)
	}
	return ParseResult{}, false
}

func looksLikeClash(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "proxies:") && !strings.Contains(l, "://") {
			return true
		}
	}
	return false
}

// --- JSON -------------------------------------------------------------------

var singBoxProxyTypes = map[string]bool{
	"vless": true, "vmess": true, "trojan": true, "shadowsocks": true,
	"hysteria2": true, "hysteria": true, "tuic": true, "anytls": true,
	"socks": true, "http": true, "ssh": true, "naive": false,
}

var xrayProxyProtocols = map[string]bool{
	"vless": true, "vmess": true, "trojan": true, "shadowsocks": true,
	"socks": true, "http": true, "hysteria": true, "wireguard": false,
}

func parseJSONSubscription(v any) (ParseResult, bool) {
	var res ParseResult
	add := func(n Node, err error) {
		if err != nil {
			res.Errors = append(res.Errors, err.Error())
			return
		}
		res.Nodes = append(res.Nodes, n)
	}
	handleObject := func(obj map[string]any) bool {
		outs, ok := obj["outbounds"].([]any)
		if !ok {
			return false
		}
		if isXrayOutbounds(outs) {
			name, _ := obj["remarks"].(string)
			add(xrayConfigNode(name, outs))
			return true
		}
		for _, o := range outs {
			ob, ok := o.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := ob["type"].(string)
			if !singBoxProxyTypes[typ] {
				continue
			}
			if d, _ := ob["detour"].(string); d != "" {
				res.Errors = append(res.Errors, errorf("сервер %v: цепочки (detour) не поддерживаются", ob["tag"]).Error())
				continue
			}
			add(singBoxNode(ob))
		}
		return true
	}
	switch val := v.(type) {
	case map[string]any:
		if !handleObject(val) {
			return ParseResult{}, false
		}
	case []any:
		matched := false
		for _, item := range val {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if _, has := obj["outbounds"]; has {
				matched = handleObject(obj) || matched
				continue
			}
			// a bare list of sing-box outbounds
			if typ, _ := obj["type"].(string); singBoxProxyTypes[typ] {
				matched = true
				add(singBoxNode(obj))
			}
		}
		if !matched {
			return ParseResult{}, false
		}
	default:
		return ParseResult{}, false
	}
	return res, true
}

func isXrayOutbounds(outs []any) bool {
	for _, o := range outs {
		if ob, ok := o.(map[string]any); ok {
			if _, has := ob["protocol"]; has {
				return true
			}
		}
	}
	return false
}

// xrayConfigNode turns one Xray client config into a server: its first proxy
// outbound plus the helper outbounds it refers to.
func xrayConfigNode(name string, outs []any) (Node, error) {
	main := -1
	for i, o := range outs {
		ob, _ := o.(map[string]any)
		p, _ := ob["protocol"].(string)
		if xrayProxyProtocols[strings.ToLower(p)] {
			main = i
			break
		}
	}
	if main < 0 {
		return Node{}, errorf("%s: в конфигурации Xray нет сервера", name)
	}
	ordered := []map[string]any{outs[main].(map[string]any)}
	for i, o := range outs {
		if ob, ok := o.(map[string]any); ok && i != main {
			ordered = append(ordered, ob)
		}
	}
	list := make([]any, len(ordered))
	for i, ob := range ordered {
		sanitizeXrayOutbound(ob)
		list[i] = ob
	}
	protocol, host, port, network, security := xrayServerInfo(ordered[0])
	if host == "" || port == 0 {
		return Node{}, errorf("%s: в конфигурации Xray нет адреса сервера", name)
	}
	if name == "" {
		name = net.JoinHostPort(host, strconv.Itoa(port))
	}
	if err := checkXray(list); err != nil {
		return Node{}, fmt.Errorf("%s: %w", name, err)
	}
	outbound := xrayOutboundOptions(ordered...)
	return jsonNode(name, outbound, Node{Protocol: protocol, Server: host, Port: port,
		Network: network, Security: security, Engine: EngineXray})
}

// singBoxNode keeps a sing-box outbound as it is.
func singBoxNode(ob map[string]any) (Node, error) {
	copied := map[string]any{}
	for k, v := range ob {
		if k != "tag" {
			copied[k] = v
		}
	}
	typ, _ := copied["type"].(string)
	host, _ := copied["server"].(string)
	port := anyInt(copied["server_port"])
	name, _ := ob["tag"].(string)
	if name == "" {
		name = net.JoinHostPort(host, strconv.Itoa(port))
	}
	info := Node{Protocol: protocolLabel(typ), Server: host, Port: port}
	if tr, ok := copied["transport"].(map[string]any); ok {
		info.Network, _ = tr["type"].(string)
	}
	if tls, ok := copied["tls"].(map[string]any); ok && tls["enabled"] == true {
		info.Security = "TLS"
		if r, ok := tls["reality"].(map[string]any); ok && r["enabled"] == true {
			info.Security = "Reality"
		}
	}
	return jsonNode(name, copied, info)
}

func jsonNode(name string, outbound map[string]any, info Node) (Node, error) {
	data, err := json.Marshal(outbound)
	if err != nil {
		return Node{}, err
	}
	link := jsonLinkScheme + "://" + base64.RawURLEncoding.EncodeToString(data) + "#" + url.PathEscape(name)
	return ParseLink(link)
}

// parseJSONLink reads a velajson:// line back.
func parseJSONLink(link string) (Node, error) {
	body := strings.TrimPrefix(link, jsonLinkScheme+"://")
	body, frag, _ := strings.Cut(body, "#")
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(body, "="))
	if err != nil {
		return Node{}, errorf("повреждённая запись сервера")
	}
	var ob map[string]any
	if err := json.Unmarshal(raw, &ob); err != nil {
		return Node{}, errorf("повреждённая запись сервера")
	}
	name, _ := url.PathUnescape(frag)
	typ, _ := ob["type"].(string)
	n := Node{Name: strings.TrimSpace(name), Outbound: ob}
	if typ == EngineXray {
		outs, _ := ob["outbounds"].([]any)
		if len(outs) == 0 {
			return Node{}, errorf("повреждённая запись сервера")
		}
		for _, o := range outs {
			if m, ok := o.(map[string]any); ok {
				sanitizeXrayOutbound(m)
			}
		}
		if err := checkXray(outs); err != nil {
			return Node{}, err
		}
		first, _ := outs[0].(map[string]any)
		n.Protocol, n.Server, n.Port, n.Network, n.Security = xrayServerInfo(first)
		n.Engine = EngineXray
	} else {
		if err := checkSingBoxOutbound(raw); err != nil {
			return Node{}, err
		}
		n.Protocol = protocolLabel(typ)
		n.Server, _ = ob["server"].(string)
		n.Port = anyInt(ob["server_port"])
		if tr, ok := ob["transport"].(map[string]any); ok {
			n.Network, _ = tr["type"].(string)
		}
		if tls, ok := ob["tls"].(map[string]any); ok && tls["enabled"] == true {
			n.Security = "TLS"
			if r, ok := tls["reality"].(map[string]any); ok && r["enabled"] == true {
				n.Security = "Reality"
			}
		}
	}
	if n.Server == "" {
		return Node{}, errorf("нет адреса сервера")
	}
	return n, nil
}

var optionsContext = sync.OnceValue(func() context.Context { return include.Context(context.Background()) })

// checkSingBoxOutbound makes sure sing-box accepts an outbound from a
// sing-box JSON subscription (unknown fields or types would stop the whole
// connection from starting).
func checkSingBoxOutbound(raw []byte) error {
	if _, err := sjson.UnmarshalExtendedContext[option.Outbound](optionsContext(), raw); err != nil {
		return errorf("sing-box не принимает настройки сервера: %v", err)
	}
	return nil
}

// --- Clash / Mihomo YAML -------------------------------------------------------

func parseClash(text string) (ParseResult, bool) {
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil || len(doc.Proxies) == 0 {
		return ParseResult{}, false
	}
	var res ParseResult
	for _, p := range doc.Proxies {
		if ob, info, ok := clashOutbound(p); ok {
			node, err := jsonNode(firstNonEmpty(str(p, "name"), net.JoinHostPort(info.Server, strconv.Itoa(info.Port))), ob, info)
			if err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("%v: %v", p["name"], err))
				continue
			}
			res.Nodes = append(res.Nodes, node)
			continue
		}
		link, err := clashToLink(p)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%v: %v", p["name"], err))
			continue
		}
		node, err := ParseLink(link)
		if err != nil {
			res.Errors = append(res.Errors, err.Error())
			continue
		}
		res.Nodes = append(res.Nodes, node)
	}
	return res, true
}

func str(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case string:
		return v
	case int:
		return strconv.Itoa(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

func boolOf(m map[string]any, key string) bool {
	switch v := m[key].(type) {
	case bool:
		return v
	case string:
		return truthy(v)
	}
	return false
}

func sub(m map[string]any, key string) map[string]any {
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return map[string]any{}
}

func strList(v any) []string {
	switch l := v.(type) {
	case []any:
		var out []string
		for _, s := range l {
			out = append(out, fmt.Sprint(s))
		}
		return out
	case string:
		return splitList(l)
	}
	return nil
}

// clashOutbound converts Clash proxies that have no share link (SOCKS5,
// HTTP, AnyTLS) into sing-box outbounds.
func clashOutbound(p map[string]any) (map[string]any, Node, bool) {
	typ := strings.ToLower(str(p, "type"))
	server := str(p, "server")
	port, _ := strconv.Atoi(str(p, "port"))
	ob := map[string]any{"server": server, "server_port": port}
	tls := func() map[string]any {
		t := map[string]any{"enabled": true}
		if sni := firstNonEmpty(str(p, "sni"), str(p, "servername")); sni != "" {
			t["server_name"] = sni
		} else if net.ParseIP(server) == nil {
			t["server_name"] = server
		}
		if boolOf(p, "skip-cert-verify") {
			t["insecure"] = true
		}
		if alpn := strList(p["alpn"]); len(alpn) > 0 {
			t["alpn"] = alpn
		}
		if fp := str(p, "client-fingerprint"); fp != "" {
			t["utls"] = map[string]any{"enabled": true, "fingerprint": utlsFingerprint(fp)}
		}
		return t
	}
	info := Node{Server: server, Port: port}
	switch typ {
	case "socks5", "http":
		ob["type"] = map[string]string{"socks5": "socks", "http": "http"}[typ]
		if u := str(p, "username"); u != "" {
			ob["username"] = u
			ob["password"] = str(p, "password")
		}
		if boolOf(p, "tls") {
			ob["tls"] = tls()
			info.Security = "TLS"
		}
		info.Protocol = protocolLabel(ob["type"].(string))
	case "anytls":
		ob["type"] = "anytls"
		ob["password"] = str(p, "password")
		ob["tls"] = tls()
		info.Protocol, info.Security = "AnyTLS", "TLS"
	default:
		return nil, Node{}, false
	}
	return ob, info, server != "" && port > 0
}

// clashToLink converts a Clash proxy into a standard share link, so every
// parser fix and the Xray fallback apply to it too.
func clashToLink(p map[string]any) (string, error) {
	typ := strings.ToLower(str(p, "type"))
	name := str(p, "name")
	server := str(p, "server")
	port := str(p, "port")
	if server == "" || port == "" {
		return "", errorf("нет адреса сервера")
	}
	hostport := net.JoinHostPort(server, port)
	q := url.Values{}
	frag := "#" + url.PathEscape(name)

	transport := func() {
		network := strings.ToLower(firstNonEmpty(str(p, "network"), "tcp"))
		switch network {
		case "ws":
			q.Set("type", "ws")
			ws := sub(p, "ws-opts")
			if path := str(ws, "path"); path != "" {
				q.Set("path", path)
			}
			if h := firstNonEmpty(str(sub(ws, "headers"), "Host"), str(sub(ws, "headers"), "host")); h != "" {
				q.Set("host", h)
			}
		case "grpc":
			q.Set("type", "grpc")
			q.Set("serviceName", str(sub(p, "grpc-opts"), "grpc-service-name"))
		case "h2", "http":
			q.Set("type", "http")
			h2 := sub(p, "h2-opts")
			if path := str(h2, "path"); path != "" {
				q.Set("path", path)
			}
			if hosts := strList(h2["host"]); len(hosts) > 0 {
				q.Set("host", strings.Join(hosts, ","))
			}
		case "xhttp":
			q.Set("type", "xhttp")
			x := sub(p, "xhttp-opts")
			if path := str(x, "path"); path != "" {
				q.Set("path", path)
			}
			if h := str(x, "host"); h != "" {
				q.Set("host", h)
			}
			if m := str(x, "mode"); m != "" {
				q.Set("mode", m)
			}
		case "httpupgrade":
			q.Set("type", "httpupgrade")
			ws := sub(p, "ws-opts")
			q.Set("path", str(ws, "path"))
			if h := str(sub(ws, "headers"), "Host"); h != "" {
				q.Set("host", h)
			}
		default:
			q.Set("type", "tcp")
		}
	}
	tlsParams := func(tlsOn bool) {
		reality := sub(p, "reality-opts")
		switch {
		case str(reality, "public-key") != "":
			q.Set("security", "reality")
			q.Set("pbk", str(reality, "public-key"))
			if sid := str(reality, "short-id"); sid != "" {
				q.Set("sid", sid)
			}
		case tlsOn:
			q.Set("security", "tls")
		default:
			q.Set("security", "none")
		}
		if sni := firstNonEmpty(str(p, "servername"), str(p, "sni")); sni != "" {
			q.Set("sni", sni)
		}
		if fp := str(p, "client-fingerprint"); fp != "" {
			q.Set("fp", fp)
		}
		if alpn := strList(p["alpn"]); len(alpn) > 0 {
			q.Set("alpn", strings.Join(alpn, ","))
		}
		if fp := str(p, "fingerprint"); fp != "" {
			q.Set("pcs", fp)
		} else if boolOf(p, "skip-cert-verify") {
			q.Set("allowInsecure", "1")
		}
	}

	switch typ {
	case "vless":
		if f := str(p, "flow"); f != "" {
			q.Set("flow", f)
		}
		if enc := str(p, "encryption"); enc != "" {
			q.Set("encryption", enc)
		}
		transport()
		tlsParams(boolOf(p, "tls"))
		return "vless://" + url.PathEscape(str(p, "uuid")) + "@" + hostport + "?" + q.Encode() + frag, nil
	case "trojan":
		transport()
		tlsParams(true)
		return "trojan://" + url.PathEscape(str(p, "password")) + "@" + hostport + "?" + q.Encode() + frag, nil
	case "vmess":
		v := map[string]any{"v": "2", "ps": name, "add": server, "port": port, "id": str(p, "uuid"),
			"aid": firstNonEmpty(str(p, "alterId"), "0"), "scy": firstNonEmpty(str(p, "cipher"), "auto")}
		transport()
		v["net"] = q.Get("type")
		v["path"] = firstNonEmpty(q.Get("path"), q.Get("serviceName"))
		v["host"] = q.Get("host")
		if boolOf(p, "tls") {
			v["tls"] = "tls"
		}
		v["sni"] = str(p, "servername")
		v["fp"] = str(p, "client-fingerprint")
		if alpn := strList(p["alpn"]); len(alpn) > 0 {
			v["alpn"] = strings.Join(alpn, ",")
		}
		if boolOf(p, "skip-cert-verify") {
			v["allowInsecure"] = "1"
		}
		data, _ := json.Marshal(v)
		return "vmess://" + base64.StdEncoding.EncodeToString(data), nil
	case "ss":
		userinfo := base64.RawURLEncoding.EncodeToString([]byte(str(p, "cipher") + ":" + str(p, "password")))
		query := ""
		if plugin := str(p, "plugin"); plugin != "" {
			spec, err := clashPlugin(plugin, sub(p, "plugin-opts"))
			if err != nil {
				return "", err
			}
			query = "?" + url.Values{"plugin": {spec}}.Encode()
		}
		return "ss://" + userinfo + "@" + hostport + query + frag, nil
	case "hysteria2", "hy2":
		if s := str(p, "sni"); s != "" {
			q.Set("sni", s)
		}
		if o := str(p, "obfs"); o != "" {
			q.Set("obfs", o)
			q.Set("obfs-password", str(p, "obfs-password"))
		}
		if boolOf(p, "skip-cert-verify") {
			q.Set("insecure", "1")
		}
		if fp := str(p, "fingerprint"); fp != "" {
			q.Set("pinSHA256", fp)
		}
		if ports := str(p, "ports"); ports != "" {
			q.Set("mport", ports) // port hopping
		}
		auth := firstNonEmpty(str(p, "password"), str(p, "auth"))
		return "hysteria2://" + url.PathEscape(auth) + "@" + hostport + "?" + q.Encode() + frag, nil
	case "tuic":
		if s := str(p, "sni"); s != "" {
			q.Set("sni", s)
		}
		if alpn := strList(p["alpn"]); len(alpn) > 0 {
			q.Set("alpn", strings.Join(alpn, ","))
		}
		if cc := str(p, "congestion-controller"); cc != "" {
			q.Set("congestion_control", cc)
		}
		if m := str(p, "udp-relay-mode"); m != "" {
			q.Set("udp_relay_mode", m)
		}
		if boolOf(p, "skip-cert-verify") {
			q.Set("allow_insecure", "1")
		}
		return "tuic://" + url.PathEscape(str(p, "uuid")) + ":" + url.PathEscape(str(p, "password")) + "@" + hostport + "?" + q.Encode() + frag, nil
	}
	return "", errorf("тип %s не поддерживается", typ)
}

// clashPlugin turns Clash "plugin" + "plugin-opts" into a SIP003 plugin
// string for the share link.
func clashPlugin(plugin string, opts map[string]any) (string, error) {
	switch strings.ToLower(plugin) {
	case "obfs", "simple-obfs":
		spec := "obfs-local;obfs=" + firstNonEmpty(str(opts, "mode"), "http")
		if h := str(opts, "host"); h != "" {
			spec += ";obfs-host=" + h
		}
		return spec, nil
	case "v2ray-plugin":
		if m := str(opts, "mode"); m != "" && m != "websocket" {
			return "", errorf("плагин Shadowsocks %s не поддерживается", plugin+" "+m)
		}
		spec := "v2ray-plugin"
		if boolOf(opts, "tls") {
			spec += ";tls"
		}
		if h := str(opts, "host"); h != "" {
			spec += ";host=" + h
		}
		if path := str(opts, "path"); path != "" {
			spec += ";path=" + path
		}
		if boolOf(opts, "mux") {
			spec += ";mux=4"
		}
		return spec, nil
	}
	return "", errorf("плагин Shadowsocks %s не поддерживается", plugin)
}
