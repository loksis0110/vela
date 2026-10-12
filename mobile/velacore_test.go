package velacore

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// JSON shapes the apps rely on: lists are [] (never null).
func TestJSONShapes(t *testing.T) {
	var parsed struct {
		Nodes  []any `json:"nodes"`
		Errors []any `json:"errors"`
	}
	if err := json.Unmarshal([]byte(ParseText("")), &parsed); err != nil || parsed.Nodes == nil || parsed.Errors == nil {
		t.Fatalf("ParseText: %v %+v", err, parsed)
	}
	for _, out := range []string{DetectImport(""), DetectImport("hello")} {
		if !strings.Contains(out, `"subscriptions":[]`) || !strings.Contains(out, `"linkCount":0`) {
			t.Fatalf("DetectImport: %s", out)
		}
	}
	if _, err := ParseDeepLink("nonsense"); err == nil {
		t.Fatal("ParseDeepLink accepted nonsense")
	}
	if out, err := DecodeBackup("not a backup"); out != "" || err != nil {
		t.Fatalf("DecodeBackup: %q %v", out, err)
	}
	if !strings.Contains(TrafficSummary(t.TempDir()+"/none.json"), `"days":[`) {
		t.Fatal("TrafficSummary")
	}
	if NodeIsEncrypted("{") || NodeIsEncrypted(`{"link":"vless://11111111-2222-3333-4444-555555555555@a.example.com:443?type=tcp"}`) {
		t.Fatal("NodeIsEncrypted")
	}
	if !NodeIsEncrypted(`{"link":"trojan://pw@t.example.com:443"}`) {
		t.Fatal("NodeIsEncrypted trojan")
	}
}

func TestNilAndBadInput(t *testing.T) {
	if err := ProbeNodes("[]", 1000, 1, nil); err == nil {
		t.Fatal("nil handler accepted")
	}
	if _, err := BuildConfig("{"); err == nil {
		t.Fatal("bad JSON accepted")
	}
	StopProbe()
	StopTrafficRecorder()
	StopTrafficRecorder()
}

func TestStopSpeedTest(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	start := time.Now()
	go func() {
		defer wg.Done()
		SpeedTest(30, nil) // no network needed: it is stopped right away
	}()
	time.Sleep(200 * time.Millisecond)
	StopSpeedTest()
	wg.Wait()
	if time.Since(start) > 10*time.Second {
		t.Fatalf("speed test did not stop: %v", time.Since(start))
	}
}
