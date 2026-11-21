package main

import (
	"sort"
	"sync"
)

type NodePingResult struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	IP    string `json:"ip"`
	RTTMs int64  `json:"rtt_ms"`
}

func (g *GUIController) BenchAllProfiles() []NodePingResult {
	g.mu.Lock()
	profiles := append([]NodeProfile(nil), g.cfg.Profiles...)
	g.mu.Unlock()

	results := make([]NodePingResult, len(profiles))
	var wg sync.WaitGroup
	work := make(chan int)
	for worker := 0; worker < min(4, len(profiles)); worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				p := profiles[i]
				r := NodePingResult{Index: i, Name: p.Name, IP: p.ServerIP, RTTMs: -1}
				cm, err := newNodeMux(p.ServerIP, p.MasterKey, p.TLSNode)
				if err == nil {
					r.RTTMs, _ = MeasureTunnelLatency(cm)
					cm.Close()
				}
				results[i] = r
			}
		}()
	}
	for i := range profiles {
		work <- i
	}
	close(work)
	wg.Wait()

	sort.Slice(results, func(i, j int) bool {
		if results[i].RTTMs < 0 {
			return false
		}
		if results[j].RTTMs < 0 {
			return true
		}
		return results[i].RTTMs < results[j].RTTMs
	})
	return results
}
