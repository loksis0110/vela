package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// TrafficDay is the traffic of one day ("2026-10-09") or month ("2026-10").
type TrafficDay struct {
	Day  string `json:"day"`
	Up   int64  `json:"up"`
	Down int64  `json:"down"`
}

// TrafficSummary is what the statistics screens show.
type TrafficSummary struct {
	// The last 31 days, oldest first, including days without traffic.
	Days []TrafficDay `json:"days"`
	// The last 12 months, oldest first.
	Months    []TrafficDay `json:"months"`
	Today     TrafficDay   `json:"today"`
	ThisMonth TrafficDay   `json:"thisMonth"`
	Total     TrafficDay   `json:"total"`
}

type trafficFile struct {
	Days map[string][2]int64 `json:"days"` // day -> [up, down]
}

var trafficMu sync.Mutex

func loadTraffic(path string) trafficFile {
	f := trafficFile{Days: map[string][2]int64{}}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &f)
		if f.Days == nil {
			f.Days = map[string][2]int64{}
		}
	}
	return f
}

// AddTraffic adds bytes to the day of now in the statistics file.
func AddTraffic(path string, up, down int64, now time.Time) error {
	if up <= 0 && down <= 0 {
		return nil
	}
	trafficMu.Lock()
	defer trafficMu.Unlock()
	f := loadTraffic(path)
	day := now.Format("2006-01-02")
	v := f.Days[day]
	v[0] += max(up, 0)
	v[1] += max(down, 0)
	f.Days[day] = v
	// keep a bit more than a year
	cutoff := now.AddDate(0, 0, -400).Format("2006-01-02")
	for d := range f.Days {
		if d < cutoff {
			delete(f.Days, d)
		}
	}
	data, _ := json.Marshal(f)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadTrafficSummary reads the statistics file.
func LoadTrafficSummary(path string, now time.Time) TrafficSummary {
	trafficMu.Lock()
	f := loadTraffic(path)
	trafficMu.Unlock()
	var s TrafficSummary
	months := map[string]*TrafficDay{}
	for d, v := range f.Days {
		s.Total.Up += v[0]
		s.Total.Down += v[1]
		if len(d) >= 7 {
			m := months[d[:7]]
			if m == nil {
				m = &TrafficDay{Day: d[:7]}
				months[d[:7]] = m
			}
			m.Up += v[0]
			m.Down += v[1]
		}
	}
	for i := 30; i >= 0; i-- {
		d := now.AddDate(0, 0, -i).Format("2006-01-02")
		v := f.Days[d]
		s.Days = append(s.Days, TrafficDay{Day: d, Up: v[0], Down: v[1]})
	}
	s.Today = s.Days[len(s.Days)-1]
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	for i := 11; i >= 0; i-- {
		m := first.AddDate(0, -i, 0).Format("2006-01")
		if v := months[m]; v != nil {
			s.Months = append(s.Months, *v)
		} else {
			s.Months = append(s.Months, TrafficDay{Day: m})
		}
	}
	s.ThisMonth = s.Months[len(s.Months)-1]
	s.Total.Day = "total"
	return s
}

// controllerGet calls the local Clash API of a running core.
func controllerGet(ctx context.Context, method string, port int, secret, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
	if err != nil {
		return err
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("controller: %s", resp.Status)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type clashConnections struct {
	DownloadTotal int64 `json:"downloadTotal"`
	UploadTotal   int64 `json:"uploadTotal"`
	Connections   []struct {
		ID       string `json:"id"`
		Metadata struct {
			Network         string `json:"network"`
			DestinationIP   string `json:"destinationIP"`
			DestinationPort string `json:"destinationPort"`
			Host            string `json:"host"`
			ProcessPath     string `json:"processPath"`
		} `json:"metadata"`
		Upload   int64     `json:"upload"`
		Download int64     `json:"download"`
		Start    time.Time `json:"start"`
		Chains   []string  `json:"chains"`
		Rule     string    `json:"rule"`
	} `json:"connections"`
}

// TrafficRecorder adds the traffic of a running core to the statistics
// file every 30 seconds and when stopped.
type TrafficRecorder struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// StartTrafficRecorder watches the core behind the local controller.
func StartTrafficRecorder(port int, secret, path string) *TrafficRecorder {
	ctx, cancel := context.WithCancel(context.Background())
	r := &TrafficRecorder{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(r.done)
		var lastUp, lastDown int64
		sample := func() {
			var c clashConnections
			qctx, qcancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer qcancel()
			if controllerGet(qctx, http.MethodGet, port, secret, "/connections", &c) != nil {
				return
			}
			up, down := c.UploadTotal-lastUp, c.DownloadTotal-lastDown
			if up < 0 || down < 0 { // the core was restarted
				up, down = c.UploadTotal, c.DownloadTotal
			}
			lastUp, lastDown = c.UploadTotal, c.DownloadTotal
			_ = AddTraffic(path, up, down, time.Now())
		}
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				sample()
				return
			case <-ticker.C:
				sample()
			}
		}
	}()
	return r
}

// Stop saves the last numbers and stops. The core must still be running
// for the last few seconds to be counted.
func (r *TrafficRecorder) Stop() {
	if r == nil {
		return
	}
	r.cancel()
	select {
	case <-r.done:
	case <-time.After(6 * time.Second):
	}
}

// Connection is one open connection of the running core.
type Connection struct {
	ID          string `json:"id"`
	Network     string `json:"network"`
	Host        string `json:"host"`
	Destination string `json:"destination"`
	// Outbound is the tag of the server (node-...) or "direct".
	Outbound string `json:"outbound"`
	Rule     string `json:"rule"`
	Process  string `json:"process,omitempty"`
	Upload   int64  `json:"upload"`
	Download int64  `json:"download"`
	// Start is the opening time in Unix milliseconds.
	Start int64 `json:"start"`
}

// ListConnections returns the open connections, newest first.
func ListConnections(port int, secret string) ([]Connection, error) {
	var c clashConnections
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := controllerGet(ctx, http.MethodGet, port, secret, "/connections", &c); err != nil {
		return nil, err
	}
	out := make([]Connection, 0, len(c.Connections))
	for _, x := range c.Connections {
		host := x.Metadata.Host
		dest := x.Metadata.DestinationIP
		if x.Metadata.DestinationPort != "" {
			dest = dest + ":" + x.Metadata.DestinationPort
		}
		if host == "" {
			host = x.Metadata.DestinationIP
		}
		ob := ""
		if len(x.Chains) > 0 {
			ob = x.Chains[0]
		}
		proc := x.Metadata.ProcessPath
		if proc != "" {
			proc = filepath.Base(proc)
		}
		out = append(out, Connection{ID: x.ID, Network: x.Metadata.Network, Host: host, Destination: dest,
			Outbound: ob, Rule: x.Rule, Process: proc, Upload: x.Upload, Download: x.Download,
			Start: x.Start.UnixMilli()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start > out[j].Start })
	return out, nil
}

// CloseConnection closes one connection, or all of them when id is empty.
func CloseConnection(port int, secret, id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	path := "/connections"
	if id != "" {
		path += "/" + url.PathEscape(id)
	}
	return controllerGet(ctx, http.MethodDelete, port, secret, path, nil)
}
