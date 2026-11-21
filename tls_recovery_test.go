package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Leave the old TCP connection open while dropping its traffic in both
// directions. A new connection to the same pinned endpoint still works.
type interruptedConn struct {
	net.Conn
	silent atomic.Bool
}

func (c *interruptedConn) Read(p []byte) (int, error) {
	for {
		n, err := c.Conn.Read(p)
		if !c.silent.Load() || err != nil {
			return n, err
		}
	}
}

func (c *interruptedConn) Write(p []byte) (int, error) {
	if c.silent.Load() {
		return len(p), nil
	}
	return c.Conn.Write(p)
}

func TestTLSOnlyReconnectsOnSilentSession(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	cert, err := serverIdentity(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("af", 32)
	users, err := NewMultiUserEngine([]UserEntry{{KeyHex: key}})
	if err != nil {
		t.Fatal(err)
	}

	first := make(chan *interruptedConn, 1)
	var sessions atomic.Int32
	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			conn := &interruptedConn{Conn: raw}
			if sessions.Add(1) == 1 {
				first <- conn
			}
			go acceptTLS(conn, cert, users)
		}
	}()
	cfg := Config{TLSOnly: true}
	node := TLSNode{TLSPort: ln.Addr().(*net.TCPAddr).Port, TLSPin: fmt.Sprintf("%x", sha256.Sum256(cert.Certificate[0])), UDPPort: 53}
	client, err := cfg.nodeMux("127.0.0.1", key, node)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := MeasureTunnelLatency(client); err != nil {
		t.Fatal(err)
	}
	origin, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer origin.Close()
	(<-first).silent.Store(true)
	start := time.Now()
	stream, err := dialWithRouting(origin.Addr().String(), client, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("request on silent pinned path ended after %s: %v (sessions=%d, transport=%s, sessionClosed=%v)", elapsed, err, sessions.Load(), client.transportName(), client.sess == nil || client.sess.IsClosed())
	}
	defer stream.Close()
	origin.SetDeadline(time.Now().Add(2 * time.Second))
	peer, err := origin.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	peer.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := stream.Write([]byte("test")); err != nil {
		t.Fatal(err)
	}
	var b [4]byte
	if _, err := io.ReadFull(peer, b[:]); err != nil || string(b[:]) != "test" {
		t.Fatalf("payload %q: %v", b, err)
	}
	if elapsed > 8*time.Second || sessions.Load() != 2 || client.transportName() != "TLS/TCP" || client.packetEngine != nil {
		t.Fatalf("recovery took %s with %d sessions on %s", elapsed, sessions.Load(), client.transportName())
	}
	t.Logf("new request recovered on a second authenticated TLS connection in %s", elapsed.Round(time.Millisecond))
}
