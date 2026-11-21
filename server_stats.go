package main

import (
	"encoding/json"
	"net/http"
	"time"
)

var serverStartTime = time.Now()

type UserStatView struct {
	Name string  `json:"name"`
	RxMB float64 `json:"rx_mb"`
	TxMB float64 `json:"tx_mb"`
}

func StartLocalStatsAPI(engine *MultiUserEngine, guard *AbuseGuard) {
	mux := http.NewServeMux()
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		var users []UserStatView
		if engine != nil {
			engine.mu.RLock()
			for _, u := range engine.users {
				users = append(users, UserStatView{
					Name: u.Name,
					RxMB: float64(u.rxByte.Load()) / (1024 * 1024),
					TxMB: float64(u.txByte.Load()) / (1024 * 1024),
				})
			}
			engine.mu.RUnlock()
		}

		bannedCount := 0
		if guard != nil {
			now := time.Now()
			guard.mu.Lock()
			for _, rec := range guard.records {
				if now.Before(rec.banUntil) {
					bannedCount++
				}
			}
			guard.mu.Unlock()
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"uptime_sec":   int64(time.Since(serverStartTime).Seconds()),
			"banned_ips":   bannedCount,
			"active_epoch": CurrentEpoch(),
			"users":        users,
		})
	})

	go func() {
		_ = http.ListenAndServe("127.0.0.1:40001", mux)
	}()
}
