//go:build windows

package main

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/xjasonlyu/tun2socks/v2/engine"
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
}

var GlobalTUN = &TUNManager{}

func (t *TUNManager) StartTUN(proxyAddr, serverIP string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.active {
		return nil
	}
	gw, err := getDefaultGateway()
	if err != nil || gw == "" {
		return fmt.Errorf("获取物理默认网关失败: %v", err)
	}
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
		"name="+tunName, "static", tunIP, tunMask, tunGateway); err != nil {
		engine.Stop()
		return fmt.Errorf("配置虚拟网卡失败（需管理员权限）: %v", err)
	}
	_ = runHiddenCmd("netsh", "interface", "ip", "set", "dns", "name="+tunName, "static", tunDNS)
	_ = runHiddenCmd("route", "add", serverIP, "mask", "255.255.255.255", gw, "metric", "5")
	_ = runHiddenCmd("route", "add", "0.0.0.0", "mask", "128.0.0.0", tunGateway, "metric", "5")
	_ = runHiddenCmd("route", "add", "128.0.0.0", "mask", "128.0.0.0", tunGateway, "metric", "5")

	t.active = true
	log.Printf("[TUN] 虚拟网卡已接管全局流量 (VPS %s 已绕过直连网关 %s)", serverIP, gw)
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
		_ = runHiddenCmd("route", "delete", t.serverIP)
	}
	engine.Stop()
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
