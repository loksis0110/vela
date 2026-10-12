package main

import (
	"sort"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"

	"github.com/velaapp/vela/core"
)

// The tray menu:
//
//	Vela: connected · Germany 1     (status line)
//	Open Vela
//	Connect / Disconnect
//	—
//	Server ▸ Automatic, favorites, the fastest servers
//	Routing ▸ Smart / Everything through VPN / Only blocked sites
//	Mode ▸ TUN / System proxy
//	—
//	Quit

type trayMenu struct {
	mu    sync.Mutex
	ready bool

	status, open, toggle, server, routing, mode, quit *systray.MenuItem
	routingItems                                      map[string]*systray.MenuItem
	modeItems                                         map[string]*systray.MenuItem

	serverItems []*systray.MenuItem
	serverIDs   []string
	serverSig   string

	iconOn, iconSet bool

	kick chan struct{}
}

var tray = trayMenu{kick: make(chan struct{}, 1)}

const trayServerLimit = 15

var routingOrder = []string{"smart", "global", "blocked"}
var modeOrder = []string{"tun", "proxy"}

// refreshTray asks for the menu to be updated (debounced).
func refreshTray() {
	select {
	case tray.kick <- struct{}{}:
	default:
	}
}

func onTrayReady() {
	systray.SetIcon(trayOff)
	systray.SetTitle("Vela")
	systray.SetTooltip("Vela")
	t := &tray
	t.mu.Lock()
	t.status = systray.AddMenuItem("Vela", "")
	t.status.Disable()
	t.open = systray.AddMenuItem("", "")
	t.toggle = systray.AddMenuItem("", "")
	systray.AddSeparator()
	t.server = systray.AddMenuItem("", "")
	t.routing = systray.AddMenuItem("", "")
	t.routingItems = map[string]*systray.MenuItem{}
	for _, r := range routingOrder {
		item := t.routing.AddSubMenuItem("", "")
		t.routingItems[r] = item
		go onClick(item, func() { setRoutingFromTray(r) })
	}
	t.mode = systray.AddMenuItem("", "")
	t.modeItems = map[string]*systray.MenuItem{}
	for _, m := range modeOrder {
		item := t.mode.AddSubMenuItem("", "")
		t.modeItems[m] = item
		go onClick(item, func() { setModeFromTray(m) })
	}
	systray.AddSeparator()
	t.quit = systray.AddMenuItem("", "")
	t.ready = true
	t.mu.Unlock()

	go onClick(t.open, showWindow)
	go onClick(t.toggle, toggleFromTray)
	go onClick(t.quit, func() { quit(theApp) })
	systray.SetOnTapped(showWindow)

	t.update()
	go func() {
		for range t.kick {
			time.Sleep(250 * time.Millisecond)
			t.update()
		}
	}()
}

// onClick runs fn for each click until the item is removed.
func onClick(item *systray.MenuItem, fn func()) {
	for range item.ClickedCh {
		fn()
	}
}

type trayServer struct {
	id, name string
}

// trayModel collects what the menu shows.
func (a *App) trayModel() (st Status, settings core.Settings, selected string, servers []trayServer, current string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st, settings, selected = a.status, a.state.Settings, a.state.SelectedNode
	p := a.findProfile(a.state.ActiveProfile)
	if p == nil {
		return
	}
	nameOf := func(n *core.Node) string {
		name := stripFlag(n.Name)
		if name == "" {
			name = n.Server
		}
		return name
	}
	shown := map[string]bool{}
	fav := map[string]bool{}
	for _, id := range a.state.Favorites {
		fav[id] = true
	}
	for i := range p.Nodes {
		if fav[p.Nodes[i].ID] && len(shown) < 20 {
			servers = append(servers, trayServer{p.Nodes[i].ID, "★ " + nameOf(&p.Nodes[i])})
			shown[p.Nodes[i].ID] = true
		}
	}
	a.pingsMu.Lock()
	var fast []*core.Node
	for i := range p.Nodes {
		if a.pings[p.Nodes[i].ID] > 0 && !shown[p.Nodes[i].ID] {
			fast = append(fast, &p.Nodes[i])
		}
	}
	sort.SliceStable(fast, func(i, j int) bool { return a.pings[fast[i].ID] < a.pings[fast[j].ID] })
	a.pingsMu.Unlock()
	if len(fast) == 0 {
		// never checked: the first servers of the list
		for i := range p.Nodes {
			if !shown[p.Nodes[i].ID] {
				fast = append(fast, &p.Nodes[i])
			}
			if len(fast) == trayServerLimit {
				break
			}
		}
	}
	for _, n := range fast {
		if len(servers) >= trayServerLimit+len(shown) {
			break
		}
		servers = append(servers, trayServer{n.ID, nameOf(n)})
	}
	// the chosen server is always in the list
	if selected != "auto" && selected != "" && !containsServer(servers, selected) {
		if n := a.findNode(p, selected); n != nil {
			servers = append(servers, trayServer{n.ID, nameOf(n)})
		}
	}
	id := st.ActiveNode
	if selected != "auto" && selected != "" {
		id = selected
	}
	if n := a.findNode(p, id); n != nil {
		current = nameOf(n)
	}
	return
}

func containsServer(list []trayServer, id string) bool {
	for _, s := range list {
		if s.id == id {
			return true
		}
	}
	return false
}

// stripFlag removes a leading flag emoji: menus show it as two letters.
func stripFlag(name string) string {
	r := []rune(strings.TrimSpace(name))
	if len(r) >= 2 && r[0] >= 0x1F1E6 && r[0] <= 0x1F1FF && r[1] >= 0x1F1E6 && r[1] <= 0x1F1FF {
		r = r[2:]
	}
	return strings.TrimSpace(string(r))
}

func trimTitle(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func (t *trayMenu) update() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.ready || theApp == nil {
		return
	}
	st, settings, selected, servers, current := theApp.trayModel()

	var line string
	switch {
	case st.Blocked:
		line = tr("Интернет заблокирован: VPN не подключён")
	case st.State == "connected":
		line = tr("Vela: подключено")
		if current != "" {
			line += " · " + current
		}
	case st.State == "connecting":
		line = tr("Vela: подключение…")
	case st.State == "disconnecting":
		line = tr("Vela: отключение…")
	default:
		line = tr("Vela: не подключено")
	}
	t.status.SetTitle(trimTitle(line, 60))
	systray.SetTooltip(trimTitle(line, 120))
	if on := st.State == "connected"; on != t.iconOn || !t.iconSet {
		if on {
			systray.SetIcon(trayOn)
		} else {
			systray.SetIcon(trayOff)
		}
		t.iconOn, t.iconSet = on, true
	}
	t.open.SetTitle(tr("Открыть Vela"))
	if st.State == "disconnected" && !st.Blocked {
		t.toggle.SetTitle(tr("Подключить"))
	} else {
		t.toggle.SetTitle(tr("Отключить"))
	}
	t.quit.SetTitle(tr("Выйти"))

	t.server.SetTitle(tr("Сервер"))
	t.routing.SetTitle(tr("Маршрутизация"))
	routingNames := map[string]string{"smart": tr("Умная"), "global": tr("Всё через VPN"), "blocked": tr("Только заблокированное")}
	for r, item := range t.routingItems {
		item.SetTitle(routingNames[r])
		setChecked(item, settings.Routing == r)
	}
	t.mode.SetTitle(tr("Режим"))
	modeNames := map[string]string{"tun": tr("TUN (весь компьютер)"), "proxy": tr("Системный прокси")}
	for m, item := range t.modeItems {
		item.SetTitle(modeNames[m])
		setChecked(item, settings.Mode == m)
	}

	// the server list is rebuilt only when it changes
	entries := append([]trayServer{{"auto", tr("Автовыбор")}}, servers...)
	if len(servers) == 0 {
		entries = append(entries, trayServer{"", tr("Нет серверов")})
	}
	var sig strings.Builder
	for _, e := range entries {
		sig.WriteString(e.id + "\x00" + e.name + "\x00")
	}
	if sig.String() != t.serverSig {
		for _, item := range t.serverItems {
			item.Remove()
		}
		t.serverItems, t.serverIDs = nil, nil
		for _, e := range entries {
			item := t.server.AddSubMenuItem(trimTitle(e.name, 48), "")
			if e.id == "" {
				item.Disable()
			} else {
				id := e.id
				go onClick(item, func() { selectFromTray(id) })
			}
			t.serverItems = append(t.serverItems, item)
			t.serverIDs = append(t.serverIDs, e.id)
		}
		t.serverSig = sig.String()
	}
	for i, item := range t.serverItems {
		id := t.serverIDs[i]
		setChecked(item, id != "" && (id == selected || (id == "auto" && (selected == "" || selected == "auto"))))
	}
}

func setChecked(item *systray.MenuItem, on bool) {
	if item.Checked() != on {
		if on {
			item.Check()
		} else {
			item.Uncheck()
		}
	}
}

func toggleFromTray() {
	if err := theApp.Toggle(); err != nil {
		showWindow()
	}
}

func selectFromTray(id string) {
	theApp.mu.Lock()
	profile := theApp.state.ActiveProfile
	theApp.mu.Unlock()
	if profile != "" {
		theApp.Select(profile, id)
	}
}

func setRoutingFromTray(routing string) {
	theApp.mu.Lock()
	s, launch := theApp.state.Settings, theApp.state.ConnectOnLaunch
	theApp.mu.Unlock()
	if s.Routing != routing {
		s.Routing = routing
		theApp.SetSettings(s, launch)
	}
}

func setModeFromTray(mode string) {
	theApp.mu.Lock()
	s, launch := theApp.state.Settings, theApp.state.ConnectOnLaunch
	connected := theApp.status.State != "disconnected"
	theApp.mu.Unlock()
	if s.Mode == mode {
		return
	}
	s.Mode = mode
	theApp.SetSettings(s, launch)
	if mode == "tun" && !isElevated() && connected {
		// reconnecting needs admin rights: the window offers to restart
		showWindow()
	}
}
