package main

import (
	"bytes"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
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
	before.rxByte.Store(50)
	before.replay.CheckAndUpdate(7, 210000, 12)
	if err = engine.ReplaceUsers(cfg.serverUsers()); err != nil {
		t.Fatal(err)
	}
	if engine.users[0] != before || before.rxByte.Load() != 50 || before.replay.CheckAndUpdate(7, 210000, 12) {
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
