package main

import (
	"encoding/json"
	"log"
	"os"
	"sync"
	"time"
)

// Compare calendar months in the server's local zone, as in traffic.json.
// Reading a different month reports zero without mutating a newer counter.
type monthlyTraffic struct {
	mu    sync.Mutex
	month int
	bytes [2]uint64
}

func trafficMonth(at time.Time) int {
	year, month, _ := at.Date()
	return year*100 + int(month)
}

func (m *monthlyTraffic) add(rx, tx uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	month := trafficMonth(time.Now())
	if m.month != month {
		m.month = month
		m.bytes = [2]uint64{}
	}
	m.bytes[0] += rx
	m.bytes[1] += tx
}

func (m *monthlyTraffic) totals(month int) [2]uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.month != month {
		return [2]uint64{}
	}
	return m.bytes
}

type TrafficSnapshot struct {
	Month string               `json:"month"`
	Users map[string][2]uint64 `json:"users"`
}

func StartTrafficPersistence(path string, engine *MultiUserEngine, stopCh <-chan struct{}) <-chan struct{} {
	loadTraffic(path, engine)

	saved := make(chan struct{})
	go func() {
		defer close(saved)
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				saveTraffic(path, engine)
				return
			case <-ticker.C:
				saveTraffic(path, engine)
			}
		}
	}()
	return saved
}

func loadTraffic(path string, engine *MultiUserEngine) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var snap TrafficSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return
	}
	now := time.Now()
	if snap.Month != now.Format("2006-01") {
		return
	}
	engine.mu.RLock()
	for _, u := range engine.users {
		if pair, ok := snap.Users[u.Name]; ok {
			u.traffic.mu.Lock()
			u.traffic.month = trafficMonth(now)
			u.traffic.bytes = pair
			u.traffic.mu.Unlock()
		}
	}
	engine.mu.RUnlock()
}

func saveTraffic(path string, engine *MultiUserEngine) {
	now := time.Now()
	month := trafficMonth(now)
	snap := TrafficSnapshot{
		Month: now.Format("2006-01"),
		Users: make(map[string][2]uint64),
	}
	engine.mu.RLock()
	for _, u := range engine.users {
		snap.Users[u.Name] = u.traffic.totals(month)
	}
	engine.mu.RUnlock()

	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err == nil {
		_ = os.Rename(tmp, path)
	}
}

func (u *UserEntry) IsOverQuota(limitGB float64) bool {
	if limitGB <= 0 {
		return false
	}
	pair := u.traffic.totals(trafficMonth(time.Now()))
	usedBytes := pair[0] + pair[1]
	limitBytes := uint64(limitGB * 1024 * 1024 * 1024)
	if usedBytes >= limitBytes {
		log.Printf("[Quota] 用户 %s 已达本月流量上限 (%.1f GB)，暂停转发", u.Name, limitGB)
		return true
	}
	return false
}
