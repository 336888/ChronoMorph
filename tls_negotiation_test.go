package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

type recordingConn struct {
	net.Conn
	read bytes.Buffer
}

func (c *recordingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.read.Write(p[:n])
	return n, err
}

// Inspect exactly what an ordinary observer can read before the server replies.
func TestTLSClientHelloOmitsApplicationMetadata(t *testing.T) {
	for _, udpPort := range []int{0, 40000} {
		t.Run(fmt.Sprintf("udp-port-%d", udpPort), func(t *testing.T) {
			ln, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			node := TLSNode{UDPPort: udpPort, TLSPort: ln.Addr().(*net.TCPAddr).Port, TLSPin: strings.Repeat("11", 32)}
			cm, err := newNodeMux("127.0.0.1", strings.Repeat("ab", 32), node)
			if err != nil {
				t.Fatal(err)
			}
			defer cm.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := cm.dialTLS(ctx); done <- err }()
			ln.(*net.TCPListener).SetDeadline(time.Now().Add(4 * time.Second))
			raw, err := ln.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			_ = raw.SetDeadline(time.Now().Add(4 * time.Second))
			wire := &recordingConn{Conn: raw}
			observed := false
			server := tls.Server(wire, &tls.Config{MinVersion: tls.VersionTLS13, GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
				observed = true
				if len(h.SupportedProtos) != 0 || bytes.Contains(bytes.ToLower(wire.read.Bytes()), []byte("chronomorph")) {
					t.Errorf("client advertised application metadata in the clear, ALPN %v", h.SupportedProtos)
				}
				return nil, errors.New("capture complete")
			}})
			_ = server.Handshake()
			if !observed {
				t.Fatal("TLS ClientHello not captured")
			}
			if <-done == nil {
				t.Fatal("unauthenticated connection unexpectedly succeeded")
			}
		})
	}
}

// Even peers offering a retired protocol name must not receive one back.
// Legacy clients use the ordinary v1 framing when no protocol is selected.
func TestTLSLegacyAcknowledgement(t *testing.T) {
	cert, err := serverIdentity(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	master := bytes.Repeat([]byte{0xab}, 32)
	users, err := NewMultiUserEngine([]UserEntry{{Name: "test", KeyHex: fmt.Sprintf("%x", master)}})
	if err != nil {
		t.Fatal(err)
	}
	go serveTLS(ln, cert, users)
	node := TLSNode{TLSPort: ln.Addr().(*net.TCPAddr).Port, TLSPin: fmt.Sprintf("%x", sha256.Sum256(cert.Certificate[0]))}
	cfg, err := clientTLSConfig(master, node)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(bytes.ToLower(cfg.Certificates[0].Certificate[0]), []byte("chronomorph")) {
		t.Fatal("new client certificate contains the project name")
	}
	key := cfg.Certificates[0].PrivateKey.(ed25519.PrivateKey)
	for _, tc := range []struct {
		name, cn, offer string
		authenticated   bool
		wantAck         byte
	}{
		{"current", "", "", true, tlsAckReady},
		{"v1.2.5", legacyReadyClientName, "", true, tlsAckReady},
		{"first TLS client", "ChronoMorph", "", true, tlsAckLegacy},
		{"retired smux", "ChronoMorph", "chronomorph-smux/2", true, tlsAckLegacy},
		{"retired ready", "ChronoMorph", "chronomorph-smux/2-ready", true, tlsAckLegacy},
		{"no client credential", "", "chronomorph-smux/2-ready", false, 0},
		{"ordinary ALPN", "", "h2", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := cfg.Clone()
			if tc.cn != "" {
				old, err := x509.ParseCertificate(cfg.Certificates[0].Certificate[0])
				if err != nil {
					t.Fatal(err)
				}
				old.RawSubject = nil
				old.Subject = pkix.Name{CommonName: tc.cn}
				der, err := x509.CreateCertificate(rand.Reader, old, old, key.Public(), key)
				if err != nil {
					t.Fatal(err)
				}
				client.Certificates = []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}
			}
			if !tc.authenticated {
				client.Certificates = nil
			}
			if tc.offer != "" {
				client.NextProtos = []string{tc.offer}
			}
			conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", ln.Addr().String(), client)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if selected := conn.ConnectionState().NegotiatedProtocol; selected != "" {
				t.Fatalf("server selected %q", selected)
			}
			var ack [1]byte
			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			n, err := io.ReadFull(conn, ack[:])
			if !tc.authenticated {
				if n != 0 || err == nil || !strings.Contains(err.Error(), "certificate required") {
					t.Fatalf("expected client certificate rejection, n=%d err=%v", n, err)
				}
			} else if err != nil || ack[0] != tc.wantAck {
				t.Fatalf("ack=%d, want=%d, err=%v", ack[0], tc.wantAck, err)
			}
		})
	}
}
