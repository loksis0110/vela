package core

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// UserAgent is sent when downloading subscriptions. Most panels
// (Marzban, 3x-ui, Remnawave) return plain share links for it.
var UserAgent = "Vela/1.0 (v2rayN-compatible)"

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
	UpdatedAt      int64    `json:"updatedAt"`
}

// FetchSubscription downloads and parses a subscription.
func FetchSubscription(ctx context.Context, rawURL string) (*Subscription, error) {
	rawURL = strings.TrimSpace(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("это не ссылка на подписку")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "*/*")
	client := &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
	}}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("не удалось загрузить подписку: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("сервер подписки ответил %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	parsed := ParseText(string(body))
	if len(parsed.Nodes) == 0 {
		msg := "в подписке нет поддерживаемых серверов"
		if len(parsed.Errors) > 0 {
			msg += ": " + parsed.Errors[0]
		}
		return nil, fmt.Errorf("%s", msg)
	}
	sub := &Subscription{URL: rawURL, Nodes: parsed.Nodes, Errors: parsed.Errors, UpdatedAt: time.Now().Unix()}
	sub.Title = decodeHeaderText(resp.Header.Get("profile-title"))
	if sub.Title == "" {
		sub.Title = u.Hostname()
	}
	if v := resp.Header.Get("profile-update-interval"); v != "" {
		sub.UpdateInterval, _ = strconv.Atoi(strings.TrimSpace(v))
	}
	sub.SupportURL = resp.Header.Get("support-url")
	sub.WebPageURL = resp.Header.Get("profile-web-page-url")
	for _, part := range strings.Split(resp.Header.Get("subscription-userinfo"), ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		n, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
		switch strings.ToLower(k) {
		case "upload":
			sub.Upload = int64(n)
		case "download":
			sub.Download = int64(n)
		case "total":
			sub.Total = int64(n)
		case "expire":
			sub.Expire = int64(n)
		}
	}
	// node ids must be stable per subscription
	for i := range sub.Nodes {
		sub.Nodes[i].ID = shortHash(rawURL + "|" + sub.Nodes[i].Link)
	}
	return sub, nil
}

func decodeHeaderText(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "base64:") {
		if b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(v, "base64:")); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	if s, err := url.QueryUnescape(v); err == nil {
		return s
	}
	return v
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

// SplitImport separates pasted text into subscription URLs (http/https) and
// the remaining share links, so several subscriptions can be added at once.
func SplitImport(text string) (subscriptions []string, links string) {
	var rest []string
	seen := map[string]bool{}
	for _, f := range strings.FieldsFunc(strings.TrimPrefix(text, "\ufeff"), func(r rune) bool {
		return r == '\n' || r == '\r' || r == ' ' || r == '\t'
	}) {
		f = strings.TrimSpace(f)
		lower := strings.ToLower(f)
		if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
			if !seen[f] {
				seen[f] = true
				subscriptions = append(subscriptions, f)
			}
			continue
		}
		rest = append(rest, f)
	}
	return subscriptions, strings.Join(rest, "\n")
}
