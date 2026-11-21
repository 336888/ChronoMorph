package main

import (
	"io"
	"net"
	"net/netip"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type socketPair struct{ source, listener netip.AddrPort }

type liveFlow struct {
	id              uint64
	protocol, route string
	source          socketPair
	start           time.Time
	target          atomic.Pointer[string]
	up, down        atomic.Uint64
	closer          io.Closer
}

type flowMonitor struct {
	mu     sync.Mutex
	next   uint64
	active map[uint64]*liveFlow
}

func (m *flowMonitor) begin(client net.Conn, protocol, route, target string, closer io.Closer) *liveFlow {
	if m == nil {
		return nil
	}
	f := &liveFlow{protocol: protocol, route: route, start: time.Now(), closer: closer}
	if a, ok := client.RemoteAddr().(*net.TCPAddr); ok {
		f.source.source = a.AddrPort()
	}
	if a, ok := client.LocalAddr().(*net.TCPAddr); ok {
		f.source.listener = a.AddrPort()
	}
	f.target.Store(&target)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		m.active = make(map[uint64]*liveFlow)
	}
	m.next++
	f.id = m.next
	m.active[f.id] = f
	return f
}

func (m *flowMonitor) end(f *liveFlow) {
	if f == nil {
		return
	}
	m.mu.Lock()
	delete(m.active, f.id)
	m.mu.Unlock()
}

func (m *flowMonitor) flows() []*liveFlow {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	rows := make([]*liveFlow, 0, len(m.active))
	for _, f := range m.active {
		rows = append(rows, f)
	}
	m.mu.Unlock()
	sort.Slice(rows, func(i, j int) bool { return rows[i].id > rows[j].id })
	return rows
}

func (m *flowMonitor) disconnect(id uint64) bool {
	m.mu.Lock()
	f := m.active[id]
	m.mu.Unlock()
	if f == nil {
		return false
	}
	_ = f.closer.Close()
	return true
}

func (r *Router) monitor() *flowMonitor {
	if r == nil {
		return nil
	}
	return r.live
}

func cmRoute(cm *ClientMux) string {
	if cm != nil && cm.entry != nil {
		return "CM 双跳"
	}
	return "CM 单跳"
}

type measuredConn struct {
	net.Conn
	m *flowMonitor
	f *liveFlow
}

func (m *flowMonitor) tcp(client, remote net.Conn, protocol, route, target string) net.Conn {
	if m == nil || client == nil {
		return remote
	}
	f := m.begin(client, protocol, route, target, remote)
	return &measuredConn{Conn: remote, m: m, f: f}
}
func (c *measuredConn) Write(p []byte) (int, error) {
	n, e := c.Conn.Write(p)
	c.f.up.Add(uint64(n))
	return n, e
}
func (c *measuredConn) Read(p []byte) (int, error) {
	n, e := c.Conn.Read(p)
	c.f.down.Add(uint64(n))
	return n, e
}
func (c *measuredConn) CloseWrite() error { return closeWrite(c.Conn) }
func (c *measuredConn) Close() error {
	defer c.m.end(c.f)
	return c.Conn.Close()
}
