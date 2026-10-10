package main

import (
	"time"
)

// firewall blocks the internet outside the VPN (see wfp_windows.go).
type firewall interface {
	// Enable blocks all traffic except Vela itself, loopback, DHCP/NDP and,
	// when allowLAN is set, local networks. Idempotent.
	Enable(allowLAN bool) error
	// SetTunnel permits traffic through the VPN interface with this LUID.
	SetTunnel(luid uint64) error
	// Disable removes all filters.
	Disable()
	Active() bool
}

// killSwitchWanted reports whether the filters must be on. Must be called
// with a.mu held.
func (a *App) killSwitchWantedLocked() bool {
	return a.state.Prefs.KillSwitch && a.state.Settings.Mode == "tun" && a.intent && killSwitchSupported()
}

// syncKillSwitch turns the filters on or off to match the settings and the
// user's intent to be connected. While the user wants the VPN and it is not
// up (connecting, reconnecting, failed), everything stays blocked.
func (a *App) syncKillSwitch() {
	a.fwMu.Lock()
	a.mu.Lock()
	want := a.killSwitchWantedLocked()
	allowLAN := !a.state.Prefs.KillSwitchNoLAN
	running := a.instance != nil && a.state.Settings.Mode == "tun"
	a.mu.Unlock()
	var err error
	wasOn := a.fw.Active()
	if want {
		err = a.fw.Enable(allowLAN)
		if err == nil && !wasOn && running {
			// turned on while connected: let the tunnel through at once
			err = a.attachTunnelLocked()
		}
	} else if wasOn {
		a.fw.Disable()
	}
	on := a.fw.Active()
	a.fwMu.Unlock()

	a.mu.Lock()
	a.status.KillSwitch = on
	a.status.Blocked = on && a.intent && a.status.State != "connected"
	if err != nil {
		a.status.KillSwitchError = tr("Kill switch не включился: ") + err.Error()
	} else if !want || on {
		a.status.KillSwitchError = ""
	}
	a.mu.Unlock()
}

// attachTunnel permits the VPN interface of the core that just started.
func (a *App) attachTunnel() error {
	a.fwMu.Lock()
	defer a.fwMu.Unlock()
	if !a.fw.Active() {
		return nil
	}
	return a.attachTunnelLocked()
}

func (a *App) attachTunnelLocked() error {
	var luid uint64
	var err error
	// the adapter can take a moment to get its name
	for i := 0; i < 20; i++ {
		if luid, err = tunnelLUID(); err == nil {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if err != nil {
		return err
	}
	return a.fw.SetTunnel(luid)
}
