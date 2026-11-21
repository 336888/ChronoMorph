package main

import "sync"

type ReplayWindow struct {
	mu      sync.Mutex
	epoch   int64
	maxSeq  uint16
	bitmap  uint64
	inited  bool
}

// CheckAndUpdate 64 位滑动窗口防重放校验
func (rw *ReplayWindow) CheckAndUpdate(epoch int64, seq uint16) bool {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	if !rw.inited || epoch > rw.epoch {
		rw.epoch = epoch
		rw.maxSeq = seq
		rw.bitmap = 1
		rw.inited = true
		return true
	}
	if epoch < rw.epoch {
		return false
	}

	diff := int16(seq - rw.maxSeq)
	if diff > 0 {
		shift := uint(diff)
		if shift >= 64 {
			rw.bitmap = 1
		} else {
			rw.bitmap = (rw.bitmap << shift) | 1
		}
		rw.maxSeq = seq
		return true
	}

	back := uint(-diff)
	if back >= 64 {
		return false
	}
	mask := uint64(1) << back
	if rw.bitmap&mask != 0 {
		return false
	}
	rw.bitmap |= mask
	return true
}
