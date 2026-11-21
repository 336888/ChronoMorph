package main

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// SOCKS4 carries an IPv4 address. SOCKS4a uses 0.0.0.x followed by a hostname.
func handleSocks4(client net.Conn, br *bufio.Reader, cm *ClientMux, router *Router) {
	reject := [8]byte{0, 91}
	var h [8]byte
	_ = client.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.ReadFull(br, h[:]); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(h[2:4])
	if h[0] != 4 || h[1] != 1 || port == 0 {
		_, _ = client.Write(reject[:])
		return
	}
	// USERID is advisory; it is never stored or forwarded.
	user, err := br.ReadSlice(0)
	if err != nil || len(user) > 256 {
		_, _ = client.Write(reject[:])
		return
	}
	host := net.IP(h[4:8]).String()
	protocol := "SOCKS4"
	if h[4] == 0 && h[5] == 0 && h[6] == 0 && h[7] != 0 {
		protocol = "SOCKS4a"
		name, err := br.ReadSlice(0)
		if err != nil || len(name) < 2 || len(name) > 255 {
			_, _ = client.Write(reject[:])
			return
		}
		host = string(name[:len(name)-1])
		for _, c := range strings.TrimSuffix(host, ".") {
			if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
				_, _ = client.Write(reject[:])
				return
			}
		}
		if strings.Trim(host, ".") == "" {
			_, _ = client.Write(reject[:])
			return
		}
	}
	_ = client.SetReadDeadline(time.Time{})
	target := net.JoinHostPort(host, strconv.Itoa(int(port)))
	remote, err := dialProxyTCP(target, cm, router, client, protocol)
	if err != nil {
		if cm != nil {
			cm.logProxyFailure(err)
		}
		_, _ = client.Write(reject[:])
		return
	}
	defer remote.Close()
	ok := [8]byte{0, 90}
	if _, err = client.Write(ok[:]); err != nil {
		return
	}
	pipeBidirectional(client, remote)
}
