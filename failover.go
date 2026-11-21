package main

import (
	"log"
	"time"
)

func (g *GUIController) StartAutoFailover(stopCh <-chan struct{}) {
	go func() {
		interval := func() time.Duration {
			g.mu.Lock()
			defer g.mu.Unlock()
			return time.Duration(g.cfg.networkOptions().Heartbeat) * time.Second
		}
		period := func() time.Duration { return max(2*time.Second, interval()) }
		ticker := time.NewTimer(period())
		defer ticker.Stop()
		var observed *ClientMux
		consecutiveFails := 0
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				ticker.Reset(period())
				g.mu.Lock()
				running, cm, cfg := g.running, g.cm, g.cfg
				g.mu.Unlock()

				if !running || cm == nil {
					consecutiveFails = 0
					continue
				}
				if observed != cm {
					consecutiveFails = 0
					observed = cm
				}
				if nodeReachable(cm) {
					consecutiveFails = 0
					continue
				}
				consecutiveFails++
				log.Printf("[Failover] 当前节点探活失败 (%d/3)", consecutiveFails)
				if consecutiveFails < 3 {
					// Confirm a failure promptly even when healthy heartbeats are infrequent.
					ticker.Reset(2 * time.Second)
					continue
				}
				if cm.entry != nil {
					cm.entry.Reset()
				}
				cm.Reset()
				if !nodeReachable(cm) && len(cfg.Profiles) > 1 {
					g.tryStandby(stopCh, cm, cfg)
				}
				consecutiveFails = 0
			}
		}
	}()
}

// A congested bulk connection must not evict a still-working game session.
func nodeReachable(cm *ClientMux) bool {
	if _, err := MeasureTunnelLatency(cm); err == nil {
		return true
	}
	cm.mu.Lock()
	relay := cm.udpRelay
	cm.mu.Unlock()
	if relay == nil {
		return false
	}
	_, err := MeasureTunnelLatency(relay)
	return err == nil
}

func (g *GUIController) tryStandby(stop <-chan struct{}, active *ClientMux, snapshot *Config) {
	start := snapshot.ActiveIndex
	if start < 0 || start >= len(snapshot.Profiles) {
		start = len(snapshot.Profiles) - 1
	}
	for offset := 1; offset <= len(snapshot.Profiles); offset++ {
		select {
		case <-stop:
			return
		default:
		}
		idx := (start + offset) % len(snapshot.Profiles)
		p := snapshot.Profiles[idx]
		if p.selectedBy(snapshot) || !snapshot.allowStandby(p) {
			continue
		}
		probe, err := g.newProbe(p)
		if err != nil {
			continue
		}
		if _, err := MeasureTunnelLatency(probe); err != nil {
			g.finishProbe(probe)
			continue
		}
		// The active node may have recovered while the other node was probed.
		if nodeReachable(active) {
			g.finishProbe(probe)
			return
		}
		g.mu.Lock()
		delete(g.probes, probe)
		if !g.running || g.cm != active || g.cfg != snapshot {
			GlobalTUN.unpin(probe)
			g.mu.Unlock()
			probe.Close()
			return
		}
		cfg := *snapshot
		cfg.ActiveIndex = idx
		cfg.ServerIP, cfg.MasterKey, cfg.TLSNode = p.ServerIP, p.MasterKey, p.TLSNode
		err = g.installConfigLocked(&cfg, probe)
		g.mu.Unlock()
		if err == nil {
			log.Printf("[Failover] 已切换至通过探活的备用节点 #%d", idx)
		} else {
			log.Printf("[Failover] 切换失败: %v", err)
		}
		return
	}
}
