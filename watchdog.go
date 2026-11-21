package main

import (
	"log"
	"time"
)

func (g *GUIController) StartTunnelWatchdog(stopCh <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		lastWall := time.Now()
		failCount := 0

		for {
			select {
			case <-stopCh:
				return
			case now := <-ticker.C:
				if now.Sub(lastWall) > 15*time.Second {
					log.Println("[Watchdog] 检测到系统休眠唤醒，重新校准 NTP 并重建隧道...")
					g.mu.Lock()
					if g.running && g.cm != nil && g.cm.tlsConfig == nil {
						go SyncTimeOffset()
					}
					g.mu.Unlock()
					g.resetActiveSession()
					failCount = 0
					lastWall = now
					continue
				}
				lastWall = now

				g.mu.Lock()
				running := g.running
				cm := g.cm
				g.mu.Unlock()

				if !running || cm == nil {
					failCount = 0
					continue
				}

				cm.mu.Lock()
				closed := cm.sess != nil && cm.sess.IsClosed()
				cm.mu.Unlock()

				if closed {
					failCount++
					if failCount >= 2 {
						log.Println("[Watchdog] 检测到会话断开，自动重建连接...")
						g.resetActiveSession()
						failCount = 0
					}
				} else {
					failCount = 0
				}
			}
		}
	}()
}

func (g *GUIController) resetActiveSession() {
	g.mu.Lock()
	cm := g.cm
	g.mu.Unlock()
	if cm == nil {
		return
	}
	cm.Reset()
}
