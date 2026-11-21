//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
)

func GeneratePAC(proxyAddr string, directDomains []string) string {
	domains := []string{}
	for _, d := range directDomains {
		if s := strings.TrimSpace(d); s != "" {
			domains = append(domains, strings.ToLower(s))
		}
	}
	list, _ := json.Marshal(domains)
	proxy, _ := json.Marshal("PROXY " + proxyAddr + "; DIRECT")
	return fmt.Sprintf(`function FindProxyForURL(url, host) {
  if (isPlainHostName(host) || shExpMatch(host, "*.local") ||
      isInNet(host, "10.0.0.0", "255.0.0.0") ||
      isInNet(host, "172.16.0.0", "255.240.0.0") ||
      isInNet(host, "192.168.0.0", "255.255.0.0") ||
      isInNet(host, "127.0.0.0", "255.0.0.0")) {
    return "DIRECT";
  }
  var directList = %s;
  for (var i = 0; i < directList.length; i++) {
    if (host.toLowerCase() === directList[i] || dnsDomainIs(host.toLowerCase(), "." + directList[i])) {
      return "DIRECT";
    }
  }
  return %s;
}`, list, proxy)
}

// Use the actual local listener even when its bind address is a wildcard or port 0.
func (g *GUIController) proxyAddressLocked() string {
	if g.ln == nil {
		return g.cfg.Listen
	}
	addr := g.ln.Addr().(*net.TCPAddr)
	if addr.IP.IsUnspecified() {
		ip := "127.0.0.1"
		if addr.IP.To4() == nil {
			ip = "::1"
		}
		return net.JoinHostPort(ip, fmt.Sprint(addr.Port))
	}
	return addr.String()
}

func RegisterPACRoute(mux *http.ServeMux, ctrl *GUIController) {
	mux.HandleFunc("/proxy.pac", func(w http.ResponseWriter, r *http.Request) {
		ctrl.mu.Lock()
		listen := ctrl.proxyAddressLocked()
		domains := ctrl.cfg.DirectDomains
		ctrl.mu.Unlock()
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		_, _ = w.Write([]byte(GeneratePAC(listen, domains)))
	})
}
