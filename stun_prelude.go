package main

import (
	"crypto/rand"
	"encoding/binary"
	"net"
)

const (
	stunHeaderLen = 20
	stunMagic     = 0x2112A442
)

func BuildSTUNBindingRequest() []byte {
	pkt := make([]byte, stunHeaderLen)
	binary.BigEndian.PutUint16(pkt[0:2], 0x0001)
	binary.BigEndian.PutUint16(pkt[2:4], 0x0000)
	binary.BigEndian.PutUint32(pkt[4:8], stunMagic)
	_, _ = rand.Read(pkt[8:20])
	return pkt
}

func IsSTUNBindingRequest(b []byte) bool {
	return len(b) >= stunHeaderLen &&
		binary.BigEndian.Uint16(b[0:2]) == 0x0001 &&
		binary.BigEndian.Uint32(b[4:8]) == stunMagic
}

func BuildSTUNBindingResponse(req []byte, remote *net.UDPAddr) []byte {
	resp := make([]byte, 32)
	binary.BigEndian.PutUint16(resp[0:2], 0x0101)
	binary.BigEndian.PutUint16(resp[2:4], 12)
	binary.BigEndian.PutUint32(resp[4:8], stunMagic)
	copy(resp[8:20], req[8:20])
	binary.BigEndian.PutUint16(resp[20:22], 0x0020)
	binary.BigEndian.PutUint16(resp[22:24], 8)
	resp[24] = 0x00
	resp[25] = 0x01
	xPort := uint16(remote.Port) ^ uint16(stunMagic>>16)
	binary.BigEndian.PutUint16(resp[26:28], xPort)
	if ip4 := remote.IP.To4(); ip4 != nil {
		xIP := binary.BigEndian.Uint32(ip4) ^ stunMagic
		binary.BigEndian.PutUint32(resp[28:32], xIP)
	}
	return resp
}
