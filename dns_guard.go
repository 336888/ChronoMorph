package main

import (
	"encoding/binary"
	"io"
	"log"
	"net"
	"time"
)

func StartSecureDNSProxy(listenAddr string, cm *ClientMux) (net.PacketConn, error) {
	pc, err := net.ListenPacket("udp", listenAddr)
	if err != nil {
		return nil, err
	}
	log.Printf("[DNS] 防污染 DNS 转发已启动 (%s -> 隧道 -> 1.1.1.1:53)", listenAddr)

	go func() {
		buf := make([]byte, 2048)
		for {
			n, clientAddr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			query := append([]byte(nil), buf[:n]...)
			go func(q []byte, addr net.Addr) {
				resp, err := resolveOverTunnelTCP(cm, q)
				if err == nil && len(resp) > 0 {
					_, _ = pc.WriteTo(resp, addr)
				}
			}(query, clientAddr)
		}
	}()
	return pc, nil
}

func resolveOverTunnelTCP(cm *ClientMux, udpQuery []byte) ([]byte, error) {
	stream, err := cm.OpenStream()
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(4 * time.Second))

	target := "1.1.1.1:53"
	hdr := make([]byte, 2+len(target))
	hdr[0] = CmdTCP
	hdr[1] = byte(len(target))
	copy(hdr[2:], target)
	if _, err := stream.Write(hdr); err != nil {
		return nil, err
	}

	tcpReq := make([]byte, 2+len(udpQuery))
	binary.BigEndian.PutUint16(tcpReq[0:2], uint16(len(udpQuery)))
	copy(tcpReq[2:], udpQuery)
	if _, err := stream.Write(tcpReq); err != nil {
		return nil, err
	}

	lenBuf := make([]byte, 2)
	if _, err := io.ReadFull(stream, lenBuf); err != nil {
		return nil, err
	}
	respLen := int(binary.BigEndian.Uint16(lenBuf))
	if respLen <= 0 || respLen > 4096 {
		return nil, io.ErrUnexpectedEOF
	}
	resp := make([]byte, respLen)
	if _, err := io.ReadFull(stream, resp); err != nil {
		return nil, err
	}
	return resp, nil
}
