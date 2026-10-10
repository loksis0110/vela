package core

import (
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestTrafficBook(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic.json")
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	AddTraffic(path, 100, 1000, now)
	AddTraffic(path, 50, 500, now)
	AddTraffic(path, 1, 2, now.AddDate(0, 0, -1))
	AddTraffic(path, 7, 8, now.AddDate(0, -2, 0))
	s := LoadTrafficSummary(path, now)
	if len(s.Days) != 31 || s.Today.Up != 150 || s.Today.Down != 1500 || s.Days[29].Down != 2 {
		t.Fatalf("days %+v today %+v", s.Days[29:], s.Today)
	}
	if len(s.Months) != 12 || s.ThisMonth.Down != 1502 || s.Months[9].Up != 7 || s.Total.Up != 158 {
		t.Fatalf("months %+v total %+v", s.Months, s.Total)
	}
}

func TestConnectionsAPI(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	cfg := fmt.Sprintf(`{"outbounds":[{"type":"direct","tag":"direct"}],"experimental":{"clash_api":{"external_controller":"127.0.0.1:%d","secret":"s"}}}`, port)
	instance, cancel, err := StartBox(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	defer instance.Close()
	list, err := ListConnections(port, "s")
	if err != nil || len(list) != 0 {
		t.Fatalf("list %v %v", list, err)
	}
	if err := CloseConnection(port, "s", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := ListConnections(port, "wrong"); err == nil {
		t.Fatal("secret not checked")
	}
	path := filepath.Join(t.TempDir(), "t.json")
	r := StartTrafficRecorder(port, "s", path)
	r.Stop()
}
