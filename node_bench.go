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

// Register temporary probes so a live policy change can close old UDP paths.
func (g *GUIController) newProbe(p NodeProfile) (*ClientMux, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	cm, err := g.cfg.nodeMux(p.ServerIP, p.MasterKey, p.TLSNode)
	if err != nil {
		return nil, err
	}
	if err := GlobalTUN.pin(cm); err != nil {
		cm.Close()
		return nil, err
	}
	if g.probes == nil {
		g.probes = make(map[*ClientMux]struct{})
	}
	g.probes[cm] = struct{}{}
	return cm, nil
}

func (g *GUIController) finishProbe(probe *ClientMux) {
	probe.Close()
	g.mu.Lock()
	GlobalTUN.unpin(probe)
	delete(g.probes, probe)
	g.mu.Unlock()
}

func (g *GUIController) BenchAllProfiles() []NodePingResult {
	g.mu.Lock()
	profiles := append([]NodeProfile(nil), g.cfg.Profiles...)
	active, cfg := g.cm, *g.cfg
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
				if active != nil && p.ServerIP == cfg.ServerIP && p.MasterKey == cfg.MasterKey && p.TLSNode == cfg.TLSNode {
					r.RTTMs, _ = profileLatency(cfg.networkOptions().Probe, active)
				} else if cm, err := g.newProbe(p); err == nil {
					r.RTTMs, _ = profileLatency(cfg.networkOptions().Probe, cm)
					g.finishProbe(cm)
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
