package main

import (
	"net"
	"sync"
	"time"
)

type ipRecord struct {
	badCount int
	banUntil time.Time
}

type AbuseGuard struct {
	mu      sync.Mutex
	records map[string]*ipRecord
}

func NewAbuseGuard() *AbuseGuard {
	ag := &AbuseGuard{records: make(map[string]*ipRecord)}
	go func() {
		for range time.Tick(2 * time.Minute) {
			now := time.Now()
			ag.mu.Lock()
			for ip, rec := range ag.records {
				if now.After(rec.banUntil) && rec.badCount == 0 {
					delete(ag.records, ip)
				} else if now.After(rec.banUntil) {
					rec.badCount = 0
				}
			}
			ag.mu.Unlock()
		}
	}()
	return ag
}

func (ag *AbuseGuard) IsBanned(addr net.Addr) bool {
	udpAddr, ok := addr.(*net.UDPAddr)
	if !ok {
		return false
	}
	ip := udpAddr.IP.String()
	ag.mu.Lock()
	defer ag.mu.Unlock()
	rec, exists := ag.records[ip]
	return exists && time.Now().Before(rec.banUntil)
}

func (ag *AbuseGuard) RecordBadPacket(addr net.Addr) {
	udpAddr, ok := addr.(*net.UDPAddr)
	if !ok {
		return
	}
	ip := udpAddr.IP.String()
	ag.mu.Lock()
	defer ag.mu.Unlock()
	rec, exists := ag.records[ip]
	if !exists {
		rec = &ipRecord{}
		ag.records[ip] = rec
	}
	rec.badCount++
	if rec.badCount >= 20 {
		rec.banUntil = time.Now().Add(5 * time.Minute)
		rec.badCount = 0
	}
}
