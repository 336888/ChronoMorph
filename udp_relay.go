package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"

	"github.com/xtaci/smux"
)

func handleSocks5UDPAssociate(tcpConn net.Conn, cm *ClientMux) {
	udpPC, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return
	}
	defer udpPC.Close()

	localAddr := udpPC.LocalAddr().(*net.UDPAddr)
	resp := []byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0, 0}
	binary.BigEndian.PutUint16(resp[8:10], uint16(localAddr.Port))
	if _, err := tcpConn.Write(resp); err != nil {
		return
	}

	stream, err := cm.OpenStream()
	if err != nil {
		return
	}
	defer stream.Close()
	if _, err := stream.Write([]byte{CmdUDP}); err != nil {
		return
	}

	go func() {
		_, _ = io.Copy(io.Discard, tcpConn)
		_ = udpPC.Close()
		_ = stream.Close()
	}()

	var clientUDPAddr atomic.Pointer[net.UDPAddr]
	go func() {
		for {
			target, payload, err := readUDPFramed(stream)
			peer := clientUDPAddr.Load()
			if err != nil || peer == nil {
				return
			}
			if pkt := packSocks5UDP(target, payload); pkt != nil {
				_, _ = udpPC.WriteTo(pkt, peer)
			}
		}
	}()

	buf := make([]byte, 65535)
	for {
		n, addr, err := udpPC.ReadFrom(buf)
		if err != nil {
			return
		}
		clientUDPAddr.Store(addr.(*net.UDPAddr))
		target, payload, ok := unpackSocks5UDP(buf[:n])
		if !ok {
			continue
		}
		if err := writeUDPFramed(stream, target, payload); err != nil {
			return
		}
	}
}

func handleServerUDPStream(stream *smux.Stream) {
	defer stream.Close()
	pc, err := net.ListenPacket("udp", ":0")
	if err != nil {
		return
	}
	defer pc.Close()

	go func() {
		buf := make([]byte, 65535)
		for {
			_ = pc.SetReadDeadline(time.Now().Add(60 * time.Second))
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				_ = stream.Close()
				return
			}
			if err := writeUDPFramed(stream, addr.String(), buf[:n]); err != nil {
				return
			}
		}
	}()

	for {
		target, payload, err := readUDPFramed(stream)
		if err != nil {
			return
		}
		raddr, err := net.ResolveUDPAddr("udp", target)
		if err != nil {
			continue
		}
		_, _ = pc.WriteTo(payload, raddr)
	}
}

func writeUDPFramed(w io.Writer, target string, payload []byte) error {
	if len(target) > 255 || len(payload) > 65535 {
		return io.ErrShortBuffer
	}
	hdr := make([]byte, 1+len(target)+2)
	hdr[0] = byte(len(target))
	copy(hdr[1:1+len(target)], target)
	binary.BigEndian.PutUint16(hdr[1+len(target):], uint16(len(payload)))
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readUDPFramed(r io.Reader) (string, []byte, error) {
	var lenBuf [1]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return "", nil, err
	}
	addrBuf := make([]byte, lenBuf[0])
	if _, err := io.ReadFull(r, addrBuf); err != nil {
		return "", nil, err
	}
	var pLenBuf [2]byte
	if _, err := io.ReadFull(r, pLenBuf[:]); err != nil {
		return "", nil, err
	}
	payload := make([]byte, binary.BigEndian.Uint16(pLenBuf[:]))
	if _, err := io.ReadFull(r, payload); err != nil {
		return "", nil, err
	}
	return string(addrBuf), payload, nil
}

func unpackSocks5UDP(b []byte) (string, []byte, bool) {
	if len(b) < 10 || b[2] != 0x00 {
		return "", nil, false
	}
	var host string
	var offset int
	switch b[3] {
	case 0x01:
		host = net.IP(b[4:8]).String()
		offset = 8
	case 0x03:
		l := int(b[4])
		if len(b) < 5+l+2 {
			return "", nil, false
		}
		host = string(b[5 : 5+l])
		offset = 5 + l
	case 0x04:
		if len(b) < 22 {
			return "", nil, false
		}
		host = net.IP(b[4:20]).String()
		offset = 20
	default:
		return "", nil, false
	}
	port := binary.BigEndian.Uint16(b[offset : offset+2])
	return net.JoinHostPort(host, fmt.Sprintf("%d", port)), b[offset+2:], true
}

func packSocks5UDP(target string, payload []byte) []byte {
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return nil
	}
	ip := net.ParseIP(host)
	var port uint16
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	out := []byte{0x00, 0x00, 0x00}
	if ip4 := ip.To4(); ip4 != nil {
		out = append(out, 0x01)
		out = append(out, ip4...)
	} else if ip16 := ip.To16(); ip16 != nil {
		out = append(out, 0x04)
		out = append(out, ip16...)
	} else {
		return nil
	}
	var p [2]byte
	binary.BigEndian.PutUint16(p[:], port)
	out = append(out, p[:]...)
	return append(out, payload...)
}
