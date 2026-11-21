package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRealityIdentityAndLink(t *testing.T) {
	dir := t.TempDir()
	r := RealityConfig{Name: "example.com", Target: "127.0.0.1:8443", Listen: ":443"}
	n, e := r.node(dir, true)
	if e != nil {
		t.Fatal(e)
	}
	next, e := r.node(dir, true)
	if e != nil || next != n {
		t.Fatalf("identity changed on repeated creation: %v", e)
	}
	k, _, e := realityKeys(dir, false)
	if e != nil {
		t.Fatal(e)
	}
	key, _, e := n.identity()
	if e != nil || !bytes.Equal(key, k.PublicKey().Bytes()) {
		t.Fatalf("exported public key: %v", e)
	}
	path := filepath.Join(dir, "reality.key")
	info, e := os.Stat(path)
	if e != nil {
		t.Fatal(e)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("key permissions: %v", info.Mode())
	}
	p := ShareProfile{TLSNode: n, ServerIP: "127.0.0.1", MasterKey: strings.Repeat("ab", 32)}
	link, e := p.encode()
	if e != nil || !strings.HasPrefix(link, "cm+reality://") {
		t.Fatal(e)
	}
	got, e := DecodeShareLink(link)
	if e != nil || *got != p {
		t.Fatalf("round trip: %v", e)
	}
	for _, change := range []func(*TLSNode){func(n *TLSNode) { n.RealityKey = "" }, func(n *TLSNode) { n.RealityID = "12" }, func(n *TLSNode) { n.TLSPin = "" }, func(n *TLSNode) { n.UDPPort = 8443 }, func(n *TLSNode) { n.WebName = "example.com" }} {
		bad := n
		change(&bad)
		if bad.validate() == nil {
			t.Fatal("accepted invalid configuration")
		}
	}
	if e := os.WriteFile(path, []byte("broken"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, e := realityKeys(dir, true); e == nil {
		t.Fatal("silently replaced damaged key")
	}
}
