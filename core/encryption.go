package core

import "strings"

// IsEncrypted reports whether a server encrypts the traffic it carries.
// Servers without TLS/Reality whose protocol sends data in the clear
// (VLESS, Trojan, SOCKS, HTTP), VMess with security "none"/"zero" and
// Shadowsocks "none" are not encrypted. Settings.RequireEncryption leaves
// such servers out of the configuration.
func IsEncrypted(n Node) bool {
	ob := n.Outbound
	if n.Link != "" {
		if fresh, err := ParseLink(n.Link); err == nil {
			ob = fresh.Outbound
		}
	}
	return outboundEncrypted(ob)
}

func outboundEncrypted(ob map[string]any) bool {
	if ob == nil {
		return false
	}
	typ, _ := ob["type"].(string)
	if typ == EngineXray {
		outs, _ := ob["outbounds"].([]any)
		if len(outs) == 0 {
			return false
		}
		first, _ := outs[0].(map[string]any)
		return xrayEncrypted(first)
	}
	if tls, ok := ob["tls"].(map[string]any); ok && tls["enabled"] == true {
		return true
	}
	switch typ {
	case "hysteria", "hysteria2", "tuic", "anytls", "naive", "shadowtls", "ssh", "wireguard":
		return true // always TLS, QUIC or their own encryption
	case "vmess":
		sec, _ := ob["security"].(string)
		return !weakVMess(sec)
	case "shadowsocks":
		m, _ := ob["method"].(string)
		return !weakShadowsocks(m)
	}
	return false
}

func weakVMess(security string) bool {
	switch strings.ToLower(strings.TrimSpace(security)) {
	case "none", "zero":
		return true
	}
	return false
}

func weakShadowsocks(method string) bool {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "", "none", "plain":
		return true
	}
	return false
}

// xrayEncrypted looks at the first (server) outbound of an Xray config.
func xrayEncrypted(ob map[string]any) bool {
	if ob == nil {
		return false
	}
	if ss, ok := ob["streamSettings"].(map[string]any); ok {
		switch sec, _ := ss["security"].(string); strings.ToLower(sec) {
		case "tls", "reality":
			return true
		}
	}
	settings, _ := ob["settings"].(map[string]any)
	user := xrayFirstUser(settings)
	get := func(key string) string {
		v, _ := user[key].(string)
		return v
	}
	proto, _ := ob["protocol"].(string)
	switch strings.ToLower(proto) {
	case "vless":
		enc := strings.ToLower(strings.TrimSpace(get("encryption")))
		return enc != "" && enc != "none" // VLESS encryption (ML-KEM)
	case "vmess":
		return !weakVMess(get("security")) // empty means "auto"
	case "shadowsocks":
		return !weakShadowsocks(get("method"))
	case "hysteria", "wireguard":
		return true
	}
	return false
}

// xrayFirstUser returns the user/server entry of Xray outbound settings in
// both the classic (vnext/servers lists) and the flat form.
func xrayFirstUser(settings map[string]any) map[string]any {
	if settings == nil {
		return map[string]any{}
	}
	for _, key := range []string{"vnext", "servers"} {
		list, _ := settings[key].([]any)
		if len(list) == 0 {
			continue
		}
		server, _ := list[0].(map[string]any)
		if server == nil {
			continue
		}
		if users, _ := server["users"].([]any); len(users) > 0 {
			if u, ok := users[0].(map[string]any); ok {
				return u
			}
		}
		return server
	}
	return settings
}
