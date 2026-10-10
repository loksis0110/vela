package core

import (
	"embed"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
	// "blocked": only sites blocked in Russia (or blocking Russia) go
	// through the server, everything else direct.
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

	// DisableFailover turns off switching to a working server when the
	// chosen one stops answering (on by default).
	DisableFailover bool `json:"disableFailover,omitempty"`
	// TLSFragment splits the TLS handshake to get past simple DPI:
	// "" off, "record" (light), "full" (strongest, slower).
	TLSFragment string `json:"tlsFragment,omitempty"`
	// Rules are user routing rules checked before everything else.
	Rules []RoutingRule `json:"rules,omitempty"`
	// Split tunneling by app: "" all apps, "exclude" listed apps go
	// direct, "only" only listed apps use the VPN. Apps are Android
	// package names or Windows process names (chrome.exe).
	AppMode string   `json:"appMode,omitempty"`
	Apps    []string `json:"apps,omitempty"`
}

// RoutingRule sends matching traffic direct, through the VPN or blocks it.
// Match is a domain (example.com also matches subdomains), a keyword in
// the form *word*, or an IP / CIDR.
type RoutingRule struct {
	Match  string `json:"match"`
	Action string `json:"action"` // "direct" | "proxy" | "block"
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
	// Favorite node ids: when set, automatic selection uses only them.
	Favorites []string `json:"favorites,omitempty"`
	// Node ids ordered by the last measured latency, best first. Used to
	// keep the automatic group small so servers aren't flooded by checks.
	Ranked []string `json:"ranked,omitempty"`
	// Node ids that failed the last check; kept out of automatic selection.
	Failed []string `json:"failed,omitempty"`
	// Optional log file (Android, iOS); empty keeps logs in memory.
	LogPath string `json:"logPath,omitempty"`
}

// MaxAutoCandidates limits how many servers the automatic selection tests.
const MaxAutoCandidates = 30

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
		return "", errorf("нет серверов для подключения")
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
	seenTag := map[string]bool{}
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
		applyFragment(ob, s.TLSFragment)
		tag := NodeTag(n)
		if seenTag[tag] {
			continue // duplicates saved by older versions
		}
		seenTag[tag] = true
		ob["tag"] = tag
		outbounds = append(outbounds, ob)
		nodeTags = append(nodeTags, tag)
		if n.ID == req.Selected {
			selected = tag
		}
	}
	if len(nodeTags) == 0 {
		return "", errorf("нет серверов для подключения")
	}
	selectorItems := append([]string{TagAuto}, nodeTags...)
	if selected == "" {
		if req.Selected == "auto" || len(nodeTags) > 1 {
			selected = TagAuto
		} else {
			selected = nodeTags[0]
		}
	}
	auto := autoCandidates(req, nodeTags)
	if req.Platform == PlatformIOS && len(nodeTags) > MaxAutoCandidates {
		// The Network Extension may use only ~50 MB: hundreds of servers don't
		// fit. Keep the chosen one, the automatic group and the favorites;
		// the app rebuilds the config when another server is picked.
		keep := map[string]bool{selected: true}
		for _, t := range auto {
			keep[t] = true
		}
		for _, id := range req.Favorites {
			keep[NodeTag(Node{ID: id})] = true
		}
		var obs []any
		var tags []string
		for _, ob := range outbounds {
			if t, _ := ob.(map[string]any)["tag"].(string); keep[t] {
				obs = append(obs, ob)
				tags = append(tags, t)
			}
		}
		outbounds, nodeTags = obs, tags
		selectorItems = append([]string{TagAuto}, nodeTags...)
	}
	outbounds = append([]any{
		map[string]any{
			"type": "selector", "tag": TagSelector,
			"outbounds": selectorItems, "default": selected,
			"interrupt_exist_connections": true,
		},
		map[string]any{
			"type": "urltest", "tag": TagAuto,
			"outbounds":                   auto,
			"url":                         "https://www.gstatic.com/generate_204",
			"interval":                    "10m",
			"tolerance":                   100,
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
	blocked := s.Routing == "blocked"
	if smart {
		addRuleSet("geosite-ru", "geosite-category-ru.srs")
		addRuleSet("geoip-ru", "geoip-ru.srs")
	}
	if blocked {
		addRuleSet("ru-blocked", "geosite-ru-blocked.srs")
		addRuleSet("ru-blocked-ip", "geoip-ru-blocked.srs")
		addRuleSet("ru-inside", "ru-inside.srs")
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
	dnsFinal := "dns-remote"
	if blocked {
		// blocked sites are resolved through the tunnel (providers' DNS may
		// return stub addresses for them), everything else locally
		dnsRules = append(dnsRules, map[string]any{"rule_set": []string{"ru-blocked", "ru-inside"}, "server": "dns-remote"})
		dnsFinal = "dns-direct"
	}
	desktopApps := req.Platform != PlatformAndroid && req.Platform != PlatformIOS && len(s.Apps) > 0 && s.Mode == "tun"
	if desktopApps {
		switch s.AppMode {
		case "exclude":
			// programs that bypass the VPN also resolve names directly
			dnsRules = append([]any{map[string]any{"process_path_regex": processRegex(s.Apps), "server": "dns-direct"}}, dnsRules...)
		case "only":
			dnsRules = append([]any{map[string]any{"process_path_regex": processRegex(s.Apps), "server": "dns-remote"}}, dnsRules...)
			dnsFinal = "dns-direct"
		}
	}
	strategy := "prefer_ipv4"
	if !s.IPv6 {
		strategy = "ipv4_only"
	}
	dns := map[string]any{
		"servers":  dnsServers,
		"rules":    dnsRules,
		"final":    dnsFinal,
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
		if req.Platform == PlatformAndroid && len(s.Apps) > 0 {
			switch s.AppMode {
			case "exclude":
				tunIn["exclude_package"] = s.Apps
			case "only":
				tunIn["include_package"] = s.Apps
			}
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
	rules = append(rules, userRules(s.Rules)...)
	final := TagSelector
	findProcess := false
	if !mobile && len(s.Apps) > 0 && s.Mode == "tun" {
		switch s.AppMode {
		case "exclude":
			rules = append(rules, map[string]any{"process_path_regex": processRegex(s.Apps), "outbound": TagDirect})
			findProcess = true
		case "only":
			rules = append(rules, map[string]any{"process_path_regex": processRegex(s.Apps), "outbound": TagSelector})
			final = TagDirect
			findProcess = true
		}
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
	if blocked {
		rules = append(rules, map[string]any{"rule_set": []string{"ru-blocked", "ru-blocked-ip", "ru-inside"}, "outbound": TagSelector})
		if final == TagSelector {
			final = TagDirect
		}
	}
	// QUIC from browsers often gets throttled; force fallback to TCP
	// only for blocked targets would be ideal, keep it simple: allow.
	route := map[string]any{
		"rules":                   rules,
		"rule_set":                ruleSets,
		"final":                   final,
		"auto_detect_interface":   true,
		"default_domain_resolver": map[string]any{"server": "dns-direct"},
	}
	if req.Platform == PlatformAndroid {
		route["override_android_vpn"] = true
	}
	if findProcess {
		route["find_process"] = true
	}

	// --- experimental -----------------------------------------------------
	// No cache_file: it would restore an old selector choice over the
	// server the user picked for this connection.
	experimental := map[string]any{}
	if req.ControllerPort > 0 {
		experimental["clash_api"] = map[string]any{
			"external_controller": fmt.Sprintf("127.0.0.1:%d", req.ControllerPort),
			"secret":              req.ControllerSecret,
		}
	}

	cfg := map[string]any{
		"log":          logOptions(s.LogLevel, req.LogPath),
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

// processRegex matches programs by file name, ignoring case
// ("discord.exe" also matches C:\...\Discord.exe).
func processRegex(apps []string) []string {
	out := make([]string, 0, len(apps))
	for _, a := range apps {
		a = strings.TrimSpace(a)
		if a != "" {
			out = append(out, `(?i)(^|[\\/])`+regexp.QuoteMeta(a)+`$`)
		}
	}
	return out
}

func logOptions(level, path string) map[string]any {
	o := map[string]any{"level": level, "timestamp": true}
	if path != "" {
		o["output"] = path
	}
	return o
}

// autoCandidates picks the servers the automatic selection may use:
// favorites if any, otherwise the best measured ones, otherwise the first
// ones of the list. A small group keeps background checks light.
func autoCandidates(req BuildRequest, all []string) []string {
	present := map[string]bool{}
	for _, t := range all {
		present[t] = true
	}
	pick := func(ids []string) []string {
		var out []string
		seen := map[string]bool{}
		for _, id := range ids {
			t := NodeTag(Node{ID: id})
			if present[t] && !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
			if len(out) == MaxAutoCandidates {
				break
			}
		}
		return out
	}
	failed := map[string]bool{}
	for _, id := range req.Failed {
		failed[id] = true
	}
	var favs []string
	for _, id := range req.Favorites {
		if !failed[id] {
			favs = append(favs, id)
		}
	}
	// two or more working favorites: automatic selection stays among them
	if fav := pick(favs); len(fav) >= 2 {
		return fav
	}
	out := pick(append(append([]string{}, favs...), req.Ranked...))
	if len(out) < MaxAutoCandidates {
		seen := map[string]bool{}
		for _, t := range out {
			seen[t] = true
		}
		for _, id := range req.Failed {
			seen[NodeTag(Node{ID: id})] = true
		}
		for _, t := range all {
			if len(out) >= MaxAutoCandidates {
				break
			}
			if !seen[t] {
				out = append(out, t)
			}
		}
	}
	if len(out) == 0 {
		// everything failed last time: still try the first ones
		n := len(all)
		if n > MaxAutoCandidates {
			n = MaxAutoCandidates
		}
		out = append(out, all[:n]...)
	}
	return out
}

// applyFragment enables TLS handshake fragmentation on TLS-based servers.
func applyFragment(ob map[string]any, mode string) {
	if mode == "" {
		return
	}
	t, ok := ob["tls"].(map[string]any)
	if !ok || t["enabled"] != true {
		return
	}
	// QUIC-based protocols have no TCP handshake to split
	switch ob["type"] {
	case "hysteria2", "hysteria", "tuic":
		return
	}
	copied := map[string]any{}
	for k, v := range t {
		copied[k] = v
	}
	if mode == "full" {
		copied["fragment"] = true
	} else {
		copied["record_fragment"] = true
	}
	ob["tls"] = copied
}

// userRules converts routing rules from the settings into sing-box rules.
func userRules(list []RoutingRule) []any {
	var out []any
	for _, r := range list {
		m := strings.TrimSpace(strings.ToLower(r.Match))
		m = strings.TrimPrefix(strings.TrimPrefix(m, "https://"), "http://")
		if i := strings.IndexByte(m, '/'); i >= 0 && net.ParseIP(m[:i]) == nil {
			m = m[:i] // drop URL path, keep CIDR
		}
		if m == "" {
			continue
		}
		rule := map[string]any{}
		switch {
		case strings.HasPrefix(m, "*") && strings.HasSuffix(m, "*") && len(m) > 2:
			rule["domain_keyword"] = []string{strings.Trim(m, "*")}
		case net.ParseIP(m) != nil:
			if strings.Contains(m, ":") {
				rule["ip_cidr"] = []string{m + "/128"}
			} else {
				rule["ip_cidr"] = []string{m + "/32"}
			}
		case strings.Contains(m, "/"):
			if _, _, err := net.ParseCIDR(m); err != nil {
				continue
			}
			rule["ip_cidr"] = []string{m}
		default:
			rule["domain_suffix"] = []string{strings.TrimPrefix(m, "*.")}
		}
		switch r.Action {
		case "block":
			rule["action"] = "reject"
		case "proxy":
			rule["outbound"] = TagSelector
		default:
			rule["outbound"] = TagDirect
		}
		out = append(out, rule)
	}
	return out
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
