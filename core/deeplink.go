package core

import (
	"encoding/base64"
	"net/url"
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
			name, _ = url.PathUnescape(rest[i+1:])
		}
		return ImportRequest{Subscriptions: []string{strings.TrimSpace(decoded)}, Name: name}, nil
	}
	if strings.HasPrefix(strings.ToLower(rest), "crypt") {
		return ImportRequest{}, errorf("зашифрованные ссылки Happ открываются только в Happ. Попросите у провайдера обычную ссылку на подписку")
	}

	name := ""
	// the name often sits in the fragment
	body := rest
	if i := strings.LastIndex(body, "#"); i >= 0 {
		name, _ = url.PathUnescape(body[i+1:])
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
		return targetRequest(target, name)
	}
	return ImportRequest{}, errorf("в ссылке нет подписки")
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
	if !strings.ContainsAny(text, " \n\r\t") {
		if scheme, _, ok := strings.Cut(text, "://"); ok {
			s := strings.ToLower(scheme)
			for _, known := range append(DeepLinkSchemes, "sub") {
				if s == known {
					if req, err := ParseDeepLink(text); err == nil {
						return req
					}
					return ImportRequest{}
				}
			}
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

// looksLikeSubscription filters out ordinary web pages copied by the user:
// subscription URLs have a long token in the path or query.
func looksLikeSubscription(u *url.URL) bool {
	p := strings.Trim(u.Path, "/")
	segments := strings.Split(p, "/")
	last := segments[len(segments)-1]
	if strings.Contains(strings.ToLower(p), "sub") || strings.Contains(strings.ToLower(u.Host), "sub") {
		return true
	}
	if len(last) >= 10 && !strings.Contains(last, ".") {
		return true
	}
	for _, v := range u.Query() {
		for _, s := range v {
			if len(s) >= 16 {
				return true
			}
		}
	}
	return false
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
