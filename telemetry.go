package main

import (
	"sync/atomic"
	"time"
)

type TrafficTelemetry struct {
	txTotal    atomic.Uint64
	rxTotal    atomic.Uint64
	txSpeedBps atomic.Uint64
	rxSpeedBps atomic.Uint64
	activePort atomic.Int32
}

var LiveStats = &TrafficTelemetry{}

func init() {
	go func() {
		var lastTx, lastRx uint64
		for range time.Tick(time.Second) {
			curTx := LiveStats.txTotal.Load()
			curRx := LiveStats.rxTotal.Load()
			LiveStats.txSpeedBps.Store(curTx - lastTx)
			LiveStats.rxSpeedBps.Store(curRx - lastRx)
			lastTx, lastRx = curTx, curRx
		}
	}()
}

func (t *TrafficTelemetry) AddTx(n uint64) { t.txTotal.Add(n) }
func (t *TrafficTelemetry) AddRx(n uint64) { t.rxTotal.Add(n) }
