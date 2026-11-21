package main

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
)

type UserEntry struct {
	Name    string  `json:"name"`
	KeyHex  string  `json:"key"`
	LimitGB float64 `json:"limit_gb,omitempty"`
	epochs  *EpochCache
	replay  ReplayWindow
	retired atomic.Bool
	seq     atomic.Uint32
	rxByte  atomic.Uint64
	txByte  atomic.Uint64
}

func (u *UserEntry) SealPacket(payload []byte) ([]byte, error) {
	st, err := u.epochs.Get(CurrentEpoch())
	if err != nil {
		return nil, err
	}
	seq := uint16(u.seq.Add(1))
	rtpHdr := make([]byte, RTPHeaderLen)
	rtpHdr[0] = 0x80
	rtpHdr[1] = serverPayloadType
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
	return pkt, nil
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
			Name:    u.Name,
			KeyHex:  u.KeyHex,
			LimitGB: u.LimitGB,
			epochs:  NewEpochCache(raw),
		}
		list = append(list, entry)
	}
	return &MultiUserEngine{users: list}, nil
}

func (m *MultiUserEngine) OpenPacket(packet []byte) (*UserEntry, []byte, error) {
	if len(packet) < RTPHeaderLen+2+16 {
		return nil, nil, errors.New("short packet")
	}
	rtpHdr := packet[:RTPHeaderLen]
	if rtpHdr[1] != clientPayloadType && rtpHdr[1] != legacyPayloadType {
		return nil, nil, errors.New("invalid packet direction")
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
			if !u.replay.CheckAndUpdate(ep, seq) {
				return nil, nil, errors.New("replay detected")
			}
			if u.IsOverQuota(u.LimitGB) {
				return nil, nil, errors.New("quota exceeded")
			}
			pLen := int(binary.BigEndian.Uint16(plain[0:2]))
			if 2+pLen > len(plain) {
				return nil, nil, errors.New("corrupt length")
			}
			u.rxByte.Add(uint64(len(packet)))
			return u, plain[2 : 2+pLen], nil
		}
	}
	return nil, nil, errors.New("unknown user or auth failed")
}
