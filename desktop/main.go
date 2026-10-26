// Vela desktop client.
package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"
)

var Version = "1.4.3"

//go:embed assets/tray-off.ico
var trayOff []byte

//go:embed assets/tray-on.ico
var trayOn []byte

var (
	theApp     *App
	theServer  *Server
	quitOnce   sync.Once
	hiddenFlag = flag.Bool("hidden", false, "start minimized to tray")
	connectArg = flag.Bool("connect", false, "connect right after start")
	restartArg = flag.Bool("restarted", false, "started by the updater")
	fromArg    = flag.String("from", "", "version before the update")
	tokenArg   = flag.String("token", "", "window token of the previous instance")
)

func main() {
	flag.Parse()
	dataDir := appDataDir()
	os.MkdirAll(dataDir, 0o755)
	link := linkArg(flag.Args())

	theApp = NewApp(dataDir)
	theServer = NewServer(theApp)
	listener, err := theServer.Listen()
	if err != nil && (*connectArg || *restartArg) {
		// relaunched with admin rights or after an update: the previous
		// instance is exiting
		for i := 0; i < 150 && err != nil; i++ {
			time.Sleep(150 * time.Millisecond)
			listener, err = theServer.Listen()
		}
	}
	if err != nil {
		// already running: hand over the link or ask it to show its window
		if forwardToRunning(dataDir, link) {
			return
		}
		fatal(tr("Не удалось запустить Vela: порт 47390 занят другой программой."))
		return
	}
	go theServer.Serve(listener)
	go theApp.AutoUpdate()
	go theApp.updateLoop()
	go theApp.alertLoop()
	go registerURLSchemes()
	go cleanupAfterUpdate()
	if err := theApp.applyHotkey(); err != nil {
		go func() {
			time.Sleep(2 * time.Second) // the tray icon must exist first
			notify(tr("Горячая клавиша Vela не работает"), err.Error(), true)
		}()
	}
	if *fromArg != "" && *fromArg != Version {
		theApp.updatedFrom = *fromArg
		go func() {
			// the window reconnects by itself; without one, say it in a notification
			time.Sleep(8 * time.Second)
			if theApp.subscribers() == 0 {
				notify("Vela", fmt.Sprintf(tr("Vela обновлена до версии %s"), Version), false)
			}
		}()
	}
	if link != "" {
		theApp.OfferLink(link)
	}

	if *connectArg || theApp.state.ConnectOnLaunch {
		go theApp.Connect()
	}
	if !*hiddenFlag || link != "" {
		openWindow(theServer.URL(), dataDir)
	}
	systray.Run(onTrayReady, func() {})
}

// linkArg returns the app link Vela was started with (vela://..., happ://...).
func linkArg(args []string) string {
	for _, a := range args {
		a = strings.TrimSpace(a)
		if i := strings.Index(a, "://"); i > 0 && !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// forwardToRunning passes a link (or a request to show the window) to the
// Vela that is already running. The request carries a secret from the data
// folder, so other programs can't feed links to Vela.
func forwardToRunning(dataDir, link string) bool {
	allowForeground()
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	body, _ := json.Marshal(map[string]string{"link": link})
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/ipc", uiPort), bytes.NewReader(body))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Vela-IPC", ipcSecret(dataDir))
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusNoContent {
				return true
			}
		}
	}
	// an older Vela without /ipc
	if resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/show", uiPort)); err == nil {
		resp.Body.Close()
		return resp.StatusCode < 300
	}
	return false
}

// showWindow brings the open window to the front or opens a new one.
func showWindow() {
	if theApp.subscribers() > 0 && focusWindow() {
		return
	}
	openWindow(theServer.URL(), theApp.dataDir)
}

// quit disconnects and exits. The kill switch filters live in a dynamic
// WFP session and go away with this process; after an update or a restart
// with admin rights the new Vela turns them on again as it connects
// (-connect), so for a moment in between (well under a second, plus the
// time the new process needs to start) traffic is not blocked. Persistent
// filters would close that gap but could block the computer for good if
// Vela never came back.
func quit(app *App) {
	quitOnce.Do(func() {
		app.Disconnect()
		app.fw.Disable()
		closeWindow()
		systray.Quit()
		time.Sleep(200 * time.Millisecond)
		os.Exit(0)
	})
}

func appDataDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "Vela")
	}
	return filepath.Join(os.TempDir(), "Vela")
}
