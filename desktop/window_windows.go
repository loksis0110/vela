//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/velaapp/vela/desktop/webview2/edge"
	"github.com/velaapp/vela/desktop/webview2/webviewloader"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// The Vela window is a native window of Vela.exe with the WebView2 control
// (the Edge engine that ships with Windows 10/11) inside. It has Vela's own
// icon and taskbar button. Without the WebView2 runtime Vela falls back to
// an Edge/Chrome window in app mode (openBrowserWindow).

const (
	wmDestroy       = 0x0002
	wmMove          = 0x0003
	wmSize          = 0x0005
	sizeMinimized   = 1
	wmActivate      = 0x0006
	wmClose         = 0x0010
	wmEraseBkgnd    = 0x0014
	wmSettingChange = 0x001A
	wmGetMinMaxInfo = 0x0024
	wmDpiChanged    = 0x02E0
	wmVelaShow      = 0x8000 + 1 // WM_APP+1: bring the window to the front
	wmVelaTheme     = 0x8000 + 2 // WM_APP+2: the app theme changed

	wsOverlappedWindow = 0x00CF0000
	cwUseDefault       = 0x80000000
	swShow             = 5
	swRestore          = 9
	swpNoZOrder        = 0x0004
	swpNoActivate      = 0x0010
	imageIcon          = 1
	lrDefaultSize      = 0x0040
	lrShared           = 0x8000
	monitorNearest     = 2
	iconResourceID     = 2 // RT_GROUP_ICON id in rsrc_windows_amd64.syso
)

var (
	dwmapi = windows.NewLazySystemDLL("dwmapi.dll")
	gdi32  = windows.NewLazySystemDLL("gdi32.dll")

	winRegisterClassExW    = user32.NewProc("RegisterClassExW")
	winCreateWindowExW     = user32.NewProc("CreateWindowExW")
	winDefWindowProcW      = user32.NewProc("DefWindowProcW")
	winDestroyWindow       = user32.NewProc("DestroyWindow")
	winTranslateMessage    = user32.NewProc("TranslateMessage")
	winDispatchMessageW    = user32.NewProc("DispatchMessageW")
	winPostMessageW        = user32.NewProc("PostMessageW")
	winPostQuitMessage     = user32.NewProc("PostQuitMessage")
	winLoadImageW          = user32.NewProc("LoadImageW")
	winLoadCursorW         = user32.NewProc("LoadCursorW")
	winGetDpiForWindow     = user32.NewProc("GetDpiForWindow")
	winSetWindowPos        = user32.NewProc("SetWindowPos")
	winGetWindowPlacement  = user32.NewProc("GetWindowPlacement")
	winSetWindowPlacement  = user32.NewProc("SetWindowPlacement")
	winMonitorFromWindow   = user32.NewProc("MonitorFromWindow")
	winGetMonitorInfoW     = user32.NewProc("GetMonitorInfoW")
	winGetClientRect       = user32.NewProc("GetClientRect")
	winFillRect            = user32.NewProc("FillRect")
	winDwmSetWindowAttr    = dwmapi.NewProc("DwmSetWindowAttribute")
	winCreateSolidBrush    = gdi32.NewProc("CreateSolidBrush")
	winDeleteObject        = gdi32.NewProc("DeleteObject")
	winOle32               = windows.NewLazySystemDLL("ole32.dll")
	winCoInitializeEx      = winOle32.NewProc("CoInitializeEx")
	winCoUninitialize      = winOle32.NewProc("CoUninitialize")
	windowWndProc          = windows.NewCallback(windowProc)
	windowClassOnce        sync.Once
	windowClassErr         error
	windowClassName, _     = windows.UTF16PtrFromString("VelaWindow")
	webviewCheckOnce       sync.Once
	webviewAvailable       bool
	embedded               embeddedWindow
	errWebViewNotInstalled = errors.New("WebView2 runtime is not installed")
)

type embeddedWindow struct {
	mu       sync.Mutex
	hwnd     uintptr
	browser  *edge.Chromium
	brush    uintptr
	dark     bool
	dataDir  string
	threadUp chan struct{} // closed when the window thread ends
	// minimized: the page is told it is hidden, so it stops drawing (the
	// traffic graph, timers) until the window is restored
	minimized bool
}

type winRect struct{ Left, Top, Right, Bottom int32 }

type winPoint struct{ X, Y int32 }

type winClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

type winPlacement struct {
	Length     uint32
	Flags      uint32
	ShowCmd    uint32
	MinPos     winPoint
	MaxPos     winPoint
	NormalRect winRect
}

type winMonitorInfo struct {
	Size    uint32
	Monitor winRect
	Work    winRect
	Flags   uint32
}

type winMinMaxInfo struct {
	Reserved     winPoint
	MaxSize      winPoint
	MaxPosition  winPoint
	MinTrackSize winPoint
	MaxTrackSize winPoint
}

// openWindow shows the Vela window: the existing one, a new native window
// with WebView2, or (without WebView2) an Edge/Chrome app-mode window.
func openWindow(url, dataDir string) {
	if focusEmbeddedWindow() {
		return
	}
	// If Vela died while creating the built-in window last time, use the
	// browser window instead of crashing again.
	marker := filepath.Join(dataDir, "window-starting")
	crashed := false
	if _, err := os.Stat(marker); err == nil {
		crashed = true
		os.Remove(marker)
		logWindowError(dataDir, errors.New("the previous start of the built-in window did not finish, using the browser window"))
	}
	if !crashed && os.Getenv("VELA_WINDOW") != "browser" && webViewInstalled(dataDir) {
		_ = os.WriteFile(marker, nil, 0o644)
		ready := make(chan error, 1)
		go runEmbeddedWindow(url, dataDir, ready)
		select {
		case err := <-ready:
			if err == nil {
				go func() {
					time.Sleep(10 * time.Second)
					os.Remove(marker)
				}()
				return
			}
			os.Remove(marker)
			logWindowError(dataDir, err)
		case <-time.After(20 * time.Second):
			os.Remove(marker)
			return // still starting; it will appear by itself
		}
	}
	openBrowserWindow(url, dataDir)
}

// webViewInstalled asks the WebView2 loader, and the registry when the
// loader can't tell, whether the WebView2 runtime is installed.
func webViewInstalled(dataDir string) bool {
	webviewCheckOnce.Do(func() {
		v, err := webviewloader.GetInstalledVersion()
		if err == nil && v != "" {
			webviewAvailable = true
			return
		}
		if rv := webViewRegistryVersion(); rv != "" {
			webviewAvailable = true // try it: a failure falls back and is logged
			if err != nil {
				logWindowError(dataDir, fmt.Errorf("WebView2 loader: %v (runtime %s found in the registry)", err, rv))
			}
			return
		}
		if err != nil {
			logWindowError(dataDir, fmt.Errorf("WebView2 loader: %v", err))
		} else {
			logWindowError(dataDir, errWebViewNotInstalled)
		}
	})
	return webviewAvailable
}

// webViewRegistryVersion reads the installed WebView2 runtime version.
func webViewRegistryVersion() string {
	const client = `Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`
	for _, loc := range []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\` + client},
		{registry.LOCAL_MACHINE, `SOFTWARE\` + client},
		{registry.CURRENT_USER, `Software\` + client},
	} {
		k, err := registry.OpenKey(loc.root, loc.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		v, _, err := k.GetStringValue("pv")
		k.Close()
		if err == nil && v != "" && v != "0.0.0.0" {
			return v
		}
	}
	return ""
}

// embeddedWindowOpen reports whether the native window is open (it closes
// together with this process, unlike a browser window).
func embeddedWindowOpen() bool {
	embedded.mu.Lock()
	defer embedded.mu.Unlock()
	return embedded.hwnd != 0
}

func focusEmbeddedWindow() bool {
	embedded.mu.Lock()
	hwnd := embedded.hwnd
	embedded.mu.Unlock()
	if hwnd == 0 {
		return false
	}
	winPostMessageW.Call(hwnd, wmVelaShow, 0, 0)
	return true
}

// closeWindow closes the native window before Vela exits, so its size is
// saved and no WebView2 processes stay behind.
func closeWindow() {
	embedded.mu.Lock()
	hwnd, done := embedded.hwnd, embedded.threadUp
	embedded.mu.Unlock()
	if hwnd == 0 {
		return
	}
	winPostMessageW.Call(hwnd, wmClose, 0, 0)
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}

// windowThemeChanged updates the title bar and background colours.
func windowThemeChanged() {
	embedded.mu.Lock()
	hwnd := embedded.hwnd
	embedded.mu.Unlock()
	if hwnd != 0 {
		winPostMessageW.Call(hwnd, wmVelaTheme, 0, 0)
	}
}

func logWindowError(dataDir string, err error) {
	f, ferr := os.OpenFile(filepath.Join(dataDir, "window.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if ferr != nil {
		return
	}
	defer f.Close()
	f.WriteString(time.Now().Format("2006-01-02 15:04:05 ") + err.Error() + "\n")
}

func registerWindowClass() error {
	windowClassOnce.Do(func() {
		var instance windows.Handle
		_ = windows.GetModuleHandleEx(0, nil, &instance)
		icon, _, _ := winLoadImageW.Call(uintptr(instance), iconResourceID, imageIcon, 0, 0, lrDefaultSize|lrShared)
		cursor, _, _ := winLoadCursorW.Call(0, 32512) // IDC_ARROW
		wc := winClassEx{
			WndProc:   windowWndProc,
			Instance:  instance,
			Icon:      windows.Handle(icon),
			IconSm:    windows.Handle(icon),
			Cursor:    windows.Handle(cursor),
			ClassName: windowClassName,
		}
		wc.Size = uint32(unsafe.Sizeof(wc))
		if r, _, err := winRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
			windowClassErr = err
		}
	})
	return windowClassErr
}

// runEmbeddedWindow creates the window and runs its message loop on a
// dedicated, locked OS thread until the window is closed.
func runEmbeddedWindow(url, dataDir string, ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	winCoInitializeEx.Call(0, 2) // COINIT_APARTMENTTHREADED
	defer winCoUninitialize.Call()

	if err := registerWindowClass(); err != nil {
		ready <- err
		return
	}
	title, _ := windows.UTF16PtrFromString("Vela")
	var instance windows.Handle
	_ = windows.GetModuleHandleEx(0, nil, &instance)
	hwnd, _, err := winCreateWindowExW.Call(0, uintptr(unsafe.Pointer(windowClassName)), uintptr(unsafe.Pointer(title)),
		wsOverlappedWindow, cwUseDefault, cwUseDefault, cwUseDefault, cwUseDefault, 0, 0, uintptr(instance), 0)
	if hwnd == 0 {
		ready <- err
		return
	}
	dark := windowDark()
	embedded.mu.Lock()
	embedded.hwnd = hwnd
	embedded.dataDir = dataDir
	embedded.dark = dark
	embedded.brush = newBackgroundBrush(dark)
	embedded.threadUp = make(chan struct{})
	done := embedded.threadUp
	embedded.mu.Unlock()
	defer close(done)

	applyTitleBar(hwnd, dark)
	placeWindow(hwnd, dataDir)

	browser := edge.NewChromium()
	folder := "webview"
	if isElevated() {
		// an elevated WebView2 can't share the profile of a normal one
		folder = "webview-admin"
	}
	browser.DataPath = filepath.Join(dataDir, folder)
	if !browser.Embed(hwnd) {
		err := browser.Err
		if err == nil {
			err = errors.New("WebView2 could not be created")
		}
		embedded.mu.Lock()
		embedded.hwnd = 0
		if embedded.brush != 0 {
			winDeleteObject.Call(embedded.brush)
			embedded.brush = 0
		}
		embedded.mu.Unlock()
		winDestroyWindow.Call(hwnd)
		drainMessages()
		ready <- err
		return
	}
	if settings, err := browser.GetSettings(); err == nil {
		_ = settings.PutAreDevToolsEnabled(false)
		_ = settings.PutIsStatusBarEnabled(false)
		// Ctrl+F, Ctrl+P, F5 and the like do nothing useful in Vela
		_ = settings.PutAreBrowserAcceleratorKeysEnabled(false)
	}
	setBrowserBackground(browser, dark)
	embedded.mu.Lock()
	embedded.browser = browser
	embedded.mu.Unlock()
	browser.Resize()
	browser.Navigate(url)
	browser.Focus()
	ready <- nil

	var msg winMsg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		winTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		winDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
	embedded.mu.Lock()
	embedded.hwnd = 0
	embedded.browser = nil
	if embedded.brush != 0 {
		winDeleteObject.Call(embedded.brush)
		embedded.brush = 0
	}
	embedded.mu.Unlock()
}

// drainMessages lets a destroyed window finish its messages.
func drainMessages() {
	var msg winMsg
	for {
		r, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 1 /* PM_REMOVE */)
		if r == 0 {
			return
		}
		winDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func windowProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	embedded.mu.Lock()
	browser := embedded.browser
	ours := embedded.hwnd == hwnd
	embedded.mu.Unlock()
	switch msg {
	case wmSize:
		if browser != nil {
			embedded.mu.Lock()
			was := embedded.minimized
			now := wparam == sizeMinimized
			embedded.minimized = now
			embedded.mu.Unlock()
			switch {
			case now && !was:
				_ = browser.Hide()
			case !now && was:
				_ = browser.Show()
			}
			if !now {
				browser.Resize()
			}
		}
		return 0
	case wmMove:
		if browser != nil {
			_ = browser.NotifyParentWindowPositionChanged()
		}
		return 0
	case wmActivate:
		if browser != nil && wparam&0xFFFF != 0 {
			browser.Focus()
		}
	case wmEraseBkgnd:
		embedded.mu.Lock()
		brush := embedded.brush
		embedded.mu.Unlock()
		if brush != 0 {
			var rc winRect
			winGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
			winFillRect.Call(wparam, uintptr(unsafe.Pointer(&rc)), brush)
			return 1
		}
	case wmGetMinMaxInfo:
		info := (*winMinMaxInfo)(lparamPointer(lparam))
		scale := windowScale(hwnd)
		info.MinTrackSize = winPoint{X: int32(380 * scale), Y: int32(520 * scale)}
		return 0
	case wmDpiChanged:
		rc := (*winRect)(lparamPointer(lparam))
		winSetWindowPos.Call(hwnd, 0, uintptr(rc.Left), uintptr(rc.Top),
			uintptr(rc.Right-rc.Left), uintptr(rc.Bottom-rc.Top), swpNoZOrder|swpNoActivate)
		return 0
	case wmSettingChange, wmVelaTheme:
		if ours {
			dark := windowDark()
			embedded.mu.Lock()
			changed := dark != embedded.dark
			if changed {
				embedded.dark = dark
				if embedded.brush != 0 {
					winDeleteObject.Call(embedded.brush)
				}
				embedded.brush = newBackgroundBrush(dark)
			}
			embedded.mu.Unlock()
			if changed {
				applyTitleBar(hwnd, dark)
				if browser != nil {
					setBrowserBackground(browser, dark)
				}
			}
		}
	case wmVelaShow:
		if r, _, _ := procIsIconic.Call(hwnd); r != 0 {
			procShowWindow.Call(hwnd, swRestore)
		} else {
			procShowWindow.Call(hwnd, swShow)
		}
		procSetForegroundWindow.Call(hwnd)
		if browser != nil {
			browser.Focus()
		}
		return 0
	case wmClose:
		if ours {
			embedded.mu.Lock()
			dataDir := embedded.dataDir
			embedded.browser = nil
			embedded.mu.Unlock()
			saveWindowPlacement(hwnd, dataDir)
			if browser != nil {
				browser.Close()
			}
		}
		winDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		if ours {
			winPostQuitMessage.Call(0)
		}
		return 0
	}
	r, _, _ := winDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
}

// windowDark tells whether the window should be dark: the app theme, or
// the Windows app theme when Vela follows the system.
func windowDark() bool {
	theme := ""
	if theApp != nil {
		theApp.mu.Lock()
		theme = theApp.state.Theme
		theApp.mu.Unlock()
	}
	switch theme {
	case "dark":
		return true
	case "light":
		return false
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return true // the UI is dark by default
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("AppsUseLightTheme")
	return err != nil || v == 0
}

// background colours of the UI (--ink in app.css)
func uiBackground(dark bool) (r, g, b uint8) {
	if dark {
		return 0x0c, 0x14, 0x24
	}
	return 0xee, 0xf2, 0xf8
}

func newBackgroundBrush(dark bool) uintptr {
	r, g, b := uiBackground(dark)
	brush, _, _ := winCreateSolidBrush.Call(uintptr(r) | uintptr(g)<<8 | uintptr(b)<<16)
	return brush
}

func setBrowserBackground(browser *edge.Chromium, dark bool) {
	controller := browser.GetController()
	if controller == nil {
		return
	}
	if c2 := controller.GetICoreWebView2Controller2(); c2 != nil {
		r, g, b := uiBackground(dark)
		_ = c2.PutDefaultBackgroundColor(edge.COREWEBVIEW2_COLOR{A: 255, R: r, G: g, B: b})
	}
}

// applyTitleBar makes the title bar dark or light (Windows 10 2004+ / 11).
func applyTitleBar(hwnd uintptr, dark bool) {
	var v int32
	if dark {
		v = 1
	}
	if r, _, _ := winDwmSetWindowAttr.Call(hwnd, 20, uintptr(unsafe.Pointer(&v)), 4); r != 0 {
		winDwmSetWindowAttr.Call(hwnd, 19, uintptr(unsafe.Pointer(&v)), 4) // older Windows 10
	}
}

func windowScale(hwnd uintptr) float64 {
	if winGetDpiForWindow.Find() != nil {
		return 1
	}
	dpi, _, _ := winGetDpiForWindow.Call(hwnd)
	if dpi == 0 {
		return 1
	}
	return float64(dpi) / 96
}

type savedPlacement struct {
	Left, Top, Right, Bottom int32
	Maximized                bool
}

func placementPath(dataDir string) string { return filepath.Join(dataDir, "window.json") }

// placeWindow restores the last size and position, or centres a phone-like
// window on the screen the first time.
func placeWindow(hwnd uintptr, dataDir string) {
	if data, err := os.ReadFile(placementPath(dataDir)); err == nil {
		var p savedPlacement
		if json.Unmarshal(data, &p) == nil && p.Right-p.Left >= 200 && p.Bottom-p.Top >= 200 {
			wp := winPlacement{ShowCmd: swShow,
				NormalRect: winRect{Left: p.Left, Top: p.Top, Right: p.Right, Bottom: p.Bottom}}
			if p.Maximized {
				wp.ShowCmd = 3 // SW_SHOWMAXIMIZED
			}
			wp.Length = uint32(unsafe.Sizeof(wp))
			// Windows moves a window that would be off screen back into view
			if r, _, _ := winSetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp))); r != 0 {
				return
			}
		}
	}
	scale := windowScale(hwnd)
	w, h := int32(460*scale), int32(820*scale)
	mon, _, _ := winMonitorFromWindow.Call(hwnd, monitorNearest)
	mi := winMonitorInfo{}
	mi.Size = uint32(unsafe.Sizeof(mi))
	if r, _, _ := winGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi))); r != 0 {
		work := mi.Work
		if maxH := work.Bottom - work.Top - int32(40*scale); h > maxH {
			h = maxH
		}
		x := work.Left + (work.Right-work.Left-w)/2
		y := work.Top + (work.Bottom-work.Top-h)/2
		winSetWindowPos.Call(hwnd, 0, uintptr(x), uintptr(y), uintptr(w), uintptr(h), swpNoZOrder|swpNoActivate)
	}
	procShowWindow.Call(hwnd, swShow)
}

func saveWindowPlacement(hwnd uintptr, dataDir string) {
	wp := winPlacement{}
	wp.Length = uint32(unsafe.Sizeof(wp))
	if r, _, _ := winGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp))); r == 0 {
		return
	}
	p := savedPlacement{Left: wp.NormalRect.Left, Top: wp.NormalRect.Top, Right: wp.NormalRect.Right,
		Bottom: wp.NormalRect.Bottom, Maximized: wp.ShowCmd == 3}
	if data, err := json.Marshal(p); err == nil {
		_ = os.WriteFile(placementPath(dataDir), data, 0o644)
	}
}

// lparamPointer reads a pointer Windows passes in a message parameter.
func lparamPointer(l uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&l))
}

// replaceBrowserWindow swaps an Edge/Chrome Vela window left from an older
// version (it reconnects to the new Vela after an update) for the built-in
// window.
func replaceBrowserWindow(url, dataDir string) {
	if os.Getenv("VELA_WINDOW") == "browser" || embeddedWindowOpen() || !webViewInstalled(dataDir) {
		return
	}
	focusMu.Lock()
	focusFound = 0
	windows.EnumWindows(focusEnum, nil)
	found := focusFound
	focusMu.Unlock()
	if found != 0 {
		openWindow(url, dataDir)
	}
}
