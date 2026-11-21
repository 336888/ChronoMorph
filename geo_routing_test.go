package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xtls/xray-core/common/geodata"
	"google.golang.org/protobuf/proto"
)

type geoTransport func(*http.Request) (*http.Response, error)

func (f geoTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGeoSitesAndOfflineUpdate(t *testing.T) {
	isolateRuleCache(t)
	site := &geodata.GeoSite{Code: "DEMO", Domain: []*geodata.Domain{
		{Type: geodata.Domain_Full, Value: "full.test"},
		{Type: geodata.Domain_Domain, Value: "suffix.test", Attribute: []*geodata.Domain_Attribute{{Key: "cn"}}},
		{Type: geodata.Domain_Substr, Value: "any-fragment"},
		{Type: geodata.Domain_Regex, Value: `^re[0-9]+\.test$`},
	}}
	raw, err := proto.Marshal(&geodata.GeoSiteList{Entry: []*geodata.GeoSite{site}})
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	fetches := 0
	httpClient := &http.Client{Transport: geoTransport(func(q *http.Request) (*http.Response, error) {
		if q.URL.String() != geoRelease+geoSiteFile {
			return nil, fmt.Errorf("unexpected source %s", q.URL)
		}
		fetches++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	cfg := RouteSettings{Manual: "DOMAIN,exception.suffix.test,PROXY\nGEOSITE,demo@cn,REJECT\nGEOSITE,demo,DIRECT", Sources: geoRelease + geoSiteFile}
	path := filepath.Join(t.TempDir(), "client.json")
	live, _, err := buildRouting(context.Background(), path, cfg, true, httpClient)
	if err != nil || fetches != 1 {
		t.Fatal(err, fetches)
	}
	cfg.GeoFiles = live.geo.files
	stored, _, err := buildRouting(context.Background(), path, cfg, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, idx := range []*routeIndex{live, stored} {
		for target, want := range map[string]string{"exception.suffix.test": "PROXY", "suffix.test": "REJECT", "X.SUFFIX.TEST.": "REJECT", "full.test": "DIRECT", "a.full.test": "", "any-fragment-foo.test": "DIRECT", "re42.test": "DIRECT", "re.test": "", "192.0.2.1": ""} {
			if got := idx.match(target).action; got != want {
				t.Errorf("%s = %q, want %q", target, got, want)
			}
		}
	}
	body = "not a geosite"
	if _, _, err := buildRouting(context.Background(), path, cfg, true, httpClient); err == nil {
		t.Fatal("invalid update accepted")
	}
	if _, _, err := buildRouting(context.Background(), path, cfg, false, nil); err != nil {
		t.Fatal("previous snapshot lost", err)
	}
	cfg.Manual = "GEOSITE,nonexistent,DIRECT"
	if _, _, err := buildRouting(context.Background(), path, cfg, false, nil); err == nil {
		t.Fatal("unknown category accepted")
	}
}

func TestPublishedGeoDatabases(t *testing.T) {
	isolateRuleCache(t)
	dir := os.Getenv("SLACKER_TEST_GEODATA")
	if dir == "" {
		t.Skip("set SLACKER_TEST_GEODATA to the three release files")
	}
	client := &http.Client{Transport: geoTransport(func(req *http.Request) (*http.Response, error) {
		name := strings.TrimPrefix(req.URL.String(), geoRelease)
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Body: f}, nil
	})}
	s := RouteSettings{Manual: "DOMAIN,except.baidu.com,PROXY\nGEOSITE,cn,DIRECT\nGEOIP,cn,DIRECT\nIP-ASN,AS13335,REJECT\nIP-CIDR,1.1.1.0/24,DIRECT"}
	path := filepath.Join(t.TempDir(), "client.json")
	idx, _, err := buildRouting(context.Background(), path, s, true, client)
	if err != nil {
		t.Fatal(err)
	}
	s.GeoFiles = idx.geo.files
	cached, _, err := buildRouting(context.Background(), path, s, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []*routeIndex{idx, cached} {
		for target, want := range map[string]string{"www.baidu.com": "GEOSITE,cn", "223.5.5.5": "GEOIP,cn", "1.1.1.1": "IP-ASN,as13335", "[2606:4700:4700::1111]:443": "IP-ASN,as13335", "except.baidu.com": "DOMAIN,except.baidu.com"} {
			if hit := r.match(target); hit.rule != want {
				t.Errorf("%s = %+v, want %s", target, hit, want)
			}
		}
	}
}

func TestMixedFeedFailureDoesNotCreateCache(t *testing.T) {
	isolateRuleCache(t)
	s := RouteSettings{Sources: "DIRECT https://example.invalid/domains\n" + geoRelease + geoSiteFile}
	path := filepath.Join(t.TempDir(), "client.json")
	client := &http.Client{Transport: geoTransport(func(q *http.Request) (*http.Response, error) {
		body := "domain.example"
		if q.URL.String() == geoRelease+geoSiteFile {
			body = "corrupt database"
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	if _, _, err := buildRouting(t.Context(), path, s, true, client); err == nil {
		t.Fatal("partial update accepted")
	}
	db, err := ruleCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatal("partial snapshot persisted", err)
	}
	assertNoCacheDirs(t, path)
}
