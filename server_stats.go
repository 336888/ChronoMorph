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
	go func() {
		_ = http.ListenAndServe("127.0.0.1:40001", serverStatsHandler(engine, guard, serverLimiter))
	}()
}

func serverStatsHandler(engine *MultiUserEngine, guard *AbuseGuard, limiter *StreamLimiter) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		month := trafficMonth(now)
		users := make([]UserStatView, 0)
		if engine != nil {
			engine.mu.RLock()
			for _, u := range engine.users {
				pair := u.traffic.totals(month)
				users = append(users, UserStatView{
					Name: u.Name,
					RxMB: float64(pair[0]) / (1024 * 1024),
					TxMB: float64(pair[1]) / (1024 * 1024),
				})
			}
			engine.mu.RUnlock()
		}

		bannedCount := 0
		if guard != nil {
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
			"month":               now.Format("2006-01"),
			"active_streams":      limiter.active.Load(),
			"stream_limit":        MaxConcurrentStreams,
			"rejected_streams":    limiter.rejected.Load(),
			"header_timeouts":     limiter.headerTimeouts.Load(),
			"half_close_timeouts": limiter.drainTimeouts.Load(),
			"uptime_sec":          int64(time.Since(serverStartTime).Seconds()),
			"banned_ips":          bannedCount,
			"active_epoch":        CurrentEpoch(),
			"users":               users,
		})
	})

	return mux
}
