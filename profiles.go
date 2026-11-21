package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type NodeProfile struct {
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
	p := g.cfg.Profiles[idx]
	cfg := *g.cfg
	cfg.ActiveIndex = idx
	cfg.ServerIP = p.ServerIP
	cfg.MasterKey = p.MasterKey
	if err := SaveConfigAtomic(g.cfgPath, &cfg); err != nil {
		g.mu.Unlock()
		return err
	}
	g.cfg = &cfg
	wasRunning := g.running
	g.mu.Unlock()

	if wasRunning {
		return g.restartTunnel()
	}
	return nil
}
