package main

import (
	"encoding/json"
	"os"
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
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (g *GUIController) SwitchProfile(idx int) error {
	g.mu.Lock()
	if idx < 0 || idx >= len(g.cfg.Profiles) {
		g.mu.Unlock()
		return os.ErrInvalid
	}
	p := g.cfg.Profiles[idx]
	g.cfg.ActiveIndex = idx
	g.cfg.ServerIP = p.ServerIP
	g.cfg.MasterKey = p.MasterKey
	wasRunning := g.running
	_ = SaveConfigAtomic(g.cfgPath, g.cfg)
	g.mu.Unlock()

	if wasRunning {
		g.StopTunnel()
		return g.StartTunnel()
	}
	return nil
}
