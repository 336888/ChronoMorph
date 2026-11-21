package main

import (
	"crypto/ed25519"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
)

type UserEntry struct {
	Name      string  `json:"name"`
	KeyHex    string  `json:"key"`
	LimitGB   float64 `json:"limit_gb,omitempty"`
	tlsPublic ed25519.PublicKey
	epochs    *EpochCache
	replay    ReplayWindow
	retired   atomic.Bool
	seq       atomic.Uint32
	traffic   monthlyTraffic
}

func (u *UserEntry) SealPacket(payload []byte) ([]byte, error) {
	packet, _, err := sealPacket(u.epochs, &u.seq, serverPayloadType, payload)
	return packet, err
}

type MultiUserEngine struct {
	mu    sync.RWMutex
	users []*UserEntry
}

func NewMultiUserEngine(cfgs []UserEntry) (*MultiUserEngine, error) {
	var list []*UserEntry
	seen := make(map[string]bool, len(cfgs))
	for i := range cfgs {
		u := &cfgs[i]
		raw, err := hex.DecodeString(u.KeyHex)
		if err != nil || len(raw) != 32 {
			return nil, errors.New("用户密钥无效: " + u.Name)
		}
		key := strings.ToLower(u.KeyHex)
		if seen[key] {
			return nil, errors.New("多名用户使用相同的密钥")
		}
		seen[key] = true
		entry := &UserEntry{
			Name:      u.Name,
			KeyHex:    u.KeyHex,
			LimitGB:   u.LimitGB,
			epochs:    NewEpochCache(raw),
			tlsPublic: tlsClientKey(raw).Public().(ed25519.PublicKey),
		}
		list = append(list, entry)
	}
	return &MultiUserEngine{users: list}, nil
}

var errUnauthenticatedPacket = errors.New("unauthenticated UDP packet")

func (m *MultiUserEngine) OpenPacket(packet []byte) (*UserEntry, []byte, error) {
	if len(packet) < RTPHeaderLen+2+16 {
		return nil, nil, errUnauthenticatedPacket
	}
	rtpHdr := packet[:RTPHeaderLen]
	if rtpHdr[1] != clientPayloadType && rtpHdr[1] != legacyPayloadType {
		return nil, nil, errUnauthenticatedPacket
	}
	ciphertext := packet[RTPHeaderLen:]
	pktSSRC := binary.BigEndian.Uint32(rtpHdr[8:12])
	seq := binary.BigEndian.Uint16(rtpHdr[2:4])
	nowEpoch := CurrentEpoch()

	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, u := range m.users {
		for _, ep := range []int64{nowEpoch, nowEpoch - 1, nowEpoch + 1} {
			st, err := u.epochs.Get(ep)
			if err != nil || pktSSRC != st.ssrc {
				continue
			}
			plain, err := st.aead.Open(nil, rtpHdr, ciphertext, rtpHdr)
			if err != nil || len(plain) < 2 {
				continue
			}
			if !u.replay.CheckAndUpdate(ep, binary.BigEndian.Uint32(rtpHdr[4:8]), seq) {
				return nil, nil, errors.New("replay detected")
			}
			if u.IsOverQuota(u.LimitGB) {
				return nil, nil, errors.New("quota exceeded")
			}
			pLen := int(binary.BigEndian.Uint16(plain[0:2]))
			if 2+pLen > len(plain) {
				return nil, nil, errors.New("corrupt length")
			}
			u.traffic.add(uint64(len(packet)), 0)
			return u, plain[2 : 2+pLen], nil
		}
	}
	return nil, nil, errUnauthenticatedPacket
}
