package main

import (
	"net"
	"sync"
	"time"
)

// A SOCKS association can send to several unrelated destinations on one socket.
// Use a connected socket per direct destination so only that peer can reply.
type directUDPFlows struct {
	mu     sync.Mutex
	peers  map[string]net.Conn
	closed bool
}

func (d *directUDPFlows) send(target string, data []byte, reply func(string, []byte)) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return net.ErrClosed
	}
	if d.peers == nil {
		d.peers = make(map[string]net.Conn)
	}
	c := d.peers[target]
	if c == nil {
		if len(d.peers) >= 64 {
			return net.ErrClosed
		}
		var err error
		c, err = net.DialTimeout("udp", target, 3*time.Second)
		if err != nil {
			return err
		}
		d.peers[target] = c
		go func() {
			defer func() {
				d.mu.Lock()
				if d.peers[target] == c {
					delete(d.peers, target)
				}
				d.mu.Unlock()
				c.Close()
			}()
			buf := make([]byte, 65535)
			for {
				c.SetReadDeadline(time.Now().Add(time.Minute))
				n, err := c.Read(buf)
				if err != nil {
					return
				}
				reply(c.RemoteAddr().String(), buf[:n])
			}
		}()
	}
	c.SetWriteDeadline(time.Now().Add(3 * time.Second))
	c.SetReadDeadline(time.Now().Add(time.Minute))
	_, err := c.Write(data)
	return err
}
func (d *directUDPFlows) close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	for _, c := range d.peers {
		c.Close()
	}
}
