// Local TLS 1.3 / HTTP2 origin. Only the loopback integration test runs this.
package main

import (
	"crypto/tls"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
)

func main() {
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "local REALITY test origin\n") }))
	origin.EnableHTTP2 = true
	origin.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	origin.StartTLS()
	defer origin.Close()
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: origin.Certificate().Raw})
	if err := os.WriteFile(os.Args[1], cert, 0600); err != nil {
		panic(err)
	}
	fmt.Println(origin.Listener.Addr().String())
	io.Copy(io.Discard, os.Stdin)
}
