//go:build !windows

package main

import "sync"

type GUIController struct {
	mu         sync.Mutex
	cfgPath    string
	cfg        *Config
	cm         *ClientMux
	probes     map[*ClientMux]struct{}
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

func (*stubTUN) pin(*ClientMux) error         { return nil }
func (*stubTUN) unpin(*ClientMux)             {}
func (*stubTUN) adopt(*ClientMux, *ClientMux) {}
func (*stubTUN) refreshNetwork()              {}
func (*stubTUN) diagnostics() string          { return "TUN 未开启" }

var GlobalTUN = &stubTUN{}

func RunWindowsGUI(cfgPath string) {}
