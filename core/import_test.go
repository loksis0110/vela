package core

import "testing"

func TestSplitImport(t *testing.T) {
	subs, links := SplitImport("https://a.example/sub1\n  https://b.example/sub2?x=1 \nvless://u@h:1?type=tcp#n\nhttps://a.example/sub1\n")
	if len(subs) != 2 || subs[1] != "https://b.example/sub2?x=1" {
		t.Fatalf("subs %v", subs)
	}
	if links != "vless://u@h:1?type=tcp#n" {
		t.Fatalf("links %q", links)
	}
	// base64 subscription body stays in links
	subs, links = SplitImport("dmxlc3M6Ly91QGg6MT90eXBlPXRjcCNu")
	if len(subs) != 0 || links == "" {
		t.Fatal("base64 body lost")
	}
}
