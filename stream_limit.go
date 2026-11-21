package main

import "sync/atomic"

const MaxConcurrentStreams = 512

type StreamLimiter struct {
	active atomic.Int32
}

func (l *StreamLimiter) Acquire() bool {
	for {
		cur := l.active.Load()
		if cur >= MaxConcurrentStreams {
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
