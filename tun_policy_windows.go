package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"github.com/xjasonlyu/tun2socks/v2/dialer"
	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// Decisions are made for each original TUN flow before opening a SOCKS connection.
type tunPolicy struct {
	proxy.Dialer
	opts     NetworkOptions
	virtual  winipcfg.LUID
	resolver string
	endpoint *modeEndpoint
	bypass   []netip.Prefix
}
type tunPath int

const (
	pathProxy tunPath = iota
	pathPhysical
	pathDNS
)

func (p *tunPolicy) path(m *M.Metadata) (tunPath, error) {
	r := p.opts.mode
	ip := m.DstIP.Unmap()
	if p.endpoint != nil && p.endpoint.router != nil {
		router := p.endpoint.router
		if h := router.routes.Load().match(ip.String()); h.action == "REJECT" {
			return pathProxy, fmt.Errorf("目标 IP 命中阻止规则")
		} else if h.action == "DIRECT" && !router.IsGlobalMode() {
			return pathPhysical, nil
		}
	}
	if r != nil {
		if containsIP(p.bypass, ip) {
			return pathPhysical, nil
		}
		if r.mode.Kind == routeMode && !r.routesIP(ip) {
			return pathPhysical, nil
		}
		if r.mode.Kind == processMode && (ip.IsLoopback() && !r.mode.Loopback || modeLAN(ip) && !r.mode.LAN) {
			return pathPhysical, nil
		}
	}
	selected := true
	needOwner := len(p.opts.Processes) > 0
	dns := m.DstPort == 53 && p.opts.DNS
	if dns && !p.opts.SelectedDNS {
		return pathDNS, nil
	}
	for _, prefix := range p.bypass {
		if prefix.Contains(m.DstIP.Unmap()) {
			return pathPhysical, nil
		}
	}
	enabled := m.Network == M.TCP && p.opts.TCP || m.Network == M.UDP && p.opts.UDP
	if !enabled && !dns {
		return pathPhysical, nil
	}
	if r != nil && r.mode.Kind == processMode {
		var e error
		selected, e = namedProcessMatches(r, flowPID(m))
		if e != nil {
			return pathProxy, e
		}
	} else if needOwner {
		pid := flowPID(m)
		if pid == 0 {
			return pathProxy, fmt.Errorf("无法识别套接字所属进程")
		}
		var known bool
		selected, known = matchesProcess(pid, p.opts.Processes, p.opts.Children)
		if !known {
			return pathProxy, fmt.Errorf("无法识别进程 %d", pid)
		}
	}
	if !selected {
		return pathPhysical, nil
	}
	if dns {
		return pathDNS, nil
	}
	if !enabled {
		return pathPhysical, nil
	}
	return pathProxy, nil
}
func physicalDial(ctx context.Context, network, address string, virtual winipcfg.LUID) (net.Conn, error) {
	ap, e := netip.ParseAddrPort(address)
	if e != nil {
		return nil, e
	}
	ip := ap.Addr().Unmap()
	if ip.IsLoopback() {
		var d net.Dialer
		return d.DialContext(ctx, network, address)
	}
	family := winipcfg.AddressFamily(windows.AF_INET)
	if ip.Is6() {
		family = windows.AF_INET6
	}
	routes, e := winipcfg.GetIPForwardTable2(family)
	if e != nil {
		return nil, e
	}
	ifaces, e := winipcfg.GetIPInterfaceTable(family)
	if e != nil {
		return nil, e
	}
	route, ok := selectUplink(ip, virtual, routes, ifaces, nil)
	if !ok {
		return nil, fmt.Errorf("找不到物理上行网卡")
	}
	nic, e := route.nic.Interface()
	if e != nil {
		return nil, e
	}
	return dialer.DefaultDialer.DialContextWithOptions(ctx, network, address, &dialer.Options{InterfaceIndex: int(nic.InterfaceIndex)})
}
func (p *tunPolicy) connect(ctx context.Context, proto string, m *M.Metadata, path tunPath) (net.Conn, error) {
	if path == pathDNS {
		var d net.Dialer
		return d.DialContext(ctx, proto, p.resolver)
	}
	return physicalDial(ctx, proto, m.DestinationAddress(), p.virtual)
}
func (p *tunPolicy) DialContext(ctx context.Context, m *M.Metadata) (net.Conn, error) {
	path, e := p.path(m)
	if e != nil {
		return nil, e
	}
	if path == pathProxy {
		if p.opts.mode != nil && p.endpoint != nil {
			target := m.DestinationAddress()
			cm := p.endpoint.current.Load()
			if cm == nil {
				return nil, fmt.Errorf("节点已断开")
			}
			if p.endpoint.router.ShouldBlock(target) {
				return nil, fmt.Errorf("blocked: %s", target)
			}
			return dialProxyTCP(target, cm, nil, nil, "TUN")
		}
		return p.Dialer.DialContext(ctx, m)
	}
	return p.connect(ctx, "tcp", m, path)
}
func (p *tunPolicy) DialUDP(m *M.Metadata) (net.PacketConn, error) {
	path, e := p.path(m)
	if e != nil {
		return nil, e
	}
	if path == pathProxy {
		return p.Dialer.DialUDP(m)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, e := p.connect(ctx, "udp", m, path)
	if e != nil {
		return nil, e
	}
	return &boundDatagrams{c, m.UDPAddr()}, nil
}

// One connected socket per original 5-tuple. Preserve the requested DNS address in replies.
type boundDatagrams struct {
	net.Conn
	peer *net.UDPAddr
}

func (c *boundDatagrams) ReadFrom(b []byte) (int, net.Addr, error) {
	n, e := c.Read(b)
	return n, c.peer, e
}
func (c *boundDatagrams) WriteTo(b []byte, _ net.Addr) (int, error) { return c.Write(b) }

var getUDPTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedUdpTable")

func flowPID(m *M.Metadata) uint32 {
	v6 := m.SrcIP.Is6() && !m.SrcIP.Is4In6()
	family := uintptr(windows.AF_INET)
	if v6 {
		family = windows.AF_INET6
	}
	udp := m.Network == M.UDP
	fn, class := getOwnerTable, uintptr(5)
	if udp {
		fn, class = getUDPTable, 1
	}
	buf := make([]byte, 16384)
	for {
		size := uint32(len(buf))
		rc, _, _ := fn.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, family, class, 0)
		if rc == uintptr(windows.ERROR_INSUFFICIENT_BUFFER) && size > uint32(len(buf)) && size <= 32<<20 {
			buf = make([]byte, size)
			continue
		}
		if rc != 0 || size < 4 || size > uint32(len(buf)) {
			return 0
		}
		stride, ip, port, remote, rport, pid, n := 24, 4, 8, 12, 16, 20, 4
		if v6 {
			stride, ip, port, remote, rport, pid, n = 56, 0, 20, 24, 44, 52, 16
		}
		if udp {
			stride, ip, port, pid = 12, 0, 4, 8
			if v6 {
				stride, ip, port, pid = 28, 0, 20, 24
			}
		}
		var result uint32
		for rows := buf[4:size]; len(rows) >= stride; rows = rows[stride:] {
			if binary.BigEndian.Uint16(rows[port:]) != m.SrcPort {
				continue
			}
			a, _ := netip.AddrFromSlice(rows[ip : ip+n])
			if a.Unmap() != m.SrcIP.Unmap() && !(udp && a.IsUnspecified()) {
				continue
			}
			if !udp {
				b, _ := netip.AddrFromSlice(rows[remote : remote+n])
				if b.Unmap() != m.DstIP.Unmap() || binary.BigEndian.Uint16(rows[rport:]) != m.DstPort {
					continue
				}
			}
			owner := binary.LittleEndian.Uint32(rows[pid:])
			if result != 0 && result != owner {
				return 0
			}
			result = owner
		}
		return result
	}
}

type procItem struct {
	name   string
	parent uint32
}

var procCache struct {
	sync.Mutex
	expires time.Time
	entries map[uint32]procItem
}

func refreshProcessCache(pid uint32) bool {
	if _, ok := procCache.entries[pid]; !ok || time.Now().After(procCache.expires) {
		snap, e := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
		if e != nil {
			return false
		}
		defer windows.CloseHandle(snap)
		var p windows.ProcessEntry32
		p.Size = uint32(unsafe.Sizeof(p))
		items := map[uint32]procItem{}
		for e = windows.Process32First(snap, &p); e == nil; e = windows.Process32Next(snap, &p) {
			items[p.ProcessID] = procItem{windows.UTF16ToString(p.ExeFile[:]), p.ParentProcessID}
		}
		procCache.entries = items
		procCache.expires = time.Now().Add(300 * time.Millisecond)
	}

	return true
}
func matchesProcess(pid uint32, names []string, children bool) (bool, bool) {
	procCache.Lock()
	defer procCache.Unlock()
	if !refreshProcessCache(pid) {
		return false, false
	}
	for depth := 0; pid != 0 && depth < 32; depth++ {
		p, ok := procCache.entries[pid]
		if !ok {
			return false, depth > 0
		}
		for _, name := range names {
			if strings.EqualFold(p.name, name) {
				return true, true
			}
		}
		if !children || p.parent == pid {
			break
		}
		pid = p.parent
	}
	return false, true
}
