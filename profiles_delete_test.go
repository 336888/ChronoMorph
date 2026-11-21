package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeleteSavedProfiles(t *testing.T) {
	a := NodeProfile{ServerIP: "127.0.0.1", MasterKey: strings.Repeat("ab", 32), Name: "A"}
	b := NodeProfile{ServerIP: "127.0.0.2", MasterKey: strings.Repeat("cd", 32), Name: "B", TLSNode: TLSNode{TLSPort: 443, TLSPin: strings.Repeat("ab", 32)}}
	c := NodeProfile{ServerIP: "127.0.0.3", MasterKey: strings.Repeat("ef", 32), Name: "C"}
	g := GUIController{cfgPath: filepath.Join(t.TempDir(), "client.json"), cfg: &Config{ServerIP: b.ServerIP, MasterKey: b.MasterKey, TLSNode: b.TLSNode, ActiveIndex: 1, Profiles: []NodeProfile{a, b, c}, Listen: "127.0.0.1:1080"}, running: true, cm: &ClientMux{}}
	active := g.cm
	if err := g.deleteProfile(0, b); err == nil {
		t.Fatal("accepted stale selection")
	}
	if err := g.deleteProfile(0, a); err != nil {
		t.Fatal(err)
	}
	if g.cfg.ActiveIndex != 0 || !b.selectedBy(g.cfg) || !g.running || g.cm != active {
		t.Fatal("removing a standby interrupted the active node")
	}
	if err := g.deleteProfile(0, b); err != nil {
		t.Fatal(err)
	}
	if !c.selectedBy(g.cfg) || g.running || g.cm != nil {
		t.Fatal("active node not removed cleanly")
	}
	if err := g.deleteProfile(0, c); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(g.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var saved Config
	if err = json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	saved.normalizeSavedNodes()
	if len(saved.Profiles) != 0 || saved.ServerIP != "" || saved.MasterKey != "" || saved.TLSNode != (TLSNode{}) || saved.Listen != "127.0.0.1:1080" {
		t.Fatal("deleted node or stale credentials restored on reload")
	}
}

func TestNormalizeLegacyNodeAndSaveFailure(t *testing.T) {
	cfg := &Config{ServerIP: "127.0.0.1", MasterKey: strings.Repeat("ab", 32)}
	cfg.normalizeSavedNodes()
	cfg.normalizeSavedNodes()
	if len(cfg.Profiles) != 1 || cfg.ActiveIndex != 0 {
		t.Fatal("legacy node missing or duplicated")
	}
	g := GUIController{cfgPath: filepath.Join(t.TempDir(), "missing", "client.json"), cfg: cfg}
	if err := g.deleteProfile(0, cfg.Profiles[0]); err == nil || g.cfg != cfg || len(cfg.Profiles) != 1 {
		t.Fatal("failed save removed the original node")
	}
}
