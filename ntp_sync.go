package main

import (
	"context"
	"encoding/binary"
	"log"
	"net"
	"sync/atomic"
	"time"
)

const (
	EpochDurationSec = 30
	ntpEpochOffset   = 2208988800
)

var timeOffsetNs atomic.Int64

// Only the time-based UDP transport needs an external clock. Closing a mux
// also stops its outstanding clock lookup before a new transport policy takes effect.
func (cm *ClientMux) startClockSync() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.closed || cm.packetEngine == nil {
		return
	}
	if cm.clockDone != nil {
		select {
		case <-cm.clockDone:
		default:
			return
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	cm.stopClock, cm.clockDone = cancel, done
	go func() {
		defer close(done)
		defer cancel()
		syncTimeOffset(ctx)
	}()
}

func syncTimeOffset(ctx context.Context) {
	servers := []string{"pool.ntp.org:123", "time.cloudflare.com:123", "ntp.aliyun.com:123"}
	for _, srv := range servers {
		if ctx.Err() != nil {
			return
		}
		if off, err := querySNTP(ctx, srv); err == nil {
			timeOffsetNs.Store(off.Nanoseconds())
			log.Printf("[NTP] 时钟校准成功 (%s), 偏移: %v", srv, off.Round(time.Millisecond))
			return
		}
	}
}

func querySNTP(ctx context.Context, addr string) (time.Duration, error) {
	dialer := net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialer.DialContext(ctx, "udp", addr)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	req := make([]byte, 48)
	req[0] = 0x1b
	t0 := time.Now()
	if _, err := conn.Write(req); err != nil {
		return 0, err
	}
	resp := make([]byte, 48)
	if _, err := conn.Read(resp); err != nil {
		return 0, err
	}
	t1 := time.Now()

	secs := uint64(binary.BigEndian.Uint32(resp[40:44])) - ntpEpochOffset
	frac := uint64(binary.BigEndian.Uint32(resp[44:48]))
	nsec := (frac * 1e9) >> 32
	serverTime := time.Unix(int64(secs), int64(nsec))
	midLocal := t0.Add(t1.Sub(t0) / 2)
	return serverTime.Sub(midLocal), nil
}

func CalibratedNow() time.Time {
	return time.Now().Add(time.Duration(timeOffsetNs.Load()))
}

func CurrentEpoch() int64 {
	return CalibratedNow().Unix() / EpochDurationSec
}

func HopCountdownSec() int64 {
	return EpochDurationSec - (CalibratedNow().Unix() % EpochDurationSec)
}
