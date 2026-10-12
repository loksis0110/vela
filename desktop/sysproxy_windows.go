//go:build windows

package main

import (
	"fmt"
	"strings"

	"github.com/sagernet/sing/common/wininet"
	"golang.org/x/sys/windows/registry"
)

// recoverSystemProxy turns off the Windows proxy left by a Vela that did not
// exit cleanly (crash, power loss) in the "system proxy" mode: it points to
// a local port nobody listens on, so no program could reach the internet.
// A proxy set by the user or another program is left alone.
func recoverSystemProxy(port int) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return
	}
	enabled, _, err := k.GetIntegerValue("ProxyEnable")
	server, _, _ := k.GetStringValue("ProxyServer")
	k.Close()
	if err != nil || enabled == 0 {
		return
	}
	if port <= 0 {
		port = 2080 // the default of core.Settings
	}
	ours := fmt.Sprintf("127.0.0.1:%d", port)
	server = strings.TrimSpace(server)
	if server == ours || server == "http://"+ours || strings.Contains(server, "="+ours) {
		wininet.ClearSystemProxy()
	}
}
