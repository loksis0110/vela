package core

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed rules/*.srs
var ruleFiles embed.FS

// Platform the configuration is generated for.
const (
	PlatformWindows = "windows"
	PlatformAndroid = "android"
	PlatformIOS     = "ios"
	PlatformLinux   = "linux"
	PlatformMacOS   = "macos"
)

// Settings are user preferences that change the generated config.
type Settings struct {
	// "tun" captures all traffic (needs admin on desktop),
	// "proxy" only sets the system proxy (desktop only),
	// "local" only opens a local SOCKS/HTTP proxy (desktop only).
	Mode string `json:"mode"`
	// "smart": Russian sites go direct, everything else through the server.
	// "global": everything through the server.
	Routing string `json:"routing"`
	// Block ads and trackers with a DNS + routing rule set.
	BlockAds bool `json:"blockAds"`
	// IPv6 inside the tunnel.
	IPv6 bool `json:"ipv6"`
	// TUN network stack: "mixed" (default), "system" or "gvisor".
	Stack string `json:"stack"`
	// Remote DNS (DoH through the tunnel) and direct DNS for local sites.
	RemoteDNS string `json:"remoteDns"`
	DirectDNS string `json:"directDns"`
	// Local mixed proxy port for proxy mode.
	ProxyPort int `json:"proxyPort"`
	// Allow other devices in LAN to use the local proxy.
	AllowLAN bool `json:"allowLan"`
	// Extra domains that always go direct / through the proxy.
	DirectDomains []string `json:"directDomains"`
	ProxyDomains  []string `json:"proxyDomains"`
	LogLevel      string   `json:"logLevel"`
}

// DefaultSettings returns sensible defaults.
func DefaultSettings() Settings {
	return Settings{
		Mode:      "tun",
		Routing:   "smart",
		BlockAds:  false,
		IPv6:      true,
		Stack:     "mixed",
		RemoteDNS: "https://1.1.1.1/dns-query",
		DirectDNS: "77.88.8.8",
		ProxyPort: 2080,
		LogLevel:  "warn",
	}
}

// BuildRequest describes what to connect to.
type BuildRequest struct {
	Platform string `json:"platform"`
	// Nodes of the active group (subscription or manual list).
	Nodes []Node `json:"nodes"`
	// ID of the selected node, or "auto" to pick the fastest one and
	// fail over automatically.
	Selected string   `json:"selected"`
	Settings Settings `json:"settings"`
	// Directory with writable data (cache, rule sets).
	DataDir string `json:"dataDir"`
	// When true the rule-set files are not written here: the process that
	// runs the config (the iOS Network Extension) puts them into DataDir
	// itself. Used with a placeholder DataDir it replaces before starting.
	SkipRuleInstall bool `json:"skipRuleInstall"`
	// Local controller used by the UI for traffic stats, switching and
	// latency tests.
	ControllerPort   int    `json:"controllerPort"`
	ControllerSecret string `json:"controllerSecret"`
}

// Tag names used in the generated config.
const (
	TagSelector = "proxy"
	TagAuto     = "auto"
	TagDirect   = "direct"
)

// NodeTag returns the outbound tag for a node.
func NodeTag(n Node) string { return "node-" + n.ID }

// InstallRules writes embedded rule sets into dir.
func InstallRules(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	entries, err := ruleFiles.ReadDir("rules")
	if err != nil {
		return err
	}
	for _, e := range entries {
		data, err := ruleFiles.ReadFile("rules/" + e.Name())
		if err != nil {
			return err
		}
		target := filepath.Join(dir, e.Name())
		if old, err := os.ReadFile(target); err == nil && len(old) == len(data) {
			continue
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// BuildConfig generates a sing-box JSON configuration.
func BuildConfig(req BuildRequest) (string, error) {
	if len(req.Nodes) == 0 {
		return "", fmt.Errorf("нет серверов для подключения")
	}
	s := req.Settings
	def := DefaultSettings()
	if s.Mode == "" {
		s.Mode = def.Mode
	}
	if s.Routing == "" {
		s.Routing = def.Routing
	}
	if s.Stack == "" {
		s.Stack = def.Stack
	}
	if s.RemoteDNS == "" {
		s.RemoteDNS = def.RemoteDNS
	}
	if s.DirectDNS == "" {
		s.DirectDNS = def.DirectDNS
	}
	if s.ProxyPort == 0 {
		s.ProxyPort = def.ProxyPort
	}
	if s.LogLevel == "" {
		s.LogLevel = def.LogLevel
	}
	mobile := req.Platform == PlatformAndroid || req.Platform == PlatformIOS
	if mobile {
		s.Mode = "tun"
	}
	ruleDir := filepath.Join(req.DataDir, "rules")
	if !req.SkipRuleInstall {
		if err := InstallRules(ruleDir); err != nil {
			return "", fmt.Errorf("rule sets: %w", err)
		}
	}

	// --- outbounds -------------------------------------------------------
	var outbounds []any
	var nodeTags []string
	selected := ""
	for _, n := range req.Nodes {
		// re-read the original link so fixes in the parser apply to servers
		// saved by older app versions
		if n.Link != "" {
			if fresh, err := ParseLink(n.Link); err == nil {
				n.Outbound = fresh.Outbound
			}
		}
		if n.Outbound == nil {
			continue
		}
		ob := map[string]any{}
		for k, v := range n.Outbound {
			ob[k] = v
		}
		tag := NodeTag(n)
		ob["tag"] = tag
		outbounds = append(outbounds, ob)
		nodeTags = append(nodeTags, tag)
		if n.ID == req.Selected {
			selected = tag
		}
	}
	if len(nodeTags) == 0 {
		return "", fmt.Errorf("нет серверов для подключения")
	}
	selectorItems := append([]string{TagAuto}, nodeTags...)
	if selected == "" {
		if req.Selected == "auto" || len(nodeTags) > 1 {
			selected = TagAuto
		} else {
			selected = nodeTags[0]
		}
	}
	outbounds = append([]any{
		map[string]any{
			"type": "selector", "tag": TagSelector,
			"outbounds": selectorItems, "default": selected,
			"interrupt_exist_connections": true,
		},
		map[string]any{
			"type": "urltest", "tag": TagAuto,
			"outbounds":                   nodeTags,
			"url":                         "https://www.gstatic.com/generate_204",
			"interval":                    "3m",
			"tolerance":                   80,
			"idle_timeout":                "30m",
			"interrupt_exist_connections": false,
		},
	}, outbounds...)
	outbounds = append(outbounds, map[string]any{"type": "direct", "tag": TagDirect})

	// --- rule sets ---------------------------------------------------------
	ruleSets := []any{}
	addRuleSet := func(tag, file string) {
		ruleSets = append(ruleSets, map[string]any{
			"type": "local", "tag": tag, "format": "binary",
			"path": filepath.Join(ruleDir, file),
		})
	}
	smart := s.Routing == "smart"
	if smart {
		addRuleSet("geosite-ru", "geosite-category-ru.srs")
		addRuleSet("geoip-ru", "geoip-ru.srs")
	}
	if s.BlockAds {
		addRuleSet("ads", "geosite-category-ads-all.srs")
	}

	// --- DNS -----------------------------------------------------------------
	dnsServers := []any{
		remoteDNS(s.RemoteDNS),
		map[string]any{"type": "udp", "tag": "dns-direct", "server": s.DirectDNS},
	}
	dnsRules := []any{}
	if s.BlockAds {
		dnsRules = append(dnsRules, map[string]any{"rule_set": []string{"ads"}, "action": "predefined", "rcode": "NOERROR"})
	}
	directDomains := append([]string{}, s.DirectDomains...)
	proxyDomains := append([]string{}, s.ProxyDomains...)
	if len(proxyDomains) > 0 {
		dnsRules = append(dnsRules, map[string]any{"domain_suffix": proxyDomains, "server": "dns-remote"})
	}
	if smart {
		directDomains = append(directDomains, "ru", "su", "xn--p1ai", "xn--d1acj3b")
	}
	if len(directDomains) > 0 {
		dnsRules = append(dnsRules, map[string]any{"domain_suffix": directDomains, "server": "dns-direct"})
	}
	if smart {
		dnsRules = append(dnsRules, map[string]any{"rule_set": []string{"geosite-ru"}, "server": "dns-direct"})
	}
	strategy := "prefer_ipv4"
	if !s.IPv6 {
		strategy = "ipv4_only"
	}
	dns := map[string]any{
		"servers":  dnsServers,
		"rules":    dnsRules,
		"final":    "dns-remote",
		"strategy": strategy,
	}

	// --- inbounds --------------------------------------------------------
	var inbounds []any
	if s.Mode == "tun" {
		tunIn := map[string]any{
			"type": "tun", "tag": "tun-in",
			"address":      []string{"172.19.0.1/30"},
			"mtu":          9000,
			"auto_route":   true,
			"strict_route": true,
			"stack":        s.Stack,
		}
		if s.IPv6 {
			tunIn["address"] = []string{"172.19.0.1/30", "fdfe:dcba:9876::1/126"}
		}
		if req.Platform == PlatformWindows {
			tunIn["interface_name"] = "Vela"
		}
		if req.Platform == PlatformIOS {
			// keep memory usage low inside the Network Extension
			tunIn["stack"] = "system"
			tunIn["mtu"] = 4064
		}
		if req.Platform == PlatformAndroid || req.Platform == PlatformIOS {
			// local network stays reachable (printers, Chromecast, routers)
			tunIn["route_exclude_address"] = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7", "fe80::/10"}
		}
		inbounds = append(inbounds, tunIn)
	}
	if !mobile {
		listen := "127.0.0.1"
		if s.AllowLAN {
			listen = "0.0.0.0"
		}
		mixed := map[string]any{
			"type": "mixed", "tag": "mixed-in",
			"listen": listen, "listen_port": s.ProxyPort,
		}
		if s.Mode == "proxy" {
			mixed["set_system_proxy"] = true
		}
		inbounds = append(inbounds, mixed)
	}

	// --- route -----------------------------------------------------------
	rules := []any{
		map[string]any{"action": "sniff"},
		map[string]any{"protocol": "dns", "action": "hijack-dns"},
		map[string]any{"ip_is_private": true, "outbound": TagDirect},
	}
	if s.BlockAds {
		rules = append(rules, map[string]any{"rule_set": []string{"ads"}, "action": "reject"})
	}
	if len(proxyDomains) > 0 {
		rules = append(rules, map[string]any{"domain_suffix": proxyDomains, "outbound": TagSelector})
	}
	if len(directDomains) > 0 {
		rules = append(rules, map[string]any{"domain_suffix": directDomains, "outbound": TagDirect})
	}
	if smart {
		rules = append(rules, map[string]any{"rule_set": []string{"geosite-ru", "geoip-ru"}, "outbound": TagDirect})
	}
	// QUIC from browsers often gets throttled; force fallback to TCP
	// only for blocked targets would be ideal, keep it simple: allow.
	route := map[string]any{
		"rules":                   rules,
		"rule_set":                ruleSets,
		"final":                   TagSelector,
		"auto_detect_interface":   true,
		"default_domain_resolver": map[string]any{"server": "dns-direct"},
	}
	if req.Platform == PlatformAndroid {
		route["override_android_vpn"] = true
	}

	// --- experimental -----------------------------------------------------
	experimental := map[string]any{
		"cache_file": map[string]any{
			"enabled":  true,
			"path":     filepath.Join(req.DataDir, "cache.db"),
			"cache_id": "vela",
		},
	}
	if req.ControllerPort > 0 {
		experimental["clash_api"] = map[string]any{
			"external_controller": fmt.Sprintf("127.0.0.1:%d", req.ControllerPort),
			"secret":              req.ControllerSecret,
		}
	}

	cfg := map[string]any{
		"log":          map[string]any{"level": s.LogLevel, "timestamp": true},
		"dns":          dns,
		"inbounds":     inbounds,
		"outbounds":    outbounds,
		"route":        route,
		"experimental": experimental,
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// remoteDNS turns "https://1.1.1.1/dns-query", "tls://8.8.8.8" or "1.1.1.1"
// into a DNS server object that is resolved through the tunnel.
func remoteDNS(addr string) map[string]any {
	server := map[string]any{"tag": "dns-remote", "detour": TagSelector}
	switch {
	case len(addr) > 8 && addr[:8] == "https://":
		rest := addr[8:]
		host, path := rest, "/dns-query"
		for i := 0; i < len(rest); i++ {
			if rest[i] == '/' {
				host, path = rest[:i], rest[i:]
				break
			}
		}
		server["type"] = "https"
		server["server"] = host
		server["path"] = path
	case len(addr) > 6 && addr[:6] == "tls://":
		server["type"] = "tls"
		server["server"] = addr[6:]
	default:
		server["type"] = "tcp"
		server["server"] = addr
	}
	return server
}
