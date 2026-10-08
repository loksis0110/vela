package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	box "github.com/sagernet/sing-box"

	"github.com/velaapp/vela/core"
)

// Profile is a subscription or a manually added list of servers.
type Profile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Title from the subscription server; CustomName is set by the user.
	Title      string      `json:"title,omitempty"`
	CustomName string      `json:"customName,omitempty"`
	Kind       string      `json:"kind"` // "subscription" | "manual"
	URL        string      `json:"url,omitempty"`
	Nodes      []core.Node `json:"nodes"`
	Upload     int64       `json:"upload,omitempty"`
	Download   int64       `json:"download,omitempty"`
	Total      int64       `json:"total,omitempty"`
	Expire     int64       `json:"expire,omitempty"`
	Interval   int         `json:"updateIntervalHours,omitempty"`
	Support    string      `json:"supportUrl,omitempty"`
	Updated    int64       `json:"updatedAt,omitempty"`
}

// State is persisted to disk.
type State struct {
	Profiles         []*Profile    `json:"profiles"`
	ActiveProfile    string        `json:"activeProfile"`
	SelectedNode     string        `json:"selectedNode"` // node id or "auto"
	Settings         core.Settings `json:"settings"`
	ConnectOnLaunch  bool          `json:"connectOnLaunch"`
	WasConnected     bool          `json:"wasConnected"`
	OnboardingPassed bool          `json:"onboardingPassed"`
}

// Status is pushed to the UI.
type Status struct {
	State      string `json:"state"` // disconnected | connecting | connected | disconnecting
	Error      string `json:"error,omitempty"`
	ErrorCode  string `json:"errorCode,omitempty"`
	Since      int64  `json:"since,omitempty"`
	Up         int64  `json:"up"`
	Down       int64  `json:"down"`
	UpTotal    int64  `json:"upTotal"`
	DownTotal  int64  `json:"downTotal"`
	ActiveNode string `json:"activeNode,omitempty"` // node id actually used (auto resolves)
	Elevated   bool   `json:"elevated"`
}

type App struct {
	dataDir string

	mu     sync.Mutex
	state  State
	status Status

	instance   *box.Box
	cancel     context.CancelFunc
	ctrlPort   int
	ctrlSecret string
	statsStop  chan struct{}

	subsMu sync.Mutex
	subs   map[chan []byte]struct{}

	pings   map[string]int
	reasons map[string]string
	pingsMu sync.Mutex
	probing atomic.Bool
}

func NewApp(dataDir string) *App {
	a := &App{dataDir: dataDir, subs: map[chan []byte]struct{}{}, pings: map[string]int{}, reasons: map[string]string{}}
	a.state.Settings = core.DefaultSettings()
	a.load()
	a.status.State = "disconnected"
	a.status.Elevated = isElevated()
	return a
}

func (a *App) statePath() string { return filepath.Join(a.dataDir, "state.json") }

func (a *App) load() {
	data, err := os.ReadFile(a.statePath())
	if err != nil {
		return
	}
	var st State
	if json.Unmarshal(data, &st) == nil {
		def := core.DefaultSettings()
		if st.Settings.Mode == "" {
			st.Settings = def
		}
		a.state = st
	}
}

// saveLocked must be called with a.mu held.
func (a *App) saveLocked() {
	data, _ := json.MarshalIndent(a.state, "", "  ")
	tmp := a.statePath() + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		os.Rename(tmp, a.statePath())
	}
}

// ---------------------------------------------------------------- snapshot

type Snapshot struct {
	Version         string            `json:"version"`
	Profiles        []*Profile        `json:"profiles"`
	ActiveProfile   string            `json:"activeProfile"`
	SelectedNode    string            `json:"selectedNode"`
	Settings        core.Settings     `json:"settings"`
	ConnectOnLaunch bool              `json:"connectOnLaunch"`
	Status          Status            `json:"status"`
	Pings           map[string]int    `json:"pings"`
	Reasons         map[string]string `json:"reasons"`
	Probing         bool              `json:"probing"`
	Onboarding      bool              `json:"onboarding"`
}

func (a *App) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pingsMu.Lock()
	pings := make(map[string]int, len(a.pings))
	for k, v := range a.pings {
		pings[k] = v
	}
	reasons := make(map[string]string, len(a.reasons))
	for k, v := range a.reasons {
		reasons[k] = v
	}
	a.pingsMu.Unlock()
	profiles := a.state.Profiles
	if profiles == nil {
		profiles = []*Profile{}
	}
	return Snapshot{
		Version: Version, Profiles: profiles, ActiveProfile: a.state.ActiveProfile,
		SelectedNode: a.state.SelectedNode, Settings: a.state.Settings,
		ConnectOnLaunch: a.state.ConnectOnLaunch, Status: a.status, Pings: pings,
		Reasons: reasons, Probing: a.probing.Load(),
		Onboarding: !a.state.OnboardingPassed,
	}
}

// ---------------------------------------------------------------- events

func (a *App) subscribe() chan []byte {
	ch := make(chan []byte, 16)
	a.subsMu.Lock()
	a.subs[ch] = struct{}{}
	a.subsMu.Unlock()
	return ch
}

func (a *App) unsubscribe(ch chan []byte) {
	a.subsMu.Lock()
	delete(a.subs, ch)
	a.subsMu.Unlock()
}

func (a *App) subscribers() int {
	a.subsMu.Lock()
	defer a.subsMu.Unlock()
	return len(a.subs)
}

func (a *App) broadcast(kind string, payload any) {
	data, _ := json.Marshal(map[string]any{"type": kind, "data": payload})
	a.subsMu.Lock()
	for ch := range a.subs {
		select {
		case ch <- data:
		default:
		}
	}
	a.subsMu.Unlock()
}

func (a *App) pushStatus() {
	a.mu.Lock()
	st := a.status
	a.mu.Unlock()
	a.broadcast("status", st)
	updateTray(st.State)
}

func (a *App) pushState() { a.broadcast("state", a.Snapshot()) }

// ---------------------------------------------------------------- profiles

const manualProfileID = "manual"

// Import accepts any mix of subscription URLs, share links and base64
// subscription bodies. Several subscriptions can be pasted at once.
func (a *App) Import(ctx context.Context, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("вставьте ссылку на подписку или сервер")
	}
	a.mu.Lock()
	prevActive := a.state.ActiveProfile
	a.mu.Unlock()

	subURLs, linksText := core.SplitImport(text)
	var parts, failures []string
	var lastProfile string

	// subscriptions are downloaded in parallel
	type fetched struct {
		url string
		sub *core.Subscription
		err error
	}
	results := make([]fetched, len(subURLs))
	var wg sync.WaitGroup
	for i, u := range subURLs {
		wg.Add(1)
		go func(i int, u string) {
			defer wg.Done()
			sub, err := core.FetchSubscription(ctx, u)
			results[i] = fetched{u, sub, err}
		}(i, u)
	}
	wg.Wait()
	for _, r := range results {
		if r.err != nil {
			failures = append(failures, shortURL(r.url)+": "+r.err.Error())
			continue
		}
		a.mu.Lock()
		p := a.findProfileByURL(r.url)
		isNew := p == nil
		if isNew {
			p = &Profile{ID: randomID(), Kind: "subscription", URL: r.url}
			a.state.Profiles = append(a.state.Profiles, p)
		}
		applySubscription(p, r.sub)
		a.dedupeNameLocked(p)
		lastProfile = p.ID
		a.saveLocked()
		a.mu.Unlock()
		if isNew {
			parts = append(parts, fmt.Sprintf("«%s»: серверов %d", p.Name, len(r.sub.Nodes)))
		} else {
			parts = append(parts, fmt.Sprintf("«%s» обновлена", p.Name))
		}
	}

	if strings.TrimSpace(linksText) != "" {
		res := core.ParseText(linksText)
		if len(res.Nodes) == 0 {
			msg := "не нашли поддерживаемых ссылок"
			if len(res.Errors) > 0 {
				msg = res.Errors[0]
			}
			failures = append(failures, msg)
		} else {
			a.mu.Lock()
			p := a.findProfile(manualProfileID)
			if p == nil {
				p = &Profile{ID: manualProfileID, Kind: "manual", Name: "Мои серверы"}
				a.state.Profiles = append([]*Profile{p}, a.state.Profiles...)
			}
			existing := map[string]bool{}
			for _, n := range p.Nodes {
				existing[n.Link] = true
			}
			added := 0
			for _, n := range res.Nodes {
				if existing[n.Link] {
					continue
				}
				p.Nodes = append(p.Nodes, n)
				added++
			}
			lastProfile = p.ID
			a.saveLocked()
			a.mu.Unlock()
			if added == 0 {
				parts = append(parts, "эти серверы уже есть в списке")
			} else {
				parts = append(parts, fmt.Sprintf("серверов добавлено: %d", added))
			}
			if len(res.Errors) > 0 {
				parts = append(parts, fmt.Sprintf("пропущено ссылок: %d", len(res.Errors)))
			}
		}
	}

	if lastProfile == "" {
		return "", errors.New(strings.Join(failures, "; "))
	}
	a.mu.Lock()
	// the first subscription becomes active; later ones are added alongside
	if prevActive == "" || a.findProfile(prevActive) == nil {
		a.state.ActiveProfile = lastProfile
		a.state.SelectedNode = "auto"
	}
	a.saveLocked()
	a.mu.Unlock()
	a.pushState()
	msg := strings.Join(parts, ", ")
	if len(failures) > 0 {
		msg += ". Не удалось: " + strings.Join(failures, "; ")
	}
	return capitalize(msg), nil
}

// dedupeNameLocked keeps subscription names unique so tabs can be told apart.
func (a *App) dedupeNameLocked(p *Profile) {
	if p.CustomName != "" {
		p.Name = p.CustomName
		return
	}
	base := p.Name
	if base == "" {
		base = shortURL(p.URL)
	}
	name, n := base, 2
	for {
		clash := false
		for _, other := range a.state.Profiles {
			if other != p && other.Name == name {
				clash = true
				break
			}
		}
		if !clash {
			break
		}
		name = fmt.Sprintf("%s %d", base, n)
		n++
	}
	p.Name = name
}

func capitalize(s string) string {
	r := []rune(s)
	if len(r) > 0 {
		r[0] = unicode.ToUpper(r[0])
	}
	return string(r)
}

func linksOf(nodes []core.Node) string {
	var b strings.Builder
	for _, n := range nodes {
		b.WriteString(n.Link)
		b.WriteByte('\n')
	}
	return b.String()
}

func shortURL(u string) string {
	if parsed, err := url.Parse(u); err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return u
}

// RenameProfile sets a custom name that survives subscription updates.
func (a *App) RenameProfile(id, name string) error {
	name = strings.TrimSpace(name)
	a.mu.Lock()
	p := a.findProfile(id)
	if p == nil {
		a.mu.Unlock()
		return errors.New("подписка не найдена")
	}
	p.CustomName = name
	if name == "" {
		p.Name = p.Title
	}
	a.dedupeNameLocked(p)
	a.saveLocked()
	a.mu.Unlock()
	a.pushState()
	return nil
}

// UpdateAll refreshes every subscription.
func (a *App) UpdateAll(ctx context.Context) (string, error) {
	a.mu.Lock()
	var ids []string
	for _, p := range a.state.Profiles {
		if p.Kind == "subscription" {
			ids = append(ids, p.ID)
		}
	}
	a.mu.Unlock()
	if len(ids) == 0 {
		return "", errors.New("подписок пока нет")
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	failed := 0
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if err := a.UpdateProfile(ctx, id); err != nil {
				mu.Lock()
				failed++
				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()
	if failed == len(ids) {
		return "", errors.New("не удалось обновить подписки, проверьте интернет")
	}
	if failed > 0 {
		return fmt.Sprintf("Обновлено %d из %d подписок", len(ids)-failed, len(ids)), nil
	}
	return "Все подписки обновлены", nil
}

func applySubscription(p *Profile, sub *core.Subscription) {
	p.Title = sub.Title
	p.Name = sub.Title
	p.Nodes = sub.Nodes
	p.Upload, p.Download, p.Total, p.Expire = sub.Upload, sub.Download, sub.Total, sub.Expire
	p.Interval = sub.UpdateInterval
	p.Support = firstNonEmpty(sub.SupportURL, sub.WebPageURL)
	p.Updated = sub.UpdatedAt
}

func (a *App) UpdateProfile(ctx context.Context, id string) error {
	a.mu.Lock()
	p := a.findProfile(id)
	if p == nil || p.Kind != "subscription" {
		a.mu.Unlock()
		return errors.New("подписка не найдена")
	}
	u := p.URL
	a.mu.Unlock()
	sub, err := core.FetchSubscription(ctx, u)
	if err != nil {
		return err
	}
	a.mu.Lock()
	changed := false
	if p = a.findProfile(id); p != nil {
		changed = linksOf(p.Nodes) != linksOf(sub.Nodes)
		applySubscription(p, sub)
		a.dedupeNameLocked(p)
		a.saveLocked()
	}
	connectedHere := changed && a.status.State == "connected" && a.state.ActiveProfile == id
	a.mu.Unlock()
	a.pushState()
	if connectedHere {
		go a.Reconnect()
	}
	return nil
}

// AutoUpdate refreshes subscriptions in the background.
func (a *App) AutoUpdate() {
	for {
		a.mu.Lock()
		var due []string
		now := time.Now().Unix()
		for _, p := range a.state.Profiles {
			if p.Kind != "subscription" {
				continue
			}
			interval := int64(p.Interval)
			if interval <= 0 {
				interval = 12
			}
			if now-p.Updated > interval*3600 {
				due = append(due, p.ID)
			}
		}
		a.mu.Unlock()
		for _, id := range due {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			a.UpdateProfile(ctx, id)
			cancel()
		}
		time.Sleep(20 * time.Minute)
	}
}

func (a *App) DeleteProfile(id string) error {
	a.mu.Lock()
	idx := -1
	for i, p := range a.state.Profiles {
		if p.ID == id {
			idx = i
		}
	}
	if idx < 0 {
		a.mu.Unlock()
		return errors.New("профиль не найден")
	}
	wasActive := a.state.ActiveProfile == id
	busy := wasActive && a.status.State != "disconnected"
	a.state.Profiles = append(a.state.Profiles[:idx], a.state.Profiles[idx+1:]...)
	if wasActive {
		a.state.ActiveProfile = ""
		a.state.SelectedNode = ""
		if len(a.state.Profiles) > 0 {
			a.state.ActiveProfile = a.state.Profiles[0].ID
			a.state.SelectedNode = "auto"
		}
	}
	a.saveLocked()
	a.mu.Unlock()
	if busy {
		a.Disconnect()
	}
	a.pushState()
	return nil
}

func (a *App) DeleteNode(profileID, nodeID string) error {
	a.mu.Lock()
	p := a.findProfile(profileID)
	if p == nil || p.Kind != "manual" {
		a.mu.Unlock()
		return errors.New("удалять можно только серверы, добавленные вручную")
	}
	out := p.Nodes[:0]
	for _, n := range p.Nodes {
		if n.ID != nodeID {
			out = append(out, n)
		}
	}
	p.Nodes = out
	if a.state.SelectedNode == nodeID {
		a.state.SelectedNode = "auto"
	}
	a.saveLocked()
	a.mu.Unlock()
	a.pushState()
	return nil
}

// Select chooses a server. When connected to the same profile the switch
// happens instantly through the controller, without reconnecting.
func (a *App) Select(profileID, nodeID string) error {
	a.mu.Lock()
	p := a.findProfile(profileID)
	if p == nil {
		a.mu.Unlock()
		return errors.New("профиль не найден")
	}
	if nodeID != "auto" && a.findNode(p, nodeID) == nil {
		a.mu.Unlock()
		return errors.New("сервер не найден")
	}
	sameProfile := a.state.ActiveProfile == profileID
	a.state.ActiveProfile = profileID
	a.state.SelectedNode = nodeID
	a.saveLocked()
	connected := a.status.State == "connected"
	a.mu.Unlock()
	a.pushState()
	if connected {
		if sameProfile {
			target := core.TagAuto
			if nodeID != "auto" {
				target = "node-" + nodeID
			}
			if err := a.controller("PUT", "/proxies/"+core.TagSelector, map[string]string{"name": target}, nil); err == nil {
				a.mu.Lock()
				a.status.Error, a.status.ErrorCode = "", ""
				a.mu.Unlock()
				a.pushStatus()
				go a.selfCheck(nodeID)
				return nil
			}
		}
		go a.Reconnect()
	}
	return nil
}

func (a *App) SetSettings(s core.Settings, connectOnLaunch bool) {
	a.mu.Lock()
	old := a.state.Settings
	a.state.Settings = s
	a.state.ConnectOnLaunch = connectOnLaunch
	a.saveLocked()
	connected := a.status.State == "connected"
	a.mu.Unlock()
	a.pushState()
	oldJSON, _ := json.Marshal(old)
	newJSON, _ := json.Marshal(s)
	if connected && !bytes.Equal(oldJSON, newJSON) {
		go a.Reconnect()
	}
}

func (a *App) FinishOnboarding() {
	a.mu.Lock()
	a.state.OnboardingPassed = true
	a.saveLocked()
	a.mu.Unlock()
}

func (a *App) findProfile(id string) *Profile {
	for _, p := range a.state.Profiles {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func (a *App) findProfileByURL(u string) *Profile {
	for _, p := range a.state.Profiles {
		if p.Kind == "subscription" && p.URL == u {
			return p
		}
	}
	return nil
}

func (a *App) findNode(p *Profile, id string) *core.Node {
	for i := range p.Nodes {
		if p.Nodes[i].ID == id {
			return &p.Nodes[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------- engine

var ErrNeedAdmin = errors.New("need_admin")

func (a *App) setStatus(state, errMsg, code string) {
	a.mu.Lock()
	a.status.State = state
	a.status.Error = errMsg
	a.status.ErrorCode = code
	if state == "connected" {
		a.status.Since = time.Now().Unix()
	}
	if state == "disconnected" {
		a.status.Since = 0
		a.status.Up, a.status.Down, a.status.UpTotal, a.status.DownTotal = 0, 0, 0, 0
		a.status.ActiveNode = ""
	}
	a.mu.Unlock()
	a.pushStatus()
}

func (a *App) Connect() error {
	a.mu.Lock()
	if a.status.State == "connected" || a.status.State == "connecting" {
		a.mu.Unlock()
		return nil
	}
	p := a.findProfile(a.state.ActiveProfile)
	if p == nil && len(a.state.Profiles) > 0 {
		p = a.state.Profiles[0]
		a.state.ActiveProfile = p.ID
	}
	if p == nil || len(p.Nodes) == 0 {
		a.mu.Unlock()
		return errors.New("сначала добавьте сервер или подписку")
	}
	settings := a.state.Settings
	settings.LogLevel = "info"
	if settings.Mode == "tun" && !isElevated() {
		a.mu.Unlock()
		a.setStatus("disconnected", "Для режима «Весь трафик» нужны права администратора", "need_admin")
		return ErrNeedAdmin
	}
	nodes := append([]core.Node(nil), p.Nodes...)
	selected := a.state.SelectedNode
	a.status.State = "connecting"
	a.status.Error, a.status.ErrorCode = "", ""
	a.mu.Unlock()
	a.pushStatus()

	port, err := freePort()
	if err != nil {
		a.setStatus("disconnected", err.Error(), "")
		return err
	}
	secret := randomID() + randomID()
	cfg, err := core.BuildConfig(core.BuildRequest{
		Platform: runtimePlatform(), Nodes: nodes, Selected: selected, Settings: settings,
		DataDir: a.dataDir, ControllerPort: port, ControllerSecret: secret,
	})
	if err != nil {
		a.setStatus("disconnected", err.Error(), "")
		return err
	}
	os.WriteFile(filepath.Join(a.dataDir, "last-config.json"), []byte(cfg), 0o600)

	instance, cancel, err := startBox(cfg, filepath.Join(a.dataDir, "core.log"))
	if err != nil {
		msg := humanizeError(err)
		a.setStatus("disconnected", msg, "")
		return errors.New(msg)
	}
	a.mu.Lock()
	a.instance, a.cancel = instance, cancel
	a.ctrlPort, a.ctrlSecret = port, secret
	a.state.WasConnected = true
	a.saveLocked()
	a.statsStop = make(chan struct{})
	stop := a.statsStop
	a.mu.Unlock()
	a.setStatus("connected", "", "")
	go a.statsLoop(stop)
	go a.selfCheck(selected)
	return nil
}

func (a *App) Disconnect() {
	a.mu.Lock()
	instance, cancel := a.instance, a.cancel
	a.instance, a.cancel = nil, nil
	if a.statsStop != nil {
		close(a.statsStop)
		a.statsStop = nil
	}
	a.state.WasConnected = false
	a.saveLocked()
	if instance == nil {
		a.mu.Unlock()
		a.setStatus("disconnected", "", "")
		return
	}
	a.status.State = "disconnecting"
	a.mu.Unlock()
	a.pushStatus()
	done := make(chan struct{})
	go func() {
		instance.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	cancel()
	a.setStatus("disconnected", "", "")
}

func (a *App) Reconnect() {
	a.Disconnect()
	a.Connect()
}

func startBox(cfg string, logPath string) (*box.Box, context.CancelFunc, error) {
	// fresh log on every connect, detailed enough to diagnose server problems
	os.Remove(logPath + ".old")
	os.Rename(logPath, logPath+".old")
	return core.StartBox(cfg, logPath)
}

func humanizeError(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "Access is denied") || strings.Contains(s, "access denied"):
		return "Нет прав администратора для создания сетевого адаптера"
	case strings.Contains(s, "address already in use") || strings.Contains(s, "Only one usage of each socket"):
		return "Порт локального прокси занят другой программой. Поменяйте порт в настройках"
	}
	return "Не удалось подключиться: " + s
}

// ---------------------------------------------------------------- controller

func (a *App) controller(method, path string, body any, out any) error {
	a.mu.Lock()
	port, secret := a.ctrlPort, a.ctrlSecret
	running := a.instance != nil
	a.mu.Unlock()
	if !running {
		return errors.New("not connected")
	}
	var reader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 12 * time.Second, Transport: &http.Transport{Proxy: nil}}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("controller: %d", resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (a *App) statsLoop(stop chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var lastUp, lastDown int64 = -1, -1
	tick := 0
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		var conns struct {
			UploadTotal   int64 `json:"uploadTotal"`
			DownloadTotal int64 `json:"downloadTotal"`
		}
		if err := a.controller("GET", "/connections", nil, &conns); err != nil {
			continue
		}
		active := ""
		if tick%3 == 0 {
			active = a.resolveActive()
		}
		tick++
		a.mu.Lock()
		if lastUp >= 0 {
			a.status.Up = max(0, conns.UploadTotal-lastUp)
			a.status.Down = max(0, conns.DownloadTotal-lastDown)
		}
		a.status.UpTotal, a.status.DownTotal = conns.UploadTotal, conns.DownloadTotal
		if active != "" {
			a.status.ActiveNode = active
		}
		a.mu.Unlock()
		lastUp, lastDown = conns.UploadTotal, conns.DownloadTotal
		if a.subscribers() > 0 {
			a.pushStatus()
		}
	}
}

// selfCheck makes a real request through the chosen server right after
// connecting and warns the user if nothing gets through.
func (a *App) selfCheck(selected string) {
	time.Sleep(500 * time.Millisecond)
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		a.mu.Lock()
		instance := a.instance
		still := a.status.State == "connected"
		a.mu.Unlock()
		if !still || instance == nil {
			return
		}
		if _, err := core.ProbeTag(context.Background(), instance, core.TagSelector, 8*time.Second); err == nil {
			return
		} else {
			lastErr = err
		}
	}
	msg := "Выбранный сервер не отвечает: " + core.ExplainError(lastErr.Error()) + ". Выберите другой сервер или «Автовыбор»"
	if selected == "auto" || selected == "" {
		msg = "Ни один сервер пока не ответил. Проверьте подписку или интернет-соединение"
	}
	a.mu.Lock()
	if a.status.State == "connected" {
		a.status.Error = msg
		a.status.ErrorCode = "server_unreachable"
	}
	a.mu.Unlock()
	a.pushStatus()
}

// resolveActive returns the node id currently carrying traffic.
func (a *App) resolveActive() string {
	var sel struct {
		Now string `json:"now"`
	}
	if a.controller("GET", "/proxies/"+core.TagSelector, nil, &sel) != nil {
		return ""
	}
	if sel.Now == core.TagAuto {
		var auto struct {
			Now string `json:"now"`
		}
		if a.controller("GET", "/proxies/"+core.TagAuto, nil, &auto) != nil {
			return ""
		}
		sel.Now = auto.Now
	}
	return strings.TrimPrefix(sel.Now, "node-")
}

// Ping makes a real request through every server of a profile, the same way
// traffic will go, and records the delay or the reason it failed. Works both
// connected (through the running core) and disconnected (temporary core).
func (a *App) Ping(profileID string) {
	if !a.probing.CompareAndSwap(false, true) {
		return
	}
	defer a.probing.Store(false)
	a.mu.Lock()
	p := a.findProfile(profileID)
	if p == nil {
		a.mu.Unlock()
		return
	}
	nodes := append([]core.Node(nil), p.Nodes...)
	var instance = a.instance
	if a.state.ActiveProfile != profileID {
		instance = nil
	}
	a.mu.Unlock()

	a.pingsMu.Lock()
	for _, n := range nodes {
		a.pings[n.ID] = 0
		delete(a.reasons, n.ID)
	}
	a.pingsMu.Unlock()
	total, done := len(nodes), 0
	a.broadcast("probe", map[string]int{"done": 0, "total": total})

	results := map[string]core.ProbeResult{}
	var mu sync.Mutex
	err := core.ProbeNodes(context.Background(), instance, nodes, 9*time.Second, 12, func(r core.ProbeResult) {
		ms := r.Delay
		if ms <= 0 {
			ms = -1
		}
		mu.Lock()
		results[r.ID] = r
		done++
		d := done
		mu.Unlock()
		a.pingsMu.Lock()
		a.pings[r.ID] = ms
		if r.Reason != "" {
			a.reasons[r.ID] = r.Reason
		}
		a.pingsMu.Unlock()
		a.broadcast("ping", map[string]any{"id": r.ID, "ms": ms, "reason": r.Reason})
		if d%10 == 0 || d == total {
			a.broadcast("probe", map[string]int{"done": d, "total": total})
		}
	})
	if err != nil {
		a.broadcast("probe", map[string]any{"done": total, "total": total, "error": humanizeError(err)})
		return
	}
	core.WriteReport(filepath.Join(a.dataDir, "diagnostics.txt"), nodes, results)
}

// FastestFirst returns node ids sorted by measured latency (unused nodes last).
func (a *App) FastestFirst(nodes []core.Node) []string {
	a.pingsMu.Lock()
	defer a.pingsMu.Unlock()
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	sort.SliceStable(ids, func(i, j int) bool {
		pi, pj := a.pings[ids[i]], a.pings[ids[j]]
		if pi <= 0 {
			return false
		}
		if pj <= 0 {
			return true
		}
		return pi < pj
	})
	return ids
}

// ---------------------------------------------------------------- utils

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func randomID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
