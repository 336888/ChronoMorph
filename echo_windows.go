package main

import (
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"sync"
	"time"
)

type echoLink struct {
	stack.LinkEndpoint
	enabled, block6 bool
	delay           time.Duration
	pending         chan struct{}
	done            chan struct{}
	wait            sync.WaitGroup
}

func newEchoLink(dev stack.LinkEndpoint, enabled bool, delay time.Duration, block6 bool) *echoLink {
	return &echoLink{LinkEndpoint: dev, enabled: enabled, delay: delay, block6: block6, pending: make(chan struct{}, 128), done: make(chan struct{})}
}
func (l *echoLink) stop() { close(l.done) }
func (l *echoLink) Attach(d stack.NetworkDispatcher) {
	if d == nil {
		l.LinkEndpoint.Attach(nil)
		return
	}
	l.LinkEndpoint.Attach(&echoDispatcher{d, l})
}

type echoDispatcher struct {
	stack.NetworkDispatcher
	l *echoLink
}

func (d *echoDispatcher) DeliverNetworkPacket(proto tcpip.NetworkProtocolNumber, pkt *stack.PacketBuffer) {
	data := pkt.Data()
	first, ok := data.PullUp(1)
	if !ok {
		return
	}
	v := first[0] >> 4
	if v == 6 && d.l.block6 {
		return
	}
	var off int
	switch v {
	case 4:
		h, ok := data.PullUp(20)
		if !ok {
			return
		}
		if h[9] == 1 {
			off = int(h[0]&15) * 4
		}
	case 6:
		h, ok := data.PullUp(40)
		if !ok {
			return
		}
		next := h[6]
		pos := 40
		for i := 0; i < 8; i++ {
			if next == 58 {
				off = pos
				break
			}
			if next != 0 && next != 43 && next != 60 {
				break
			}
			ext, ok := data.PullUp(pos + 2)
			if !ok {
				return
			}
			next = ext[pos]
			pos += (int(ext[pos+1]) + 1) * 8
		}
	}
	if off == 0 {
		d.NetworkDispatcher.DeliverNetworkPacket(proto, pkt)
		return
	}
	b, ok := data.PullUp(off + 1)
	if !ok {
		return
	}
	if !(v == 4 && b[off] == 8 || v == 6 && b[off] == 128) {
		d.NetworkDispatcher.DeliverNetworkPacket(proto, pkt)
		return
	}
	if !d.l.enabled {
		return
	}
	if d.l.delay == 0 {
		d.NetworkDispatcher.DeliverNetworkPacket(proto, pkt)
		return
	}
	select {
	case d.l.pending <- struct{}{}:
	default:
		return
	}
	pkt.IncRef()
	d.l.wait.Add(1)
	go func() {
		defer d.l.wait.Done()
		defer pkt.DecRef()
		defer func() { <-d.l.pending }()
		timer := time.NewTimer(d.l.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
			d.NetworkDispatcher.DeliverNetworkPacket(proto, pkt)
		case <-d.l.done:
		}
	}()
}
