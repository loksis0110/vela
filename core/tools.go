package core

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
	qrgen "github.com/skip2/go-qrcode"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// ---------------------------------------------------------------- QR codes

// EncodeQR renders text as a PNG QR code.
func EncodeQR(text string, size int) ([]byte, error) {
	if size <= 0 {
		size = 512
	}
	level := qrgen.Medium
	if len(text) > 900 {
		level = qrgen.Low
	}
	return qrgen.Encode(text, level, size)
}

// DecodeQR finds a QR code in a PNG/JPEG/GIF image and returns its text.
func DecodeQR(data []byte) (string, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", errors.New(T("это не картинка"))
	}
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", err
	}
	hints := map[gozxing.DecodeHintType]interface{}{gozxing.DecodeHintType_TRY_HARDER: true}
	res, err := qrcode.NewQRCodeReader().Decode(bmp, hints)
	if err != nil {
		return "", errors.New(T("на картинке не найден QR-код"))
	}
	return strings.TrimSpace(res.GetText()), nil
}

// ---------------------------------------------------------------- HTTP

// HTTPClient returns a client that dials through the given outbound; nil
// uses the system network (which goes through the VPN when it's on).
func HTTPClient(dialer N.Dialer, timeout time.Duration) *http.Client {
	tr := &http.Transport{
		Proxy:                 nil,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		ForceAttemptHTTP2:     true,
		DisableCompression:    true,
	}
	if dialer != nil {
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, M.ParseSocksaddr(addr))
		}
	}
	return &http.Client{Transport: tr, Timeout: timeout}
}

// ---------------------------------------------------------------- My IP

// IPInfo describes how websites see the user.
type IPInfo struct {
	IP          string `json:"ip"`
	Country     string `json:"country"`
	CountryCode string `json:"countryCode"`
	City        string `json:"city"`
	ISP         string `json:"isp"`
	Flag        string `json:"flag"`
}

// MyIP asks public services for the external address.
func MyIP(ctx context.Context, dialer N.Dialer) (*IPInfo, error) {
	client := HTTPClient(dialer, 12*time.Second)
	var info IPInfo
	if err := getJSON(ctx, client, "https://ipwho.is/?lang=ru", func(b []byte) error {
		var r struct {
			Success     bool   `json:"success"`
			IP          string `json:"ip"`
			Country     string `json:"country"`
			CountryCode string `json:"country_code"`
			City        string `json:"city"`
			Connection  struct {
				ISP string `json:"isp"`
				Org string `json:"org"`
			} `json:"connection"`
		}
		if err := json.Unmarshal(b, &r); err != nil || !r.Success || r.IP == "" {
			return errors.New("bad answer")
		}
		info = IPInfo{IP: r.IP, Country: r.Country, CountryCode: r.CountryCode, City: r.City, ISP: firstNonEmpty(r.Connection.ISP, r.Connection.Org)}
		return nil
	}); err != nil {
		// fallback: Cloudflare trace has the address and country code
		if err2 := getJSON(ctx, client, "https://www.cloudflare.com/cdn-cgi/trace", func(b []byte) error {
			for _, line := range strings.Split(string(b), "\n") {
				k, v, _ := strings.Cut(line, "=")
				switch k {
				case "ip":
					info.IP = v
				case "loc":
					info.CountryCode = v
				}
			}
			if info.IP == "" {
				return errors.New("bad answer")
			}
			return nil
		}); err2 != nil {
			return nil, errors.New(T("нет ответа: ") + ExplainError(err.Error()))
		}
	}
	info.Flag = flagEmoji(info.CountryCode)
	return &info, nil
}

func getJSON(ctx context.Context, client *http.Client, url string, parse func([]byte) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return parse(b)
}

func flagEmoji(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) != 2 || code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z' {
		return ""
	}
	return string([]rune{rune(code[0]-'A') + 0x1F1E6, rune(code[1]-'A') + 0x1F1E6})
}

// ---------------------------------------------------------------- speed test

// SpeedResult is the outcome of a speed test through the current route.
type SpeedResult struct {
	PingMs       int     `json:"pingMs"`
	DownloadMbps float64 `json:"downloadMbps"`
	UploadMbps   float64 `json:"uploadMbps"`
	Error        string  `json:"error,omitempty"`
}

// SpeedProgress reports intermediate values: phase is "ping", "download",
// "upload" or "done"; value is ms or Mbit/s.
type SpeedProgress func(phase string, value float64)

var (
	pingURL          = "https://speed.cloudflare.com/__down?bytes=0"
	SpeedDownloadURL = "https://speed.cloudflare.com/__down?bytes=200000000"
	SpeedUploadURL   = "https://speed.cloudflare.com/__up"
)

// SpeedTest measures latency, download and upload through the dialer
// (nil = system network). Each transfer runs for up to `limit`.
func SpeedTest(ctx context.Context, dialer N.Dialer, limit time.Duration, progress SpeedProgress) SpeedResult {
	if progress == nil {
		progress = func(string, float64) {}
	}
	if limit <= 0 {
		limit = 8 * time.Second
	}
	var res SpeedResult
	client := HTTPClient(dialer, limit+15*time.Second)

	// latency: best of a few small requests over a warm connection
	best := 0
	for i := 0; i < 4; i++ {
		start := time.Now()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, pingURL, nil)
		resp, err := client.Do(req)
		if err != nil {
			if i == 0 {
				res.Error = T("сервер не отвечает: ") + ExplainError(err.Error())
				progress("done", 0)
				return res
			}
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		ms := int(time.Since(start).Milliseconds())
		if i > 0 && (best == 0 || ms < best) { // first request includes the handshake
			best = ms
		}
	}
	res.PingMs = best
	progress("ping", float64(best))

	// download with 4 parallel streams
	res.DownloadMbps = transfer(ctx, limit, 4, func(ctx context.Context, counter *atomic.Int64) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, SpeedDownloadURL, nil)
		resp, err := client.Do(req)
		if err != nil {
			pause(ctx)
			return
		}
		defer resp.Body.Close()
		buf := make([]byte, 64<<10)
		for {
			n, err := resp.Body.Read(buf)
			counter.Add(int64(n))
			if err != nil {
				return
			}
		}
	}, func(mbps float64) { progress("download", mbps) })

	// upload with 3 parallel streams
	res.UploadMbps = transfer(ctx, limit, 3, func(ctx context.Context, counter *atomic.Int64) {
		body := &countingReader{ctx: ctx, counter: counter, left: 50 << 20}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, SpeedUploadURL, body)
		req.ContentLength = body.left
		resp, err := client.Do(req)
		if err != nil {
			pause(ctx)
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}, func(mbps float64) { progress("upload", mbps) })

	if res.DownloadMbps == 0 && res.UploadMbps == 0 {
		res.Error = T("не удалось измерить скорость")
	}
	progress("done", 0)
	return res
}

func transfer(parent context.Context, limit time.Duration, streams int, worker func(context.Context, *atomic.Int64), tick func(float64)) float64 {
	ctx, cancel := context.WithTimeout(parent, limit)
	defer cancel()
	var counter atomic.Int64
	for i := 0; i < streams; i++ {
		go func() {
			// repeat until time is up, so a fast link never runs out of data
			for ctx.Err() == nil {
				worker(ctx, &counter)
			}
		}()
	}
	start := time.Now()
	// the first second is TCP slow start; measure after it
	var baseBytes int64
	var baseTime time.Time
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	result := 0.0
	for {
		select {
		case <-ctx.Done():
			return result
		case now := <-ticker.C:
			total := counter.Load()
			if baseTime.IsZero() && now.Sub(start) >= time.Second {
				baseBytes, baseTime = total, now
				continue
			}
			if !baseTime.IsZero() {
				secs := now.Sub(baseTime).Seconds()
				if secs > 0 {
					result = float64(total-baseBytes) * 8 / secs / 1e6
					tick(result)
				}
			}
		}
	}
}

func pause(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(300 * time.Millisecond):
	}
}

type countingReader struct {
	ctx     context.Context
	counter *atomic.Int64
	left    int64
}

var randomBlock = func() []byte {
	b := make([]byte, 64<<10)
	rand.Read(b)
	return b
}()

func (r *countingReader) Read(p []byte) (int, error) {
	if r.ctx.Err() != nil {
		return 0, r.ctx.Err()
	}
	if r.left <= 0 {
		return 0, io.EOF
	}
	n := copy(p, randomBlock)
	if int64(n) > r.left {
		n = int(r.left)
	}
	r.left -= int64(n)
	r.counter.Add(int64(n))
	return n, nil
}

// ---------------------------------------------------------------- backup

// BackupPrefix starts an exported Vela configuration.
const BackupPrefix = "vela-backup:"

// Backup is a portable copy of everything needed to restore Vela.
type Backup struct {
	Version       int            `json:"v"`
	Subscriptions []BackupSub    `json:"subscriptions,omitempty"`
	Links         []string       `json:"links,omitempty"`
	Settings      *Settings      `json:"settings,omitempty"`
	Favorites     []string       `json:"favorites,omitempty"` // share links of favorite servers
	Extra         map[string]any `json:"extra,omitempty"`     // platform specific (theme, …)
}

type BackupSub struct {
	URL  string `json:"url"`
	Name string `json:"name,omitempty"`
}

// EncodeBackup returns text that can be pasted or scanned on another device.
func EncodeBackup(b Backup) (string, error) {
	b.Version = 1
	data, err := json.Marshal(b)
	if err != nil {
		return "", err
	}
	return BackupPrefix + base64.RawURLEncoding.EncodeToString(data), nil
}

// DecodeBackup parses text produced by EncodeBackup. ok is false when the
// text is not a backup at all.
func DecodeBackup(text string) (b Backup, ok bool, err error) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, BackupPrefix) {
		return b, false, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(text, BackupPrefix))
	if err != nil {
		return b, true, errors.New(T("резервная копия повреждена"))
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return b, true, errors.New(T("резервная копия повреждена"))
	}
	return b, true, nil
}

// ---------------------------------------------------------------- logs

var (
	logPrefix = regexp.MustCompile(`^[+-]\d{4} \d{4}-\d{2}-\d{2} (\d{2}:\d{2}:\d{2}) `)
	logConnID = regexp.MustCompile(`^(\w+) \[\d+ [\d.hmsµ]+\] `)
	logANSI   = regexp.MustCompile("\x1b\\[[0-9;]*m")
)

// TidyLogLine shortens a core log line for reading on a small screen:
// "+0300 2026-10-08 22:27:49 INFO [158 0ms] outbound/..." becomes
// "22:27:49 INFO outbound/...".
func TidyLogLine(line string) string {
	line = logANSI.ReplaceAllString(line, "")
	if m := logPrefix.FindStringSubmatch(line); m != nil {
		line = m[1] + " " + line[len(m[0]):]
		if len(line) > 9 {
			line = line[:9] + logConnID.ReplaceAllString(line[9:], "$1 ")
		}
	}
	return line
}

// ReadLog returns up to limit last lines of a core log file, tidied.
func ReadLog(path string, limit int) []string {
	f, err := os.Open(path)
	if err != nil {
		return []string{}
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() > 512<<10 {
		f.Seek(st.Size()-512<<10, 0)
	}
	lines := []string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		lines = append(lines, TidyLogLine(sc.Text()))
		if len(lines) > limit*2 {
			lines = lines[len(lines)-limit:]
		}
	}
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return lines
}

// HostHasIPv6 reports whether this computer has a global IPv6 address, i.e.
// IPv6 websites can be reached directly. Without it IPv6 inside the tunnel
// only slows things down (apps try IPv6 first and wait).
func HostHasIPv6() bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return true
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() != nil {
				continue
			}
			ip := ipn.IP
			// global unicast, not ULA (fc00::/7) — VPN adapters use ULA
			if ip.IsGlobalUnicast() && ip[0]&0xfe != 0xfc {
				return true
			}
		}
	}
	return false
}
