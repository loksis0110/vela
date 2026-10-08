//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func runtimePlatform() string { return "windows" }

func isElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

func relaunchElevated() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	args, _ := windows.UTF16PtrFromString("--connect")
	dir, _ := windows.UTF16PtrFromString(filepath.Dir(exe))
	if err := windows.ShellExecute(0, verb, file, args, dir, windows.SW_NORMAL); err != nil {
		return errors.New("запуск с правами администратора отменён")
	}
	return nil
}

func findBrowser() string {
	candidates := []string{}
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LOCALAPPDATA"} {
		base := os.Getenv(env)
		if base == "" {
			continue
		}
		candidates = append(candidates,
			filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"),
		)
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// openWindow shows the UI as a standalone app window (Edge/Chrome app mode,
// no tabs or address bar). Falls back to the default browser.
func openWindow(url, dataDir string) {
	if browser := findBrowser(); browser != "" {
		cmd := exec.Command(browser,
			"--app="+url,
			"--user-data-dir="+filepath.Join(dataDir, "window"),
			"--window-size=440,800",
			"--no-first-run",
			"--no-default-browser-check",
			"--disable-features=Translate,msEdgeSidebarV2,msUndersideButton",
			"--disable-extensions",
			"--disable-sync",
		)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: false}
		if cmd.Start() == nil {
			go cmd.Wait()
			return
		}
	}
	shellOpen(url)
}

func shellOpen(target string) {
	verb, _ := windows.UTF16PtrFromString("open")
	file, _ := windows.UTF16PtrFromString(target)
	windows.ShellExecute(0, verb, file, nil, nil, windows.SW_SHOWNORMAL)
}

func openExternal(target, dataDir string) {
	switch {
	case target == "logs":
		shellOpen(dataDir)
	case target == "report":
		shellOpen(filepath.Join(dataDir, "diagnostics.txt"))
	case strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "tg://"):
		shellOpen(target)
	}
}

var (
	user32         = windows.NewLazySystemDLL("user32.dll")
	procMessageBox = user32.NewProc("MessageBoxW")
)

func fatal(msg string) {
	title, _ := windows.UTF16PtrFromString("Vela")
	text, _ := windows.UTF16PtrFromString(msg)
	procMessageBox.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
	os.Exit(1)
}
