// Vela desktop client.
package main

import (
	_ "embed"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"fyne.io/systray"
)

var Version = "1.3.0"

//go:embed assets/tray-off.ico
var trayOff []byte

//go:embed assets/tray-on.ico
var trayOn []byte

var (
	theApp     *App
	theServer  *Server
	trayReady  bool
	trayMu     sync.Mutex
	mToggle    *systray.MenuItem
	quitOnce   sync.Once
	hiddenFlag = flag.Bool("hidden", false, "start minimized to tray")
	connectArg = flag.Bool("connect", false, "connect right after start")
)

func main() {
	flag.Parse()
	dataDir := appDataDir()
	os.MkdirAll(dataDir, 0o755)

	theApp = NewApp(dataDir)
	theServer = NewServer(theApp)
	listener, err := theServer.Listen()
	if err != nil && *connectArg {
		// relaunched with admin rights: the previous instance is exiting
		for i := 0; i < 40 && err != nil; i++ {
			time.Sleep(150 * time.Millisecond)
			listener, err = theServer.Listen()
		}
	}
	if err != nil {
		// already running: ask it to show its window
		client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}
		if resp, err := client.Get("http://127.0.0.1:47390/show"); err == nil {
			resp.Body.Close()
			return
		}
		fatal("Не удалось запустить Vela: порт 47390 занят другой программой.")
		return
	}
	go theServer.Serve(listener)
	go theApp.AutoUpdate()

	if *connectArg || theApp.state.ConnectOnLaunch {
		go theApp.Connect()
	}
	if !*hiddenFlag {
		openWindow(theServer.URL(), dataDir)
	}
	systray.Run(onTrayReady, func() {})
}

func onTrayReady() {
	systray.SetIcon(trayOff)
	systray.SetTitle("Vela")
	systray.SetTooltip("Vela: не подключено")
	mOpen := systray.AddMenuItem("Открыть Vela", "")
	mToggle = systray.AddMenuItem("Подключить", "")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Выйти", "Отключиться и закрыть Vela")
	systray.SetOnTapped(func() { openWindow(theServer.URL(), theApp.dataDir) })
	trayMu.Lock()
	trayReady = true
	trayMu.Unlock()
	updateTray(theApp.Snapshot().Status.State)
	go func() {
		for {
			select {
			case <-mOpen.ClickedCh:
				openWindow(theServer.URL(), theApp.dataDir)
			case <-mToggle.ClickedCh:
				if theApp.Snapshot().Status.State == "disconnected" {
					if err := theApp.Connect(); err == ErrNeedAdmin {
						openWindow(theServer.URL(), theApp.dataDir)
					}
				} else {
					theApp.Disconnect()
				}
			case <-mQuit.ClickedCh:
				quit(theApp)
			}
		}
	}()
}

func updateTray(state string) {
	trayMu.Lock()
	defer trayMu.Unlock()
	if !trayReady {
		return
	}
	switch state {
	case "connected":
		systray.SetIcon(trayOn)
		systray.SetTooltip("Vela: подключено")
		mToggle.SetTitle("Отключить")
	case "connecting":
		systray.SetTooltip("Vela: подключение…")
		mToggle.SetTitle("Отключить")
	default:
		systray.SetIcon(trayOff)
		systray.SetTooltip("Vela: не подключено")
		mToggle.SetTitle("Подключить")
	}
}

func quit(app *App) {
	quitOnce.Do(func() {
		app.Disconnect()
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
