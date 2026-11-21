package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type NodeProfile struct {
	TLSNode
	Name      string `json:"name"`
	ServerIP  string `json:"server_ip"`
	MasterKey string `json:"master_key"`
}

func SaveConfigAtomic(path string, cfg *Config) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".chronomorph-config-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (g *GUIController) SwitchProfile(idx int) error {
	g.mu.Lock()
	if idx < 0 || idx >= len(g.cfg.Profiles) {
		g.mu.Unlock()
		return os.ErrInvalid
	}
	defer g.mu.Unlock()
	p := g.cfg.Profiles[idx]
	cfg := *g.cfg
	cfg.ActiveIndex = idx
	cfg.ServerIP, cfg.MasterKey, cfg.TLSNode = p.ServerIP, p.MasterKey, p.TLSNode
	return g.useConfigLocked(&cfg)
}

// Retain the local listener and the OS proxy settings during a profile change.
func (g *GUIController) useConfigLocked(cfg *Config) error {
	changed := g.cfg.ServerIP != cfg.ServerIP || g.cfg.MasterKey != cfg.MasterKey || g.cfg.TLSNode != cfg.TLSNode
	var next *ClientMux
	if changed && g.running {
		if GlobalTUN.IsActive() {
			return fmt.Errorf("切换节点前请先关闭 TUN")
		}
		var err error
		next, err = newNodeMux(cfg.ServerIP, cfg.MasterKey, cfg.TLSNode)
		if err != nil {
			return err
		}
	}
	if err := SaveConfigAtomic(g.cfgPath, cfg); err != nil {
		if next != nil {
			next.Close()
		}
		return err
	}
	g.cfg = cfg
	if next != nil {
		old := g.cm
		g.cm = next
		old.Close()
		if next.tlsConfig == nil {
			go SyncTimeOffset()
		}
	}
	return nil
}
