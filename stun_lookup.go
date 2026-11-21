package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"time"
)

func stunMapping(server string) (netip.AddrPort, error) {
	var req [20]byte
	req[1] = 1
	binary.BigEndian.PutUint32(req[4:8], 0x2112a442)
	if _, e := rand.Read(req[8:]); e != nil {
		return netip.AddrPort{}, e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var d net.Dialer
	c, e := d.DialContext(ctx, "udp", server)
	if e != nil {
		return netip.AddrPort{}, e
	}
	defer c.Close()
	deadline, _ := ctx.Deadline()
	c.SetDeadline(deadline)
	if _, e = c.Write(req[:]); e != nil {
		return netip.AddrPort{}, e
	}
	b := make([]byte, 2048)
	n, e := c.Read(b)
	if e != nil {
		return netip.AddrPort{}, e
	}
	return parseMapping(b[:n], req[:])
}
func parseMapping(b, req []byte) (netip.AddrPort, error) {
	bad := fmt.Errorf("STUN 返回无效或未提供映射地址")
	if len(b) < 20 || len(req) != 20 || binary.BigEndian.Uint16(b) != 0x101 || !bytes.Equal(b[4:20], req[4:20]) || int(binary.BigEndian.Uint16(b[2:])) != len(b)-20 {
		return netip.AddrPort{}, bad
	}
	for i := 20; i+4 <= len(b); {
		typ, n := binary.BigEndian.Uint16(b[i:]), int(binary.BigEndian.Uint16(b[i+2:]))
		i += 4
		if i+n > len(b) {
			break
		}
		v := b[i : i+n]
		i += (n + 3) &^ 3
		if typ != 0x20 && typ != 1 || n != 8 && n != 20 || v[0] != 0 || v[1] != 1 && v[1] != 2 || v[1] == 1 && n != 8 || v[1] == 2 && n != 20 {
			continue
		}
		port := binary.BigEndian.Uint16(v[2:])
		addr := append([]byte(nil), v[4:]...)
		if typ == 0x20 {
			port ^= 0x2112
			for j := range addr {
				addr[j] ^= req[4+j]
			}
		}
		a, _ := netip.AddrFromSlice(addr)
		if port == 0 || a.IsUnspecified() {
			continue
		}
		return netip.AddrPortFrom(a, port), nil
	}
	return netip.AddrPort{}, bad
}
