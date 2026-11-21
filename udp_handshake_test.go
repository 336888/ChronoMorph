package main

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/xtaci/smux"
)

func TestUDPCapabilityNegotiation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reply  []byte
		want   byte
		reject bool
	}{
		{"legacy", nil, CmdTCP, false},
		{"dial ack", []byte{CmdCapabilities, capDialAck}, CmdDialTCP, false},
		{"two stage", []byte{CmdCapabilities, capDialAck | capDialProgress}, CmdDialReady, false},
		{"malformed", []byte{CmdPing, 0}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local, remote := net.Pipe()
			client, err := smux.Client(local, smuxConfig())
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			server, err := smux.Server(remote, smuxConfig())
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			go func() {
				s, err := server.AcceptStream()
				if err != nil {
					return
				}
				defer s.Close()
				var cmd [1]byte
				if _, err := io.ReadFull(s, cmd[:]); err == nil && cmd[0] == CmdCapabilities && tc.reply != nil {
					_, _ = s.Write(tc.reply)
				}
			}()
			got, err := negotiateDialMode(client)
			if (err != nil) != tc.reject || got != tc.want {
				t.Fatalf("mode=%d err=%v", got, err)
			}
		})
	}
}
func TestLateTimeoutKeepsReceivingSession(t *testing.T) {
	local, remote := net.Pipe()
	defer remote.Close()
	session, err := smux.Client(local, smuxConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	cm := ClientMux{sess: session, udpPath: &RTPPacketConn{}}
	before := time.Now()
	cm.udpPath.lastReply.Store(before.Add(time.Millisecond).UnixNano())
	if cm.discardSilentPath(session, before) || session.IsClosed() {
		t.Fatal("late request timeout closed a working session")
	}
	// A timeout belonging to another session may retry, but cannot evict this one.
	other, end := net.Pipe()
	defer end.Close()
	stale, err := smux.Client(other, smuxConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer stale.Close()
	if !cm.discardSilentPath(stale, before) || cm.sess != session || session.IsClosed() {
		t.Fatal("late timeout closed replacement")
	}
}

func TestDualRecoveryTriesOtherTransportOnce(t *testing.T) {
	for _, tcp := range []bool{false, true} {
		a, b := net.Pipe()
		session, err := smux.Client(a, smuxConfig())
		if err != nil {
			t.Fatal(err)
		}
		cm := ClientMux{sess: session, tlsNode: TLSNode{UDPPort: 40000}}
		want := int32(transportUDP)
		if tcp {
			cm.tcpPath = &tlsMeterConn{profile: tlsProfileReady}
		} else {
			pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			cm.udpPath = &RTPPacketConn{raw: pc}
			want = transportTCP
		}
		if !cm.discardSilentPath(session, time.Now()) || cm.retryFirst != want {
			t.Fatalf("silent TCP=%t: next preference %d, want %d", tcp, cm.retryFirst, want)
		}
		cm.Reset()
		if cm.retryFirst != 0 {
			t.Fatal("stale transport preference after network reset")
		}
		b.Close()
	}
}
