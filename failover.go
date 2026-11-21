package main

import (
	"log"
	"time"
)

func (g *GUIController) StartAutoFailover(stopCh <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()

		consecutiveFails := 0
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				g.mu.Lock()
				running := g.running
				cm := g.cm
				total := len(g.cfg.Profiles)
				curIdx := g.cfg.ActiveIndex
				g.mu.Unlock()

				if !running || cm == nil || total < 2 {
					consecutiveFails = 0
					continue
				}

				if _, err := MeasureTunnelLatency(cm); err == nil {
					consecutiveFails = 0
					continue
				}

				consecutiveFails++
				log.Printf("[Failover] 当前节点探活失败 (%d/3)", consecutiveFails)
				if consecutiveFails >= 3 {
					nextIdx := (curIdx + 1) % total
					log.Printf("[Failover] 触发自动容灾，切换至备用节点 #%d...", nextIdx)
					_ = g.SwitchProfile(nextIdx)
					consecutiveFails = 0
				}
			}
		}
	}()
}
