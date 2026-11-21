package main

import (
	"crypto/rand"
	"encoding/binary"
	"net"
	"time"
)

func StartJitterKeepalive(pc net.PacketConn, serverIP net.IP, masterKey []byte, stopCh <-chan struct{}) {
	eng := NewChronoPacketEngine(masterKey)
	go func() {
		for {
			var b [2]byte
			_, _ = rand.Read(b[:])
			delay := time.Duration(12+int(binary.BigEndian.Uint16(b[:])%14)) * time.Second
			select {
			case <-stopCh:
				return
			case <-time.After(delay):
				pkt, port, err := eng.SealAsRTP([]byte{0x00})
				if err != nil {
					continue
				}
				_, _ = pc.WriteTo(pkt, &net.UDPAddr{IP: serverIP, Port: port})
			}
		}
	}()
}
