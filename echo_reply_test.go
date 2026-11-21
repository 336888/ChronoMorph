package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestLocalEchoReplies(t *testing.T) {
	for _, v := range []byte{4, 6} {
		off := 20
		if v == 6 {
			off = 40
		}
		b := make([]byte, off+13)
		if v == 4 {
			b[0] = 0x45
			b[9] = 1
			binary.BigEndian.PutUint16(b[2:], uint16(len(b)))
			copy(b[12:20], []byte{10, 0, 0, 2, 192, 0, 2, 1})
			b[off] = 8
		} else {
			b[0] = 0x60
			b[6] = 58
			binary.BigEndian.PutUint16(b[4:], 13)
			b[8] = 0xfd
			b[23] = 2
			b[24] = 0x20
			b[25] = 1
			b[39] = 9
			b[off] = 128
		}
		copy(b[off+4:], []byte("idseqtest"))
		r := localEchoReply(b, off)
		if len(r) != len(b) || !bytes.Equal(r[off+4:], b[off+4:]) {
			t.Fatal(v, r)
		}
		var pseudo uint32
		if v == 4 {
			if r[off] != 0 || echoSum(r[:20], 0) != 65535 || !bytes.Equal(r[12:16], b[16:20]) {
				t.Fatal("IPv4 header")
			}
		} else {
			if r[off] != 129 || !bytes.Equal(r[8:24], b[24:40]) {
				t.Fatal("IPv6 header")
			}
			pseudo = uint32(echoSum(r[8:40], 0)) + 13 + 58
		}
		if echoSum(r[off:], pseudo) != 65535 {
			t.Fatal("echo checksum")
		}
		if localEchoReply(b[:off+2], off) != nil {
			t.Fatal("truncated request accepted")
		}
	}
}
