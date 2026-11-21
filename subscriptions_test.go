package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func TestSubscriptionRulesAndDNS(t *testing.T) {
	src, err := adSources([]string{"https://github.com/217heidai/adblockfilters", defaultAdSource})
	if err != nil || len(src) != 1 || src[0] != defaultAdSource {
		t.Fatal(src, err)
	}
	dir := t.TempDir()
	cache := filepath.Join(dir, "adblock-cache")
	if err = os.Mkdir(cache, 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte("! test\nads.example\n||metrics.example^\n@@||allow.ads.example^\nexample.org##.banner\n")
	if err = os.WriteFile(filepath.Join(cache, fmt.Sprintf("%x.txt", sha256.Sum256([]byte(src[0])))), data, 0600); err != nil {
		t.Fatal(err)
	}
	r, _, err := loadAdRules(t.Context(), filepath.Join(dir, "client.json"), src, false)
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(nil, nil)
	router.ads.Store(r)
	for host, block := range map[string]bool{"x.ads.example:443": true, "metrics.example.": true, "notads.example": false, "x.allow.ads.example": false, "example.org": false} {
		if router.ShouldBlock(host) != block {
			t.Fatal(host)
		}
	}
	q := dnsmessage.Message{Header: dnsmessage.Header{ID: 42, RecursionDesired: true}, Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName("x.ads.example."), Type: dnsmessage.TypeAAAA, Class: dnsmessage.ClassINET}}}
	query, _ := q.Pack()
	var reply dnsmessage.Message
	if reply.Unpack(blockedDNS(query, router)) != nil || !reply.Response || reply.ID != 42 || reply.RCode != dnsmessage.RCodeNameError {
		t.Fatal("bad DNS denial")
	}
	if r, _, err := loadAdRules(t.Context(), filepath.Join(dir, "client.json"), nil, false); err != nil || len(r.blocks) != 0 {
		t.Fatal("failed to disable subscriptions")
	}
}

func TestAdDecisionsMatchAcrossDNSAndProxy(t *testing.T) {
	cfg := Config{AdAllowDomains: []string{"login.ads.example"}, BlockDomains: []string{"always.login.ads.example"}}
	router := cfg.newRouter()
	rules := &adRules{blocks: map[string]uint8{}, exceptions: map[string]uint8{}}
	if err := parseAdSource([]byte("ads.example\n@@||public.ads.example^\n"), rules, 1); err != nil {
		t.Fatal(err)
	}
	if err := parseAdSource([]byte("ads.example\nmetrics.ads.example\n"), rules, 2); err != nil {
		t.Fatal(err)
	}
	router.ads.Store(rules)
	for _, tc := range []struct {
		host    string
		blocked bool
		matched string
		src     uint8
	}{
		{"a.metrics.ads.example", true, "metrics.ads.example", 2},
		{"ADS.EXAMPLE.", true, "ads.example", 1},
		{"safe.public.ads.example", false, "public.ads.example", 1},
		{"x.login.ads.example", false, "login.ads.example", 0},
		{"always.login.ads.example", true, "always.login.ads.example", 0},
		{"other.example", false, "", 0},
	} {
		result := router.filter(tc.host)
		if result.blocked != tc.blocked || result.domain != tc.matched || result.source != tc.src {
			t.Fatalf("%s: %+v", tc.host, result)
		}
		q := dnsmessage.Message{Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName(strings.TrimSuffix(tc.host, ".") + "."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}}}
		wire, err := q.Pack()
		if err != nil {
			t.Fatal(err)
		}
		if blocked := blockedDNS(wire, router) != nil; blocked != tc.blocked || router.ShouldBlock(tc.host+":443") != blocked {
			t.Fatalf("proxy/DNS disagree on %s", tc.host)
		}
	}
	router.setAdPreferences(nil, true)
	if router.ShouldBlock("metrics.ads.example") || !router.ShouldBlock("always.login.ads.example") {
		t.Fatal("pause incorrectly applied to local rules")
	}
	router.setAdPreferences(nil, false)
	if !router.ShouldBlock("login.ads.example") {
		t.Fatal("removing an exception did not restore filtering")
	}
	domains, err := allowDomains("https://LOGIN.ads.example:8443/user?private=123\nlogin.ads.example.\r\n")
	if err != nil || !slices.Equal(domains, []string{"login.ads.example"}) {
		t.Fatal(domains, err)
	}
	for _, bad := range []string{"https://", "*.ads.example", "127.0.0.1", "https://user:secret@ads.example/", "bad domain", "a.example\ninvalid"} {
		if _, err := allowDomains(bad); err == nil {
			t.Fatalf("invalid allow entry accepted: %q", bad)
		}
	}
}
