package main

import (
	"encoding/json"
	"testing"

	"github.com/velaapp/vela/core"
)

func testNodes(ids ...string) []core.Node {
	var out []core.Node
	for _, id := range ids {
		out = append(out, core.Node{ID: id, Link: "vless://" + id, Outbound: map[string]any{"type": "vless", "uuid": id}})
	}
	return out
}

// A subscription refresh restarts the core only when the server in use or
// the chosen one is gone.
func TestInUseGone(t *testing.T) {
	a := &App{}
	old := testNodes("a", "b", "c")
	cases := []struct {
		active, selected string
		fresh            []core.Node
		want             bool
	}{
		{"a", "auto", testNodes("a", "c", "d"), false},
		{"b", "auto", testNodes("a", "c", "d"), true},
		{"a", "a", testNodes("a"), false},
		{"a", "c", testNodes("a", "b"), true},
		{"", "auto", testNodes("a", "b", "d"), true},
	}
	for i, c := range cases {
		a.status.ActiveNode, a.state.SelectedNode = c.active, c.selected
		if got := a.inUseGoneLocked(old, c.fresh); got != c.want {
			t.Errorf("case %d: got %v", i, got)
		}
	}
}

// The window gets the servers without their outbound settings (most of the
// size with hundreds of servers); the saved state keeps them.
func TestSnapshotWithoutOutbounds(t *testing.T) {
	a := &App{pings: map[string]int{}, reasons: map[string]string{}, offers: map[string]*Offer{}}
	a.state.Profiles = []*Profile{{ID: "p", Kind: "subscription", Nodes: testNodes("a", "b")}}
	data, _ := json.Marshal(a.Snapshot())
	var snap struct {
		Profiles []struct {
			Nodes []map[string]any `json:"nodes"`
		} `json:"profiles"`
	}
	json.Unmarshal(data, &snap)
	if len(snap.Profiles) != 1 || len(snap.Profiles[0].Nodes) != 2 {
		t.Fatalf("snapshot: %s", data)
	}
	for _, n := range snap.Profiles[0].Nodes {
		if n["outbound"] != nil || n["link"] == "" {
			t.Fatalf("node in snapshot: %v", n)
		}
	}
	if a.state.Profiles[0].Nodes[0].Outbound == nil {
		t.Fatal("the saved state lost its outbound")
	}
}
