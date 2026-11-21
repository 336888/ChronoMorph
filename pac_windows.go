//go:build windows

package main

import (
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/sys/windows/registry"
)

func GeneratePAC(proxyAddr string, directDomains []string) string {
	var quoted []string
	for _, d := range directDomains {
		if s := strings.TrimSpace(d); s != "" {
			quoted = append(quoted, fmt.Sprintf(`"%s"`, s))
		}
	}
	return fmt.Sprintf(`function FindProxyForURL(url, host) {
  if (isPlainHostName(host) || shExpMatch(host, "*.local") ||
      isInNet(host, "10.0.0.0", "255.0.0.0") ||
      isInNet(host, "172.16.0.0", "255.240.0.0") ||
      isInNet(host, "192.168.0.0", "255.255.0.0") ||
      isInNet(host, "127.0.0.0", "255.0.0.0")) {
    return "DIRECT";
  }
  var directList = [%s];
  for (var i = 0; i < directList.length; i++) {
    if (dnsDomainIs(host, directList[i]) || dnsDomainIs(host, "." + directList[i])) {
      return "DIRECT";
    }
  }
  return "PROXY %s; DIRECT";
}`, strings.Join(quoted, ", "), proxyAddr)
}

func SetWindowsSystemProxy(enable bool, proxyAddr string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if enable {
		_ = k.DeleteValue("AutoConfigURL")
		_ = k.SetStringValue("ProxyServer", proxyAddr)
		return k.SetDWordValue("ProxyEnable", 1)
	}
	return k.SetDWordValue("ProxyEnable", 0)
}

func SetWindowsPACProxy(enable bool, pacURL string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if enable {
		_ = k.SetDWordValue("ProxyEnable", 0)
		return k.SetStringValue("AutoConfigURL", pacURL)
	}
	err = k.DeleteValue("AutoConfigURL")
	if err == registry.ErrNotExist {
		return nil
	}
	return err
}

func RegisterPACRoute(mux *http.ServeMux, ctrl *GUIController) {
	mux.HandleFunc("/proxy.pac", func(w http.ResponseWriter, r *http.Request) {
		ctrl.mu.Lock()
		listen := ctrl.cfg.Listen
		domains := ctrl.cfg.DirectDomains
		ctrl.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		_, _ = w.Write([]byte(GeneratePAC(listen, domains)))
	})
}
