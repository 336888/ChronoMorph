package main

import (
	"encoding/json"
	"log"
	"os"
	"time"
)

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
	if snap.Month != time.Now().Format("2006-01") {
		return
	}
	engine.mu.RLock()
	for _, u := range engine.users {
		if pair, ok := snap.Users[u.Name]; ok {
			u.rxByte.Store(pair[0])
			u.txByte.Store(pair[1])
		}
	}
	engine.mu.RUnlock()
}

func saveTraffic(path string, engine *MultiUserEngine) {
	snap := TrafficSnapshot{
		Month: time.Now().Format("2006-01"),
		Users: make(map[string][2]uint64),
	}
	engine.mu.RLock()
	for _, u := range engine.users {
		snap.Users[u.Name] = [2]uint64{u.rxByte.Load(), u.txByte.Load()}
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
	usedBytes := u.rxByte.Load() + u.txByte.Load()
	limitBytes := uint64(limitGB * 1024 * 1024 * 1024)
	if usedBytes >= limitBytes {
		log.Printf("[Quota] 用户 %s 已达本月流量上限 (%.1f GB)，暂停转发", u.Name, limitGB)
		return true
	}
	return false
}
