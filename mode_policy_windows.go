package main

import (
	"fmt"
	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
	"net"
	"net/netip"
	"os"
	"slices"
	"strings"
	"sync/atomic"
)

type modeEndpoint struct {
	current atomic.Pointer[ClientMux]
	router  *Router
}

func namedProcessMatches(r *modeRules, pid uint32) (bool, error) {
	if pid == 0 {
		return false, fmt.Errorf("无法识别套接字所属进程")
	}
	// CM's own sockets must never be captured by a user rule.
	if pid == uint32(os.Getpid()) {
		return false, nil
	}
	procCache.Lock()
	defer procCache.Unlock()
	if !refreshProcessCache(pid) {
		return false, fmt.Errorf("无法读取进程表")
	}
	var chain []processImage
	for depth := 0; pid != 0 && depth < 32; depth++ {
		p, ok := procCache.entries[pid]
		if !ok {
			if depth == 0 {
				return false, fmt.Errorf("进程已退出")
			}
			break
		}
		img := processImage{name: p.name}
		if path, ok := procCache.paths[pid]; ok {
			img.path = path
		} else if r.needsPath {
			h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
			if e == nil {
				var buf [32768]uint16
				n := uint32(len(buf))
				if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) == nil {
					img.path = windows.UTF16ToString(buf[:n])
				}
				windows.CloseHandle(h)
			}
			procCache.paths[pid] = img.path
		}
		chain = append(chain, img)
		if !r.mode.Children || p.parent == pid {
			break
		}
		pid = p.parent
	}
	return r.selects(chain)
}
func modeProxyOwner(c net.Conn, r *modeRules) (bool, error) {
	a, aok := c.RemoteAddr().(*net.TCPAddr)
	b, bok := c.LocalAddr().(*net.TCPAddr)
	if !aok || !bok || !a.IP.IsLoopback() {
		return false, fmt.Errorf("只支持本机进程的回环代理请求")
	}
	m := &M.Metadata{Network: M.TCP, SrcIP: a.AddrPort().Addr().Unmap(), SrcPort: uint16(a.Port), DstIP: b.AddrPort().Addr().Unmap(), DstPort: uint16(b.Port)}
	return namedProcessMatches(r, flowPID(m))
}
func modeLAN(a netip.Addr) bool { return a.IsPrivate() || a.IsLinkLocalUnicast() }
func processScoped(o NetworkOptions) bool {
	return len(o.Processes) > 0 || o.mode != nil && o.mode.mode.Kind == processMode
}
func modeDestinations(r *modeRules, def []netip.Prefix) ([]netip.Prefix, error) {
	if r == nil {
		return def, nil
	}
	if r.mode.Kind == routeMode && len(r.routes) > 0 {
		var list []netip.Prefix
		seen := map[netip.Prefix]bool{}
		for _, prefix := range r.routes {
			if !seen[prefix] {
				list = append(list, prefix)
				seen[prefix] = true
			}
		}
		return list, nil
	}
	result := slices.Clone(def)
	if r.mode.Kind == routeMode || r.mode.LAN {
		// A pair of more specific routes also captures peers on an on-link LAN.
		// Windows' local host routes and the pinned CM node retain priority.
		for _, af := range []winipcfg.AddressFamily{windows.AF_INET, windows.AF_INET6} {
			rows, e := winipcfg.GetIPForwardTable2(af)
			if e != nil {
				return nil, e
			}
			for _, row := range rows {
				p := row.DestinationPrefix.Prefix().Masked()
				if !p.IsValid() || row.Loopback || !row.NextHop.Addr().IsUnspecified() || !p.Addr().IsPrivate() || p.Bits() < 1 || p.Bits() >= p.Addr().BitLen()-1 {
					continue
				}
				bits := p.Bits() + 1
				v := p.Addr().AsSlice()
				v[p.Bits()/8] |= 0x80 >> uint(p.Bits()%8)
				other, _ := netip.AddrFromSlice(v)
				for _, q := range []netip.Prefix{netip.PrefixFrom(p.Addr(), bits), netip.PrefixFrom(other, bits)} {
					if !slices.Contains(result, q) {
						result = append(result, q)
					}
				}
			}
		}
	}
	return result, nil
}
func mergeRuleLines(text string, add []string) string {
	var out []string
	seen := map[string]bool{}
	for _, line := range append(strings.Split(text, "\n"), add...) {
		s := strings.TrimSpace(line)
		key := strings.ToLower(s)
		if s != "" && !seen[key] {
			seen[key] = true
			out = append(out, s)
		}
	}
	return strings.Join(out, "\r\n")
}
