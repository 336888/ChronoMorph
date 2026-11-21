package main

import (
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	RTPHeaderLen = 12
	// These authenticated bytes keep the two send directions in disjoint nonce spaces.
	legacyPayloadType = 0x60
	clientPayloadType = 0x61
	serverPayloadType = 0x62
	PortMin           = 20000
	PortMax           = 50000
)

type epochState struct {
	epoch int64
	aead  cipher.AEAD
	ssrc  uint32
	port  int
}

type EpochCache struct {
	masterKey []byte
	mu        sync.RWMutex
	cache     map[int64]*epochState
}

func NewEpochCache(masterKey []byte) *EpochCache {
	return &EpochCache{
		masterKey: append([]byte(nil), masterKey...),
		cache:     make(map[int64]*epochState),
	}
}

func (ec *EpochCache) Get(epoch int64) (*epochState, error) {
	ec.mu.RLock()
	if st, ok := ec.cache[epoch]; ok {
		ec.mu.RUnlock()
		return st, nil
	}
	ec.mu.RUnlock()

	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], uint64(epoch))

	mac := hmac.New(sha256.New, ec.masterKey)
	mac.Write([]byte("ChronoMorph-Epoch-v1"))
	mac.Write(eb[:])
	derived := mac.Sum(nil)

	aead, err := chacha20poly1305.New(derived)
	if err != nil {
		return nil, err
	}

	mac2 := hmac.New(sha256.New, ec.masterKey)
	mac2.Write([]byte("ChronoMorph-Meta-v1"))
	mac2.Write(eb[:])
	meta := mac2.Sum(nil)

	ssrc := binary.BigEndian.Uint32(meta[0:4])
	port := PortMin + int(binary.BigEndian.Uint16(meta[4:6]))%(PortMax-PortMin)

	st := &epochState{epoch: epoch, aead: aead, ssrc: ssrc, port: port}

	ec.mu.Lock()
	if len(ec.cache) > 8 {
		for k := range ec.cache {
			if k < epoch-2 {
				delete(ec.cache, k)
			}
		}
	}
	ec.cache[epoch] = st
	ec.mu.Unlock()
	return st, nil
}

type ChronoPacketEngine struct {
	epochs *EpochCache
	replay ReplayWindow
	seq    atomic.Uint32
}

func NewChronoPacketEngine(masterKey []byte) *ChronoPacketEngine {
	return &ChronoPacketEngine{epochs: NewEpochCache(masterKey)}
}

func (e *ChronoPacketEngine) SealAsRTP(payload []byte) ([]byte, int, error) {
	epoch := CurrentEpoch()
	st, err := e.epochs.Get(epoch)
	if err != nil {
		return nil, 0, err
	}

	seq := uint16(e.seq.Add(1))
	rtpHdr := make([]byte, RTPHeaderLen)
	rtpHdr[0] = 0x80
	rtpHdr[1] = clientPayloadType
	binary.BigEndian.PutUint16(rtpHdr[2:4], seq)
	binary.BigEndian.PutUint32(rtpHdr[4:8], uint32(CalibratedNow().UnixMilli()))
	binary.BigEndian.PutUint32(rtpHdr[8:12], st.ssrc)

	plain := make([]byte, 2+len(payload))
	binary.BigEndian.PutUint16(plain[0:2], uint16(len(payload)))
	copy(plain[2:], payload)

	ciphertext := st.aead.Seal(nil, rtpHdr, plain, rtpHdr)
	pkt := make([]byte, RTPHeaderLen+len(ciphertext))
	copy(pkt[:RTPHeaderLen], rtpHdr)
	copy(pkt[RTPHeaderLen:], ciphertext)
	return pkt, st.port, nil
}

func (e *ChronoPacketEngine) OpenFromRTP(packet []byte) ([]byte, error) {
	if len(packet) < RTPHeaderLen+2+16 {
		return nil, errors.New("short packet")
	}
	rtpHdr := packet[:RTPHeaderLen]
	if rtpHdr[1] != serverPayloadType && rtpHdr[1] != legacyPayloadType {
		return nil, errors.New("invalid packet direction")
	}
	ciphertext := packet[RTPHeaderLen:]
	pktSSRC := binary.BigEndian.Uint32(rtpHdr[8:12])
	seq := binary.BigEndian.Uint16(rtpHdr[2:4])
	nowEpoch := CurrentEpoch()

	for _, ep := range []int64{nowEpoch, nowEpoch - 1, nowEpoch + 1} {
		st, err := e.epochs.Get(ep)
		if err != nil || pktSSRC != st.ssrc {
			continue
		}
		plain, err := st.aead.Open(nil, rtpHdr, ciphertext, rtpHdr)
		if err != nil || len(plain) < 2 {
			continue
		}
		if !e.replay.CheckAndUpdate(ep, seq) {
			return nil, errors.New("replay detected")
		}
		pLen := int(binary.BigEndian.Uint16(plain[0:2]))
		if 2+pLen > len(plain) {
			return nil, errors.New("corrupt length")
		}
		return plain[2 : 2+pLen], nil
	}
	return nil, errors.New("auth failed")
}

// RTPPacketConn 实现 net.PacketConn，桥接 KCP 与 RTP 跳频层
type RTPPacketConn struct {
	raw       net.PacketConn
	isClient  bool
	serverIP  net.IP
	clientEng *ChronoPacketEngine
	multiEng  *MultiUserEngine
	guard     *AbuseGuard
	lastPort  atomic.Int32
	peerUser  sync.Map // addr string -> *UserEntry
}

func NewClientRTPPacketConn(raw net.PacketConn, serverIP net.IP, engine *ChronoPacketEngine) *RTPPacketConn {
	return &RTPPacketConn{
		raw:       raw,
		isClient:  true,
		serverIP:  serverIP,
		clientEng: engine,
	}
}

func NewServerRTPPacketConn(raw net.PacketConn, multiEng *MultiUserEngine, guard *AbuseGuard) *RTPPacketConn {
	return &RTPPacketConn{
		raw:      raw,
		isClient: false,
		multiEng: multiEng,
		guard:    guard,
	}
}

func (c *RTPPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	buf := make([]byte, 2048)
	for {
		n, addr, err := c.raw.ReadFrom(buf)
		if err != nil {
			return 0, nil, err
		}
		rawPkt := buf[:n]

		if !c.isClient {
			if c.guard != nil && c.guard.IsBanned(addr) {
				continue
			}
			if IsSTUNBindingRequest(rawPkt) {
				if ua, ok := addr.(*net.UDPAddr); ok {
					_, _ = c.raw.WriteTo(BuildSTUNBindingResponse(rawPkt, ua), addr)
				}
				continue
			}
			u, plain, err := c.multiEng.OpenPacket(rawPkt)
			if err != nil {
				if c.guard != nil {
					c.guard.RecordBadPacket(addr)
				}
				continue
			}
			c.peerUser.Store(addr.String(), u)
			copied := copy(p, plain)
			return copied, addr, nil
		}

		plain, err := c.clientEng.OpenFromRTP(rawPkt)
		if err != nil {
			continue
		}
		LiveStats.AddRx(uint64(n))
		copied := copy(p, plain)
		return copied, &net.UDPAddr{IP: c.serverIP, Port: PortMin}, nil
	}
}

func (c *RTPPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	if c.isClient {
		pkt, port, err := c.clientEng.SealAsRTP(p)
		if err != nil {
			return 0, err
		}
		dst := &net.UDPAddr{IP: c.serverIP, Port: port}
		if c.lastPort.Swap(int32(port)) != int32(port) {
			LiveStats.activePort.Store(int32(port))
			_, _ = c.raw.WriteTo(BuildSTUNBindingRequest(), dst)
		}
		_, err = c.raw.WriteTo(pkt, dst)
		if err == nil {
			LiveStats.AddTx(uint64(len(pkt)))
		}
		return len(p), err
	}

	val, ok := c.peerUser.Load(addr.String())
	if !ok {
		return len(p), nil
	}
	u := val.(*UserEntry)
	if u.retired.Load() {
		c.peerUser.CompareAndDelete(addr.String(), u)
		return len(p), nil
	}
	pkt, err := u.SealPacket(p)
	if err != nil {
		return 0, err
	}
	_, err = c.raw.WriteTo(pkt, addr)
	if err == nil {
		u.txByte.Add(uint64(len(pkt)))
	}
	return len(p), err
}

func (c *RTPPacketConn) Close() error                       { return c.raw.Close() }
func (c *RTPPacketConn) LocalAddr() net.Addr                { return c.raw.LocalAddr() }
func (c *RTPPacketConn) SetDeadline(t time.Time) error      { return c.raw.SetDeadline(t) }
func (c *RTPPacketConn) SetReadDeadline(t time.Time) error  { return c.raw.SetReadDeadline(t) }
func (c *RTPPacketConn) SetWriteDeadline(t time.Time) error { return c.raw.SetWriteDeadline(t) }
