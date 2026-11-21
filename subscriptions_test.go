package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
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
