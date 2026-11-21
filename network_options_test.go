package main

import (
	"encoding/binary"
	"github.com/miekg/dns"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestNetworkOptionsMigrationAndValidation(t *testing.T) {
	cfg := Config{DNSUpstreams: []string{"8.8.8.8:53"}}
	o := cfg.networkOptions()
	if e := o.validate(); e != nil || o.OtherDNS != "8.8.8.8:53" || o.Startup != -1 || !o.ProxyDNS {
		t.Fatal(o, e)
	}
	for _, s := range []string{"10.0.85.2", "10.0.86.1", "10.0.85.255", "10.0.85.0"} {
		o.Gateway = s
		if o.validate() == nil {
			t.Fatal("invalid gateway accepted", s)
		}
	}
	o.Gateway = "10.0.85.1"
	o.ChinaDNS = "tcp://223.5.5.5:53"
	o.Bypass = []string{"2001:db8::/32", "192.0.2.7"}
	if e := o.validate(); e != nil {
		t.Fatal(e)
	}
	o.Mask = "255.0.255.0"
	if o.validate() == nil {
		t.Fatal("noncontiguous mask accepted")
	}
	if _, e := dnsAddresses("udp://1.1.1.1"); e == nil {
		t.Fatal("invalid upstream accepted")
	}
}
func TestSplitDNSOverDirectSocketAndTunnel(t *testing.T) {
	cm, _, serve := recoveryTLSNode(t, false)
	serve()
	for _, direct := range []bool{true, false} {
		cn, a := testDNSOrigin(t, dns.RcodeSuccess)
		other, b := testDNSOrigin(t, dns.RcodeSuccess)
		resolver, e := startTunnelDNS("127.0.0.1:0", cm, NewRouter(nil, []string{"ads.cn"}), []string{other}, func(s *tunnelDNS) {
			s.domains = []string{"cn", "example.com"}
			s.china = []string{cn}
			if direct {
				s.directDial = func(addr string) (net.Conn, error) { return net.DialTimeout("tcp", addr, time.Second) }
			}
		})
		if e != nil {
			t.Fatal(e)
		}
		for _, name := range []string{"TEST.CN.", "a.example.com.", "notexample.com.", "ads.cn."} {
			q := new(dns.Msg).SetQuestion(name, dns.TypeA)
			r, _, e := (&dns.Client{Net: "udp", Timeout: 2 * time.Second}).Exchange(q, resolver.udp.LocalAddr().String())
			if e != nil || r.Rcode != dns.RcodeSuccess && r.Rcode != dns.RcodeNameError {
				t.Fatal(name, r, e)
			}
		}
		resolver.Close()
		if a.Load() != 2 || b.Load() != 1 {
			t.Fatal("wrong DNS group", direct, a.Load(), b.Load())
		}
	}
}
func TestSTUNMappingTransaction(t *testing.T) {
	pc, e := net.ListenPacket("udp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer pc.Close()
	go func() {
		var b [32]byte
		n, addr, e := pc.ReadFrom(b[:])
		if e != nil || n != 20 {
			return
		}
		b[0] = 1
		b[1] = 1
		b[3] = 12
		b[21] = 32
		b[23] = 8
		b[25] = 1
		binary.BigEndian.PutUint16(b[26:], 1234^0x2112)
		a := [4]byte{192, 0, 2, 1}
		for i := range a {
			b[28+i] = a[i] ^ b[4+i]
		}
		pc.WriteTo(b[:], addr)
	}()
	a, e := stunMapping(pc.LocalAddr().String())
	if e != nil || a != netip.MustParseAddrPort("192.0.2.1:1234") {
		t.Fatal(a, e)
	}
	if _, e = parseMapping([]byte(strings.Repeat("x", 32)), make([]byte, 20)); e == nil {
		t.Fatal("unrelated response accepted")
	}
}
