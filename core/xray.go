package core

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/velaapp/vela/xrayout"
	xtls "github.com/xtls/xray-core/transport/internet/tls"
)

// EngineXray marks servers that run on the embedded Xray core because
// sing-box can't handle their transport (XHTTP, mKCP) or encryption.
const EngineXray = "xray"

// needsXray tells whether a link must run on the Xray engine.
func needsXray(network, encryption string) bool {
	switch network {
	case "xhttp", "splithttp", "kcp", "mkcp":
		return true
	}
	enc := strings.ToLower(strings.TrimSpace(encryption))
	return enc != "" && enc != "none"
}

// xrayOutboundOptions wraps Xray outbounds into the sing-box "xray" outbound.
func xrayOutboundOptions(outbounds ...map[string]any) map[string]any {
	list := make([]any, len(outbounds))
	for i, o := range outbounds {
		list[i] = o
	}
	return map[string]any{"type": EngineXray, "outbounds": list}
}

// pinnedHex returns `pcs` hashes in the hex form Xray expects.
func pinnedHex(q url.Values) string {
	var out []string
	for _, b64 := range pinnedHashes(q) {
		if raw, err := base64.StdEncoding.DecodeString(b64); err == nil {
			out = append(out, hex.EncodeToString(raw))
		}
	}
	return strings.Join(out, ",")
}

// xrayStream builds Xray "streamSettings" from share-link parameters.
func xrayStream(q url.Values, network, security, host string) (map[string]any, error) {
	switch network {
	case "", "raw":
		network = "tcp"
	case "splithttp":
		network = "xhttp"
	case "mkcp":
		network = "kcp"
	case "h2":
		network = "http"
	}
	stream := map[string]any{"network": network}
	path := q.Get("path")
	hostHeader := q.Get("host")
	switch network {
	case "tcp":
		if strings.EqualFold(q.Get("headerType"), "http") {
			req := map[string]any{}
			if path != "" {
				req["path"] = splitList(path)
			}
			if hostHeader != "" {
				req["headers"] = map[string]any{"Host": splitList(hostHeader)}
			}
			stream["rawSettings"] = map[string]any{"header": map[string]any{"type": "http", "request": req}}
		}
	case "xhttp":
		x := map[string]any{"mode": firstNonEmpty(q.Get("mode"), "auto")}
		if path != "" {
			x["path"] = path
		}
		if hostHeader != "" {
			x["host"] = hostHeader
		}
		if extra := q.Get("extra"); extra != "" {
			var obj map[string]any
			if err := json.Unmarshal([]byte(extra), &obj); err != nil {
				return nil, errorf("неверный параметр extra у XHTTP")
			}
			x["extra"] = obj
		}
		stream["xhttpSettings"] = x
	case "kcp":
		// mKCP obfuscation lives in "finalmask" now: the encryption (seed or
		// the original one) first, the fake header closest to the wire
		enc := map[string]any{}
		if seed := q.Get("seed"); seed != "" {
			enc["value"] = seed
		}
		masks := []any{map[string]any{"type": "mkcp-legacy", "settings": enc}}
		ht := strings.ToLower(q.Get("headerType"))
		if ht == "wechat-video" {
			ht = "wechat" // the old name, still in most links
		}
		if ht != "" && ht != "none" {
			h := map[string]any{"header": ht}
			if ht == "dns" && hostHeader != "" {
				h["value"] = hostHeader
			}
			masks = append(masks, map[string]any{"type": "mkcp-legacy", "settings": h})
		}
		stream["kcpSettings"] = map[string]any{}
		stream["finalmask"] = map[string]any{"udp": masks}
	case "ws":
		w := map[string]any{}
		if path != "" {
			w["path"] = path
		}
		if hostHeader != "" {
			w["host"] = hostHeader
		}
		stream["wsSettings"] = w
	case "grpc":
		g := map[string]any{"serviceName": firstNonEmpty(q.Get("serviceName"), path)}
		if q.Get("mode") == "multi" {
			g["multiMode"] = true
		}
		if a := q.Get("authority"); a != "" {
			g["authority"] = a
		}
		stream["grpcSettings"] = g
	case "httpupgrade":
		h := map[string]any{}
		if path != "" {
			h["path"] = path
		}
		if hostHeader != "" {
			h["host"] = hostHeader
		}
		stream["httpupgradeSettings"] = h
	default:
		return nil, errorf("транспорт %s не поддерживается", network)
	}

	sni := firstNonEmpty(q.Get("sni"), q.Get("peer"))
	if sni == "" && hostHeader != "" && network != "kcp" {
		sni = strings.Split(hostHeader, ",")[0]
	}
	if sni == "" && net.ParseIP(host) == nil {
		sni = host
	}
	fp := xrayFingerprint(q.Get("fp"))
	switch security {
	case "", "none":
		stream["security"] = "none"
	case "tls", "xtls":
		t := map[string]any{"fingerprint": fp}
		if sni != "" {
			t["serverName"] = sni
		}
		if alpn := splitList(q.Get("alpn")); len(alpn) > 0 {
			t["alpn"] = alpn
		}
		if pins := pinnedHex(q); pins != "" {
			t["pinnedPeerCertSha256"] = pins
		}
		if vcn := q.Get("vcn"); vcn != "" {
			t["verifyPeerCertByName"] = vcn
		}
		if ech := q.Get("ech"); ech != "" {
			t["echConfigList"] = ech
		}
		stream["security"] = "tls"
		stream["tlsSettings"] = t
	case "reality":
		pbk := q.Get("pbk")
		if pbk == "" {
			return nil, errorf("в ссылке Reality нет pbk")
		}
		r := map[string]any{"fingerprint": fp, "publicKey": pbk}
		if sni != "" {
			r["serverName"] = sni
		}
		if sid := q.Get("sid"); sid != "" {
			r["shortId"] = sid
		}
		if spx := q.Get("spx"); spx != "" {
			r["spiderX"] = spx
		}
		if pqv := q.Get("pqv"); pqv != "" {
			r["mldsa65Verify"] = pqv
		}
		stream["security"] = "reality"
		stream["realitySettings"] = r
	default:
		return nil, errorf("security=%s не поддерживается", security)
	}
	return stream, nil
}

// xrayFingerprint maps "fp" to a fingerprint Xray knows: an unknown one
// would stop the whole connection from starting.
func xrayFingerprint(fp string) string {
	fp = strings.ToLower(strings.TrimSpace(fp))
	if fp == "" || fp == "none" || fp == "unsafe" || fp == "hellogolang" || xtls.GetFingerprint(fp) == nil {
		return "chrome"
	}
	return fp
}

// sanitizeXrayOutbound fixes settings of imported Xray configs that the
// current Xray rejects outright, so one old server doesn't break the
// whole connection: "allowInsecure" was removed, unknown fingerprints.
func sanitizeXrayOutbound(ob map[string]any) {
	ss, _ := ob["streamSettings"].(map[string]any)
	if ss == nil {
		return
	}
	for _, key := range []string{"tlsSettings", "realitySettings"} {
		t, _ := ss[key].(map[string]any)
		if t == nil {
			continue
		}
		delete(t, "allowInsecure")
		if fp, ok := t["fingerprint"].(string); ok && fp != "" {
			t["fingerprint"] = xrayFingerprint(fp)
		}
	}
}

// checkXray asks the Xray engine whether it accepts the outbounds.
func checkXray(outbounds []any) error {
	if err := xrayout.Check(outbounds); err != nil {
		return errorf("Xray не принимает настройки сервера: %v", err)
	}
	return nil
}

// xrayNode builds a node that runs on the Xray engine.
func xrayNode(protocol, xrayProtocol, host string, port int, settings map[string]any, q url.Values, network, security, name string) (Node, error) {
	stream, err := xrayStream(q, network, security, host)
	if err != nil {
		return Node{}, err
	}
	out := map[string]any{
		"protocol":       xrayProtocol,
		"tag":            "proxy",
		"settings":       settings,
		"streamSettings": stream,
	}
	if err := checkXray([]any{out}); err != nil {
		return Node{}, err
	}
	nw, _ := stream["network"].(string)
	return Node{Name: name, Protocol: protocol, Server: host, Port: port,
		Security: securityLabel(security), Network: nw, Engine: EngineXray,
		Outbound: xrayOutboundOptions(out)}, nil
}

func xrayVLESS(u *url.URL, host string, port int, q url.Values, network, security string) (Node, error) {
	user := map[string]any{"id": u.User.Username(), "encryption": firstNonEmpty(q.Get("encryption"), "none")}
	if flow := q.Get("flow"); flow != "" {
		user["flow"] = flow
	}
	settings := map[string]any{"vnext": []any{map[string]any{"address": host, "port": port, "users": []any{user}}}}
	return xrayNode("VLESS", "vless", host, port, settings, q, network, security, fragmentName(u))
}

func xrayTrojan(u *url.URL, host string, port int, password string, q url.Values, network, security string) (Node, error) {
	settings := map[string]any{"servers": []any{map[string]any{"address": host, "port": port, "password": password}}}
	return xrayNode("Trojan", "trojan", host, port, settings, q, network, security, fragmentName(u))
}

func xrayVMess(v vmessJSON, port int, q url.Values, network, security string) (Node, error) {
	user := map[string]any{"id": v.ID, "security": firstNonEmpty(v.Scy, "auto")}
	settings := map[string]any{"vnext": []any{map[string]any{"address": v.Add, "port": port, "users": []any{user}}}}
	return xrayNode("VMess", "vmess", v.Add, port, settings, q, network, security, strings.TrimSpace(v.PS))
}

// xrayServerInfo reads address, port and labels of an Xray outbound for
// display.
func xrayServerInfo(ob map[string]any) (protocol, host string, port int, network, security string) {
	proto, _ := ob["protocol"].(string)
	protocol = protocolLabel(proto)
	settings, _ := ob["settings"].(map[string]any)
	pick := func(m map[string]any) {
		if m == nil {
			return
		}
		if a, ok := m["address"].(string); ok && host == "" {
			host = a
		}
		if port == 0 {
			port = anyInt(m["port"])
		}
	}
	pick(settings)
	for _, key := range []string{"vnext", "servers"} {
		if list, ok := settings[key].([]any); ok && len(list) > 0 {
			m, _ := list[0].(map[string]any)
			pick(m)
		}
	}
	if ss, ok := ob["streamSettings"].(map[string]any); ok {
		network, _ = ss["network"].(string)
		security, _ = ss["security"].(string)
	}
	if network == "" {
		network = "tcp"
	}
	return protocol, host, port, network, securityLabel(security)
}

func protocolLabel(p string) string {
	switch strings.ToLower(p) {
	case "vless":
		return "VLESS"
	case "vmess":
		return "VMess"
	case "trojan":
		return "Trojan"
	case "shadowsocks", "ss":
		return "Shadowsocks"
	case "hysteria2", "hy2":
		return "Hysteria2"
	case "hysteria":
		return "Hysteria"
	case "tuic":
		return "TUIC"
	case "socks":
		return "SOCKS"
	case "http":
		return "HTTP"
	case "wireguard":
		return "WireGuard"
	case "anytls":
		return "AnyTLS"
	case "ssh":
		return "SSH"
	}
	if p == "" {
		return "?"
	}
	return strings.ToUpper(p[:1]) + p[1:]
}

func anyInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	}
	return 0
}
