package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/velaapp/vela/core"
)

// Offer is something to import that waits for the user's confirmation: an
// app link opened in the browser (vela://, happ://, ...) or a subscription
// found in the clipboard.
type Offer struct {
	ID            string   `json:"id"`
	Source        string   `json:"source"` // "link" | "clipboard"
	Subscriptions []string `json:"subscriptions"`
	Hosts         []string `json:"hosts"`
	Links         string   `json:"links,omitempty"`
	LinkCount     int      `json:"linkCount"`
	Name          string   `json:"name,omitempty"`
	// Existing counts subscriptions that are already added (they will be
	// updated).
	Existing int    `json:"existing,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (a *App) newOfferLocked(source string, req core.ImportRequest) *Offer {
	o := &Offer{ID: randomID(), Source: source, Subscriptions: req.Subscriptions, Links: req.Links,
		LinkCount: req.LinkCount, Name: strings.TrimSpace(req.Name)}
	if o.Subscriptions == nil {
		o.Subscriptions = []string{}
	}
	for _, u := range o.Subscriptions {
		o.Hosts = append(o.Hosts, shortURL(u))
		if a.findProfileByURL(u) != nil {
			o.Existing++
		}
	}
	// a few at a time: old ones the user ignored are dropped
	if len(a.offers) > 8 {
		for id := range a.offers {
			if id != a.linkWait {
				delete(a.offers, id)
			}
		}
	}
	a.offers[o.ID] = o
	return o
}

// OfferLink handles an app link Vela was opened with: the window shows what
// will be added and asks for confirmation.
func (a *App) OfferLink(raw string) {
	req, err := core.ParseDeepLink(raw)
	a.mu.Lock()
	if a.linkWait != "" {
		delete(a.offers, a.linkWait)
	}
	var o *Offer
	if err != nil {
		o = &Offer{ID: randomID(), Source: "link", Subscriptions: []string{}, Error: err.Error()}
		a.offers[o.ID] = o
	} else {
		o = a.newOfferLocked("link", req)
	}
	a.linkWait = o.ID
	a.mu.Unlock()
	a.pushState()
}

// CheckClipboard returns a subscription or servers found in the clipboard,
// only once per copied text and only when they are not added yet.
func (a *App) CheckClipboard() *Offer {
	a.mu.Lock()
	off, last := a.state.Prefs.NoClipboard, a.state.ClipHash
	a.mu.Unlock()
	if off {
		return nil
	}
	text, err := readClipboard()
	if err != nil || strings.TrimSpace(text) == "" || len(text) > 1<<20 {
		return nil
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	hash := hex.EncodeToString(sum[:16])
	if hash == last {
		return nil
	}
	req := core.DetectImport(text)
	if req.Empty() {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var subs []string
	for _, u := range req.Subscriptions {
		if a.findProfileByURL(u) == nil {
			subs = append(subs, u)
		}
	}
	have := map[string]bool{}
	for _, p := range a.state.Profiles {
		for _, n := range p.Nodes {
			have[n.Link] = true
		}
	}
	var links []string
	for _, l := range strings.Split(req.Links, "\n") {
		if l = strings.TrimSpace(l); l != "" && !have[l] {
			links = append(links, l)
		}
	}
	if len(subs) == 0 && len(links) == 0 {
		return nil // everything is already in Vela
	}
	a.state.ClipHash = hash
	a.saveLocked()
	o := a.newOfferLocked("clipboard", core.ImportRequest{Subscriptions: subs, Links: strings.Join(links, "\n"), LinkCount: len(links), Name: req.Name})
	cp := *o
	return &cp
}

// AcceptOffer imports what the offer contains.
func (a *App) AcceptOffer(ctx context.Context, id string) (string, error) {
	a.mu.Lock()
	o := a.offers[id]
	a.mu.Unlock()
	if o == nil {
		return "", errors.New(tr("ссылка устарела, откройте её ещё раз"))
	}
	if o.Error != "" {
		return "", errors.New(o.Error)
	}
	lines := append([]string{}, o.Subscriptions...)
	if strings.TrimSpace(o.Links) != "" {
		lines = append(lines, o.Links)
	}
	// the offer stays until the import works, so the user can try again
	msg, err := a.Import(ctx, strings.Join(lines, "\n"))
	if err != nil {
		return "", err
	}
	a.DismissOffer(id)
	if o.Name != "" {
		// the name from the link, when the provider gives none
		a.mu.Lock()
		changed := false
		for _, u := range o.Subscriptions {
			if p := a.findProfileByURL(u); p != nil && p.Title == "" && p.CustomName == "" {
				p.CustomName = o.Name
				a.dedupeNameLocked(p)
				changed = true
			}
		}
		if changed {
			a.saveLocked()
		}
		a.mu.Unlock()
		if changed {
			a.pushState()
		}
	}
	return msg, nil
}

// DismissOffer forgets an offer the user declined.
func (a *App) DismissOffer(id string) {
	a.mu.Lock()
	delete(a.offers, id)
	wasLink := a.linkWait == id
	if wasLink {
		a.linkWait = ""
	}
	a.mu.Unlock()
	if wasLink {
		a.pushState()
	}
}
