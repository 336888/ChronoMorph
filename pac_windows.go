//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
)

func GeneratePAC(proxyAddr string) string {
	proxy, _ := json.Marshal("PROXY " + proxyAddr)
	// Domain routing is handled behind the local listener. PAC never needs a DNS lookup.
	return fmt.Sprintf(`function FindProxyForURL(url, host) {
  if (url.indexOf("http://127.0.0.1:18080/") === 0 || url.indexOf("http://localhost:18080/") === 0) {
    return "DIRECT";
  }
  return %s;
}`, proxy)
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
		ctrl.mu.Unlock()
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		_, _ = w.Write([]byte(GeneratePAC(listen)))
	})
}
