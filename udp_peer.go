package main

import (
	"encoding/binary"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

// KCP keys a server session by RemoteAddr().String(). Give it a stable address
// while retaining the most recent authenticated UDP return path.
type udpConversation struct {
	user *UserEntry
	conv uint32
}

type udpEndpoint struct {
	user *UserEntry
	addr netip.AddrPort
}

type udpPeer struct {
	id       string
	key      udpConversation
	endpoint netip.AddrPort
	seq      uint32
	seen     time.Time
}

func (p *udpPeer) Network() string { return "udp" }
func (p *udpPeer) String() string  { return p.id }

type udpPeers struct {
	mu         sync.Mutex
	byConv     map[udpConversation]*udpPeer
	byEndpoint map[udpEndpoint]*udpPeer
	lastSweep  time.Time
	nextID     uint64
}

func (r *udpPeers) forgetEndpoint(p *udpPeer) {
	key := udpEndpoint{p.key.user, p.endpoint}
	if r.byEndpoint[key] == p {
		delete(r.byEndpoint, key)
	}
}

// Only call after authentication and replay checks. FEC parity carries no
// conversation ID, so it may use an established path but cannot change one.
func (r *udpPeers) receive(user *UserEntry, from netip.AddrPort, payload []byte, now time.Time) *udpPeer {
	const fecData = 0xf1
	const fecParity = 0xf2
	const minKCPData = 8 + 24 // FEC header and length, then one KCP header
	if len(payload) < 6 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byConv == nil {
		r.byConv = make(map[udpConversation]*udpPeer)
		r.byEndpoint = make(map[udpEndpoint]*udpPeer)
	}
	if now.Sub(r.lastSweep) >= time.Minute {
		for key, p := range r.byConv {
			if now.Sub(p.seen) > 5*time.Minute || key.user.retired.Load() {
				r.forgetEndpoint(p)
				delete(r.byConv, key)
			}
		}
		r.lastSweep = now
	}
	source := udpEndpoint{user, from}
	switch binary.LittleEndian.Uint16(payload[4:6]) {
	case fecParity:
		return r.byEndpoint[source]
	case fecData:
		if len(payload) < minKCPData {
			return nil
		}
	default:
		return nil
	}
	key := udpConversation{user, binary.LittleEndian.Uint32(payload[8:12])}
	seq := binary.LittleEndian.Uint32(payload)
	p := r.byConv[key]
	if p == nil {
		if len(r.byConv) >= 4096 {
			return nil
		}
		r.nextID++
		p = &udpPeer{key: key, id: "cm-udp-" + strconv.FormatUint(r.nextID, 10), endpoint: from, seq: seq}
		r.byConv[key] = p
		r.byEndpoint[source] = p
	} else if int32(seq-p.seq) > 0 {
		if p.endpoint != from {
			r.forgetEndpoint(p)
			p.endpoint = from
		}
		p.seq = seq
		r.byEndpoint[source] = p
	}
	p.seen = now
	return p
}

func (r *udpPeers) destination(p *udpPeer) *net.UDPAddr {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byConv[p.key] != p || p.key.user.retired.Load() {
		return nil
	}
	return net.UDPAddrFromAddrPort(p.endpoint)
}
