package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sagernet/sing-box/adapter"
	N "github.com/sagernet/sing/common/network"

	"github.com/velaapp/vela/core"
)

// ---------------------------------------------------------------- favorites

func (a *App) SetFavorite(nodeID string, on bool) {
	a.mu.Lock()
	out := a.state.Favorites[:0:0]
	for _, id := range a.state.Favorites {
		if id != nodeID {
			out = append(out, id)
		}
	}
	if on {
		out = append(out, nodeID)
	}
	a.state.Favorites = out
	a.saveLocked()
	a.mu.Unlock()
	a.pushState()
}

func (a *App) SetTheme(theme string) {
	switch theme {
	case "light", "dark", "system":
	default:
		theme = "system"
	}
	a.mu.Lock()
	a.state.Theme = theme
	a.saveLocked()
	a.mu.Unlock()
	a.pushState()
}

// ---------------------------------------------------------------- pings on disk

type savedPings struct {
	Pings   map[string]int    `json:"pings"`
	Reasons map[string]string `json:"reasons"`
}

func (a *App) loadPings() {
	data, err := os.ReadFile(filepath.Join(a.dataDir, "pings.json"))
	if err != nil {
		return
	}
	var sp savedPings
	if json.Unmarshal(data, &sp) != nil {
		return
	}
	a.pingsMu.Lock()
	for k, v := range sp.Pings {
		a.pings[k] = v
	}
	for k, v := range sp.Reasons {
		a.reasons[k] = v
	}
	a.pingsMu.Unlock()
}

func (a *App) savePings() {
	a.pingsMu.Lock()
	data, _ := json.Marshal(savedPings{Pings: a.pings, Reasons: a.reasons})
	a.pingsMu.Unlock()
	os.WriteFile(filepath.Join(a.dataDir, "pings.json"), data, 0o600)
}

// ---------------------------------------------------------------- failover

// watchdog checks the connection every 45 seconds. When the chosen server
// stops answering twice in a row it switches to automatic selection; when
// automatic selection is in use it asks it to re-test its servers.
func (a *App) watchdog(stop chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	fails := 0
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		a.mu.Lock()
		instance := a.instance
		disabled := a.state.Settings.DisableFailover
		selected := a.state.SelectedNode
		// the chosen server already failed the check right after connecting
		alreadyDown := a.status.ErrorCode == "server_unreachable"
		a.mu.Unlock()
		if instance == nil || disabled {
			continue
		}
		if _, err := core.ProbeTag(context.Background(), instance, core.TagSelector, 8*time.Second); err == nil {
			fails = 0
			continue
		}
		fails++
		if fails < 2 && !alreadyDown {
			continue
		}
		fails = 0
		if selected != "auto" && selected != "" {
			name := a.nodeName(selected)
			// remembered as failed, so automatic selection skips it next time
			a.pingsMu.Lock()
			a.pings[selected] = -1
			a.reasons[selected] = tr("перестал отвечать во время работы")
			a.pingsMu.Unlock()
			a.savePings()
			if err := a.controller("PUT", "/proxies/"+core.TagSelector, map[string]string{"name": core.TagAuto}, nil); err == nil {
				a.mu.Lock()
				a.state.SelectedNode = "auto"
				a.saveLocked()
				a.status.Error, a.status.ErrorCode = "", ""
				a.status.Notice = fmt.Sprintf(tr("«%s» перестал отвечать. Vela переключилась на автовыбор"), name)
				a.mu.Unlock()
				a.pushState()
				a.pushStatus()
			}
			continue
		}
		if out, ok := instance.Outbound().Outbound(core.TagAuto); ok {
			if group, ok := out.(adapter.URLTestGroup); ok {
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				group.URLTest(ctx)
				cancel()
			}
		}
	}
}

func (a *App) nodeName(id string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if p := a.findProfile(a.state.ActiveProfile); p != nil {
		if n := a.findNode(p, id); n != nil {
			return n.Name
		}
	}
	return tr("Сервер")
}

// routeDialer returns the dialer that carries traffic now (nil = direct).
func (a *App) routeDialer() N.Dialer {
	a.mu.Lock()
	instance := a.instance
	a.mu.Unlock()
	if instance == nil {
		return nil
	}
	if out, ok := instance.Outbound().Outbound(core.TagSelector); ok {
		return out
	}
	return nil
}

// ---------------------------------------------------------------- my ip / speed

func (a *App) MyIP(ctx context.Context) (*core.IPInfo, error) {
	return core.MyIP(ctx, a.routeDialer())
}

// SpeedTest runs in the background and streams progress as "speed" events.
func (a *App) SpeedTest() error {
	if !a.speeding.CompareAndSwap(false, true) {
		return errors.New(tr("тест скорости уже идёт"))
	}
	dialer := a.routeDialer()
	go func() {
		defer a.speeding.Store(false)
		res := core.SpeedTest(context.Background(), dialer, 8*time.Second, func(phase string, v float64) {
			a.broadcast("speed", map[string]any{"phase": phase, "value": v})
		})
		a.broadcast("speed", map[string]any{"phase": "result", "result": res, "viaVPN": dialer != nil})
	}()
	return nil
}

// ---------------------------------------------------------------- QR

func (a *App) QRDataURL(text string) (string, error) {
	png, err := core.EncodeQR(text, 520)
	if err != nil {
		return "", errors.New(tr("слишком много данных для QR-кода"))
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}

// ---------------------------------------------------------------- backup

// Export returns a text with all subscriptions, own servers, settings and
// favorites, ready to paste or scan on another device.
func (a *App) Export() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	b := core.Backup{Extra: map[string]any{"theme": a.state.Theme, "platform": "desktop"}}
	settings := a.state.Settings
	b.Settings = &settings
	fav := map[string]bool{}
	for _, id := range a.state.Favorites {
		fav[id] = true
	}
	for _, p := range a.state.Profiles {
		if p.Kind == "subscription" {
			b.Subscriptions = append(b.Subscriptions, core.BackupSub{URL: p.URL, Name: p.CustomName})
		} else {
			for _, n := range p.Nodes {
				b.Links = append(b.Links, n.Link)
			}
		}
		for _, n := range p.Nodes {
			if fav[n.ID] {
				b.Favorites = append(b.Favorites, n.Link)
			}
		}
	}
	return core.EncodeBackup(b)
}

// importBackup restores an exported configuration. Existing subscriptions
// stay; new ones are added.
func (a *App) importBackup(ctx context.Context, b core.Backup) (string, error) {
	var lines []string
	for _, s := range b.Subscriptions {
		lines = append(lines, s.URL)
	}
	lines = append(lines, b.Links...)
	msg := ""
	var importErr error
	if len(lines) > 0 {
		// settings are restored even when the subscriptions can't be downloaded now
		if m, err := a.Import(ctx, strings.Join(lines, "\n")); err != nil {
			importErr = err
		} else {
			msg = m
		}
	}
	a.mu.Lock()
	oldSettings, _ := json.Marshal(a.state.Settings)
	for _, s := range b.Subscriptions {
		if s.Name != "" {
			if p := a.findProfileByURL(s.URL); p != nil {
				p.CustomName = s.Name
				a.dedupeNameLocked(p)
			}
		}
	}
	if b.Settings != nil {
		cur := a.state.Settings
		a.state.Settings = *b.Settings
		if a.state.Settings.Mode == "" {
			a.state.Settings.Mode = cur.Mode
		}
		if p, _ := b.Extra["platform"].(string); p != "desktop" {
			// apps of a phone mean nothing on a computer
			a.state.Settings.AppMode, a.state.Settings.Apps = cur.AppMode, cur.Apps
			a.state.Settings.Mode, a.state.Settings.ProxyPort, a.state.Settings.AllowLAN = cur.Mode, cur.ProxyPort, cur.AllowLAN
		}
		if a.state.Settings.ProxyPort == 0 {
			a.state.Settings.ProxyPort = cur.ProxyPort
		}
	}
	if theme, ok := b.Extra["theme"].(string); ok {
		a.state.Theme = theme
	}
	links := map[string]bool{}
	for _, l := range b.Favorites {
		links[l] = true
	}
	have := map[string]bool{}
	for _, id := range a.state.Favorites {
		have[id] = true
	}
	for _, p := range a.state.Profiles {
		for _, n := range p.Nodes {
			if links[n.Link] && !have[n.ID] {
				a.state.Favorites = append(a.state.Favorites, n.ID)
				have[n.ID] = true
			}
		}
	}
	newSettings, _ := json.Marshal(a.state.Settings)
	connected := a.status.State == "connected"
	a.saveLocked()
	a.mu.Unlock()
	a.pushState()
	if connected && !bytes.Equal(oldSettings, newSettings) {
		go a.Reconnect()
	}
	switch {
	case importErr != nil:
		msg = tr("Настройки восстановлены. Подписки не загрузились: ") + importErr.Error()
	case msg == "":
		msg = tr("Настройки восстановлены")
	default:
		msg = tr("Восстановлено. ") + msg
	}
	return msg, nil
}

// ---------------------------------------------------------------- logs

// Logs returns the last lines of the core log.
func (a *App) Logs(limit int) []string {
	return core.ReadLog(filepath.Join(a.dataDir, "core.log"), limit)
}

// StopPing stops the server check in progress.
func (a *App) StopPing() {
	a.mu.Lock()
	stop := a.probeStop
	a.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// PingNode checks one server, through the running core when it belongs to
// the connected profile, otherwise with a temporary core.
func (a *App) PingNode(profileID, nodeID string) error {
	a.mu.Lock()
	p := a.findProfile(profileID)
	if p == nil {
		a.mu.Unlock()
		return errors.New(tr("профиль не найден"))
	}
	n := a.findNode(p, nodeID)
	if n == nil {
		a.mu.Unlock()
		return errors.New(tr("сервер не найден"))
	}
	node := *n
	instance := a.instance
	if a.state.ActiveProfile != profileID {
		instance = nil
	}
	a.mu.Unlock()
	a.pingsMu.Lock()
	a.pings[node.ID] = 0
	delete(a.reasons, node.ID)
	a.pingsMu.Unlock()
	a.broadcast("ping", map[string]any{"id": node.ID, "ms": 0})
	var res core.ProbeResult
	err := core.ProbeNodes(context.Background(), instance, []core.Node{node}, 9*time.Second, 1, func(r core.ProbeResult) { res = r })
	ms := res.Delay
	if ms <= 0 {
		ms = -1
	}
	if err != nil && res.ID == "" {
		res.Reason = core.ExplainError(err.Error())
	}
	a.pingsMu.Lock()
	a.pings[node.ID] = ms
	if ms < 0 && res.Reason != "" {
		a.reasons[node.ID] = res.Reason
	}
	a.pingsMu.Unlock()
	a.broadcast("ping", map[string]any{"id": node.ID, "ms": ms, "reason": res.Reason})
	a.savePings()
	return nil
}
