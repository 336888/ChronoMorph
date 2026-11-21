package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/xjasonlyu/tun2socks/v2/transport/socks5"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// Route only the two documentation addresses into Wintun. Translate them at
// the test proxy to loopback origins so the exit on this same machine cannot
// route the test packets back into its own virtual adapter.
func serveTestProxy(c net.Conn, cm *ClientMux, tcp, udp string) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(15 * time.Second))
	var hello [2]byte
	if _, e := io.ReadFull(c, hello[:]); e != nil {
		return
	}
	if _, e := io.CopyN(io.Discard, c, int64(hello[1])); e != nil {
		return
	}
	if _, e := c.Write([]byte{5, 0}); e != nil {
		return
	}
	var h [3]byte
	if _, e := io.ReadFull(c, h[:]); e != nil {
		return
	}
	if _, e := socks5.ReadAddr(c, make([]byte, socks5.MaxAddrLen)); e != nil {
		return
	}
	if h[1] == 1 {
		remote, e := dialWithRouting(tcp, cm, nil)
		if e != nil {
			return
		}
		defer remote.Close()
		if _, e = c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); e != nil {
			return
		}
		pipeBidirectional(c, remote)
		return
	}
	if h[1] != 3 {
		return
	}
	pc, e := net.ListenPacket("udp4", "127.0.0.1:0")
	if e != nil {
		return
	}
	defer pc.Close()
	lane, e := cm.openUDPRelayStream()
	if e != nil {
		return
	}
	defer lane.Close()
	if _, e = lane.Write([]byte{CmdUDP}); e != nil {
		return
	}
	port := pc.LocalAddr().(*net.UDPAddr).Port
	if _, e = c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, byte(port >> 8), byte(port)}); e != nil {
		return
	}
	go func() { _, _ = io.Copy(io.Discard, c); pc.Close(); lane.Close() }()
	buf := make([]byte, 2048)
	for {
		n, from, e := pc.ReadFrom(buf)
		if e != nil {
			return
		}
		dst, packet, ok := unpackSocks5UDP(buf[:n])
		if !ok {
			return
		}
		if writeUDPFramed(lane, udp, packet) != nil {
			return
		}
		_, payload, e := readUDPFramed(lane)
		if e != nil {
			return
		}
		if _, e = pc.WriteTo(packSocks5UDP(dst, payload), from); e != nil {
			return
		}
	}
}

func TestWintunChainRouteRecovery(t *testing.T) {
	if os.Getenv("CHRONOMORPH_TEST_WINTUN") != "1" {
		t.Skip("signed Wintun runner required")
	}
	entry, _, a := recoveryTLSNode(t, false)
	a()
	exit, _, b := recoveryTLSNode(t, false)
	b()
	backup, _, d := recoveryTLSNode(t, false)
	d()
	profile := func(cm *ClientMux) NodeProfile {
		return NodeProfile{ServerIP: "127.0.0.1", MasterKey: strings.Repeat("a1", 32), TLSNode: cm.tlsNode}
	}
	first, second, third := profile(entry), profile(exit), profile(backup)
	cfg := &Config{ServerIP: second.ServerIP, MasterKey: second.MasterKey, TLSNode: second.TLSNode, Profiles: []NodeProfile{first, second, third}, ActiveIndex: 1, ChainEntry: first.entryID(), LockEntryType: true}
	cm, err := cfg.nodeMux(cfg.ServerIP, cfg.MasterKey, cfg.TLSNode)
	if err != nil {
		t.Fatal(err)
	}
	defer cm.Close()
	r := NewRouter(nil, nil)
	g := &GUIController{cm: cm, cfg: cfg, running: true, router: r, cfgPath: filepath.Join(t.TempDir(), "client.json")}
	var client atomic.Pointer[ClientMux]
	client.Store(cm)
	tcp, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	go func() {
		for {
			c, e := tcp.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	go func() {
		var buf [2048]byte
		for {
			n, from, e := udp.ReadFrom(buf[:])
			if e != nil {
				return
			}
			udp.WriteTo(buf[:n], from)
		}
	}()
	proxy, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	go func() {
		for {
			c, e := proxy.Accept()
			if e != nil {
				return
			}
			go serveTestProxy(c, client.Load(), tcp.Addr().String(), udp.LocalAddr().String())
		}
	}()
	bad, _ := testDNSOrigin(t, dns.RcodeServerFailure)
	good, _ := testDNSOrigin(t, dns.RcodeSuccess)
	prefixes := []netip.Prefix{netip.MustParsePrefix("192.0.2.1/32"), netip.MustParsePrefix("2001:db8::1/128")}
	start := func(block bool) {
		t.Helper()
		if err := GlobalTUN.start(proxy.Addr().String(), []*ClientMux{client.Load()}, r, "127.0.0.1:0", []string{bad, good}, prefixes, block); err != nil {
			t.Fatal(err)
		}
	}
	start(false)
	defer GlobalTUN.StopTUN()
	// Allow Windows duplicate address detection to complete.
	roundtrip := func(proto, ip string, port int) error {
		c, e := net.DialTimeout(proto, net.JoinHostPort(ip, fmt.Sprint(port)), time.Second)
		if e != nil {
			return e
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(time.Second))
		var p [8]byte
		binary.BigEndian.PutUint64(p[:], uint64(port))
		if _, e = c.Write(p[:]); e != nil {
			return e
		}
		var reply [8]byte
		_, e = io.ReadFull(c, reply[:])
		if e == nil && !bytes.Equal(reply[:], p[:]) {
			return fmt.Errorf("echo mismatch")
		}
		return e
	}
	for _, proto := range []string{"tcp", "udp"} {
		for i, p := range prefixes {
			end := time.Now().Add(8 * time.Second)
			for {
				err = roundtrip(proto, p.Addr().String(), 43210+i)
				if err == nil {
					break
				}
				if time.Now().After(end) {
					t.Fatalf("%s %s: %v; %s", proto, p, err, GlobalTUN.diagnostics())
				}
				time.Sleep(150 * time.Millisecond)
			}
		}
	}
	load := make(chan error, 1)
	go func() {
		s, e := openQualityBlock(cm)
		if e == nil {
			defer s.Close()
			_, e = io.CopyN(io.Discard, s, qualityBlockSize)
		}
		load <- e
	}()
	if e := roundtrip("udp", "192.0.2.1", 43214); e != nil {
		t.Fatal(e)
	}
	if e := <-load; e != nil {
		t.Fatal(e)
	}
	cm.Close()
	since := time.Now()
	g.tryStandby(nil, cm, cfg)
	if g.cm == cm || g.cfg.ActiveIndex != 2 {
		t.Fatal("failed TUN standby")
	}
	client.Store(g.cm)
	defer g.cm.Close()
	if e := roundtrip("udp", "2001:db8::1", 43217); e != nil {
		t.Fatal(e)
	}
	check := g.networkCheck()
	if !strings.Contains(check, "IPv4 路由 TUN · IPv6 路由 TUN") || !strings.Contains(check, "NOERROR") {
		t.Fatal(check)
	}
	t.Logf("Wintun IPv4+IPv6 TCP/UDP over two CM hops; authenticated exit failover and subsequent UDP/DNS in %v (after detection); %s", time.Since(since), check)
	GlobalTUN.StopTUN()
	start(true)
	if e := roundtrip("udp", "2001:db8::1", 43219); e == nil {
		t.Fatal("TUN IPv6 block bypassed")
	}
	if e := roundtrip("tcp", "192.0.2.1", 43220); e != nil {
		t.Fatal(e)
	}
}

func TestWindowsGatewayMetricsChange(t *testing.T) {
	ip := netip.MustParseAddr("192.0.2.66")
	var rows []winipcfg.MibIPforwardRow2
	ifaces := []winipcfg.MibIPInterfaceRow{{InterfaceLUID: 1, Connected: true, Metric: 10}, {InterfaceLUID: 2, Connected: true, Metric: 20}, {InterfaceLUID: 3, Connected: true}}
	for i := 1; i <= 3; i++ {
		var r winipcfg.MibIPforwardRow2
		r.InterfaceLUID = winipcfg.LUID(i)
		r.DestinationPrefix.SetPrefix(netip.MustParsePrefix("0.0.0.0/0"))
		r.NextHop.SetAddr(netip.MustParseAddr("192.0.2.1"))
		rows = append(rows, r)
	}
	old, ok := selectUplink(ip, 3, rows, ifaces, nil)
	if !ok || old.nic != 1 {
		t.Fatal(old)
	}
	pinned := rows[0]
	pinned.DestinationPrefix.SetPrefix(netip.PrefixFrom(ip, 32))
	rows = append(rows, pinned)
	ifaces[1].Metric = 3
	next, ok := selectUplink(ip, 3, rows, ifaces, map[hostRoute]bool{old: true})
	if !ok || next.nic != 2 {
		t.Fatal("stale gateway", next)
	}
	ifaces[0].Connected = false
	ifaces[1].Connected = false
	if _, ok = selectUplink(ip, 3, rows, ifaces, nil); ok {
		t.Fatal("offline or virtual uplink selected")
	}
}
