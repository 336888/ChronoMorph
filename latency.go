package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

func MeasureTunnelLatency(cm *ClientMux) (int64, error) {
	start := time.Now()
	stream, err := cm.OpenStream()
	if err != nil {
		return -1, err
	}
	_ = stream.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err = stream.Write([]byte{CmdPing}); err == nil {
		var reply [1]byte
		_, err = io.ReadFull(stream, reply[:])
		if err == nil && reply[0] != CmdPing {
			err = errors.New("服务端探测响应无效")
		}
	}
	_ = stream.Close()
	if err == nil {
		return time.Since(start).Milliseconds(), nil
	}
	// v1.0.0 closes a stream when it receives an unknown command.
	if errors.Is(err, io.EOF) {
		return measureLegacyLatency(cm)
	}
	return -1, fmt.Errorf("服务端未确认连接，请检查 UDP 放行、节点密钥和两端时间：%w", err)
}

func measureLegacyLatency(cm *ClientMux) (int64, error) {
	start := time.Now()
	stream, err := cm.OpenStream()
	if err != nil {
		return -1, err
	}
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(5 * time.Second))

	target := "cp.cloudflare.com:80"
	hdr := make([]byte, 2+len(target))
	hdr[0] = CmdTCP
	hdr[1] = byte(len(target))
	copy(hdr[2:], target)
	if _, err := stream.Write(hdr); err != nil {
		return -1, err
	}

	req := "HEAD /generate_204 HTTP/1.1\r\nHost: cp.cloudflare.com\r\nConnection: close\r\n\r\n"
	if _, err := stream.Write([]byte(req)); err != nil {
		return -1, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(stream), nil)
	if err != nil {
		return -1, err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 204 && resp.StatusCode != 200 {
		return -1, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	return time.Since(start).Milliseconds(), nil
}
