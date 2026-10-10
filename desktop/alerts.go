package main

import (
	"fmt"
	"strings"
	"time"
)

// Alert warns about a subscription that ends soon or runs out of traffic.
// It is shown as a banner in the window while it holds and once as a
// Windows notification.
type Alert struct {
	Key     string `json:"key"`
	Profile string `json:"profile"`
	Name    string `json:"name"`
	// "expired", "1d" (less than a day left), "3d" (up to three days left)
	// or "traffic" (90% or more of the traffic used).
	Kind     string `json:"kind"`
	DaysLeft int    `json:"daysLeft,omitempty"`
	Percent  int    `json:"percent,omitempty"`
	Support  string `json:"supportUrl,omitempty"`
}

func profileAlerts(p *Profile, now time.Time) []Alert {
	if p.Kind != "subscription" {
		return nil
	}
	var out []Alert
	if p.Expire > 0 {
		left := p.Expire - now.Unix()
		kind := ""
		switch {
		case left <= 0:
			kind = "expired"
		case left <= 86400:
			kind = "1d"
		case left <= 3*86400:
			kind = "3d"
		}
		if kind != "" {
			// the period is the expiry date: a renewed subscription warns again
			out = append(out, Alert{Key: fmt.Sprintf("%s:%s:%d", p.ID, kind, p.Expire), Profile: p.ID, Name: p.Name,
				Kind: kind, DaysLeft: int((max(left, 0) + 86399) / 86400), Support: p.Support})
		}
	}
	if p.Total > 0 {
		used := p.Upload + p.Download
		if used*10 >= p.Total*9 {
			out = append(out, Alert{Key: fmt.Sprintf("%s:traffic:%d:%d", p.ID, p.Expire, p.Total), Profile: p.ID, Name: p.Name,
				Kind: "traffic", Percent: int(min(100, used*100/p.Total)), Support: p.Support})
		}
	}
	return out
}

// alertsLocked lists the alerts not closed by the user. Needs a.mu.
func (a *App) alertsLocked(now time.Time) []Alert {
	closed := map[string]bool{}
	for _, k := range a.state.Dismissed {
		closed[k] = true
	}
	out := []Alert{}
	for _, p := range a.state.Profiles {
		for _, al := range profileAlerts(p, now) {
			if !closed[al.Key] {
				out = append(out, al)
			}
		}
	}
	return out
}

var alertRank = map[string]int{"expired": 4, "1d": 3, "traffic": 2, "3d": 1}

func alertText(al Alert) string {
	switch al.Kind {
	case "expired":
		return fmt.Sprintf(tr("Подписка «%s» закончилась. Продлите её у провайдера"), al.Name)
	case "1d":
		return fmt.Sprintf(tr("Подписка «%s» закончится меньше чем через сутки"), al.Name)
	case "3d":
		return fmt.Sprintf(tr("Подписка «%s» закончится через %d дня"), al.Name, al.DaysLeft)
	case "traffic":
		return fmt.Sprintf(tr("В подписке «%s» израсходовано %d%% трафика"), al.Name, al.Percent)
	}
	return ""
}

// checkAlerts shows a Windows notification for each new alert (one at a
// time: the most important).
func (a *App) checkAlerts() {
	now := time.Now()
	a.mu.Lock()
	if a.state.Notified == nil {
		a.state.Notified = map[string]int64{}
	}
	changed := false
	ids := map[string]bool{}
	var fire []Alert
	for _, p := range a.state.Profiles {
		ids[p.ID] = true
		// traffic was reset (new period without an expiry date)
		if p.Total > 0 && (p.Upload+p.Download)*2 < p.Total {
			for k := range a.state.Notified {
				if strings.HasPrefix(k, p.ID+":traffic:") {
					delete(a.state.Notified, k)
					changed = true
				}
			}
		}
		for _, al := range profileAlerts(p, now) {
			if _, done := a.state.Notified[al.Key]; done {
				continue
			}
			a.state.Notified[al.Key] = now.Unix()
			fire = append(fire, al)
			changed = true
		}
	}
	// forget deleted subscriptions
	for k := range a.state.Notified {
		if !ids[strings.SplitN(k, ":", 2)[0]] {
			delete(a.state.Notified, k)
			changed = true
		}
	}
	if changed {
		a.saveLocked()
	}
	a.mu.Unlock()
	if len(fire) == 0 {
		return
	}
	best := fire[0]
	for _, al := range fire[1:] {
		if alertRank[al.Kind] > alertRank[best.Kind] {
			best = al
		}
	}
	notify("Vela", alertText(best), best.Kind != "3d")
	a.pushState()
}

// alertLoop re-checks as time passes (a subscription can expire while Vela
// is open).
func (a *App) alertLoop() {
	time.Sleep(15 * time.Second)
	for {
		a.checkAlerts()
		a.pushState()
		time.Sleep(30 * time.Minute)
	}
}

// Dismiss hides a banner (alert or provider announcement) for good.
func (a *App) Dismiss(key string) {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 200 {
		return
	}
	a.mu.Lock()
	for _, k := range a.state.Dismissed {
		if k == key {
			a.mu.Unlock()
			return
		}
	}
	a.state.Dismissed = append(a.state.Dismissed, key)
	if len(a.state.Dismissed) > 300 {
		a.state.Dismissed = a.state.Dismissed[len(a.state.Dismissed)-300:]
	}
	a.saveLocked()
	a.mu.Unlock()
	a.pushState()
}
