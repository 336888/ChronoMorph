package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func dataShard(seq, conv uint32) []byte {
	b := make([]byte, 32)
	binary.LittleEndian.PutUint32(b, seq)
	binary.LittleEndian.PutUint16(b[4:], 0xf1)
	binary.LittleEndian.PutUint32(b[8:], conv)
	return b
}

func TestUDPReturnPath(t *testing.T) {
	var peers udpPeers
	a := netip.MustParseAddrPort("127.0.0.1:1234")
	b := netip.MustParseAddrPort("127.0.0.1:5678")
	u, other := new(UserEntry), new(UserEntry)
	when := time.Now()
	p := peers.receive(u, a, dataShard(10, 99), when)
	if p == nil || peers.receive(u, b, dataShard(12, 99), when) != p {
		t.Fatal("lost session on rebind")
	}
	peers.receive(u, a, dataShard(11, 99), when)
	if peers.destination(p).AddrPort() != b {
		t.Fatal("late packet redirected replies to the old endpoint")
	}
	parity := []byte{0, 0, 0, 0, 0xf2, 0}
	if peers.receive(u, a, parity, when) != nil || peers.receive(u, b, parity, when) != p {
		t.Fatal("parity accepted on the wrong path")
	}
	if q := peers.receive(other, b, dataShard(12, 99), when); q == p || q == nil {
		t.Fatal("users with the same conversation ID shared a return path")
	}
	later := when.Add(6 * time.Minute)
	peers.receive(other, a, dataShard(13, 7), later)
	if peers.destination(p) != nil || peers.receive(u, b, parity, later) != nil {
		t.Fatal("expired path retained")
	}
}

// WriteTo records what the UDP client would hand to the socket, including
// when its moving destination port cannot be reserved by a loopback test.
type packetRecorder struct {
	net.PacketConn
	writes [][]byte
}

func (r *packetRecorder) WriteTo(b []byte, _ net.Addr) (int, error) {
	r.writes = append(r.writes, bytes.Clone(b))
	return len(b), nil
}

func TestUDPStartsWithAuthenticatedData(t *testing.T) {
	key := bytes.Repeat([]byte{0xab}, 32)
	for _, fixed := range []int{0, 40000} {
		sink := &packetRecorder{}
		conn := NewClientRTPPacketConn(sink, net.IPv4(127, 0, 0, 1), NewChronoPacketEngine(key))
		conn.fixedPort = fixed
		data := dataShard(1, 99)
		if _, err := conn.WriteTo(data, nil); err != nil {
			t.Fatal(err)
		}
		if len(sink.writes) != 1 {
			t.Fatalf("port %d: first write emitted %d datagrams", fixed, len(sink.writes))
		}
		engine, err := NewMultiUserEngine([]UserEntry{{KeyHex: hex.EncodeToString(key)}})
		if err != nil {
			t.Fatal(err)
		}
		if _, plain, err := engine.OpenPacket(sink.writes[0]); err != nil || !bytes.Equal(plain, data) {
			t.Fatalf("port %d: first datagram did not authenticate: %v", fixed, err)
		}
	}
}

func TestAuthenticatedDuplicatesDoNotBanUDPPeer(t *testing.T) {
	key, _ := hex.DecodeString(strings.Repeat("ab", 32))
	engine, err := NewMultiUserEngine([]UserEntry{{KeyHex: hex.EncodeToString(key)}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	tx, err := net.Dial("udp4", raw.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	guard := &AbuseGuard{records: make(map[string]*ipRecord)}
	server := NewServerRTPPacketConn(raw, engine, guard)
	client := NewChronoPacketEngine(key)
	first, _, err := client.SealAsRTP(dataShard(1, 99))
	if err != nil {
		t.Fatal(err)
	}
	next, _, err := client.SealAsRTP(dataShard(2, 99))
	if err != nil {
		t.Fatal(err)
	}
	if err := server.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	// A real request from the old hopping client, followed by valid data.
	prelude, _ := hex.DecodeString("000100002112a44200112233445566778899aabb")
	if _, err := tx.Write(prelude); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 26; i++ {
		if _, err := tx.Write(first); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Write(next); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	for _, want := range []uint32{1, 2} {
		n, _, err := server.ReadFrom(buf)
		if err != nil || n != 32 || binary.LittleEndian.Uint32(buf) != want {
			t.Fatalf("expected shard %d, got %d bytes, %v", want, n, err)
		}
	}
	if err := tx.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var timeout net.Error
	if _, err := tx.Read(buf); !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("UDP server answered an unauthenticated prelude: %v", err)
	}
	if len(guard.records) != 0 {
		t.Fatal("authenticated duplicate charged to the IP")
	}
	if _, err := tx.Write([]byte("invalid")); err != nil {
		t.Fatal(err)
	}
	last, _, err := client.SealAsRTP(dataShard(3, 99))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Write(last); err != nil {
		t.Fatal(err)
	}
	if _, _, err := server.ReadFrom(buf); err != nil {
		t.Fatal(err)
	}
	if len(guard.records) != 1 {
		t.Fatal("unauthenticated packet was not counted")
	}
}
