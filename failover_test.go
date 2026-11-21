package main

import (
	"crypto/sha256"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
)

func TestFailoverRequiresLiveStandby(t *testing.T) {
	key := strings.Repeat("ab", 32)
	users, err := NewMultiUserEngine([]UserEntry{{KeyHex: key}})
	if err != nil {
		t.Fatal(err)
	}
	cert, err := serverIdentity(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go serveTLS(ln, cert, users)
	node := TLSNode{TLSPort: ln.Addr().(*net.TCPAddr).Port, TLSPin: fmt.Sprintf("%x", sha256.Sum256(cert.Certificate[0]))}
	active, err := NewClientMux("127.0.0.1", key)
	if err != nil {
		t.Fatal(err)
	}
	active.Close()
	cfg := &Config{ServerIP: "127.0.0.1", MasterKey: key, Profiles: []NodeProfile{
		{ServerIP: "127.0.0.1", MasterKey: key},
		{ServerIP: "127.0.0.1", MasterKey: strings.Repeat("ef", 32), TLSNode: node},
	}}
	g := GUIController{running: true, cm: active, cfg: cfg, cfgPath: filepath.Join(t.TempDir(), "client.json")}
	g.tryStandby(nil, active, cfg)
	if g.cm != active || g.cfg.ActiveIndex != 0 {
		t.Fatal("switched to a standby that failed authentication")
	}
	cfg.Profiles = append(cfg.Profiles, NodeProfile{ServerIP: "127.0.0.1", MasterKey: key, TLSNode: node})
	g.tryStandby(nil, active, cfg)
	if g.cm == active || g.cfg.ActiveIndex != 2 {
		t.Fatal("did not select the authenticated standby")
	}
	defer g.cm.Close()
	if g.cm.sess == nil || g.cm.sess.IsClosed() {
		t.Fatal("discarded the confirmed connection during activation")
	}
}
