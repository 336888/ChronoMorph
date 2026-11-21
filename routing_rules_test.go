package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSlackerRoutingFeedsAndOfflineCache(t *testing.T) {
	isolateRuleCache(t)
	var body = "DOMAIN-SUFFIX,example.org\nDOMAIN,other.test\nIP-CIDR,192.0.2.0/24,no-resolve\nIP-CIDR6,2001:db8::/32,no-resolve\nUSER-AGENT,unsupported*\n.domainset.example\nexact.host.test\n"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body)) }))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "client.json")
	s := RouteSettings{Manual: "DOMAIN,login.example.org,PROXY\nDOMAIN-KEYWORD,annoying,REJECT", Sources: "DIRECT " + server.URL}
	idx, cache, err := buildRouting(context.Background(), path, s, true, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if idx.skipped != 1 || idx.count != 8 {
		t.Fatal(idx.skipped, idx.count)
	}
	s.Cache = cache
	loaded, _, err := buildRouting(context.Background(), path, s, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []*routeIndex{idx, loaded} {
		for host, action := range map[string]string{"login.example.org:443": "PROXY", "child.example.org": "DIRECT", "notexample.org": "", "sub.domainset.example": "DIRECT", "exact.host.test": "DIRECT", "sub.exact.host.test": "", "other.test": "DIRECT", "a.annoying.test": "REJECT", "192.0.2.5": "DIRECT", "[2001:db8::1]:80": "DIRECT"} {
			if h := r.match(host); h.action != action {
				t.Fatal(host, h)
			}
		}
	}
	body = "bad file"
	if _, _, err = buildRouting(context.Background(), path, s, true, server.Client()); err == nil {
		t.Fatal("invalid refresh accepted")
	}
	if _, _, err = buildRouting(context.Background(), path, s, false, nil); err != nil {
		t.Fatal(err)
	}
	assertNoCacheDirs(t, path)
	if err = writeRuleCache(path, routingCache, []cachedRule{{key: cache + ".txt", data: []byte("tampered"), modified: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = buildRouting(context.Background(), path, s, false, nil); err == nil {
		t.Fatal("corrupt cache accepted")
	}
}
func TestNamedTrafficModes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.json")
	o := (&Config{}).networkOptions()
	m := newTrafficMode(processMode, o)
	m.Remark = "游戏"
	m.Handle = []string{"game.exe", "C:\\Games\\"}
	m.Bypass = []string{"update.exe"}
	m.Children = true
	id, err := saveMode(path, "Game", m, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = saveMode(path, id, m, true); err == nil {
		t.Fatal("overwrote existing")
	}
	m, err = readMode(path, id)
	if err != nil {
		t.Fatal(err)
	}
	r, err := compileMode(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		chain    []processImage
		selected bool
	}{
		{[]processImage{{"x.exe", "C:\\Games\\X.exe"}}, true}, {[]processImage{{"update.exe", "C:\\Games\\update.exe"}, {"game.exe", "C:\\Games\\game.exe"}}, false},
		{[]processImage{{"x.exe", "C:\\Tools\\x.exe"}, {"GAME.EXE", "C:\\Tools\\game.exe"}}, true}, {[]processImage{{"x.exe", "C:\\Games2\\x.exe"}}, false},
	} {
		if got, e := r.selects(tc.chain); e != nil || got != tc.selected {
			t.Fatal(tc, got, e)
		}
	}
	for _, n := range []string{"../escape", "CON.txt", "A:B"} {
		if _, e := modeFilename(n); e == nil {
			t.Fatal(n)
		}
	}
	m.Handle = []string{"re:["}
	if _, err = compileMode(m); err == nil || !strings.Contains(err.Error(), "第 1 行") {
		t.Fatal(err)
	}
	m = newTrafficMode(routeMode, o)
	m.Remark = "IP"
	m.Handle = []string{"192.0.2.0/24", "2001:db8::/32"}
	m.Bypass = []string{"192.0.2.1"}
	r, err = compileMode(m)
	if err != nil {
		t.Fatal(err)
	}
	if r.routesIP(netip.MustParseAddr("192.0.2.1")) || !r.routesIP(netip.MustParseAddr("192.0.2.2")) || r.routesIP(netip.MustParseAddr("1.1.1.1")) {
		t.Fatal("route precedence")
	}
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "child"), 0700)
	for _, n := range []string{"one.EXE", "child/two.exe", "readme.txt"} {
		if e := os.WriteFile(filepath.Join(dir, n), nil, 0600); e != nil {
			t.Fatal(e)
		}
	}
	files, e := scanExecutables(dir)
	if e != nil || len(files) != 2 {
		t.Fatal(files, e)
	}
}
