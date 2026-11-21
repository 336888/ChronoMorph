//go:build windows

package main

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
)

var guiToken string

func InitGUIToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	guiToken = hex.EncodeToString(b)
	return guiToken
}

func ProtectLocalAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "127.0.0.1:18080" && r.Host != "localhost:18080" {
			http.Error(w, "Forbidden Host", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if r.Header.Get("X-CM-Token") != guiToken {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
