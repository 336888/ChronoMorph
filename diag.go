package main

import (
	"fmt"
	"log"
	"runtime"
	"strings"
	"time"
)

func (cm *ClientMux) logProxyFailure(err error) {
	now := time.Now()
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.lastFailure, cm.lastFailureAt = err.Error(), now
	if now.Sub(cm.lastLogged) >= 5*time.Second {
		log.Printf("[连接] %v", err)
		cm.lastLogged = now
	}
}

func (g *GUIController) BuildDiagReport() string {
	g.mu.Lock()
	ip := g.cfg.ServerIP
	listen := g.cfg.Listen
	running := g.running
	cm := g.cm
	tlsPort := g.cfg.TLSPort
	sysProxy := g.sysProxyOn
	g.mu.Unlock()

	var sb strings.Builder
	sb.WriteString("=== ChronoMorph Diagnostic Report ===\n")
	sb.WriteString(fmt.Sprintf("Version      : %s\n", VersionString()))
	sb.WriteString(fmt.Sprintf("OS / Arch    : %s / %s\n", runtime.GOOS, runtime.GOARCH))
	sb.WriteString(fmt.Sprintf("Time (UTC)   : %s\n", time.Now().UTC().Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("Calibrated   : %s (Epoch %d)\n", CalibratedNow().UTC().Format(time.RFC3339), CurrentEpoch()))
	sb.WriteString(fmt.Sprintf("Server IP    : %s\n", ip))
	sb.WriteString(fmt.Sprintf("Listen       : %s\n", listen))
	sb.WriteString(fmt.Sprintf("Tunnel Active: %v | SysProxy: %v | TUN: %v\n", running, sysProxy, GlobalTUN.IsActive()))
	if tlsPort != 0 {
		fmt.Fprintf(&sb, "Transport    : TLS/TCP %d\n", tlsPort)
	} else {
		fmt.Fprintf(&sb, "Transport    : UDP (cm://), current port %d\n", LiveStats.activePort.Load())
	}
	if cm != nil {
		cm.mu.Lock()
		if !cm.lastFailureAt.IsZero() {
			fmt.Fprintf(&sb, "Last failure : %s %s\n", cm.lastFailureAt.UTC().Format(time.RFC3339), cm.lastFailure)
		}
		cm.mu.Unlock()
	}
	sb.WriteString(fmt.Sprintf("Speed        : TX %d KB/s | RX %d KB/s\n", LiveStats.txSpeedBps.Load()/1024, LiveStats.rxSpeedBps.Load()/1024))
	sb.WriteString("\n--- Recent Logs (Last 20) ---\n")

	logs := AppLogs.Snapshot()
	if len(logs) > 20 {
		logs = logs[len(logs)-20:]
	}
	for _, line := range logs {
		sb.WriteString(line + "\n")
	}
	return sb.String()
}
