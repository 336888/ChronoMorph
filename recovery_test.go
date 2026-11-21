package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/xtaci/smux"
)

func TestClosedClientCannotReconnect(t *testing.T) {
	cm, err := NewClientMux("127.0.0.1", strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	cm.Reset()
	cm.Close()
	cm.Reset()
	if _, err = cm.OpenStream(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed client reopened: %v", err)
	}
}

func TestReloadPreservesLiveUser(t *testing.T) {
	cfg := Config{MasterKey: strings.Repeat("ab", 32)}
	engine, err := NewMultiUserEngine(cfg.serverUsers())
	if err != nil {
		t.Fatal(err)
	}
	before := engine.users[0]
	before.traffic.add(50, 0)
	before.replay.CheckAndUpdate(7, 210000, 12)
	if err = engine.ReplaceUsers(cfg.serverUsers()); err != nil {
		t.Fatal(err)
	}
	if engine.users[0] != before || before.traffic.totals(trafficMonth(time.Now()))[0] != 50 || before.replay.CheckAndUpdate(7, 210000, 12) {
		t.Fatal("live state lost on reload")
	}
	if err = engine.ReplaceUsers([]UserEntry{{KeyHex: "invalid"}}); err == nil || before.retired.Load() {
		t.Fatal("invalid reload changed current user")
	}
	if err = engine.ReplaceUsers(nil); err != nil || !before.retired.Load() {
		t.Fatal("removed peer still active")
	}
}

func TestSocksRejectsUnsupportedMethods(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	client.SetDeadline(time.Now().Add(time.Second))
	go handleMixedProxy(server, nil, NewRouter(nil, nil))
	if _, err := client.Write([]byte{5, 1, 2}); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(client, reply); err != nil || !bytes.Equal(reply, []byte{5, 255}) {
		t.Fatalf("reply %v, err %v", reply, err)
	}
}

func TestShortUDPDatagrams(t *testing.T) {
	target, _, ok := unpackSocks5UDP([]byte{0, 0, 0, 3, 1, 'a', 0, 53})
	if !ok || target != "a:53" {
		t.Fatalf("short domain datagram: %q", target)
	}
	for _, pkt := range [][]byte{{0, 0, 0, 1}, {0, 0, 0, 3}, {0, 0, 1, 1, 0, 0, 0, 0, 0, 53}, {0, 1, 0, 1, 0, 0, 0, 0, 0, 53}} {
		if _, _, ok := unpackSocks5UDP(pkt); ok {
			t.Fatalf("accepted invalid packet %x", pkt)
		}
	}
}

func recoveryTLSNode(t *testing.T, dual bool) (*ClientMux, net.Listener, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	cert, err := serverIdentity(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("a1", 32)
	users, err := NewMultiUserEngine([]UserEntry{{KeyHex: key}})
	if err != nil {
		t.Fatal(err)
	}
	node := TLSNode{TLSPort: ln.Addr().(*net.TCPAddr).Port, TLSPin: fmt.Sprintf("%x", sha256.Sum256(cert.Certificate[0]))}
	if dual {
		silent, err := net.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { silent.Close() })
		node.UDPPort = silent.LocalAddr().(*net.UDPAddr).Port
	}
	cm, err := newNodeMux("127.0.0.1", key, node)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cm.Close)
	return cm, ln, func() { go serveTLS(ln, cert, users) }
}

func TestInterruptPendingHandshake(t *testing.T) {
	for _, tc := range []struct {
		name       string
		stop, dual bool
	}{
		{"close", true, false}, {"reset", false, false},
		{"close-dual", true, true}, {"reset-dual", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cm, ln, serve := recoveryTLSNode(t, tc.dual)
			result := make(chan error, 1)
			go func() { _, err := cm.OpenStream(); result <- err }()
			silent, err := ln.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer silent.Close()
			interrupted := make(chan struct{})
			go func() {
				if tc.stop {
					cm.Close()
				} else {
					cm.Reset()
				}
				close(interrupted)
			}()
			select {
			case <-interrupted:
			case <-time.After(time.Second):
				t.Fatal("stop/reset blocked behind a silent TLS handshake")
			}
			select {
			case err := <-result:
				if !errors.Is(err, net.ErrClosed) {
					t.Fatalf("interrupted request: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("interrupted request still waiting")
			}
			if tc.stop {
				if _, err := cm.OpenStream(); !errors.Is(err, net.ErrClosed) {
					t.Fatalf("client reopened: %v", err)
				}
			} else {
				serve()
				if _, err := MeasureTunnelLatency(cm); err != nil {
					t.Fatalf("fresh connection after reset: %v", err)
				}
			}
		})
	}
}

func TestGameLaneWhileWebHandshakeStalls(t *testing.T) {
	cm, ln, serve := recoveryTLSNode(t, false)
	origin, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer origin.Close()
	go func() {
		var data [32]byte
		if n, peer, err := origin.ReadFrom(data[:]); err == nil {
			_, _ = origin.WriteTo(data[:n], peer)
		}
	}()
	web := make(chan error, 1)
	go func() { _, err := cm.OpenStream(); web <- err }()
	silent, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	// Keep the first handshake silent while accepting real TLS on the same port.
	serve()
	game := make(chan error, 1)
	go func() {
		stream, err := cm.openUDPRelayStream()
		if err == nil {
			defer stream.Close()
			_ = stream.SetDeadline(time.Now().Add(2 * time.Second))
			_, err = stream.Write([]byte{CmdUDP})
			if err == nil {
				err = writeUDPFramed(stream, origin.LocalAddr().String(), []byte("game"))
			}
			if err == nil {
				peer, payload, readErr := readUDPFramed(stream)
				err = readErr
				if err == nil && (peer != origin.LocalAddr().String() || string(payload) != "game") {
					err = fmt.Errorf("unexpected game-lane reply from %s: %q", peer, payload)
				}
			}
		}
		game <- err
	}()
	select {
	case err := <-game:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("game lane waited for the unrelated web handshake")
	}
	cm.Close()
	if err := <-web; !errors.Is(err, net.ErrClosed) {
		t.Fatalf("web handshake: %v", err)
	}
}

func TestCloseInterruptsBlockedStreamOpen(t *testing.T) {
	local, remote := net.Pipe()
	defer remote.Close()
	session, err := smux.Client(local, smuxConfig())
	if err != nil {
		t.Fatal(err)
	}
	cm := &ClientMux{sess: session}
	defer cm.Close()
	result := make(chan error, 1)
	go func() { _, err := cm.OpenStream(); result <- err }()
	// Reading only one byte leaves the stream-open control frame blocked in Write.
	_ = remote.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := io.ReadFull(remote, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() { cm.Close(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		remote.Close()
		t.Fatal("close blocked behind the stream-open write")
	}
	if err := <-result; !errors.Is(err, net.ErrClosed) {
		t.Fatalf("stream survived closure: %v", err)
	}
}

func TestConcurrentOpensShareSession(t *testing.T) {
	cm, _, serve := recoveryTLSNode(t, false)
	serve()
	const workers = 32
	start := make(chan struct{})
	results := make(chan *smux.Session, workers)
	for range workers {
		go func() {
			<-start
			stream, session, _, err := cm.openSessionStream()
			if err != nil {
				results <- nil
				return
			}
			defer stream.Close()
			results <- session
		}()
	}
	close(start)
	var first *smux.Session
	for range workers {
		session := <-results
		if session == nil || (first != nil && first != session) {
			t.Fatal("concurrent requests failed to share the live session")
		}
		first = session
	}
}
