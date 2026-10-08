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

func relaunchElevated() error {
	return errors.New("запустите Vela через sudo, чтобы перехватывать весь трафик")
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
