//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"

	"github.com/velaapp/vela/core"
)

// registerURLSchemes makes links like vela://import/... and the "add
// subscription" links for other clients (happ://, v2rayng://, ...) open
// Vela. vela:// is always ours; the others are taken only when no other
// program handles them. Runs on every start so a moved Vela.exe keeps
// working.
func registerURLSchemes() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	command := `"` + exe + `" "%1"`
	for _, scheme := range append(append([]string{}, core.DeepLinkSchemes...), "sub") {
		if scheme != "vela" {
			if machineHandler(scheme) {
				// a program installed for the whole computer handles it: our
				// per-user key would hide it
				removeOwnScheme(scheme)
				continue
			}
			if schemeOwnedByOther(scheme) {
				continue
			}
		}
		registerScheme(scheme, exe, command)
	}
}

// machineHandler reports whether another program registered the scheme for
// all users (HKEY_LOCAL_MACHINE).
func machineHandler(scheme string) bool {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `Software\Classes\`+scheme+`\shell\open\command`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	command, _, _ := k.GetStringValue("")
	return strings.TrimSpace(command) != "" && !strings.Contains(strings.ToLower(commandExe(command)), "vela")
}

// removeOwnScheme deletes the per-user key of a scheme if Vela created it.
func removeOwnScheme(scheme string) {
	base := `Software\Classes\` + scheme
	k, err := registry.OpenKey(registry.CURRENT_USER, base, registry.QUERY_VALUE)
	if err != nil {
		return
	}
	v, _, err := k.GetStringValue("VelaHandler")
	k.Close()
	if err != nil || v != "1" {
		return
	}
	for _, sub := range []string{`\shell\open\command`, `\shell\open`, `\shell`, `\DefaultIcon`, ""} {
		registry.DeleteKey(registry.CURRENT_USER, base+sub)
	}
}

// schemeOwnedByOther reports whether another program handles the scheme.
func schemeOwnedByOther(scheme string) bool {
	k, err := registry.OpenKey(registry.CLASSES_ROOT, scheme, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	command := ""
	if cmd, err := registry.OpenKey(registry.CLASSES_ROOT, scheme+`\shell\open\command`, registry.QUERY_VALUE); err == nil {
		command, _, _ = cmd.GetStringValue("")
		cmd.Close()
	}
	if v, _, err := k.GetStringValue("VelaHandler"); err == nil && v == "1" {
		// registered by Vela earlier (possibly from another folder), unless
		// another program has rewritten the command since
		if command == "" || strings.Contains(strings.ToLower(commandExe(command)), "vela") {
			return false
		}
		return true
	}
	if _, _, err := k.GetStringValue("URL Protocol"); err == nil {
		return true
	}
	return strings.TrimSpace(command) != "" // otherwise an empty leftover key
}

// commandExe returns the file name of the program in a shell command.
func commandExe(command string) string {
	command = strings.TrimSpace(command)
	if strings.HasPrefix(command, `"`) {
		if end := strings.Index(command[1:], `"`); end >= 0 {
			return filepath.Base(command[1 : end+1])
		}
	}
	if fields := strings.Fields(command); len(fields) > 0 {
		return filepath.Base(fields[0])
	}
	return ""
}

func registerScheme(scheme, exe, command string) {
	base := `Software\Classes\` + scheme
	k, _, err := registry.CreateKey(registry.CURRENT_USER, base, registry.SET_VALUE)
	if err != nil {
		return
	}
	k.SetStringValue("", "URL:"+scheme)
	k.SetStringValue("URL Protocol", "")
	k.SetStringValue("VelaHandler", "1")
	k.Close()
	if icon, _, err := registry.CreateKey(registry.CURRENT_USER, base+`\DefaultIcon`, registry.SET_VALUE); err == nil {
		icon.SetStringValue("", `"`+exe+`",0`)
		icon.Close()
	}
	if cmd, _, err := registry.CreateKey(registry.CURRENT_USER, base+`\shell\open\command`, registry.SET_VALUE); err == nil {
		cmd.SetStringValue("", command)
		cmd.Close()
	}
}
