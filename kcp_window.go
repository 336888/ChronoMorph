package main

import (
	"sync"
	"time"

	"github.com/xtaci/kcp-go/v5"
)

const (
	initialKCPWindow = 256
	maxKCPWindow     = 2048
	tunnelMTU        = 1150
)

// Grow only when this sender is using its window. A large receive window
// permits faster peers to advance and does not reserve memory for idle peers.
type kcpWindow struct {
	size                  int
	from, last, baseSince time.Time
	bytes                 int64
	baseRTT               time.Duration
}

func (w *kcpWindow) sample(now time.Time, n int, rtt time.Duration) bool {
	old := w.size
	if w.from.IsZero() || now.Sub(w.last) > 5*time.Second {
		*w = kcpWindow{size: initialKCPWindow, from: now, baseSince: now}
	}
	w.bytes += int64(n)
	w.last = now
	if rtt <= 0 {
		return old != w.size
	}
	if w.baseRTT == 0 || rtt < w.baseRTT || now.Sub(w.baseSince) > time.Minute {
		w.baseRTT = rtt
		w.baseSince = now
	}
	elapsed := now.Sub(w.from)
	if elapsed < max(500*time.Millisecond, 3*rtt) {
		return old != w.size
	}
	// Observed payload delivered per base RTT in MSS-sized units. Include
	// modest headroom for ACK cadence, while respecting persistent queue delay.
	inFlight := int(float64(w.bytes) * float64(w.baseRTT) / float64(elapsed) / (tunnelMTU - 32))
	if rtt > w.baseRTT+w.baseRTT/4+10*time.Millisecond {
		w.size = max(initialKCPWindow, w.size*3/4)
	} else if inFlight >= w.size*2/3 {
		w.size = min(maxKCPWindow, w.size+w.size/2)
	}
	w.bytes, w.from = 0, now
	return old != w.size
}

// smux uses WriteBuffers when available. Keep its vectored writes and sample
// application bytes once per write, outside KCP's per-packet hot path.
type bulkKCPConn struct {
	*kcp.UDPSession
	mu     sync.Mutex
	window kcpWindow
}

func tunedKCP(conn *kcp.UDPSession) *bulkKCPConn {
	conn.SetNoDelay(1, 20, 2, 1)
	conn.SetWindowSize(initialKCPWindow, maxKCPWindow)
	conn.SetMtu(tunnelMTU)
	conn.SetACKNoDelay(true)
	return &bulkKCPConn{UDPSession: conn, window: kcpWindow{size: initialKCPWindow}}
}

func (c *bulkKCPConn) Write(p []byte) (int, error) { return c.WriteBuffers([][]byte{p}) }

func (c *bulkKCPConn) WriteBuffers(v [][]byte) (int, error) {
	n, err := c.UDPSession.WriteBuffers(v)
	if n > 0 {
		c.mu.Lock()
		if c.window.sample(time.Now(), n, time.Duration(c.GetSRTT())*time.Millisecond) {
			c.SetWindowSize(c.window.size, maxKCPWindow)
		}
		c.mu.Unlock()
	}
	return n, err
}
