package main

import (
	"github.com/miekg/dns"
	"net/netip"
	"os"
	"testing"
	"time"
)

func TestWintunSplitDNSAndEcho(t *testing.T) {
	if os.Getenv("CHRONOMORPH_TEST_WINTUN") != "1" {
		t.Skip("signed Wintun runner required")
	}
	cm, _, serve := recoveryTLSNode(t, false)
	serve()
	defer cm.Close()
	china, cn := testDNSOrigin(t, dns.RcodeSuccess)
	other, rest := testDNSOrigin(t, dns.RcodeSuccess)
	opts := (&Config{}).networkOptions()
	opts.Address = "10.0.236.10"
	opts.Gateway = "10.0.236.1"
	opts.Echo = true
	opts.EchoDelay = 35
	opts.ChinaDNS = china
	opts.ProxyDNS = false
	ip := netip.MustParseAddr("192.0.2.88")
	prefix := []netip.Prefix{netip.PrefixFrom(ip, 32)}
	if e := GlobalTUN.start("127.0.0.1:1", []*ClientMux{cm}, NewRouter(nil, nil), "127.0.0.1:0", []string{other}, prefix, false, opts); e != nil {
		t.Fatal(e)
	}
	defer GlobalTUN.StopTUN()
	end := time.Now().Add(9 * time.Second)
	for {
		ms, e := icmpLatency(ip)
		if e == nil && ms >= 25 {
			break
		}
		if time.Now().After(end) {
			t.Fatal("delayed TUN echo", ms, e)
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, v := range []struct{ proto, domain string }{{"udp", "sample.cn."}, {"tcp", "sample.net."}} {
		q := new(dns.Msg).SetQuestion(v.domain, dns.TypeA)
		r, _, e := (&dns.Client{Net: v.proto, Timeout: 3 * time.Second}).Exchange(q, netip.AddrPortFrom(ip, 53).String())
		if e != nil || r.Rcode != dns.RcodeSuccess {
			t.Fatal(v, r, e)
		}
	}
	if cn.Load() != 1 || rest.Load() != 1 {
		t.Fatal("wrong upstream", cn.Load(), rest.Load())
	}
}
