package main

import (
	"net"
	"sort"
	"sync"
	"time"
)

type NodePingResult struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	IP    string `json:"ip"`
	RTTMs int64  `json:"rtt_ms"`
}

func (g *GUIController) BenchAllProfiles() []NodePingResult {
	g.mu.Lock()
	profiles := append([]NodeProfile(nil), g.cfg.Profiles...)
	g.mu.Unlock()

	results := make([]NodePingResult, len(profiles))
	var wg sync.WaitGroup
	for i, p := range profiles {
		wg.Add(1)
		go func(idx int, prof NodeProfile) {
			defer wg.Done()
			results[idx] = NodePingResult{
				Index: idx,
				Name:  prof.Name,
				IP:    prof.ServerIP,
				RTTMs: probeNodeUDP(prof.ServerIP),
			}
		}(i, p)
	}
	wg.Wait()

	sort.Slice(results, func(i, j int) bool {
		if results[i].RTTMs < 0 {
			return false
		}
		if results[j].RTTMs < 0 {
			return true
		}
		return results[i].RTTMs < results[j].RTTMs
	})
	return results
}

func probeNodeUDP(ipStr string) int64 {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return -1
	}
	network := "udp4"
	if ip.To4() == nil {
		network = "udp6"
	}
	conn, err := net.ListenPacket(network, ":0")
	if err != nil {
		return -1
	}
	defer conn.Close()

	dst := &net.UDPAddr{IP: ip, Port: PortMin + 1000}
	start := time.Now()
	if _, err := conn.WriteTo(BuildSTUNBindingRequest(), dst); err != nil {
		return -1
	}
	_ = conn.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
	buf := make([]byte, 128)
	if _, _, err := conn.ReadFrom(buf); err != nil {
		return -1
	}
	return time.Since(start).Milliseconds()
}
