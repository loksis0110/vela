package core

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestQRRoundTrip(t *testing.T) {
	link := "vless://11111111-2222-3333-4444-555555555555@example.com:443?type=tcp&security=reality&pbk=abc&sid=01&sni=www.microsoft.com&fp=chrome#Test"
	png, err := EncodeQR(link, 400)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeQR(png)
	if err != nil || got != link {
		t.Fatalf("decode %q %v", got, err)
	}
	if _, err := DecodeQR([]byte("not an image")); err == nil {
		t.Fatal("expected error")
	}
}

func TestBackupRoundTrip(t *testing.T) {
	s := DefaultSettings()
	s.Rules = []RoutingRule{{Match: "example.com", Action: "proxy"}}
	text, err := EncodeBackup(Backup{Subscriptions: []BackupSub{{URL: "https://a/sub", Name: "A"}}, Links: []string{"vless://x@h:1#n"}, Settings: &s})
	if err != nil || !strings.HasPrefix(text, BackupPrefix) {
		t.Fatal(err)
	}
	b, ok, err := DecodeBackup(text)
	if !ok || err != nil || b.Subscriptions[0].URL != "https://a/sub" || b.Settings.Rules[0].Match != "example.com" {
		t.Fatalf("%+v %v %v", b, ok, err)
	}
	if _, ok, _ := DecodeBackup("vless://x"); ok {
		t.Fatal("not a backup")
	}
	if _, ok, err := DecodeBackup(BackupPrefix + "!!!"); !ok || err == nil {
		t.Fatal("broken backup must error")
	}
}

func TestFlag(t *testing.T) {
	if flagEmoji("de") != "🇩🇪" || flagEmoji("x") != "" {
		t.Fatal(flagEmoji("de"))
	}
}

func TestSpeedTestLocal(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/__down", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("bytes") == "0" {
			return
		}
		buf := make([]byte, 64<<10)
		for {
			if _, err := w.Write(buf); err != nil {
				return
			}
		}
	})
	mux.HandleFunc("/__up", func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body) })
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	srv := &http.Server{Handler: mux}
	go srv.Serve(l)
	defer srv.Close()
	base := "http://" + l.Addr().String()
	oldD, oldU := SpeedDownloadURL, SpeedUploadURL
	SpeedDownloadURL, SpeedUploadURL = base+"/__down?bytes=1", base+"/__up"
	defer func() { SpeedDownloadURL, SpeedUploadURL = oldD, oldU }()
	pingURL = base + "/__down?bytes=0"
	defer func() { pingURL = "https://speed.cloudflare.com/__down?bytes=0" }()
	var phases []string
	res := SpeedTest(context.Background(), nil, 2*time.Second, func(p string, v float64) {
		if len(phases) == 0 || phases[len(phases)-1] != p {
			phases = append(phases, p)
		}
	})
	t.Logf("%+v phases=%v", res, phases)
	if res.Error != "" || res.DownloadMbps <= 0 || res.UploadMbps <= 0 {
		t.Fatalf("%+v", res)
	}
}

func TestTidyLogLine(t *testing.T) {
	got := TidyLogLine("+0300 2026-10-08 22:27:49 INFO [1589944357 0ms] outbound/direct[direct]: outbound connection to 127.0.0.1:38999")
	if got != "22:27:49 INFO outbound/direct[direct]: outbound connection to 127.0.0.1:38999" {
		t.Fatal(got)
	}
	if got := TidyLogLine("+0300 2026-10-08 22:27:49 INFO [12 2m5s] router: x"); got != "22:27:49 INFO router: x" {
		t.Fatal(got)
	}
	if TidyLogLine("plain") != "plain" {
		t.Fatal("plain")
	}
}

func TestFetchHeaders(t *testing.T) {
	SetFetchOptions(FetchOptions{UserAgent: "Test/1", HWID: "abc", DeviceOS: "Android", OSVersion: "15", DeviceModel: "Pixel"})
	defer SetFetchOptions(FetchOptions{})
	h := http.Header{}
	applyFetchHeaders(h, true)
	if h.Get("User-Agent") != "Test/1" || h.Get("x-hwid") != "abc" || h.Get("x-device-model") != "Pixel" {
		t.Fatal(h)
	}
	h = http.Header{}
	applyFetchHeaders(h, false)
	if h.Get("x-hwid") != "" || h.Get("x-device-model") != "" {
		t.Fatal("HWID sent over plain HTTP", h)
	}
	SetFetchOptions(FetchOptions{})
	h = http.Header{}
	applyFetchHeaders(h, true)
	if h.Get("User-Agent") != UserAgent || h.Get("x-hwid") != "" {
		t.Fatal(h)
	}
}
