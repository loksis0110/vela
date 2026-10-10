//go:build !windows || !(amd64 || arm64)

package main

import "errors"

// The kill switch exists only on Windows (Windows Filtering Platform).

type noFirewall struct{}

func newFirewall() firewall { return noFirewall{} }

func killSwitchSupported() bool { return false }

func (noFirewall) Enable(bool) error {
	return errors.New(tr("Kill switch работает только в Windows"))
}
func (noFirewall) SetTunnel(uint64) error { return nil }
func (noFirewall) Disable()               {}
func (noFirewall) Active() bool           { return false }
