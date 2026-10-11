package core

import (
	"encoding/base64"
	"testing"
)

// The parsers read untrusted text (subscriptions, the clipboard, app links):
// they must never panic, a panic would crash the mobile apps.

func fuzzSeeds(f *testing.F) {
	for _, s := range sampleLinks {
		f.Add(s)
	}
	for _, s := range []string{
		xhttpLink, "vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"add":"a","port":"1","id":"x","net":"kcp"}`)),
		"hy2://a@[::1]:1-2,3?mport=4-5", "ss://YWVzLTEyOC1nY206eA@[::1]:1/?plugin=obfs-local%3Bobfs%3Dhttp",
		"proxies:\n  - {name: a, type: vless, server: x, port: 1}", `{"outbounds":[{"protocol":"vless"}]}`,
		`[{"type":"vless","server":"a","server_port":1}]`, "vela://import/sub://aHR0cHM6Ly94", "happ://add/https://x/sub#n",
		"velajson://e30#x", "trojan://%zz@h:1", "vless://@:0", "https://sub.example.com:2096/sub/abc123def",
	} {
		f.Add(s)
	}
}

func FuzzParseText(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, s string) {
		res := ParseText(s)
		for _, n := range res.Nodes {
			IsEncrypted(n)
		}
	})
}

func FuzzImport(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, s string) {
		DetectImport(s)
		ParseDeepLink(s)
		SplitImport(s)
		DecodeBackup(BackupPrefix + s)
	})
}
