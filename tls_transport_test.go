package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTLSOnlyTransport(t *testing.T) {
	dir := t.TempDir()
	key, err := serverIdentity(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	again, err := serverIdentity(dir, true)
	if err != nil || !bytes.Equal(again.Certificate[0], key.Certificate[0]) {
		t.Fatal("existing TLS identity changed", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	master := strings.Repeat("ab", 32)
	users, err := NewMultiUserEngine([]UserEntry{{Name: "test", KeyHex: master}})
	if err != nil {
		t.Fatal(err)
	}
	go serveTLS(ln, key, users)
	node := TLSNode{TLSPort: ln.Addr().(*net.TCPAddr).Port, TLSPin: fmt.Sprintf("%x", sha256.Sum256(key.Certificate[0]))}
	client, err := newNodeMux("127.0.0.1", master, node)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err = MeasureTunnelLatency(client); err != nil {
		t.Fatal(err)
	}
	if got := users.users[0]; got.rxByte.Load() == 0 || got.txByte.Load() == 0 {
		t.Fatal("missing traffic counts")
	}
	for _, tc := range []struct{ name, master, pin string }{
		{"wrong server fingerprint", master, strings.Repeat("00", 32)},
		{"unknown user", strings.Repeat("cd", 32), node.TLSPin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad, err := newNodeMux("127.0.0.1", tc.master, TLSNode{TLSPort: node.TLSPort, TLSPin: tc.pin})
			if err != nil {
				t.Fatal(err)
			}
			defer bad.Close()
			if _, err := bad.OpenStream(); err == nil || bad.udpPath != nil {
				t.Fatal("unexpected connection or UDP fallback")
			}
		})
	}
	if err = users.ReplaceUsers(nil); err != nil {
		t.Fatal(err)
	}
	if _, err = MeasureTunnelLatency(client); err == nil {
		t.Fatal("revoked user could still ping")
	}
	if err = os.WriteFile(filepath.Join(dir, tlsIdentityFile), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = serverIdentity(dir, true); err == nil {
		t.Fatal("corrupt identity silently replaced")
	}
}

func TestTLSLinksPreserveTrust(t *testing.T) {
	p := ShareProfile{ServerIP: "::1", MasterKey: strings.Repeat("12", 32), Name: "test", TLSNode: TLSNode{TLSPort: 443, TLSPin: strings.Repeat("34", 32)}}
	link, err := p.encode()
	if err != nil {
		t.Fatal(err)
	}
	q, err := DecodeShareLink(link)
	if err != nil || *q != p || !strings.HasPrefix(link, "cm+tls://") {
		t.Fatal("TLS link lost information", err)
	}
	if _, err = DecodeShareLink(strings.Replace(link, "cm+tls://", "cm://", 1)); err == nil {
		t.Fatal("TLS link accepted as UDP")
	}
	bad := TLSNode{TLSPort: 443}
	if err = bad.validate(); err == nil {
		t.Fatal("missing fingerprint accepted")
	}
	master, _ := hex.DecodeString(p.MasterKey)
	cfg, err := clientTLSConfig(master, p.TLSNode)
	if err != nil || cfg.MinVersion != tls.VersionTLS13 || cfg.VerifyConnection == nil {
		t.Fatal("TLS policy missing")
	}
}

func TestRoutingDomainBoundariesAndTrailingDot(t *testing.T) {
	r := NewRouter([]string{"allow.example."}, []string{"BLOCK.EXAMPLE."})
	for _, target := range []string{"block.example.:80", "sub.block.example:443"} {
		if !r.ShouldBlock(target) {
			t.Fatal(target)
		}
	}
	if !r.ShouldDirect("site.allow.example.:443") || r.ShouldDirect("notallow.example:443") || r.ShouldDirect("example.net:443") {
		t.Fatal("wrong direct decision")
	}
	r.SetGlobalMode(true)
	if r.ShouldDirect("site.allow.example:443") {
		t.Fatal("global mode bypassed tunnel for a public host")
	}
}

func TestDualShareLink(t *testing.T) {
	profile := ShareProfile{ServerIP: "::1", MasterKey: strings.Repeat("21", 32), TLSNode: TLSNode{TLSPort: 443, TLSPin: strings.Repeat("8a", 32), UDPPort: 40000}}
	link, err := profile.encode()
	if err != nil || !strings.HasPrefix(link, "cm+auto://") {
		t.Fatal("encode", err)
	}
	parsed, err := DecodeShareLink(link)
	if err != nil || *parsed != profile {
		t.Fatal("round trip", err)
	}
	for _, scheme := range []string{"cm://", "cm+tls://"} {
		if _, err = DecodeShareLink(strings.Replace(link, "cm+auto://", scheme, 1)); err == nil {
			t.Fatalf("accepted mismatched scheme %s", scheme)
		}
	}
	profile.TLSPin = ""
	if _, err = profile.encode(); err == nil {
		t.Fatal("accepted missing TLS identity")
	}
	profile.TLSPin = strings.Repeat("8a", 32)
	profile.UDPPort = 65536
	if _, err = profile.encode(); err == nil {
		t.Fatal("accepted invalid fixed UDP port")
	}
}
