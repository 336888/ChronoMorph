package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// Each direction can close independently, and all reads and writes run on
// the test clock. The TLS loopback suite exercises actual TCP and smux peers.
type testHalfConn struct {
	*io.PipeReader
	*io.PipeWriter
}

func (c testHalfConn) CloseWrite() error { return c.PipeWriter.Close() }
func (c testHalfConn) Close() error      { return errors.Join(c.PipeReader.Close(), c.PipeWriter.Close()) }

func halfConnPair() (testHalfConn, testHalfConn) {
	leftRead, rightWrite := io.Pipe()
	rightRead, leftWrite := io.Pipe()
	return testHalfConn{leftRead, leftWrite}, testHalfConn{rightRead, rightWrite}
}

func TestServerPartialRequestReleasesSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()
		beforeActive := serverLimiter.active.Load()
		beforeTimeout := serverLimiter.headerTimeouts.Load()
		done := make(chan struct{})
		go func() { handleServerStream(server); close(done) }()
		if _, err := client.Write([]byte{CmdTCP, 6, 'a'}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if got := serverLimiter.active.Load(); got != beforeActive+1 {
			t.Fatalf("active with incomplete request = %d", got)
		}
		time.Sleep(11 * time.Second)
		<-done
		if serverLimiter.active.Load() != beforeActive || serverLimiter.headerTimeouts.Load() != beforeTimeout+1 {
			t.Fatal("partial header did not time out and release its slot")
		}
	})
}

func TestServerHalfCloseProgress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, ingress := halfConnPair()
		origin, egress := halfConnPair()
		done := make(chan bool, 1)
		const idle = time.Minute
		go func() { done <- relayBidirectional(ingress, egress, idle) }()
		time.Sleep(3 * idle) // Fully open, idle streams are still valid.
		if err := client.CloseWrite(); err != nil {
			t.Fatal(err)
		}
		if data, err := io.ReadAll(origin); err != nil || len(data) != 0 {
			t.Fatalf("origin read after FIN = %q, %v", data, err)
		}
		var b [1]byte
		for i := range 6 {
			time.Sleep(idle / 2)
			if _, err := origin.Write([]byte{byte(i)}); err != nil {
				t.Fatal(err)
			}
			if _, err := io.ReadFull(client, b[:]); err != nil || b[0] != byte(i) {
				t.Fatalf("reverse byte %d = %v, %v", i, b, err)
			}
		}
		time.Sleep(idle + time.Second)
		if !<-done {
			t.Fatal("idle half-closed relay did not time out")
		}
	})
}

func TestUDPTargetResolution(t *testing.T) {
	var cache udpTargetCache
	var calls int
	now := time.Now()
	lookup := func(network, target string) (*net.UDPAddr, error) {
		calls++
		if network != "udp" || target == "missing:123" {
			return nil, errors.New("lookup failed")
		}
		return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: calls}, nil
	}
	resolve := func(target string, at time.Time) *net.UDPAddr {
		t.Helper()
		addr, err := cache.resolve(target, at, lookup)
		if err != nil {
			t.Fatal(err)
		}
		return addr
	}
	first := resolve("example.test:80", now)
	if resolve("example.test:80", now.Add(udpCacheLifetime-1)) != first || calls != 1 {
		t.Fatal("destination resolved twice within its lifetime")
	}
	if resolve("example.test:80", now.Add(udpCacheLifetime)) == first {
		t.Fatal("expired destination reused")
	}
	resolve("example.test:443", now)
	if calls != 3 {
		t.Fatal("ports shared an entry")
	}
	if _, err := cache.resolve("missing:123", now, lookup); err == nil {
		t.Fatal("failed lookup returned a destination")
	}
	for i := range udpCacheEntries {
		resolve(fmt.Sprintf("host-%d.test:53", i), now)
	}
	if _, ok := cache.entries["example.test:80"]; ok || len(cache.entries) != udpCacheEntries {
		t.Fatal("cache did not evict its oldest destination")
	}
}

func TestQuotaCalendarRollover(t *testing.T) {
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		cfg := []UserEntry{{Name: "test", KeyHex: strings.Repeat("ad", 32), LimitGB: 1}}
		engine, err := NewMultiUserEngine(cfg)
		if err != nil {
			t.Fatal(err)
		}
		user := engine.users[0]
		user.traffic.add(1<<30, 100)
		if !user.IsOverQuota(1) {
			t.Fatal("over-quota user allowed")
		}
		path := filepath.Join(dir, "traffic.json")
		saveTraffic(path, engine)
		now := time.Now()
		next := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())
		time.Sleep(next.Sub(now))
		if user.IsOverQuota(1) {
			t.Fatal("old quota still enforced")
		}
		loadTraffic(path, engine)
		newMonth := trafficMonth(time.Now())
		if got := user.traffic.totals(newMonth); got != [2]uint64{} {
			t.Fatalf("old usage loaded: %v", got)
		}
		var wg sync.WaitGroup
		for range 20 {
			wg.Go(func() { user.traffic.add(3, 7) })
		}
		wg.Wait()
		if err := engine.ReplaceUsers(cfg); err != nil {
			t.Fatal(err)
		}
		saveTraffic(path, engine)
		var saved TrafficSnapshot
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &saved); err != nil {
			t.Fatal(err)
		}
		want := [2]uint64{60, 140}
		if saved.Users["test"] != want || saved.Month != next.Format("2006-01") {
			t.Fatalf("saved snapshot %+v", saved)
		}
		restarted, err := NewMultiUserEngine(cfg)
		if err != nil {
			t.Fatal(err)
		}
		loadTraffic(path, restarted)
		if got := restarted.users[0].traffic.totals(newMonth); got != want {
			t.Fatalf("restored %v, want %v", got, want)
		}
	})
}

func TestLocalStatsConcurrency(t *testing.T) {
	var limiter StreamLimiter
	for range MaxConcurrentStreams {
		if !limiter.Acquire() {
			t.Fatal("premature admission rejection")
		}
	}
	if limiter.Acquire() {
		t.Fatal("admitted beyond capacity")
	}
	limiter.Release()
	limiter.drainTimeouts.Add(3)
	limiter.headerTimeouts.Add(2)
	rec := httptest.NewRecorder()
	serverStatsHandler(nil, nil, &limiter).ServeHTTP(rec, httptest.NewRequest("GET", "/stats", nil))
	var stats map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]int{"active_streams": MaxConcurrentStreams - 1, "stream_limit": MaxConcurrentStreams, "rejected_streams": 1, "header_timeouts": 2, "half_close_timeouts": 3} {
		if string(stats[key]) != fmt.Sprint(want) {
			t.Errorf("%s = %s, want %d", key, stats[key], want)
		}
	}
}
