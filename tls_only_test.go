package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTLSOnlyAppliesToActiveAndTemporaryNodes(t *testing.T) {
	node := NodeProfile{ServerIP: "127.0.0.1", MasterKey: strings.Repeat("12", 32), TLSNode: TLSNode{TLSPort: 8443, TLSPin: strings.Repeat("11", 32), UDPPort: 41000}}
	legacy := node
	legacy.TLSNode = TLSNode{}
	cfg := &Config{ServerIP: node.ServerIP, MasterKey: node.MasterKey, TLSNode: node.TLSNode, Profiles: []NodeProfile{node, legacy}}
	active, err := cfg.nodeMux(node.ServerIP, node.MasterKey, node.TLSNode)
	if err != nil {
		t.Fatal(err)
	}
	g := &GUIController{cfgPath: filepath.Join(t.TempDir(), "client.json"), cfg: cfg, cm: active, running: true}
	probe, err := g.newProbe(node)
	if err != nil {
		t.Fatal(err)
	}
	defer g.finishProbe(probe)
	if err = g.setTLSOnly(true); err != nil {
		t.Fatal(err)
	}
	defer g.cm.Close()
	if g.cm.packetEngine != nil || g.cm.clockDone != nil || g.cm.tlsNode.UDPPort != 0 || g.cm.tlsConfig == nil {
		t.Fatal("strict active mux can use UDP or started its clock worker")
	}
	if _, err = active.OpenStream(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("old connection: %v", err)
	}
	if _, err = probe.OpenStream(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("old probe: %v", err)
	}
	if _, err = g.newProbe(legacy); err == nil {
		t.Fatal("legacy probe accepted")
	}
	newProbe, err := g.newProbe(node)
	if err != nil {
		t.Fatal(err)
	}
	defer g.finishProbe(newProbe)
	if newProbe.packetEngine != nil || newProbe.tlsNode.TLSPin != node.TLSPin {
		t.Fatal("unexpected probe transport or trust")
	}
	current := g.cm
	if err = g.SwitchProfile(1); err == nil || current != g.cm || !g.cfg.TLSOnly {
		t.Fatal("UDP-only switch changed active mode")
	}
	data, err := os.ReadFile(g.cfgPath)
	var saved Config
	if err != nil || json.Unmarshal(data, &saved) != nil || !saved.TLSOnly || saved.TLSNode != node.TLSNode || saved.Profiles[0] != node {
		t.Fatal("preference or original link lost")
	}
	if err = g.setTLSOnly(false); err != nil {
		t.Fatal(err)
	}
	defer g.cm.Close()
	if g.cm.packetEngine == nil || g.cm.tlsNode != node.TLSNode {
		t.Fatal("turning off TLS-only failed to restore original node")
	}
}

func TestTimeSyncCancellation(t *testing.T) {
	silent, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := querySNTP(ctx, silent.LocalAddr().String()); done <- err }()
	if err = silent.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, err = silent.ReadFromUDP(make([]byte, 48)); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("canceled clock request succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("clock request still waiting for a UDP reply")
	}
}
