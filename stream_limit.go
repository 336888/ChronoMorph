package main

import "sync/atomic"

const MaxConcurrentStreams = 512

type StreamLimiter struct {
	active         atomic.Int32
	rejected       atomic.Uint64
	headerTimeouts atomic.Uint64
	drainTimeouts  atomic.Uint64
}

func (l *StreamLimiter) Acquire() bool {
	for {
		cur := l.active.Load()
		if cur >= MaxConcurrentStreams {
			l.rejected.Add(1)
			return false
		}
		if l.active.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

func (l *StreamLimiter) Release() {
	l.active.Add(-1)
}
