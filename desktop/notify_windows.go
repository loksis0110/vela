//go:build windows

package main

import (
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows notifications are shown as a balloon of Vela's own tray icon.
// Windows 10 and 11 turn it into a regular toast notification, which works
// for an app without installer or packaging and needs no PowerShell.

var (
	shell32              = windows.NewLazySystemDLL("shell32.dll")
	procShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
)

// NOTIFYICONDATAW
type notifyIconDataW struct {
	Size            uint32
	Wnd             windows.HWND
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            windows.Handle
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	TimeoutVersion  uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GuidItem        windows.GUID
	BalloonIcon     windows.Handle
}

const (
	nimModify   = 0x1
	nifInfo     = 0x10
	niifInfo    = 0x1
	niifWarning = 0x2
	// the id fyne.io/systray gives its icon
	systrayIconID = 100
)

var (
	trayWndMu    sync.Mutex
	trayWnd      windows.HWND
	trayWndFound windows.HWND
	trayWndEnum  = windows.NewCallback(func(hwnd windows.HWND, _ uintptr) uintptr {
		var pid uint32
		windows.GetWindowThreadProcessId(hwnd, &pid)
		if pid != uint32(os.Getpid()) {
			return 1
		}
		var cls [32]uint16
		if n, _ := windows.GetClassName(hwnd, &cls[0], int32(len(cls))); n > 0 && windows.UTF16ToString(cls[:n]) == "SystrayClass" {
			trayWndFound = hwnd
			return 0
		}
		return 1
	})
)

func trayWindow() windows.HWND {
	trayWndMu.Lock()
	defer trayWndMu.Unlock()
	if trayWnd == 0 || !windows.IsWindow(trayWnd) {
		trayWndFound = 0
		windows.EnumWindows(trayWndEnum, nil)
		trayWnd = trayWndFound
	}
	return trayWnd
}

// notify shows a Windows notification. It never blocks and silently does
// nothing when it can't be shown (the in-app banner is enough then).
func notify(title, text string, warning bool) {
	go func() {
		defer func() { recover() }()
		hwnd := trayWindow()
		if hwnd == 0 || procShellNotifyIconW.Find() != nil {
			return
		}
		nid := notifyIconDataW{Wnd: hwnd, ID: systrayIconID, Flags: nifInfo, InfoFlags: niifInfo}
		if warning {
			nid.InfoFlags = niifWarning
		}
		nid.Size = uint32(unsafe.Sizeof(nid))
		copyUTF16(nid.InfoTitle[:], title)
		copyUTF16(nid.Info[:], text)
		procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
	}()
}

func copyUTF16(dst []uint16, s string) {
	src, _ := windows.UTF16FromString(s)
	if len(src) > len(dst) {
		src = src[:len(dst)]
		src[len(src)-1] = 0
	}
	copy(dst, src)
}
