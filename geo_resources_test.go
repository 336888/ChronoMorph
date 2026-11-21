package main

import (
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/xtls/xray-core/common/geodata"
	"google.golang.org/protobuf/proto"
)

func TestResourceEdits(t *testing.T) {
	var o geoOverrides
	for name, text := range map[string]string{
		geoSiteFile: "work,example.com\nwork,!skip.example.com",
		geoIPFile:   "203.0.113.0/24,CN\n203.0.113.8,-\n2001:db8::/32,lab",
		geoASNFile:  "203.0.113.0/24,AS13335\n203.0.113.7,-",
	} {
		if err := o.parse(name, text); err != nil {
			t.Fatal(err)
		}
	}
	s := siteRoute{edits: o.sites["work"]}
	if !s.matches("app.example.com") || s.matches("x.skip.example.com") {
		t.Fatal("site precedence")
	}
	for ip, want := range map[string]string{"203.0.113.1": "cn", "203.0.113.8": "", "2001:db8::123": "lab"} {
		label, ok := prefixOverride(o.countries, netip.MustParseAddr(ip))
		if !ok || label != want {
			t.Fatal(ip, label, ok)
		}
	}
	if n, ok := prefixOverride(o.asns, netip.MustParseAddr("203.0.113.7")); !ok || n != 0 {
		t.Fatal(n, ok)
	}
	for name, text := range map[string]string{geoSiteFile: "work,*.example.com", geoIPFile: "10.0.0.0/33,cn", geoASNFile: "10.0.0.1,AS0"} {
		if err := o.parse(name, text); err == nil {
			t.Fatal("accepted invalid entry", name, text)
		}
	}
}

func TestIndependentResourceSync(t *testing.T) {
	isolateRuleCache(t)
	path := filepath.Join(t.TempDir(), "client.json")
	mirror := "https://mirror.example.test/site"
	site := &geodata.GeoSite{Code: "work", Domain: []*geodata.Domain{{Type: geodata.Domain_Domain, Value: "upstream.example"}}}
	data, err := proto.Marshal(&geodata.GeoSiteList{Entry: []*geodata.GeoSite{site}})
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	client := &http.Client{Transport: geoTransport(func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.URL.String())
		if r.URL.String() != mirror {
			return nil, fmt.Errorf("wrong source %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})}
	cfg := RouteSettings{Manual: "GEOIP,cn,DIRECT\nGEOSITE,work,PROXY", Sources: "DIRECT https://unrelated.invalid/text"}
	cfg = cfg.withResource(geoSiteFile, GeoResource{URL: mirror, Overrides: "work,custom.example\nwork,!upstream.example"})
	idx, hash, err := buildResourceRouting(t.Context(), path, cfg, true, []string{geoSiteFile}, client)
	if err != nil || idx.missing == nil || !slices.Equal(calls, []string{mirror}) {
		t.Fatal("single resource sync", err, calls)
	}
	cfg = cfg.withIndex(idx, hash)
	if cfg.GeoFiles[geoSiteFile] == "" || cfg.Resources[geoSiteFile].SyncedURL != mirror || cfg.Resources[geoSiteFile].SyncedAt == 0 {
		t.Fatal(cfg)
	}
	cfg.Sources, cfg.Manual = "", "GEOSITE,work,PROXY"
	idx, _, err = buildRouting(t.Context(), path, cfg, false, nil)
	if err != nil || idx.match("sub.custom.example").action != "PROXY" || idx.match("upstream.example").action != "" {
		t.Fatal("offline overrides", err)
	}
	cfg.Manual = ""
	idx, hash, err = buildRouting(t.Context(), path, cfg, false, nil)
	if err != nil || cfg.withIndex(idx, hash).GeoFiles[geoSiteFile] == "" {
		t.Fatal("removed installed resource", err)
	}
	cfg = cfg.withResource(geoSiteFile, GeoResource{URL: "http://invalid.test"})
	if _, _, err = buildRouting(t.Context(), path, cfg, false, nil); err == nil {
		t.Fatal("accepted insecure source")
	}
	assertNoCacheDirs(t, path)
}

func TestRealResourceSyncAndEdits(t *testing.T) {
	dir := os.Getenv("SLACKER_TEST_GEODATA")
	if dir == "" {
		t.Skip("real databases not supplied")
	}
	isolateRuleCache(t)
	path := filepath.Join(t.TempDir(), "client.json")
	var fetched []string
	client := &http.Client{Transport: geoTransport(func(r *http.Request) (*http.Response, error) {
		name := strings.TrimPrefix(r.URL.String(), geoRelease)
		fetched = append(fetched, name)
		f, e := os.Open(filepath.Join(dir, name))
		if e != nil {
			return nil, e
		}
		return &http.Response{StatusCode: 200, Body: f}, nil
	})}
	cfg := RouteSettings{Manual: "GEOSITE,cn,REJECT\nGEOIP,lab,PROXY\nIP-ASN,64512,REJECT\nIP-ASN,13335,DIRECT"}
	cfg = cfg.withResource(geoSiteFile, GeoResource{Overrides: "cn,!baidu.com\ncn,local.example"})
	cfg = cfg.withResource(geoIPFile, GeoResource{Overrides: "223.5.5.5,lab"})
	cfg = cfg.withResource(geoASNFile, GeoResource{Overrides: "2606:4700:4700::1111,64512"})
	for _, selected := range [][]string{geoFilenames[:], {geoASNFile}, {}} {
		fetched = nil
		idx, hash, e := buildResourceRouting(t.Context(), path, cfg, len(selected) > 0, selected, client)
		if e != nil || idx.missing != nil || !slices.Equal(fetched, selected) {
			t.Fatal(selected, fetched, e)
		}
		cfg = cfg.withIndex(idx, hash)
		for target, want := range map[string]string{"www.baidu.com": "", "x.local.example": "REJECT", "223.5.5.5": "PROXY", "2606:4700:4700::1111": "REJECT", "1.1.1.1": "DIRECT"} {
			if got := idx.match(target).action; got != want {
				t.Fatal(target, got, want)
			}
		}
	}
	assertNoCacheDirs(t, path)
}
