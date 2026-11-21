package main

import (
	"context"
	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWindowsProcessRoutesAndICMP(t *testing.T) {
	listener, e := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	client, e := net.Dial("tcp4", listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	peer, e := listener.Accept()
	if e != nil {
		t.Fatal(e)
	}
	defer peer.Close()
	source := client.LocalAddr().(*net.TCPAddr).AddrPort()
	dest := client.RemoteAddr().(*net.TCPAddr).AddrPort()
	m := &M.Metadata{Network: M.TCP, SrcIP: source.Addr(), SrcPort: source.Port(), DstIP: dest.Addr(), DstPort: dest.Port()}
	path, _ := os.Executable()
	p := tunPolicy{opts: (&Config{}).networkOptions()}
	p.opts.Processes = []string{filepath.Base(path)}
	if v, e := p.path(m); v != pathProxy || e != nil {
		t.Fatal(v, e, flowPID(m))
	}
	p.opts.Processes = []string{"different-test.exe"}
	if v, e := p.path(m); v != pathPhysical || e != nil {
		t.Fatal(v, e)
	}
	sock, e := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if e != nil {
		t.Fatal(e)
	}
	defer sock.Close()
	m.Network = M.UDP
	m.SrcPort = sock.LocalAddr().(*net.UDPAddr).AddrPort().Port()
	if got := flowPID(m); got != uint32(os.Getpid()) {
		t.Fatal("UDP owner", got)
	}
	p.opts.Processes = nil
	m.DstPort = 53
	if v, e := p.path(m); v != pathDNS || e != nil {
		t.Fatal(v, e)
	}
	p.opts.DNS = false
	p.bypass = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	if v, e := p.path(m); v != pathPhysical || e != nil {
		t.Fatal(v, e)
	}
	if _, e := icmpLatency(netip.MustParseAddr("127.0.0.1")); e != nil {
		t.Fatal("Windows ICMP", e)
	}
	pc, e := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if e != nil {
		t.Fatal(e)
	}
	defer pc.Close()
	go func() {
		b := make([]byte, 8)
		n, a, e := pc.ReadFromUDP(b)
		if e == nil {
			pc.WriteToUDP(b[:n], a)
		}
	}()
	p.resolver = pc.LocalAddr().String()
	p.opts.DNS = true
	m.DstIP = netip.MustParseAddr("192.0.2.1")
	c, e := p.DialUDP(m)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(time.Second))
	if _, e = c.WriteTo([]byte("ping"), m.UDPAddr()); e != nil {
		t.Fatal(e)
	}
	var b [8]byte
	n, a, e := c.ReadFrom(b[:])
	if e != nil || a.String() != m.DestinationAddress() || string(b[:n]) != "ping" {
		t.Fatal(a, e)
	}
	p.resolver = listener.Addr().String()
	m.Network = M.TCP
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	routed, e := p.DialContext(ctx, m)
	if e != nil {
		t.Fatal(e)
	}
	routed.Close()
}
