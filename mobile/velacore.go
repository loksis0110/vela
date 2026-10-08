// Package velacore exposes the shared Vela logic to Android (Kotlin) and
// iOS (Swift) through gomobile. All values cross the boundary as JSON
// strings so both apps stay simple.
package velacore

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/velaapp/vela/core"
)

// ParseText parses share links or a base64 subscription body.
// Returns JSON: {"nodes":[...],"errors":[...]}.
func ParseText(text string) string {
	data, _ := json.Marshal(core.ParseText(text))
	return string(data)
}

// FetchSubscription downloads a subscription URL.
// Returns JSON of core.Subscription.
func FetchSubscription(url string) (string, error) {
	sub, err := core.FetchSubscription(context.Background(), url)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(sub)
	return string(data), err
}

// BuildConfig takes JSON of core.BuildRequest and returns sing-box JSON.
func BuildConfig(requestJSON string) (string, error) {
	var req core.BuildRequest
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return "", err
	}
	return core.BuildConfig(req)
}

// DefaultSettings returns JSON of the default settings.
func DefaultSettings() string {
	data, _ := json.Marshal(core.DefaultSettings())
	return string(data)
}

// TCPPing returns TCP connect latency in ms, or -1.
func TCPPing(host string, port int32) int32 {
	return int32(core.TCPPing(host, int(port), 4*time.Second))
}

// NodeTag returns the outbound tag used for a node id (for the controller).
func NodeTag(nodeID string) string {
	return core.NodeTag(core.Node{ID: nodeID})
}

// SplitImport separates pasted text into subscription URLs and share links.
// Returns JSON: {"subscriptions":[...],"links":"..."}.
func SplitImport(text string) string {
	subs, links := core.SplitImport(text)
	if subs == nil {
		subs = []string{}
	}
	data, _ := json.Marshal(map[string]any{"subscriptions": subs, "links": links})
	return string(data)
}

// ProbeHandler receives results of ProbeNodes as they arrive.
type ProbeHandler interface {
	OnResult(nodeID string, delayMs int32, reason string, rawError string)
}

// ProbeNodes makes a real request through every node using a temporary core
// (call it while the VPN is off). nodesJSON is a JSON array of nodes as
// returned by ParseText/FetchSubscription. Blocks until all are checked.
func ProbeNodes(nodesJSON string, timeoutMs int32, parallel int32, handler ProbeHandler) error {
	var nodes []core.Node
	if err := json.Unmarshal([]byte(nodesJSON), &nodes); err != nil {
		return err
	}
	return core.ProbeNodes(context.Background(), nil, nodes, time.Duration(timeoutMs)*time.Millisecond, int(parallel),
		func(r core.ProbeResult) {
			handler.OnResult(r.ID, int32(r.Delay), r.Reason, r.Error)
		})
}

// ExplainError turns a core error message into a short Russian explanation.
func ExplainError(message string) string {
	return core.ExplainError(message)
}

// EncodeQR renders text as a PNG QR code.
func EncodeQR(text string, size int32) ([]byte, error) {
	return core.EncodeQR(text, int(size))
}

// DecodeQR returns the text of a QR code found in a PNG/JPEG image.
func DecodeQR(image []byte) (string, error) {
	return core.DecodeQR(image)
}

// MyIP returns JSON {ip,country,countryCode,city,isp,flag} as websites see
// this device (through the VPN when it's on).
func MyIP() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	info, err := core.MyIP(ctx, nil)
	if err != nil {
		return "", err
	}
	data, _ := json.Marshal(info)
	return string(data), nil
}

// SpeedHandler receives speed test progress: phase is "ping", "download",
// "upload" or "done"; value is ms or Mbit/s.
type SpeedHandler interface {
	OnProgress(phase string, value float64)
}

// SpeedTest measures ping, download and upload over the current network
// (through the VPN when it's on). Returns JSON of core.SpeedResult.
func SpeedTest(seconds int32, handler SpeedHandler) string {
	res := core.SpeedTest(context.Background(), nil, time.Duration(seconds)*time.Second, func(p string, v float64) {
		if handler != nil {
			handler.OnProgress(p, v)
		}
	})
	data, _ := json.Marshal(res)
	return string(data)
}

// EncodeBackup turns backup JSON (core.Backup) into shareable text.
func EncodeBackup(backupJSON string) (string, error) {
	var b core.Backup
	if err := json.Unmarshal([]byte(backupJSON), &b); err != nil {
		return "", err
	}
	return core.EncodeBackup(b)
}

// DecodeBackup returns the backup JSON, or "" when text is not a backup.
func DecodeBackup(text string) (string, error) {
	b, ok, err := core.DecodeBackup(text)
	if !ok || err != nil {
		return "", err
	}
	data, _ := json.Marshal(b)
	return string(data), nil
}

// ReadLog returns the last lines of the core log file, tidied for a phone
// screen and joined with "\n".
func ReadLog(path string, limit int32) string {
	return strings.Join(core.ReadLog(path, int(limit)), "\n")
}

// TidyLog shortens every line of a raw core log (see ReadLog).
func TidyLog(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, l := range lines {
		lines[i] = core.TidyLogLine(l)
	}
	return strings.Join(lines, "\n")
}
