package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/velaapp/vela/core"
)

func (a *App) trafficPath() string { return filepath.Join(a.dataDir, "traffic.json") }

// TrafficSummary is the statistics screen: last 31 days, 12 months, totals.
func (a *App) TrafficSummary() core.TrafficSummary {
	return core.LoadTrafficSummary(a.trafficPath(), time.Now())
}

// ConnView is an open connection with the server name for the UI.
type ConnView struct {
	core.Connection
	// Server is the name of the server for connections through the VPN.
	Server string `json:"server,omitempty"`
}

// Connections lists the open connections of the running core.
func (a *App) Connections() (bool, []ConnView, error) {
	a.mu.Lock()
	port, secret := a.ctrlPort, a.ctrlSecret
	running := a.instance != nil && a.status.State == "connected"
	names := map[string]string{}
	if p := a.findProfile(a.state.ActiveProfile); p != nil && running {
		for _, n := range p.Nodes {
			names[core.NodeTag(n)] = n.Name
		}
	}
	a.mu.Unlock()
	if !running {
		return false, []ConnView{}, nil
	}
	list, err := core.ListConnections(port, secret)
	if err != nil {
		return true, []ConnView{}, err
	}
	out := make([]ConnView, len(list))
	for i, c := range list {
		out[i] = ConnView{Connection: c, Server: names[c.Outbound]}
	}
	return true, out, nil
}

// CloseConnection closes one connection, or all when id is "".
func (a *App) CloseConnection(id string) error {
	a.mu.Lock()
	port, secret, running := a.ctrlPort, a.ctrlSecret, a.instance != nil
	a.mu.Unlock()
	if !running {
		return nil
	}
	return core.CloseConnection(port, secret, id)
}

// desktopConfig adjusts the generated config for Windows: the core looks up
// which program opened each connection, so the connections screen can
// show it.
func desktopConfig(cfg string) string {
	if runtimePlatform() != "windows" {
		return cfg
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(cfg)))
	dec.UseNumber() // keeps ports and sizes as written
	var m map[string]any
	if dec.Decode(&m) != nil {
		return cfg
	}
	route, _ := m["route"].(map[string]any)
	if route == nil {
		return cfg
	}
	route["find_process"] = true
	out, err := json.Marshal(m)
	if err != nil {
		return cfg
	}
	return string(out)
}
