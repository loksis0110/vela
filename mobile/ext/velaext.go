// Package velaext is bound into the iOS Network Extension framework next to
// sing-box's libbox. Importing it adds the Xray engine to the core there and
// gives the extension the traffic statistics recorder.
package velaext

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/velaapp/vela/core"
	_ "github.com/velaapp/vela/xrayout"
)

var (
	mu       sync.Mutex
	recorder *core.TrafficRecorder
)

// StartTrafficRecorder counts the traffic of the running core into the
// statistics file at path.
func StartTrafficRecorder(port int32, secret, path string) {
	mu.Lock()
	defer mu.Unlock()
	recorder.Stop()
	recorder = core.StartTrafficRecorder(int(port), secret, path)
}

// StopTrafficRecorder saves the last numbers.
func StopTrafficRecorder() {
	mu.Lock()
	defer mu.Unlock()
	recorder.Stop()
	recorder = nil
}

// TrafficSummary returns JSON of core.TrafficSummary.
func TrafficSummary(path string) string {
	data, _ := json.Marshal(core.LoadTrafficSummary(path, time.Now()))
	return string(data)
}
