// Command diag checks every server of a subscription with the Vela core and
// writes the results (without keys) to diag.json. Used by the "Diagnose"
// GitHub workflow to compare with other clients.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/velaapp/vela/core"
)

type result struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Security string `json:"security"`
	Network  string `json:"network"`
	FP       string `json:"fp"`
	TCP      int    `json:"tcp"`
	Delay    int    `json:"delay"`
	Reason   string `json:"reason,omitempty"`
	Error    string `json:"error,omitempty"`
	Link     string `json:"link"`
}

func main() {
	sub := os.Getenv("SUB_URL")
	if ua := os.Getenv("SUB_UA"); ua != "" {
		core.SetFetchOptions(core.FetchOptions{UserAgent: ua})
	}
	s, err := core.FetchSubscription(context.Background(), sub)
	if err != nil {
		fmt.Println("::error title=diag::subscription:", err)
		os.Exit(1)
	}
	fmt.Printf("nodes: %d\n", len(s.Nodes))
	res := make([]result, len(s.Nodes))
	idx := map[string]int{}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 32)
	for i, n := range s.Nodes {
		u, _ := url.Parse(n.Link)
		fp := ""
		if u != nil {
			fp = u.Query().Get("fp")
		}
		res[i] = result{ID: n.ID, Name: n.Name, Host: n.Server, Port: n.Port, Protocol: n.Protocol,
			Security: n.Security, Network: n.Network, FP: fp, Link: n.Link}
		idx[n.ID] = i
		wg.Add(1)
		go func(i int, n core.Node) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res[i].TCP = core.TCPPing(n.Server, n.Port, 5*time.Second)
		}(i, n)
	}
	wg.Wait()
	var mu sync.Mutex
	err = core.ProbeNodes(context.Background(), nil, s.Nodes, 10*time.Second, 16, func(r core.ProbeResult) {
		mu.Lock()
		defer mu.Unlock()
		i := idx[r.ID]
		res[i].Delay, res[i].Reason, res[i].Error = r.Delay, r.Reason, r.Error
	})
	if err != nil {
		fmt.Println("probe:", err)
	}
	data, _ := json.MarshalIndent(res, "", " ")
	os.WriteFile("diag.json", data, 0o600)
	ok := 0
	for _, r := range res {
		if r.Delay > 0 {
			ok++
		}
	}
	fmt.Printf("vela ok: %d of %d\n", ok, len(res))
}
