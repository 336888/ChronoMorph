package main

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func testDNSOrigin(t *testing.T, rcode int) (string, *atomic.Int32) {
	t.Helper()
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { l.Close() })
	hits := new(atomic.Int32)
	go func() {
		for {
			c, e := l.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				w := &dns.Conn{Conn: c}
				q, e := w.ReadMsg()
				if e != nil {
					return
				}
				hits.Add(1)
				r := new(dns.Msg).SetRcode(q, rcode)
				if rcode == dns.RcodeSuccess {
					r.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 30}, A: net.IPv4(192, 0, 2, 11)}}
				}
				_ = w.WriteMsg(r)
			}()
		}
	}()
	return l.Addr().String(), hits
}

func TestTunnelDNSFailoverAndErrors(t *testing.T) {
	cm, _, serve := recoveryTLSNode(t, false)
	serve()
	bad, fails := testDNSOrigin(t, dns.RcodeServerFailure)
	good, hits := testDNSOrigin(t, dns.RcodeSuccess)
	proxy, err := startTunnelDNS("127.0.0.1:0", cm, NewRouter(nil, []string{"ads.example"}), []string{bad, good})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	ask := func(network, name string, want int) {
		t.Helper()
		q := new(dns.Msg).SetQuestion(name, dns.TypeA)
		resp, _, err := (&dns.Client{Net: network, Timeout: 2 * time.Second}).Exchange(q, proxy.udp.LocalAddr().String())
		if err != nil || resp.Rcode != want {
			t.Fatalf("%s %s: %v %v", network, name, resp, err)
		}
	}
	ask("udp", "example.com.", dns.RcodeSuccess)
	ask("tcp", "example.com.", dns.RcodeSuccess)
	ask("udp", "ads.example.", dns.RcodeNameError)
	if fails.Load() != 1 || hits.Load() != 2 {
		t.Fatal("wrong dns failover counts", fails.Load(), hits.Load())
	}
	cm.Close()
	ask("udp", "example.com.", dns.RcodeServerFailure)
	t.Log("UDP and TCP queries use authenticated tunnel; primary SERVFAIL recovered, standby reused, total failure reported")
}

func TestTransportPolicyFailover(t *testing.T) {
	profile := func(cm *ClientMux) NodeProfile {
		return NodeProfile{ServerIP: "127.0.0.1", MasterKey: strings.Repeat("a1", 32), TLSNode: cm.tlsNode}
	}
	entry, _, serveA := recoveryTLSNode(t, false)
	serveA()
	b, _, serveB := recoveryTLSNode(t, false)
	serveB()
	c, _, serveC := recoveryTLSNode(t, false)
	serveC()
	aNode, bNode, cNode := profile(entry), profile(b), profile(c)
	cfg := &Config{TLSNode: bNode.TLSNode, MasterKey: bNode.MasterKey, ServerIP: bNode.ServerIP, Profiles: []NodeProfile{aNode, bNode, cNode}, ChainEntry: aNode.entryID(), ActiveIndex: 1, LockEntryType: true}
	dead, err := cfg.nodeMux(cfg.ServerIP, cfg.MasterKey, cfg.TLSNode)
	if err != nil {
		t.Fatal(err)
	}
	dead.Close()
	g := &GUIController{running: true, cfg: cfg, cm: dead, cfgPath: filepath.Join(t.TempDir(), "client.json")}
	g.tryStandby(nil, dead, cfg)
	if g.cm == dead || g.cm.entry == nil || g.cm.entry.tlsNode != entry.tlsNode || g.cfg.ActiveIndex != 2 {
		t.Fatal("authenticated chain not preserved")
	}
	defer g.cm.Close()
	if _, err := MeasureTunnelLatency(g.cm); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.cm.routeLabel(), "TLS/TCP) →") {
		t.Fatal(g.cm.routeLabel())
	}
	if cfg.allowStandby(NodeProfile{}) {
		t.Fatal("locked TLS downgraded to UDP")
	}
	t.Log("Authenticated TLS standby changed exit, retained original entry, new requests succeed")
}

func TestQualityLoadAndPercentiles(t *testing.T) {
	cm, _, serve := recoveryTLSNode(t, false)
	serve()
	report := measureQuality(context.Background(), cm, 700*time.Millisecond, 8<<20)
	if !strings.Contains(report, "P95") || !strings.Contains(report, "8.0 MiB") || strings.Contains(report, "中断") {
		t.Fatal(report)
	}
	s := qualitySamples{last: time.Now()}
	for i := int64(1); i <= 20; i++ {
		s.observe(s.last.Add(500*time.Millisecond), i, nil)
	}
	s.observe(s.last.Add(time.Second), -1, io.EOF)
	out := s.report(s.last.Add(2 * time.Second))
	if !strings.Contains(out, "P95 19 ms") || !strings.Contains(out, "20 / 21") || !strings.Contains(out, "2.0 秒") {
		t.Fatal(out)
	}
	t.Log(report)
}

func TestSixtySecondQuality(t *testing.T) {
	if !testing.Verbose() || testing.Short() {
		t.Skip("extended benchmark")
	}
	if os.Getenv("CM_TEST_QUALITY") != "1" {
		t.Skip("set CM_TEST_QUALITY=1")
	}
	cm, _, serve := recoveryTLSNode(t, false)
	serve()
	report := measureQuality(context.Background(), cm, 30*time.Second, 128<<20)
	t.Logf("60-second quality budget on loopback: %s", report)
	if strings.Count(report, "P95") != 2 {
		t.Fatalf("missing measurements: %s", report)
	}
}
