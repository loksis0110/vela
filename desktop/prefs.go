package main

import (
	"errors"
)

// AppPrefs are the preferences of the Windows app itself. Zero values are
// the defaults.
type AppPrefs struct {
	// "" follows Windows, "ru" or "en".
	Language string `json:"language,omitempty"`
	// Kill switch: block the internet while the VPN (TUN mode) is not up.
	KillSwitch bool `json:"killSwitch,omitempty"`
	// KillSwitchNoLAN also blocks local networks (allowed by default).
	KillSwitchNoLAN bool `json:"killSwitchNoLan,omitempty"`
	NoUpdateCheck   bool `json:"noUpdateCheck,omitempty"`
	NoClipboard     bool `json:"noClipboard,omitempty"`
	// "" the default combination, "off" none, or e.g. "Ctrl+Shift+F9".
	Hotkey string `json:"hotkey,omitempty"`
}

// SetPrefs saves the app preferences (the hotkey has its own call).
func (a *App) SetPrefs(p AppPrefs) error {
	switch p.Language {
	case "", "ru", "en":
	default:
		return errors.New(tr("неизвестный язык"))
	}
	a.mu.Lock()
	old := a.state.Prefs
	p.Hotkey = old.Hotkey
	a.state.Prefs = p
	a.saveLocked()
	a.mu.Unlock()
	if p.Language != old.Language {
		setLanguage(resolveLanguage(p.Language))
	}
	if p.KillSwitch != old.KillSwitch || p.KillSwitchNoLAN != old.KillSwitchNoLAN {
		a.syncKillSwitch()
		a.pushStatus()
	}
	if old.NoUpdateCheck && !p.NoUpdateCheck {
		a.kickUpdateCheck()
	}
	a.pushState()
	return nil
}

// SetHotkey changes the global hotkey ("" or "off" turns it off). A
// combination taken by another program is refused and the previous one
// stays.
func (a *App) SetHotkey(text string) error {
	var spec *hotkeySpec
	pref := "off"
	if text != "" && text != "off" {
		s, err := parseHotkey(text)
		if err != nil {
			return err
		}
		spec, pref = &s, s.Text
		if pref == defaultHotkey {
			pref = ""
		}
	}
	if err := registerHotkey(spec, a.hotkeyPressed); err != nil {
		a.applyHotkey()
		return err
	}
	a.mu.Lock()
	a.state.Prefs.Hotkey = pref
	a.hotkeyErr = ""
	a.saveLocked()
	a.mu.Unlock()
	a.pushState()
	return nil
}

// applyHotkey registers the saved combination.
func (a *App) applyHotkey() error {
	a.mu.Lock()
	text := effectiveHotkey(a.state.Prefs.Hotkey)
	a.mu.Unlock()
	var spec *hotkeySpec
	if text != "" {
		if s, err := parseHotkey(text); err == nil {
			spec = &s
		}
	}
	err := registerHotkey(spec, a.hotkeyPressed)
	a.mu.Lock()
	a.hotkeyErr = ""
	if err != nil {
		a.hotkeyErr = err.Error()
	}
	a.mu.Unlock()
	return err
}

// hotkeyPressed connects or disconnects.
func (a *App) hotkeyPressed() {
	a.mu.Lock()
	state := a.status.State
	a.mu.Unlock()
	if state == "disconnected" {
		if err := a.Connect(); err != nil {
			// no servers yet or admin rights needed: the window explains
			showWindow()
		}
		return
	}
	a.Disconnect()
}
