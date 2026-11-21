package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"golang.org/x/sys/windows"
	"net"
	"net/netip"
	"time"
	"unsafe"
)

var icmpDLL = windows.NewLazySystemDLL("iphlpapi.dll")

func icmpLatency(ip netip.Addr) (int64, error) {
	family := "IcmpCreateFile"
	if ip.Is6() {
		family = "Icmp6CreateFile"
	}
	h, _, e := icmpDLL.NewProc(family).Call()
	if h == ^uintptr(0) || h == 0 {
		return -1, e
	}
	defer icmpDLL.NewProc("IcmpCloseHandle").Call(h)
	var reply [512]byte
	payload := [4]byte{'C', 'M', 'R', 'T'}
	var n uintptr
	status := 4
	if ip.Is4() {
		a := ip.As4()
		n, _, e = icmpDLL.NewProc("IcmpSendEcho").Call(h, uintptr(binary.LittleEndian.Uint32(a[:])), uintptr(unsafe.Pointer(&payload[0])), 4, 0, uintptr(unsafe.Pointer(&reply[0])), uintptr(len(reply)), 2500)
	} else {
		src := windows.RawSockaddrInet6{Family: windows.AF_INET6}
		dst := windows.RawSockaddrInet6{Family: windows.AF_INET6, Addr: ip.As16()}
		n, _, e = icmpDLL.NewProc("Icmp6SendEcho2").Call(h, 0, 0, 0, uintptr(unsafe.Pointer(&src)), uintptr(unsafe.Pointer(&dst)), uintptr(unsafe.Pointer(&payload[0])), 4, 0, uintptr(unsafe.Pointer(&reply[0])), uintptr(len(reply)), 2500)
		status = 28
	}
	if n == 0 {
		return -1, e
	}
	if s := binary.LittleEndian.Uint32(reply[status:]); s != 0 {
		return -1, fmt.Errorf("ICMP 状态 %d", s)
	}
	return int64(binary.LittleEndian.Uint32(reply[status+4:])), nil
}
func profileLatency(mode string, cm *ClientMux) (int64, error) {
	if mode == "" || mode == "CM" {
		return MeasureTunnelLatency(cm)
	}
	if cm.entry != nil {
		cm = cm.entry
	}
	ip, ok := netip.AddrFromSlice(cm.serverIP)
	if !ok {
		return -1, fmt.Errorf("无效入口 IP")
	}
	ip = ip.Unmap()
	if mode == "ICMP" {
		return icmpLatency(ip)
	}
	port := cm.tlsNode.TLSPort
	if port == 0 {
		return -1, fmt.Errorf("仅 UDP 节点没有 TCP 端口")
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var d net.Dialer
	c, e := d.DialContext(ctx, "tcp", netip.AddrPortFrom(ip, uint16(port)).String())
	if e != nil {
		return -1, e
	}
	c.Close()
	return time.Since(start).Milliseconds(), nil
}
