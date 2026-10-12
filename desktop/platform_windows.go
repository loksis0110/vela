//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func runtimePlatform() string { return "windows" }

func isElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// relaunchElevated starts Vela with admin rights (Windows asks the user).
func relaunchElevated(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = syscall.EscapeArg(a)
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString(strings.Join(quoted, " "))
	dir, _ := windows.UTF16PtrFromString(filepath.Dir(exe))
	if err := windows.ShellExecute(0, verb, file, params, dir, windows.SW_NORMAL); err != nil {
		return errors.New(tr("запуск с правами администратора отменён"))
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
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procMessageBox               = user32.NewProc("MessageBoxW")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procIsIconic                 = user32.NewProc("IsIconic")
	procShowWindow               = user32.NewProc("ShowWindow")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procAllowSetForegroundWindow = user32.NewProc("AllowSetForegroundWindow")

	kernel32                     = windows.NewLazySystemDLL("kernel32.dll")
	procGetUserDefaultUILanguage = kernel32.NewProc("GetUserDefaultUILanguage")

	iphlpapi                        = windows.NewLazySystemDLL("iphlpapi.dll")
	procConvertInterfaceAliasToLuid = iphlpapi.NewProc("ConvertInterfaceAliasToLuid")
)

func fatal(msg string) {
	title, _ := windows.UTF16PtrFromString("Vela")
	text, _ := windows.UTF16PtrFromString(msg)
	procMessageBox.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
	os.Exit(1)
}

// systemLanguage follows the Windows display language.
func systemLanguage() string {
	if procGetUserDefaultUILanguage.Find() == nil {
		id, _, _ := procGetUserDefaultUILanguage.Call()
		if id&0x3ff == 0x19 { // LANG_RUSSIAN
			return "ru"
		}
		return "en"
	}
	if langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME); err == nil && len(langs) > 0 {
		if strings.HasPrefix(strings.ToLower(langs[0]), "ru") {
			return "ru"
		}
	}
	return "en"
}

var (
	focusMu    sync.Mutex
	focusFound windows.HWND
	// created once: Windows callbacks are a limited resource
	focusEnum = windows.NewCallback(func(hwnd windows.HWND, _ uintptr) uintptr {
		if !windows.IsWindowVisible(hwnd) {
			return 1
		}
		var cls [64]uint16
		if n, _ := windows.GetClassName(hwnd, &cls[0], int32(len(cls))); n == 0 || windows.UTF16ToString(cls[:n]) != "Chrome_WidgetWin_1" {
			return 1
		}
		var title [64]uint16
		n, _, _ := procGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&title[0])), uintptr(len(title)))
		if n == 0 || windows.UTF16ToString(title[:n]) != "Vela" {
			return 1
		}
		focusFound = hwnd
		return 0
	})
)

// focusWindow brings an open Vela window (Edge/Chrome in app mode titled
// "Vela") to the front. Reports false when there is none.
func focusWindow() bool {
	focusMu.Lock()
	focusFound = 0
	windows.EnumWindows(focusEnum, nil)
	found := focusFound
	focusMu.Unlock()
	if found == 0 {
		return false
	}
	if r, _, _ := procIsIconic.Call(uintptr(found)); r != 0 {
		procShowWindow.Call(uintptr(found), windows.SW_RESTORE)
	}
	procSetForegroundWindow.Call(uintptr(found))
	return true
}

// allowForeground lets the running instance bring its window to the front
// when this process (just started by the user) forwards a request to it.
func allowForeground() {
	const asfwAny = ^uintptr(0) // ASFW_ANY = (DWORD)-1
	procAllowSetForegroundWindow.Call(asfwAny & 0xffffffff)
}

// tunnelLUID finds the VPN adapter created by the core ("Vela").
func tunnelLUID() (uint64, error) {
	var luid uint64
	alias, _ := windows.UTF16PtrFromString("Vela")
	if procConvertInterfaceAliasToLuid.Find() == nil {
		if r, _, _ := procConvertInterfaceAliasToLuid.Call(uintptr(unsafe.Pointer(alias)), uintptr(unsafe.Pointer(&luid))); r == 0 && luid != 0 {
			return luid, nil
		}
	}
	// fallback: the adapter that owns the tunnel address
	size := uint32(15000)
	for i := 0; i < 3; i++ {
		buf := make([]byte, size)
		aa := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, windows.GAA_FLAG_SKIP_ANYCAST|windows.GAA_FLAG_SKIP_MULTICAST|windows.GAA_FLAG_SKIP_DNS_SERVER, 0, aa, &size)
		if err == windows.ERROR_BUFFER_OVERFLOW {
			continue
		}
		if err != nil {
			return 0, err
		}
		for a := aa; a != nil; a = a.Next {
			for u := a.FirstUnicastAddress; u != nil; u = u.Next {
				if ip := u.Address.IP(); ip != nil && ip.String() == "172.19.0.1" {
					return a.Luid, nil
				}
			}
		}
		break
	}
	return 0, errors.New(tr("не найден сетевой адаптер Vela"))
}

// startProcess starts a copy of Vela (used after an update). An elevated
// Vela starts an elevated copy.
func startProcess(exe string, args []string, env []string) error {
	cmd := exec.Command(exe, args...)
	cmd.Env = env
	cmd.Dir = filepath.Dir(exe)
	return cmd.Start()
}

// listProcesses returns names of running programs for the split tunneling
// picker (e.g. "chrome.exe"), without duplicates and system processes.
func listProcesses() []string {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return []string{}
	}
	defer windows.CloseHandle(snap)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	seen := map[string]bool{}
	skip := map[string]bool{"system": true, "[system process]": true, "svchost.exe": true, "csrss.exe": true,
		"wininit.exe": true, "services.exe": true, "lsass.exe": true, "smss.exe": true, "winlogon.exe": true,
		"registry": true, "fontdrvhost.exe": true, "dwm.exe": true, "conhost.exe": true, "vela.exe": true,
		"memory compression": true, "runtimebroker.exe": true, "sihost.exe": true, "dllhost.exe": true}
	out := []string{}
	for err = windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		name := windows.UTF16ToString(entry.ExeFile[:])
		key := strings.ToLower(name)
		if name == "" || seen[key] || skip[key] {
			continue
		}
		seen[key] = true
		out = append(out, name)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

func deviceOS() string { return "Windows" }

func osVersion() string {
	v := windows.RtlGetVersion()
	return fmt.Sprintf("%d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
}
