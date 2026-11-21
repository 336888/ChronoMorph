//go:build windows

package main

import (
	"fmt"
	"log"
	"net"
	"os/exec"

	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/xjasonlyu/tun2socks/v2/engine"
	"golang.org/x/sys/windows"
)

const (
	tunName    = "ChronoMorphTUN"
	tunIP      = "10.0.85.2"
	tunMask    = "255.255.255.0"
	tunGateway = "10.0.85.1"
	tunDNS     = "127.0.0.1"
)

type TUNManager struct {
	mu       sync.Mutex
	active   bool
	serverIP string
	origGW   string
	dns      net.PacketConn
}

var GlobalTUN = &TUNManager{}

func (t *TUNManager) StartTUN(proxyAddr, serverIP string, cm *ClientMux, router *Router) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.active {
		return nil
	}
	if cm != nil {
		serverIP = cm.physicalIP().String()
	}
	if net.ParseIP(serverIP).To4() == nil {
		return fmt.Errorf("当前 TUN 只支持 IPv4 节点；IPv6 节点请使用系统代理")
	}
	if !IsAdmin() {
		return fmt.Errorf("请以管理员身份运行客户端后再启用 TUN")
	}
	dll, err := loadBundledWintun()
	if err != nil {
		return err
	}
	defer windows.FreeLibrary(dll)
	gw, err := getDefaultGateway()
	if err != nil || gw == "" {
		return fmt.Errorf("获取物理默认网关失败: %v", err)
	}
	dns, err := StartSecureDNSProxy(net.JoinHostPort(tunDNS, "53"), cm, router)
	if err != nil {
		return fmt.Errorf("本地 DNS 监听失败: %w", err)
	}
	defer func() {
		if !t.active {
			_ = dns.Close()
		}
	}()
	t.origGW = gw
	t.serverIP = serverIP

	key := &engine.Key{
		MTU:      1400,
		Device:   "tun://" + tunName,
		Proxy:    "socks5://" + proxyAddr,
		LogLevel: "silent",
	}
	engine.Insert(key)
	engine.Start()
	time.Sleep(800 * time.Millisecond)

	if err := runHiddenCmd("netsh", "interface", "ip", "set", "address",
		"name="+tunName, "static", tunIP, tunMask, "none"); err != nil {
		engine.Stop()
		return fmt.Errorf("配置虚拟网卡失败（需管理员权限）: %v", err)
	}
	if err := runHiddenCmd("netsh", "interface", "ip", "set", "dns", "name="+tunName, "static", tunDNS, "validate=no"); err != nil {
		engine.Stop()
		return fmt.Errorf("配置 TUN DNS 失败: %w", err)
	}
	routes := [][]string{{serverIP, "mask", "255.255.255.255", gw}, {"0.0.0.0", "mask", "128.0.0.0", tunGateway}, {"128.0.0.0", "mask", "128.0.0.0", tunGateway}}
	for i, route := range routes {
		args := append([]string{"add"}, route...)
		args = append(args, "metric", "5")
		if err := runHiddenCmd("route", args...); err != nil {
			for j := i - 1; j >= 0; j-- {
				_ = runHiddenCmd("route", append([]string{"delete"}, routes[j]...)...)
			}
			engine.Stop()
			return fmt.Errorf("添加 IPv4 路由 %s 失败: %w", route[0], err)
		}
	}

	t.dns = dns
	t.active = true
	log.Printf("[TUN] 虚拟网卡已接管 IPv4 流量 (VPS %s 已绕过直连网关 %s)", serverIP, gw)
	return nil
}

func (t *TUNManager) StopTUN() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		return
	}
	_ = runHiddenCmd("route", "delete", "0.0.0.0", "mask", "128.0.0.0", tunGateway)
	_ = runHiddenCmd("route", "delete", "128.0.0.0", "mask", "128.0.0.0", tunGateway)
	if t.serverIP != "" {
		_ = runHiddenCmd("route", "delete", t.serverIP, "mask", "255.255.255.255", t.origGW)
	}
	engine.Stop()
	_ = t.dns.Close()
	t.dns = nil
	t.active = false
	log.Println("[TUN] 虚拟网卡已关闭，系统路由已还原")
}

func (t *TUNManager) IsActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.active
}

func getDefaultGateway() (string, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		`(Get-NetRoute -DestinationPrefix "0.0.0.0/0" | Sort-Object RouteMetric | Select-Object -First 1).NextHop`)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
