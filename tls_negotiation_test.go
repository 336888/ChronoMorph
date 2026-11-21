package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
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

// Older binaries accept exactly byte 1 after authentication, including the
// first TLS client version, which offered no ALPN and used multiplexing v1.
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
	key := cfg.Certificates[0].PrivateKey.(ed25519.PrivateKey)
	legacyCert, err := issueCertificate(key, x509.ExtKeyUsageClientAuth, "ChronoMorph")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Certificates = []tls.Certificate{{Certificate: [][]byte{legacyCert}, PrivateKey: key}}
	for _, alpn := range []string{"", tlsMuxV2, tlsMuxV2Ready} {
		client := cfg.Clone()
		if alpn != "" {
			client.NextProtos = []string{alpn}
		}
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", ln.Addr().String(), client)
		if err != nil {
			t.Fatal(err)
		}
		var ack [1]byte
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, err = io.ReadFull(conn, ack[:])
		selected := conn.ConnectionState().NegotiatedProtocol
		_ = conn.Close()
		if err != nil || ack[0] != 1 || selected != alpn {
			t.Fatalf("legacy %q selected %q, ack %d, err %v", alpn, selected, ack[0], err)
		}
	}
}
