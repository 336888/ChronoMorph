//go:build !windows

package main

import "sync"

type GUIController struct {
	mu         sync.Mutex
	cfgPath    string
	cfg        *Config
	cm         *ClientMux
	router     *Router
	running    bool
	sysProxyOn bool
}

func (g *GUIController) StartTunnel() error { return nil }
func (g *GUIController) stopTunnelLocked() error {
	if g.cm != nil {
		g.cm.Close()
		g.cm = nil
	}
	g.running = false
	return nil
}
func restoreSystemProxy() error { return nil }

type stubTUN struct{}

func (s *stubTUN) IsActive() bool { return false }

var GlobalTUN = &stubTUN{}

func RunWindowsGUI(cfgPath string) {}
