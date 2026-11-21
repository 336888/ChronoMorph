package main

import (
	"testing"
	"time"
)

func TestKCPWindowFollowsLoadAndDelay(t *testing.T) {
	var w kcpWindow
	now := time.Unix(100, 0)
	const rtt = 200 * time.Millisecond
	sample := func(n int, r time.Duration) {
		now = now.Add(time.Second)
		w.sample(now, n, r)
	}
	// Sparse voice/game traffic should keep the original send window.
	for i := 0; i < 8; i++ {
		sample(1024, rtt)
	}
	if w.size != initialKCPWindow {
		t.Fatalf("small writes: %d", w.size)
	}
	sample(256*(tunnelMTU-32)*5, rtt)
	if w.size <= initialKCPWindow {
		t.Fatalf("saturated window did not grow: %d", w.size)
	}
	for i := 0; i < 10; i++ {
		sample(20<<20, rtt)
	}
	if w.size != maxKCPWindow {
		t.Fatalf("ceiling: %d", w.size)
	}
	// Allow at least three of the inflated RTTs before evaluating the sample.
	sample(20<<20, 2*rtt)
	sample(20<<20, 2*rtt)
	if w.size >= maxKCPWindow {
		t.Fatalf("persistent delay did not reduce the window: %d", w.size)
	}
	now = now.Add(8 * time.Second)
	sample(1024, 0)
	if w.size != initialKCPWindow {
		t.Fatalf("idle restart: %d", w.size)
	}
}
