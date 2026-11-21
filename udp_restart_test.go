package main

import (
	"bytes"
	"encoding/hex"
	"testing"
	"time"
)

// A new client process restarts its sequence counter. Its later authenticated
// timestamp distinguishes its first packet from a copy of an earlier packet.
func TestImmediateUDPClientRestart(t *testing.T) {
	key := bytes.Repeat([]byte{0xab}, 32)
	engine, err := NewMultiUserEngine([]UserEntry{{KeyHex: hex.EncodeToString(key)}})
	if err != nil {
		t.Fatal(err)
	}
	client := NewChronoPacketEngine(key)
	var prior []byte
	for i := 0; i < 200; i++ {
		prior, _, err = client.SealAsRTP([]byte("before restart"))
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := engine.OpenPacket(prior); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(3 * time.Millisecond)
	restarted := NewChronoPacketEngine(key)
	packet, _, err := restarted.SealAsRTP([]byte("after restart"))
	if err != nil {
		t.Fatal(err)
	}
	if _, data, err := engine.OpenPacket(packet); err != nil || string(data) != "after restart" {
		t.Fatalf("first packet after restart: %v", err)
	}
	for _, dup := range [][]byte{prior, packet} {
		if _, _, err := engine.OpenPacket(dup); err == nil {
			t.Fatal("duplicate accepted across restart")
		}
	}
}

func BenchmarkUDPSeal(b *testing.B) {
	client := NewChronoPacketEngine(make([]byte, 32))
	payload := make([]byte, 1150)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, _, err := client.SealAsRTP(payload); err != nil {
			b.Fatal(err)
		}
	}
}

func TestReplayAcrossReorderingAndClockWrap(t *testing.T) {
	var rw ReplayWindow
	// Exercise the RTP timestamp rollover and a 30s epoch boundary separately.
	for _, base := range []int64{30000, 1 << 32} {
		for _, offset := range []int64{1, -92, 0, -91, 2} {
			stamp := base + offset
			seq := uint16(65000 + offset*512)
			if !rw.CheckAndUpdate(stamp/30000, uint32(stamp), seq) || rw.CheckAndUpdate(stamp/30000, uint32(stamp), seq) {
				t.Fatalf("reorder or duplicate handling for stamp %d", stamp)
			}
		}
	}
	base := int64(1<<32) + 5000
	for _, seq := range []uint16{65530, 8, 65531, 9} {
		if !rw.CheckAndUpdate(base/30000, uint32(base), seq) {
			t.Fatalf("rejected sequence %d across wrap", seq)
		}
	}
	if rw.CheckAndUpdate(base/30000, uint32(base), 65530) {
		t.Fatal("duplicate reopened at sequence wrap")
	}
	old := base - 2049
	if rw.CheckAndUpdate(old/30000, uint32(old), 1) {
		t.Fatal("accepted expired timestamp")
	}
}
