package core

import (
	"encoding/base64"
	"net/url"
	"regexp"
	"strings"
)

// ImportRequest is what a link from a provider's site (or the clipboard)
// asks the app to add.
type ImportRequest struct {
	// Subscription URLs (http/https).
	Subscriptions []string `json:"subscriptions"`
	// Share links of single servers, one per line.
	Links string `json:"links"`
	// Number of servers in Links.
	LinkCount int `json:"linkCount"`
	// Name suggested by the link, if any.
	Name string `json:"name,omitempty"`
}

// Empty reports whether there is nothing to add.
func (r ImportRequest) Empty() bool {
	return len(r.Subscriptions) == 0 && r.LinkCount == 0
}

// DeepLinkSchemes are the URL schemes Vela registers. Besides its own it
// answers the "add subscription" links that provider sites show for other
// popular clients. On desktop these are registered only when no other app
// handles them.
var DeepLinkSchemes = []string{
	"vela", "happ", "v2rayng", "v2rayn", "hiddify", "sing-box", "clash", "clashmeta",
	"mihomo", "flclash", "streisand", "v2box", "karing", "sn", "incy",
}

var proxySchemes = []string{"vless://", "vmess://", "trojan://", "ss://", "hysteria2://", "hy2://", "tuic://"}

func isProxyLink(s string) bool {
	l := strings.ToLower(s)
	for _, p := range proxySchemes {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

func isHTTP(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

// ParseDeepLink understands links like:
//
//	vela://import/https://example.com/sub#Name
//	happ://add/https://example.com/sub
//	v2rayng://install-config?url=https%3A%2F%2Fexample.com%2Fsub&name=Name
//	hiddify://import/https://example.com/sub#Name
//	sing-box://import-remote-profile?url=...#Name
//	clash://install-config?url=...&name=...
//	sub://<base64 of a subscription URL>
//
// It returns an error for anything it can't use.
func ParseDeepLink(raw string) (ImportRequest, error) {
	raw = strings.TrimSpace(raw)
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return ImportRequest{}, errorf("это не ссылка")
	}
	scheme = strings.ToLower(scheme)
	if isHTTP(raw) {
		return ImportRequest{Subscriptions: []string{raw}}, nil
	}
	if isProxyLink(raw) {
		return linksRequest(raw, "")
	}
	if scheme == "sub" {
		decoded, ok := tryBase64(strings.SplitN(rest, "#", 2)[0])
		if !ok || !isHTTP(decoded) {
			return ImportRequest{}, errorf("не удалось прочитать ссылку")
		}
		name := ""
		if i := strings.Index(rest, "#"); i >= 0 {
			name = unescapeName(rest[i+1:])
		}
		return ImportRequest{Subscriptions: []string{strings.TrimSpace(decoded)}, Name: name}, nil
	}
	if strings.HasPrefix(strings.ToLower(rest), "crypt") {
		return ImportRequest{}, errorf("зашифрованные ссылки Happ открываются только в Happ. Попросите у провайдера обычную ссылку на подписку")
	}

	name := ""
	// the name often sits in the fragment
	body := rest
	fragment := ""
	if i := strings.LastIndex(body, "#"); i >= 0 {
		fragment = body[i+1:]
		name = unescapeName(fragment)
		body = body[:i]
	}
	// ?url=...&name=...
	if pathPart, query, hasQuery := strings.Cut(body, "?"); hasQuery {
		if q, err := url.ParseQuery(query); err == nil {
			if n := firstNonEmpty(q.Get("name"), q.Get("remarks"), q.Get("title")); n != "" {
				name = n
			}
			if target := firstNonEmpty(q.Get("url"), q.Get("link"), q.Get("config")); target != "" {
				return targetRequest(target, name)
			}
		}
		// happ://add/https://host/sub?token=x: the query belongs to the URL
		body = pathPart + "?" + query
	}
	// <action>/<target>
	if _, target, ok := strings.Cut(body, "/"); ok && target != "" {
		if fragment != "" && isProxyLink(target) {
			// vela://import/vless://...#Server: the name belongs to the server
			return linksRequest(target+"#"+fragment, "")
		}
		return targetRequest(target, name)
	}
	// streisand://<base64 of a link>
	if decoded, ok := tryBase64(body); ok {
		if decoded = strings.TrimSpace(decoded); isHTTP(decoded) || isProxyLink(decoded) {
			return targetRequest(decoded, name)
		}
	}
	return ImportRequest{}, errorf("в ссылке нет подписки")
}

func unescapeName(s string) string {
	if n, err := url.PathUnescape(s); err == nil {
		return strings.TrimSpace(n)
	}
	return strings.TrimSpace(s)
}

func targetRequest(target, name string) (ImportRequest, error) {
	target = strings.TrimSpace(target)
	if !strings.Contains(target, "://") {
		if unescaped, err := url.QueryUnescape(target); err == nil {
			target = unescaped
		}
	}
	if !strings.Contains(target, "://") {
		if decoded, ok := tryBase64(target); ok {
			target = strings.TrimSpace(decoded)
		}
	}
	switch {
	case isHTTP(target):
		return ImportRequest{Subscriptions: []string{target}, Name: name}, nil
	case isProxyLink(target) || strings.HasPrefix(strings.ToLower(target), jsonLinkScheme+"://"):
		return linksRequest(target, name)
	}
	return ImportRequest{}, errorf("в ссылке нет подписки")
}

func linksRequest(text, name string) (ImportRequest, error) {
	res := ParseText(text)
	if len(res.Nodes) == 0 {
		if len(res.Errors) > 0 {
			return ImportRequest{}, errorf("%s", res.Errors[0])
		}
		return ImportRequest{}, errorf("в ссылке нет серверов")
	}
	var links []string
	for _, n := range res.Nodes {
		links = append(links, n.Link)
	}
	return ImportRequest{Links: strings.Join(links, "\n"), LinkCount: len(links), Name: name}, nil
}

// DetectImport looks at text (usually the clipboard) and says what could be
// imported from it: subscription URLs, share links or an app link. Anything
// else gives an empty request, so apps can check the clipboard quietly.
func DetectImport(text string) ImportRequest {
	text = strings.TrimSpace(strings.TrimPrefix(text, "\ufeff"))
	if text == "" || len(text) > 1<<20 {
		return ImportRequest{}
	}
	// a single app link
	if single := trimURL(text); !strings.ContainsAny(single, " \n\r\t") {
		if scheme, _, ok := strings.Cut(single, "://"); ok && isDeepLinkScheme(scheme) {
			if req, err := ParseDeepLink(single); err == nil {
				return req
			}
			return ImportRequest{}
		}
	}
	subs, rest := SplitImport(text)
	var req ImportRequest
	for _, s := range subs {
		u, err := url.Parse(s)
		if err != nil || u.Host == "" || !strings.Contains(u.Host, ".") {
			continue
		}
		// only links that look like subscriptions, not every web page
		if looksLikeSubscription(u) {
			req.Subscriptions = append(req.Subscriptions, s)
		}
	}
	if strings.Contains(rest, "://") || looksLikeBase64(rest) {
		res := ParseText(rest)
		var links []string
		for _, n := range res.Nodes {
			links = append(links, n.Link)
		}
		req.Links = strings.Join(links, "\n")
		req.LinkCount = len(links)
	}
	return req
}

func isDeepLinkScheme(scheme string) bool {
	s := strings.ToLower(scheme)
	if s == "sub" {
		return true
	}
	for _, known := range DeepLinkSchemes {
		if s == known {
			return true
		}
	}
	return false
}

// Sites people often copy links from; their URLs carry ids that look like
// subscription tokens.
var commonSites = []string{
	"google.", "youtube.com", "youtu.be", "github.com", "gitlab.com",
	"reddit.com", "wikipedia.org", "t.me", "telegram.", "vk.com", "vk.ru", "yandex.", "ya.ru",
	"mail.ru", "ok.ru", "dzen.ru", "rutube.ru", "instagram.com", "facebook.com", "twitter.com",
	"x.com", "tiktok.com", "apple.com", "microsoft.com", "live.com", "office.com", "amazon.",
	"ozon.ru", "wildberries.ru", "avito.ru", "aliexpress.", "notion.", "figma.com", "dropbox.com",
	"medium.com", "substack.com", "habr.com", "stackoverflow.com", "linkedin.com", "spotify.com",
	"discord.com", "discord.gg", "zoom.us", "twitch.tv", "pinterest.", "imgur.com", "bit.ly",
}

func isCommonSite(host string) bool {
	host = strings.ToLower(host)
	for _, site := range commonSites {
		if strings.HasSuffix(site, ".") {
			if strings.HasPrefix(host, site) || strings.Contains(host, "."+site) {
				return true
			}
		} else if host == site || strings.HasSuffix(host, "."+site) {
			return true
		}
	}
	return false
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// tokenLike tells random tokens (Marzban, 3x-ui, Remnawave ids, UUIDs)
// from words, numbers and article slugs ("post-2024-01-15-title").
func tokenLike(s string, minLen int) bool {
	if len(s) < minLen {
		return false
	}
	if uuidPattern.MatchString(s) {
		return true
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-' || c == '_' || c == '=') {
			return false
		}
	}
	// at least one part mixes letters with digits or both letter cases
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '_' }) {
		var lower, upper, digit bool
		for _, c := range part {
			switch {
			case c >= 'a' && c <= 'z':
				lower = true
			case c >= 'A' && c <= 'Z':
				upper = true
			case c >= '0' && c <= '9':
				digit = true
			}
		}
		if (lower || upper) && digit || lower && upper && len(part) >= 8 {
			return true
		}
	}
	return false
}

var subscriptionID = regexp.MustCompile(`^[A-Za-z0-9_=-]{6,}$`)

// subscriptionWords are path segments and host labels subscription panels use.
var subscriptionWords = map[string]bool{
	"sub": true, "subs": true, "subscribe": true, "subscription": true, "vpn": true, "proxy": true,
}

// looksLikeSubscription filters out ordinary web pages copied by the user:
// subscription URLs have a token (not a word or an article slug) in the
// path or in a token-like query parameter.
func looksLikeSubscription(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	if strings.HasSuffix(host, "githubusercontent.com") {
		// free server lists published as files on GitHub
		p := strings.ToLower(u.Path)
		for _, w := range []string{"sub", "v2ray", "vless", "vmess", "vpn", "proxy", "config", "clash", "sing", "xray", "hysteria"} {
			if strings.Contains(p, w) {
				return true
			}
		}
		return false
	}
	if isCommonSite(host) {
		return false
	}
	keyword := false
	for _, label := range strings.FieldsFunc(strings.ToLower(u.Hostname()), func(r rune) bool { return r == '.' || r == '-' }) {
		if subscriptionWords[label] || strings.HasPrefix(label, "sub") && strings.Trim(label[3:], "0123456789") == "" {
			keyword = true
		}
	}
	shortToken, longToken := false, false
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, seg := range segments {
		if subscriptionWords[strings.ToLower(seg)] {
			keyword = true
		}
		// "/sub/<id>": panels let admins pick readable ids
		if l := strings.ToLower(seg); (l == "sub" || l == "subs") && i+1 < len(segments) && subscriptionID.MatchString(segments[i+1]) {
			return true
		}
		if tokenLike(seg, 16) {
			longToken = true
		} else if tokenLike(seg, 8) {
			shortToken = true
		}
	}
	for key, values := range u.Query() {
		switch strings.ToLower(key) {
		case "token", "key", "sub", "subid", "uuid", "auth", "secret", "access_token", "apikey":
			for _, v := range values {
				if tokenLike(v, 8) {
					return true
				}
			}
		}
	}
	// panels often listen on their own port (2096, 8443, ...)
	odd := u.Port() != "" && u.Port() != "443" && u.Port() != "80"
	return longToken || (shortToken && (keyword || odd))
}

func looksLikeBase64(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 16 || strings.ContainsAny(s, " ") {
		return false
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(strings.NewReplacer("\n", "", "\r", "").Replace(s)); err == nil {
			return strings.Contains(string(b), "://")
		}
	}
	return false
}
