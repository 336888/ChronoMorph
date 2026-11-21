package main

import "encoding/binary"

// Older hopping clients send a bare STUN Binding request before data.
// Discard it without replying or counting it against the client IP.
func isLegacySTUNPrelude(b []byte) bool {
	return len(b) == 20 &&
		binary.BigEndian.Uint32(b[:4]) == 0x00010000 &&
		binary.BigEndian.Uint32(b[4:8]) == 0x2112a442
}
