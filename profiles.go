package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

type NodeProfile struct {
	TLSNode
	Name      string `json:"name"`
	ServerIP  string `json:"server_ip"`
	MasterKey string `json:"master_key"`
}

// Apply the local transport preference to a copy of the imported endpoint.
func (cfg *Config) nodeMux(ip, key string, node TLSNode) (*ClientMux, error) {
	if cfg.ChainEntry == "" {
		return cfg.singleNodeMux(ip, key, node)
	}
	entry, ok := cfg.entryProfile()
	if !ok {
		return nil, fmt.Errorf("前置 CM 节点已失效，请在网络规则中重新选择")
	}
	if net.ParseIP(entry.ServerIP).Equal(net.ParseIP(ip)) && entry.TLSPort == node.TLSPort && entry.UDPPort == node.UDPPort {
		return nil, fmt.Errorf("前置和出口必须是两个不同的 CM 入口")
	}
	if node.TLSPort == 0 {
		return nil, fmt.Errorf("出口节点需要支持 TLS、HTTPS 或 REALITY")
	}
	node.UDPPort = 0 // Only the entry is dialled physically, including the game lane.
	exit, err := newNodeMux(ip, key, node)
	if err != nil {
		return nil, err
	}
	exit.entry, err = cfg.singleNodeMux(entry.ServerIP, entry.MasterKey, entry.TLSNode)
	if err != nil {
		return nil, err
	}
	exit.ownsEntry = true
	return exit, nil
}

func (cfg *Config) singleNodeMux(ip, key string, node TLSNode) (*ClientMux, error) {
	if cfg.TLSOnly {
		if node.TLSPort == 0 {
			return nil, fmt.Errorf("仅 TLS 模式需要 HTTPS 或带证书指纹的 TLS 节点")
		}
		node.UDPPort = 0
	}
	return newNodeMux(ip, key, node)
}

func (p NodeProfile) entryID() string {
	// A rename or refreshed certificate should not break the selected route.
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s|%s|%s|%s", p.ServerIP, p.TLSPort, p.scheme(), p.WebName, p.RealityName, p.MasterKey))))
}
func (cfg *Config) entryProfile() (NodeProfile, bool) {
	if cfg.ChainEntry == "" {
		return NodeProfile{}, false
	}
	for _, p := range cfg.Profiles {
		if p.entryID() == cfg.ChainEntry {
			return p, true
		}
	}
	return NodeProfile{}, false
}
func (cm *ClientMux) physicalIP() net.IP {
	if cm.entry != nil {
		return cm.entry.serverIP
	}
	return cm.serverIP
}

func (g *GUIController) setTLSOnly(enabled bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cfg.TLSOnly == enabled {
		return nil
	}
	cfg := *g.cfg
	cfg.TLSOnly = enabled
	if err := g.useConfigLocked(&cfg); err != nil {
		return err
	}
	if enabled {
		for probe := range g.probes {
			if probe.packetEngine != nil {
				probe.Close()
			}
		}
	}
	return nil
}

func (cfg *Config) allowStandby(p NodeProfile) bool {
	// The configured first hop is retained by cfg.nodeMux for chained exits.
	return !(cfg.LockEntryType || cfg.TLSPort != 0 && cfg.UDPPort == 0 || cfg.RealityName != "" || cfg.WebName != "") || p.scheme() == cfg.TLSNode.scheme()
}
func (g *GUIController) toggleLockEntry() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	cfg := *g.cfg
	cfg.LockEntryType = !cfg.LockEntryType
	return g.installConfigLocked(&cfg, nil)
}

func (p NodeProfile) selectedBy(cfg *Config) bool {
	return p.ServerIP == cfg.ServerIP && p.MasterKey == cfg.MasterKey && p.TLSNode == cfg.TLSNode
}

// Older configurations may store the active node only at the top level.
func (cfg *Config) normalizeSavedNodes() {
	if cfg.ServerIP == "" || cfg.MasterKey == "" {
		return
	}
	if i := cfg.ActiveIndex; i >= 0 && i < len(cfg.Profiles) && cfg.Profiles[i].selectedBy(cfg) {
		return
	}
	for i, p := range cfg.Profiles {
		if p.selectedBy(cfg) {
			cfg.ActiveIndex = i
			return
		}
	}
	cfg.ActiveIndex = len(cfg.Profiles)
	cfg.Profiles = append(cfg.Profiles, NodeProfile{Name: cfg.ServerIP, ServerIP: cfg.ServerIP, MasterKey: cfg.MasterKey, TLSNode: cfg.TLSNode})
}

func (g *GUIController) deleteProfile(idx int, selected NodeProfile) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	cfg := *g.cfg
	if idx < 0 || idx >= len(cfg.Profiles) || cfg.Profiles[idx] != selected {
		return fmt.Errorf("节点列表已变更，请重新选择")
	}
	if selected.entryID() == cfg.ChainEntry {
		return fmt.Errorf("该节点正在用作前置，请先关闭链式代理或更换前置")
	}
	nodes := make([]NodeProfile, 0, len(cfg.Profiles)-1)
	nodes = append(nodes, cfg.Profiles[:idx]...)
	cfg.Profiles = append(nodes, cfg.Profiles[idx+1:]...)
	if len(cfg.Profiles) == 0 {
		cfg.ServerIP, cfg.MasterKey, cfg.TLSNode, cfg.ActiveIndex = "", "", TLSNode{}, -1
	} else if idx == cfg.ActiveIndex {
		cfg.ActiveIndex = min(idx, len(cfg.Profiles)-1)
		next := cfg.Profiles[cfg.ActiveIndex]
		cfg.ServerIP, cfg.MasterKey, cfg.TLSNode = next.ServerIP, next.MasterKey, next.TLSNode
	} else if idx < cfg.ActiveIndex {
		cfg.ActiveIndex--
	}
	// Stop the system proxy and TUN before removing their active endpoint.
	// If persistence fails, the original node is still available for restart.
	if idx == g.cfg.ActiveIndex && g.running {
		if err := g.stopTunnelLocked(); err != nil {
			return err
		}
	}
	return g.useConfigLocked(&cfg)
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
	oldEntry, _ := g.cfg.entryProfile()
	newEntry, _ := cfg.entryProfile()
	changed := oldEntry != newEntry || cfg.ChainEntry != g.cfg.ChainEntry || g.cfg.ServerIP != cfg.ServerIP || g.cfg.MasterKey != cfg.MasterKey || g.cfg.TLSNode != cfg.TLSNode || g.cfg.TLSOnly != cfg.TLSOnly
	var next *ClientMux
	if changed && cfg.ChainEntry != "" && cfg.ServerIP != "" && !g.running {
		probe, err := cfg.nodeMux(cfg.ServerIP, cfg.MasterKey, cfg.TLSNode)
		if err != nil {
			return err
		}
		probe.Close()
	}
	if changed && g.running {
		var err error
		next, err = cfg.nodeMux(cfg.ServerIP, cfg.MasterKey, cfg.TLSNode)
		if err != nil {
			return err
		}
	}
	return g.installConfigLocked(cfg, next)
}

// next, when supplied, is owned by this method, including on error.
func (g *GUIController) installConfigLocked(cfg *Config, next *ClientMux) error {
	if next != nil {
		if err := GlobalTUN.pin(next); err != nil {
			GlobalTUN.unpin(next)
			next.Close()
			return err
		}
	}
	if err := SaveConfigAtomic(g.cfgPath, cfg); err != nil {
		if next != nil {
			GlobalTUN.unpin(next)
			next.Close()
		}
		return err
	}
	g.cfg = cfg
	if next != nil {
		old := g.cm
		g.cm = next
		GlobalTUN.adopt(old, next)
		old.Close()
		next.startClockSync()
	}
	return nil
}
