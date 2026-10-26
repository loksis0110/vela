//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func runtimePlatform() string {
	if runtime.GOOS == "darwin" {
		return "macos"
	}
	return "linux"
}

func isElevated() bool { return os.Geteuid() == 0 }

func relaunchElevated(args []string) error {
	return errors.New(tr("запустите Vela через sudo, чтобы перехватывать весь трафик"))
}

func openWindow(url, dataDir string) {
	fmt.Println("Vela UI:", url)
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	exec.Command(opener, url).Start()
}

func openExternal(target, dataDir string) {
	if target == "logs" {
		target = dataDir
	} else if target == "report" {
		target = dataDir + "/diagnostics.txt"
	} else if !strings.HasPrefix(target, "http") && !strings.HasPrefix(target, "tg://") {
		return
	}
	openWindow(target, dataDir)
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

func listProcesses() []string {
	out, err := exec.Command("ps", "-eo", "comm=").Output()
	if err != nil {
		return []string{}
	}
	seen := map[string]bool{}
	list := []string{}
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !seen[l] {
			seen[l] = true
			list = append(list, l)
		}
	}
	return list
}

func deviceOS() string {
	if runtime.GOOS == "darwin" {
		return "macOS"
	}
	return "Linux"
}

func osVersion() string { return "" }

func systemLanguage() string {
	for _, v := range []string{os.Getenv("LC_ALL"), os.Getenv("LC_MESSAGES"), os.Getenv("LANG")} {
		if v != "" {
			if strings.HasPrefix(strings.ToLower(v), "ru") {
				return "ru"
			}
			return "en"
		}
	}
	return "en"
}

func focusWindow() bool { return false }

func embeddedWindowOpen() bool { return false }

func closeWindow() {}

func replaceBrowserWindow(url, dataDir string) {}

func windowThemeChanged() {}

func allowForeground() {}

func tunnelLUID() (uint64, error) { return 0, errors.New("not supported") }

func startProcess(exe string, args []string, env []string) error {
	cmd := exec.Command(exe, args...)
	cmd.Env = env
	return cmd.Start()
}

// readClipboard uses the usual desktop tools when they are installed.
func readClipboard() (string, error) {
	for _, c := range [][]string{{"wl-paste", "--no-newline"}, {"xclip", "-selection", "clipboard", "-o"}, {"pbpaste"}} {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		out, err := exec.Command(c[0], c[1:]...).Output()
		return string(out), err
	}
	return "", nil
}

func notify(title, text string, warning bool) {}

func registerURLSchemes() {}

// Global hotkeys are a Windows feature; elsewhere (development builds) the
// setting is kept but does nothing.
func registerHotkey(spec *hotkeySpec, onPress func()) error { return nil }

func recoverSystemProxy(port int) {}
