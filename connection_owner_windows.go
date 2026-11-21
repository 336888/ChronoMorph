package main

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

var getOwnerTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

// Match the application side of each proxy socket, including both endpoints.
// For SOCKS5 UDP the application owns the persistent TCP association.
func connectionOwners(flows []*liveFlow, old map[uint64]string) map[uint64]string {
	out := make(map[uint64]string, len(flows))
	wanted := make(map[socketPair][]uint64)
	families := make(map[uint32]bool)
	for _, f := range flows {
		if label := old[f.id]; label != "" {
			out[f.id] = label
			continue
		}
		pair := f.source
		if !pair.source.IsValid() || !pair.listener.IsValid() {
			continue
		}
		pair.source = netip.AddrPortFrom(pair.source.Addr().Unmap().WithZone(""), pair.source.Port())
		pair.listener = netip.AddrPortFrom(pair.listener.Addr().Unmap().WithZone(""), pair.listener.Port())
		wanted[pair] = append(wanted[pair], f.id)
		if pair.source.Addr().Is4() {
			families[windows.AF_INET] = true
		} else {
			families[windows.AF_INET6] = true
		}
	}
	names := make(map[uint32]string)
	for family := range families {
		buf := make([]byte, 16384)
		for {
			size := uint32(len(buf))
			rc, _, _ := getOwnerTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, uintptr(family), 4, 0)
			if rc == uintptr(windows.ERROR_INSUFFICIENT_BUFFER) && size > uint32(len(buf)) && size <= 32<<20 {
				buf = make([]byte, size)
				continue
			}
			if rc != 0 || size < 4 || size > uint32(len(buf)) {
				break
			}
			data := buf[4:size]
			// MIB_TCPROW_OWNER_PID and MIB_TCP6ROW_OWNER_PID (Windows SDK).
			stride, localIP, remoteIP, addrLen, localPort, remotePort, state, pidOffset := 24, 4, 12, 4, 8, 16, 0, 20
			if family == windows.AF_INET6 {
				stride, localIP, remoteIP, addrLen, localPort, remotePort, state, pidOffset = 56, 0, 24, 16, 20, 44, 48, 52
			}
			for n := binary.LittleEndian.Uint32(buf); n > 0 && len(data) >= stride; n-- {
				row := data[:stride]
				data = data[stride:]
				if binary.LittleEndian.Uint32(row[state:]) != 5 {
					continue
				}
				a, _ := netip.AddrFromSlice(row[localIP : localIP+addrLen])
				b, _ := netip.AddrFromSlice(row[remoteIP : remoteIP+addrLen])
				pair := socketPair{netip.AddrPortFrom(a.Unmap(), binary.BigEndian.Uint16(row[localPort:])), netip.AddrPortFrom(b.Unmap(), binary.BigEndian.Uint16(row[remotePort:]))}
				ids := wanted[pair]
				if len(ids) == 0 {
					continue
				}
				pid := binary.LittleEndian.Uint32(row[pidOffset:])
				if names[pid] == "" {
					names[pid] = processLabel(pid)
				}
				for _, id := range ids {
					out[id] = names[pid]
				}
			}
			break
		}
	}
	return out
}

func processLabel(pid uint32) string {
	if int(pid) == os.Getpid() {
		return fmt.Sprintf("CM 本机转接 (%d)", pid)
	}
	label := fmt.Sprintf("PID %d", pid)
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return label
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, 32768)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) == nil {
		return fmt.Sprintf("%s (%d)", filepath.Base(windows.UTF16ToString(buf[:n])), pid)
	}
	return label
}
