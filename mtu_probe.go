package main

import (
	"log"
	"net"
	"time"
)

func ProbeOptimalMTU(serverIP net.IP, masterKey []byte) int {
	const overhead = 96
	candidates := []int{1320, 1240, 1150, 1050}

	network := "udp4"
	if serverIP.To4() == nil {
		network = "udp6"
	}
	conn, err := net.ListenPacket(network, ":0")
	if err != nil {
		return 1150
	}
	defer conn.Close()

	eng := NewChronoPacketEngine(masterKey)
	buf := make([]byte, 2048)

	for _, size := range candidates {
		dummy := make([]byte, size-overhead)
		pkt, port, err := eng.SealAsRTP(dummy)
		if err != nil {
			continue
		}
		dst := &net.UDPAddr{IP: serverIP, Port: port}
		_, _ = conn.WriteTo(BuildSTUNBindingRequest(), dst)
		_, _ = conn.WriteTo(pkt, dst)

		_ = conn.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
		n, _, err := conn.ReadFrom(buf)
		if err == nil && n >= stunHeaderLen {
			kcpMTU := size - overhead
			log.Printf("[MTU] 探测完成，最优 KCP MTU: %d", kcpMTU)
			return kcpMTU
		}
	}
	return 1050
}
