package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// UserAgent is sent when downloading subscriptions. Most panels
// (Marzban, 3x-ui, Remnawave) return plain share links for it.
var UserAgent = "Vela/1.4 (v2rayN-compatible)"

// FetchOptions change how subscriptions are requested.
type FetchOptions struct {
	// UserAgent replaces the default one when not empty (some providers
	// only answer known clients).
	UserAgent string `json:"userAgent,omitempty"`
	// HWID identifies this installation to panels that limit the number of
	// devices (Remnawave: x-hwid). Empty: not sent.
	HWID        string `json:"hwid,omitempty"`
	DeviceOS    string `json:"deviceOs,omitempty"`
	OSVersion   string `json:"osVersion,omitempty"`
	DeviceModel string `json:"deviceModel,omitempty"`
}

var (
	fetchMu   sync.RWMutex
	fetchOpts FetchOptions
)

// SetFetchOptions applies to all following subscription downloads.
func SetFetchOptions(o FetchOptions) {
	fetchMu.Lock()
	fetchOpts = o
	fetchMu.Unlock()
}

// applyFetchHeaders sets the User-Agent and, over HTTPS only, the device
// headers: the HWID never travels unencrypted.
func applyFetchHeaders(h http.Header, secure bool) {
	fetchMu.RLock()
	o := fetchOpts
	fetchMu.RUnlock()
	ua := strings.TrimSpace(o.UserAgent)
	if ua == "" {
		ua = UserAgent
	}
	h.Set("User-Agent", ua)
	if o.HWID != "" && secure {
		h.Set("x-hwid", o.HWID)
		if o.DeviceOS != "" {
			h.Set("x-device-os", o.DeviceOS)
		}
		if o.OSVersion != "" {
			h.Set("x-ver-os", o.OSVersion)
		}
		if o.DeviceModel != "" {
			h.Set("x-device-model", o.DeviceModel)
		}
	}
}

// Subscription is the result of downloading a subscription URL.
type Subscription struct {
	URL            string   `json:"url"`
	Title          string   `json:"title"`
	Nodes          []Node   `json:"nodes"`
	Errors         []string `json:"errors"`
	Upload         int64    `json:"upload"`
	Download       int64    `json:"download"`
	Total          int64    `json:"total"`
	Expire         int64    `json:"expire"`
	UpdateInterval int      `json:"updateIntervalHours"`
	SupportURL     string   `json:"supportUrl,omitempty"`
	WebPageURL     string   `json:"webPageUrl,omitempty"`
	// Announce is a message from the provider (the "announce" header or an
	// "#announce:" line), shown as a banner.
	Announce  string `json:"announce,omitempty"`
	UpdatedAt int64  `json:"updatedAt"`
}

// FetchSubscription downloads and parses a subscription.
func FetchSubscription(ctx context.Context, rawURL string) (*Subscription, error) {
	rawURL = strings.TrimSpace(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errorf("это не ссылка на подписку")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	applyFetchHeaders(req.Header, u.Scheme == "https")
	req.Header.Set("Accept", "*/*")
	client := &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
	}, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errorf("слишком много перенаправлений")
		}
		if next.URL.Scheme != "https" {
			for _, k := range []string{"x-hwid", "x-device-os", "x-ver-os", "x-device-model"} {
				next.Header.Del(k)
			}
		}
		return nil
	}}
	// the transport is used once: don't leave its connections open
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		// the URL (with the user's token) is not part of the message
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		if errors.Is(err, context.DeadlineExceeded) {
			err = errors.New("timeout")
		}
		return nil, errorf("не удалось загрузить подписку: %s (%v)", ExplainError(err.Error()), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		switch resp.StatusCode {
		case http.StatusForbidden, http.StatusUnauthorized:
			return nil, errorf("сервер подписки отказал (%d). Возможно, нужен HWID или превышен лимит устройств", resp.StatusCode)
		case http.StatusNotFound:
			return nil, errorf("подписка не найдена (404). Проверьте ссылку")
		}
		return nil, errorf("сервер подписки ответил %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSubscriptionSize+1))
	if err != nil {
		return nil, errorf("не удалось загрузить подписку: %s (%v)", ExplainError(err.Error()), err)
	}
	if len(body) > maxSubscriptionSize {
		return nil, errorf("подписка слишком большая")
	}
	text, directives := extractDirectives(string(body))
	parsed := ParseText(text)
	if len(parsed.Nodes) == 0 {
		msg := T("в подписке нет поддерживаемых серверов")
		if len(parsed.Errors) > 0 {
			msg += ": " + parsed.Errors[0]
		}
		return nil, fmt.Errorf("%s", msg)
	}
	// headers win over "#key: value" lines in the body
	header := func(key string) string {
		if v := strings.TrimSpace(resp.Header.Get(key)); v != "" {
			return v
		}
		return directives[key]
	}
	sub := &Subscription{URL: rawURL, Nodes: parsed.Nodes, Errors: parsed.Errors, UpdatedAt: time.Now().Unix()}
	sub.Title = decodeHeaderText(header("profile-title"))
	if sub.Title == "" {
		sub.Title = u.Hostname()
	}
	if v := header("profile-update-interval"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			sub.UpdateInterval = min(n, 24*30)
		}
	}
	sub.SupportURL = safeLink(header("support-url"))
	sub.WebPageURL = safeLink(header("profile-web-page-url"))
	sub.Announce = decodeHeaderText(header("announce"))
	if len([]rune(sub.Announce)) > 500 {
		sub.Announce = string([]rune(sub.Announce)[:500]) + "…"
	}
	for _, part := range strings.Split(header("subscription-userinfo"), ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		n, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if n < 0 || n != n { // negative or NaN
			n = 0
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "upload":
			sub.Upload = int64(n)
		case "download":
			sub.Download = int64(n)
		case "total":
			sub.Total = int64(n)
		case "expire":
			if n > 1e11 { // some panels send milliseconds
				n /= 1000
			}
			sub.Expire = int64(n)
		}
	}
	// node ids must be stable per subscription
	for i := range sub.Nodes {
		sub.Nodes[i].ID = shortHash(rawURL + "|" + sub.Nodes[i].Link)
	}
	return sub, nil
}

// extractDirectives takes "#key: value" lines (Happ-style subscription
// metadata for providers that can't set headers) out of a plain or base64
// body.
func extractDirectives(body string) (string, map[string]string) {
	directives := map[string]string{}
	take := func(text string) (string, bool) {
		var rest []string
		found := false
		for _, line := range strings.Split(text, "\n") {
			l := strings.TrimSpace(line)
			if strings.HasPrefix(l, "#") {
				k, v, ok := strings.Cut(strings.TrimPrefix(l, "#"), ":")
				k = strings.ToLower(strings.TrimSpace(k))
				if ok && k != "" && !strings.Contains(k, " ") {
					directives[k] = strings.TrimSpace(v)
					found = true
				}
				continue
			}
			rest = append(rest, line)
		}
		return strings.Join(rest, "\n"), found
	}
	trimmed := strings.TrimSpace(strings.TrimPrefix(body, "\ufeff"))
	if !strings.Contains(trimmed, "://") && !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		if decoded, ok := tryBase64(trimmed); ok && strings.Contains(decoded, "://") {
			if rest, found := take(decoded); found {
				return rest, directives
			}
			return body, directives
		}
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") || looksLikeClash(trimmed) {
		return body, directives
	}
	rest, _ := take(body)
	return rest, directives
}

// maxSubscriptionSize limits a subscription body (tens of thousands of servers).
const maxSubscriptionSize = 8 << 20

func decodeHeaderText(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(strings.ToLower(v), "base64:") {
		if b, ok := tryBase64(v[len("base64:"):]); ok {
			return strings.TrimSpace(b)
		}
	}
	// percent-encoded UTF-8. "+" is a space only in form encoding (escapes
	// and no raw spaces: "%D0%9C%D0%BE%D0%B9+VPN"); "C++ VPN" keeps its pluses
	unescape := url.PathUnescape
	if strings.Contains(v, "%") && strings.Contains(v, "+") && !strings.Contains(v, " ") {
		unescape = url.QueryUnescape
	}
	if s, err := unescape(v); err == nil {
		return strings.TrimSpace(s)
	}
	return v
}

// safeLink keeps provider links (support, web page) the apps may open only
// when they are web or Telegram links: no javascript:, file:, intent: ...
func safeLink(v string) string {
	v = strings.TrimSpace(v)
	u, err := url.Parse(v)
	if err != nil {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "https", "http":
		if u.Host == "" {
			return ""
		}
		return v
	case "tg", "mailto":
		return v
	}
	return ""
}

func shortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:6])
}

// TCPPing measures TCP connect time to host:port in milliseconds.
// Returns -1 when the server is unreachable.
func TCPPing(host string, port int, timeout time.Duration) int {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), timeout)
	if err != nil {
		return -1
	}
	conn.Close()
	ms := int(time.Since(start).Milliseconds())
	if ms < 1 {
		ms = 1
	}
	return ms
}

// trimURL removes quotes, brackets and trailing punctuation around a URL
// copied from a message: «https://x/sub», (https://x/sub), https://x/sub.
func trimURL(s string) string {
	s = strings.TrimLeft(s, "\"'«<([{“„")
	if i := strings.Index(strings.ToLower(s), "http"); i > 0 && i < 4 {
		s = s[i:]
	}
	return strings.TrimRight(s, "\"'»>)]}”.,;:!?")
}

// SplitImport separates pasted text into subscription URLs (http/https) and
// the remaining share links, so several subscriptions can be added at once.
func SplitImport(text string) (subscriptions []string, links string) {
	var rest []string
	seen := map[string]bool{}
	for _, line := range strings.FieldsFunc(strings.TrimPrefix(text, "\ufeff"), func(r rune) bool {
		return r == '\n' || r == '\r'
	}) {
		for _, f := range strings.Fields(line) {
			if u := trimURL(f); isHTTP(u) {
				if !seen[u] {
					seen[u] = true
					subscriptions = append(subscriptions, u)
				}
				continue
			}
			// a raw space inside the "#name" of the previous share link
			if n := len(rest); n > 0 && !strings.Contains(f, "://") && strings.Contains(rest[n-1], "://") && strings.Contains(rest[n-1], "#") {
				rest[n-1] += " " + f
				continue
			}
			rest = append(rest, f)
		}
	}
	return subscriptions, strings.Join(rest, "\n")
}
