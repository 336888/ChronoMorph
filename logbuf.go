package main

import (
	"strings"
	"sync"
)

type RingLogBuffer struct {
	mu    sync.Mutex
	lines []string
	max   int
}

var AppLogs = &RingLogBuffer{max: 100}

func (b *RingLogBuffer) Write(p []byte) (int, error) {
	s := strings.TrimSpace(string(p))
	if s != "" {
		b.mu.Lock()
		b.lines = append(b.lines, s)
		if len(b.lines) > b.max {
			b.lines = b.lines[len(b.lines)-b.max:]
		}
		b.mu.Unlock()
	}
	return len(p), nil
}

func (b *RingLogBuffer) Snapshot() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.lines))
	copy(out, b.lines)
	return out
}
