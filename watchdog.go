package main

import "time"

func (g *GUIController) StartTunnelWatchdog(stopCh <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		last := time.Now().Unix()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				now := time.Now().Unix()
				GlobalTUN.refreshNetwork()
				g.mu.Lock()
				cm, running := g.cm, g.running
				g.mu.Unlock()
				if cm != nil && running {
					if now-last > 15 {
						cm.startClockSync()
						if !nodeReachable(cm) {
							if cm.entry != nil {
								cm.entry.Reset()
							}
							cm.Reset()
						}
					}
					// Test and remove the same closed session under one lock.
					// A delayed tick alone says nothing about transport health.
					cm.mu.Lock()
					if cm.sess != nil && cm.sess.IsClosed() {
						cm.closeLocked()
					}
					cm.mu.Unlock()
				}
				last = now
			}
		}
	}()
}
