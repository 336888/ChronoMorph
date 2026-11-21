package main

import "sync"

// Both the millisecond timestamp and the sequence number are authenticated.
// A restarted sender can reuse a sequence number at a later timestamp.
const replayMillis = 2048

type sequenceGroup struct {
	bits uint64
	id   uint16
}

type replaySlot struct {
	stamp  int64
	first  uint16
	ready  bool
	groups [4]sequenceGroup
}

type ReplayWindow struct {
	mu     sync.Mutex
	latest int64
	slots  *[replayMillis]replaySlot
}

func (rw *ReplayWindow) CheckAndUpdate(epoch int64, timestamp uint32, seq uint16) bool {
	// Use the authenticated epoch to unwrap the 32-bit millisecond clock.
	base := epoch * EpochDurationSec * 1000
	stamp := base + int64(int32(timestamp-uint32(base)))
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.slots == nil {
		rw.slots = new([replayMillis]replaySlot)
		rw.latest = stamp
	}
	if stamp <= rw.latest-replayMillis {
		return false
	}
	if stamp > rw.latest {
		rw.latest = stamp
	}
	slot := &rw.slots[uint64(stamp)%replayMillis]
	if !slot.ready || slot.stamp != stamp {
		*slot = replaySlot{stamp: stamp, first: seq, ready: true}
	}
	// Ordering is relative to the first sequence in this millisecond, so a
	// uint16 rollover stays adjacent. A group replaced by a newer group can
	// never reopen, even when a very late copy of one of its packets arrives.
	position := int32(int16(seq-slot.first)) + 32768
	id := uint16(position/64 + 1)
	group := &slot.groups[id%uint16(len(slot.groups))]
	if id < group.id {
		return false
	}
	if id > group.id {
		*group = sequenceGroup{id: id}
	}
	mask := uint64(1) << (uint(position) % 64)
	if group.bits&mask != 0 {
		return false
	}
	group.bits |= mask
	return true
}
