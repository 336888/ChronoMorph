package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync/atomic"

	"github.com/xtaci/smux"
)

func handleSocks5UDPAssociate(tcpConn net.Conn, cm *ClientMux, router *Router, requestedPort int) {
	local := tcpConn.LocalAddr().(*net.TCPAddr)
	peerIP := tcpConn.RemoteAddr().(*net.TCPAddr).IP
	udpPC, err := net.ListenUDP("udp", &net.UDPAddr{IP: local.IP, Zone: local.Zone})
	if err != nil {
		return
	}
	defer udpPC.Close()

	stream, err := cm.openUDPRelayStream()
	if err != nil {
		cm.logProxyFailure(err)
		_, _ = tcpConn.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
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

	reply := packSocks5UDP(udpPC.LocalAddr().String(), nil)
	if reply == nil {
		return
	}
	reply[0] = 5
	if _, err = tcpConn.Write(reply); err != nil {
		return
	}
	monitor := router.monitor()
	flow := monitor.begin(tcpConn, "SOCKS5 UDP", cmRoute(cm), "等待数据报", tcpConn)
	defer monitor.end(flow)
	var clientUDPAddr atomic.Pointer[net.UDPAddr]
	var direct directUDPFlows
	defer direct.close()
	replyDirect := func(target string, data []byte) {
		peer := clientUDPAddr.Load()
		if peer == nil {
			return
		}
		if pkt := packSocks5UDP(target, data); pkt != nil {
			if _, e := udpPC.WriteToUDP(pkt, peer); e == nil && flow != nil {
				flow.down.Add(uint64(len(data)))
			}
		}
	}
	go func() {
		defer udpPC.Close()
		defer tcpConn.Close()
		for {
			target, payload, err := readUDPFramed(stream)
			peer := clientUDPAddr.Load()
			if err != nil || peer == nil {
				return
			}
			if pkt := packSocks5UDP(target, payload); pkt != nil {
				if n, err := udpPC.WriteTo(pkt, peer); flow != nil && err == nil && n == len(pkt) {
					flow.down.Add(uint64(len(payload)))
				}
			}
		}
	}()

	buf := make([]byte, 65535)
	for {
		n, addr, err := udpPC.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if !addr.IP.Equal(peerIP) || (requestedPort != 0 && addr.Port != requestedPort) {
			continue
		}
		if peer := clientUDPAddr.Load(); peer != nil && peer.Port != addr.Port {
			continue
		}
		target, payload, ok := unpackSocks5UDP(buf[:n])
		if !ok || router.ShouldBlock(target) {
			continue
		}
		clientUDPAddr.CompareAndSwap(nil, addr)
		if router != nil && cm.entry == nil && !router.IsGlobalMode() && router.routes.Load().match(target).action == "DIRECT" {
			if err := direct.send(target, payload, replyDirect); err != nil {
				continue
			}
		} else if err := writeUDPFramed(stream, target, payload); err != nil {
			return
		}
		if flow != nil {
			if *flow.target.Load() != target {
				latest := target
				flow.target.Store(&latest)
			}
			flow.up.Add(uint64(len(payload)))
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
	headerLen := 1 + len(target) + 2
	frame := make([]byte, headerLen+len(payload))
	frame[0] = byte(len(target))
	copy(frame[1:], target)
	binary.BigEndian.PutUint16(frame[1+len(target):headerLen], uint16(len(payload)))
	copy(frame[headerLen:], payload)
	n, err := w.Write(frame)
	if err == nil && n != len(frame) {
		return io.ErrShortWrite
	}
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
	if len(b) < 4 || b[0] != 0 || b[1] != 0 || b[2] != 0 {
		return "", nil, false
	}
	var host string
	var offset int
	switch b[3] {
	case 0x01:
		if len(b) < 10 {
			return "", nil, false
		}
		host = net.IP(b[4:8]).String()
		offset = 8
	case 0x03:
		if len(b) < 5 || b[4] == 0 {
			return "", nil, false
		}
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
