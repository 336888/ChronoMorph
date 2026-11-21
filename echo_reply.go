package main

import (
	"encoding/binary"
	"net/netip"
)

// Build a local Echo Reply for a complete, unfragmented unicast request.
func localEchoReply(raw []byte, off int) []byte {
	if off < 20 || off+8 > len(raw) || raw[off+1] != 0 {
		return nil
	}
	v := raw[0] >> 4
	total := 0
	switch v {
	case 4:
		if raw[off] != 8 || int(raw[0]&15)*4 != off || binary.BigEndian.Uint16(raw[6:8])&0x3fff != 0 {
			return nil
		}
		total = int(binary.BigEndian.Uint16(raw[2:4]))
	case 6:
		if raw[off] != 128 || off < 40 {
			return nil
		}
		total = 40 + int(binary.BigEndian.Uint16(raw[4:6]))
	default:
		return nil
	}
	if total < off+8 || total > len(raw) {
		return nil
	}
	b := append([]byte(nil), raw[:total]...)
	start, width := 12, 4
	if v == 6 {
		start, width = 8, 16
	}
	a, z := b[start:start+width], b[start+width:start+2*width]
	src, _ := netip.AddrFromSlice(a)
	dst, _ := netip.AddrFromSlice(z)
	if !src.IsGlobalUnicast() || !dst.IsGlobalUnicast() {
		return nil
	}
	for i := range a {
		a[i], z[i] = z[i], a[i]
	}
	var pseudo uint32
	if v == 4 {
		b[8] = 64
		b[10], b[11] = 0, 0
		binary.BigEndian.PutUint16(b[10:], ^echoSum(b[:off], 0))
		b[off] = 0
	} else {
		b[7] = 64
		b[off] = 129
		pseudo = uint32(echoSum(b[8:40], 0)) + uint32(total-off) + 58
	}
	b[off+2], b[off+3] = 0, 0
	binary.BigEndian.PutUint16(b[off+2:], ^echoSum(b[off:], pseudo))
	return b
}
func echoSum(b []byte, sum uint32) uint16 {
	for len(b) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(b))
		b = b[2:]
	}
	if len(b) > 0 {
		sum += uint32(b[0]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 65535) + (sum >> 16)
	}
	return uint16(sum)
}
