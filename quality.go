package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"slices"
	"time"
)

const cmdQualityDownload byte = 7
const qualityBlockSize = 1 << 20

var qualityChunk [32 << 10]byte

// A single authenticated stream can request at most one MiB of diagnostic data.
func serveQualityBlock(conn net.Conn) {
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte{cmdQualityDownload}); err != nil {
		return
	}
	for range qualityBlockSize / len(qualityChunk) {
		if _, err := conn.Write(qualityChunk[:]); err != nil {
			return
		}
	}
}

func (cm *ClientMux) routeLabel() string {
	if cm == nil {
		return "尚未启动"
	}
	hop := func(n *ClientMux) string {
		return fmt.Sprintf("%s (%s)", n.serverIP, n.transportName())
	}
	if cm.entry != nil {
		return "本机 → " + hop(cm.entry) + " → " + hop(cm)
	}
	return "本机 → " + hop(cm)
}

type qualitySamples struct {
	rtts        []int64
	failures    int
	longest     time.Duration
	start, last time.Time
}

func (s *qualitySamples) observe(at time.Time, rtt int64, err error) {
	if err != nil {
		s.failures++
		return
	}
	s.rtts = append(s.rtts, rtt)
	s.longest = max(s.longest, at.Sub(s.last))
	s.last = at
}
func (s qualitySamples) report(end time.Time) string {
	if len(s.rtts) == 0 {
		return fmt.Sprintf("无有效应答，失败 %d 次", s.failures)
	}
	slices.Sort(s.rtts)
	s.longest = max(s.longest, end.Sub(s.last))
	return fmt.Sprintf("P50 %d ms · P95 %d ms · 最大采样应答间隔 %.1f 秒 · 成功 %d / %d", s.rtts[(len(s.rtts)-1)/2], s.rtts[(95*len(s.rtts)+99)/100-1], s.longest.Seconds(), len(s.rtts), len(s.rtts)+s.failures)
}

func sampleQuality(ctx context.Context, cm *ClientMux) qualitySamples {
	start := time.Now()
	s := qualitySamples{start: start, last: start}
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for ctx.Err() == nil {
		rtt, err := MeasureTunnelLatency(cm)
		if ctx.Err() != nil {
			break
		}
		s.observe(time.Now(), rtt, err)
		select {
		case <-ctx.Done():
			return s
		case <-t.C:
		}
	}
	return s
}

func openQualityBlock(cm *ClientMux) (net.Conn, error) {
	s, err := cm.OpenStream()
	if err != nil {
		return nil, err
	}
	s.SetDeadline(time.Now().Add(5 * time.Second))
	var b [1]byte
	if _, err = s.Write([]byte{cmdQualityDownload}); err == nil {
		_, err = io.ReadFull(s, b[:])
	}
	if err != nil || b[0] != cmdQualityDownload {
		s.Close()
		return nil, fmt.Errorf("出口未确认负载测试，请核对服务端版本及连通性")
	}
	return s, nil
}
func measureQuality(parent context.Context, cm *ClientMux, phase time.Duration, limit int64) string {
	game, err := cm.gameLane()
	if err != nil {
		return "应用 UDP 通道已关闭"
	}
	idle, stop := context.WithTimeout(parent, phase)
	s := sampleQuality(idle, game)
	stop()
	report := "应用 UDP 通道往返延迟\r\n空闲   " + s.report(time.Now())
	if parent.Err() != nil {
		return report
	}
	stream, err := openQualityBlock(cm)
	if err != nil {
		return report + "\r\n负载   " + err.Error()
	}
	load, cancel := context.WithTimeout(parent, phase)
	defer cancel()
	type result struct {
		n   int64
		err error
		end time.Time
	}
	done := make(chan result, 1)
	go func() {
		var n int64
		var err error
		for load.Err() == nil && n < limit {
			current := stream
			interrupt := context.AfterFunc(load, func() { current.Close() })
			read, e := io.CopyN(io.Discard, stream, min(int64(qualityBlockSize), limit-n))
			interrupt()
			stream.Close()
			n += read
			if e != nil {
				if load.Err() == nil {
					err = e
				}
				break
			}
			if load.Err() != nil || n >= limit {
				break
			}
			stream, err = openQualityBlock(cm)
			if err != nil {
				break
			}
		}
		if stream != nil {
			stream.Close()
		}
		done <- result{n, err, time.Now()}
		cancel()
	}()
	active := sampleQuality(load, game)
	r := <-done
	report += fmt.Sprintf("\r\n负载   %s\r\n实际下载 %.1f MiB，持续 %.1f 秒", active.report(r.end), float64(r.n)/(1<<20), r.end.Sub(active.start).Seconds())
	if r.err != nil {
		report += " · 下载中断"
	}
	return report
}
