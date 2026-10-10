package main

import (
	"errors"
	"fmt"
	"strings"
)

// The global hotkey connects or disconnects the VPN from anywhere.

const defaultHotkey = "Ctrl+Alt+V"

const (
	modAlt     = 0x1
	modControl = 0x2
	modShift   = 0x4
	modWin     = 0x8
)

type hotkeySpec struct {
	Mods uint32
	VK   uint32
	Text string // canonical form, e.g. "Ctrl+Alt+V"
}

// effectiveHotkey returns the combination in use: "" in the settings means
// the default one, "off" means none.
func effectiveHotkey(pref string) string {
	switch pref {
	case "":
		return defaultHotkey
	case "off":
		return ""
	}
	return pref
}

// parseHotkey reads combinations like "Ctrl+Alt+V", "Ctrl+Shift+F9".
func parseHotkey(s string) (hotkeySpec, error) {
	parts := strings.Split(strings.ReplaceAll(s, " ", ""), "+")
	if len(parts) < 2 {
		return hotkeySpec{}, errors.New(tr("добавьте к клавише Ctrl, Alt или Win"))
	}
	var spec hotkeySpec
	for _, p := range parts[:len(parts)-1] {
		switch strings.ToLower(p) {
		case "ctrl", "control":
			spec.Mods |= modControl
		case "alt":
			spec.Mods |= modAlt
		case "shift":
			spec.Mods |= modShift
		case "win", "meta", "super", "cmd":
			spec.Mods |= modWin
		default:
			return hotkeySpec{}, fmt.Errorf(tr("неизвестная клавиша %s"), p)
		}
	}
	if spec.Mods&(modControl|modAlt|modWin) == 0 {
		return hotkeySpec{}, errors.New(tr("добавьте к клавише Ctrl, Alt или Win"))
	}
	key := strings.ToUpper(parts[len(parts)-1])
	switch {
	case len(key) == 1 && key[0] >= 'A' && key[0] <= 'Z', len(key) == 1 && key[0] >= '0' && key[0] <= '9':
		spec.VK = uint32(key[0])
	case len(key) >= 2 && key[0] == 'F':
		n := 0
		if _, err := fmt.Sscanf(key[1:], "%d", &n); err != nil || n < 1 || n > 24 || fmt.Sprint(n) != key[1:] {
			return hotkeySpec{}, fmt.Errorf(tr("неизвестная клавиша %s"), parts[len(parts)-1])
		}
		spec.VK = 0x70 + uint32(n-1)
	default:
		return hotkeySpec{}, errors.New(tr("используйте букву, цифру или F1–F24"))
	}
	var names []string
	if spec.Mods&modControl != 0 {
		names = append(names, "Ctrl")
	}
	if spec.Mods&modAlt != 0 {
		names = append(names, "Alt")
	}
	if spec.Mods&modShift != 0 {
		names = append(names, "Shift")
	}
	if spec.Mods&modWin != 0 {
		names = append(names, "Win")
	}
	spec.Text = strings.Join(append(names, key), "+")
	return spec, nil
}
