package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func waitFlows(t *testing.T, m *flowMonitor, ok func([]*liveFlow) bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok(m.flows()) {
		if time.Now().After(deadline) {
			t.Fatal("unexpected live connection state")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSocks4aRoutingAndLiveConnections(t *testing.T) {
	r := NewRouter([]string{"localhost"}, []string{"blocked.example"})
	r.live = &flowMonitor{}
	echo, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, e := echo.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	port := echo.Addr().(*net.TCPAddr).Port
	request := func(name string, cmd byte) net.Conn {
		c, s := net.Pipe()
		go handleMixedProxy(s, nil, r)
		t.Cleanup(func() { c.Close() })
		c.SetDeadline(time.Now().Add(4 * time.Second))
		h := []byte{4, cmd, byte(port >> 8), byte(port), 0, 0, 0, 1, 'u', 0}
		h = append(h, name...)
		h = append(h, 0)
		if _, err := c.Write(h); err != nil {
			t.Fatal(err)
		}
		return c
	}
	for _, c := range []net.Conn{request("blocked.example", 1), request("localhost", 2)} {
		var b [8]byte
		if _, err := io.ReadFull(c, b[:]); err != nil || b[1] != 91 {
			t.Fatalf("reject %x %v", b, err)
		}
	}
	var clients []net.Conn
	for i := 0; i < 2; i++ {
		c := request("localhost", 1)
		clients = append(clients, c)
		var b [8]byte
		if _, e := io.ReadFull(c, b[:]); e != nil || b[1] != 90 {
			t.Fatalf("grant %x %v", b, e)
		}
		if _, e := io.WriteString(c, "abc"); e != nil {
			t.Fatal(e)
		}
		if _, e := io.ReadFull(c, b[:3]); e != nil || string(b[:3]) != "abc" {
			t.Fatal(e)
		}
	}
	waitFlows(t, r.live, func(rows []*liveFlow) bool {
		return len(rows) == 2 && rows[0].up.Load() == 3 && rows[0].down.Load() == 3
	})
	newest := r.live.flows()[0]
	if newest.route != "直连" || newest.protocol != "SOCKS4a" || *newest.target.Load() != fmt.Sprintf("localhost:%d", port) {
		t.Fatal("wrong flow metadata")
	}
	if !r.live.disconnect(newest.id) {
		t.Fatal("flow missing")
	}
	var b [3]byte
	if _, e := clients[1].Read(b[:]); e == nil {
		t.Fatal("disconnect failed")
	}
	waitFlows(t, r.live, func(rows []*liveFlow) bool { return len(rows) == 1 })
	if _, e := clients[0].Write([]byte("xyz")); e != nil {
		t.Fatal(e)
	}
	if _, e := io.ReadFull(clients[0], b[:]); e != nil || string(b[:]) != "xyz" {
		t.Fatal("other flow damaged", e)
	}
}

func TestUDPAssociationLiveCounters(t *testing.T) {
	cm, _, serve := recoveryTLSNode(t, false)
	serve()
	router := NewRouter(nil, nil)
	router.live = &flowMonitor{}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		if c, e := listener.Accept(); e == nil {
			handleMixedProxy(c, cm, router)
		}
	}()
	c, err := net.Dial("tcp4", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err = c.Write([]byte{5, 1, 0, 5, 3, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	var reply [12]byte
	if _, err = io.ReadFull(c, reply[:]); err != nil || reply[0] != 5 || reply[1] != 0 || reply[2] != 5 || reply[3] != 0 {
		t.Fatal(reply, err)
	}
	to := &net.UDPAddr{IP: net.IP(reply[6:10]), Port: int(binary.BigEndian.Uint16(reply[10:]))}
	origin, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer origin.Close()
	go func() {
		var buf [32]byte
		n, from, e := origin.ReadFrom(buf[:])
		if e == nil {
			_, _ = origin.WriteTo(buf[:n], from)
		}
	}()
	app, err := net.DialUDP("udp4", nil, to)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	app.SetDeadline(time.Now().Add(4 * time.Second))
	if _, err = app.Write(packSocks5UDP(origin.LocalAddr().String(), []byte("game"))); err != nil {
		t.Fatal(err)
	}
	var b [128]byte
	n, err := app.Read(b[:])
	if err != nil {
		t.Fatal(err)
	}
	if _, p, ok := unpackSocks5UDP(b[:n]); !ok || string(p) != "game" {
		t.Fatal("bad datagram")
	}
	waitFlows(t, router.live, func(rows []*liveFlow) bool {
		return len(rows) == 1 && rows[0].up.Load() == 4 && rows[0].down.Load() == 4 && *rows[0].target.Load() == origin.LocalAddr().String() && rows[0].protocol == "SOCKS5 UDP"
	})
	router.live.disconnect(router.live.flows()[0].id)
	waitFlows(t, router.live, func(rows []*liveFlow) bool { return len(rows) == 0 })
}
