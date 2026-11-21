package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func isolateRuleCache(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", root)
	t.Setenv("LocalAppData", root)
}

func assertNoCacheDirs(t *testing.T, config string) {
	t.Helper()
	for _, name := range []string{adCache, routingCache} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(config), name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unexpected %s beside config: %v", name, err)
		}
	}
}

func TestFreshAdDownloadStoresOneDatabase(t *testing.T) {
	isolateRuleCache(t)
	config := filepath.Join(t.TempDir(), "client.json")
	body := "one.example\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/broken" {
			http.Error(w, "failed", http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, body)
	}))
	defer server.Close()
	sources := []string{server.URL}
	if _, _, err := loadAdRules(t.Context(), config, sources, true); err != nil {
		t.Fatal(err)
	}
	body = "two.example\n"
	if _, _, err := loadAdRules(t.Context(), config, []string{server.URL, server.URL + "/broken"}, true); err == nil {
		t.Fatal("partial refresh accepted")
	}
	server.Close()
	r, stale, err := loadAdRules(t.Context(), config, sources, false)
	if err != nil || stale || !r.match("one.example").blocked || r.match("two.example").blocked {
		t.Fatal("last good ad rules were not restored", err)
	}
	assertNoCacheDirs(t, config)
	db, err := ruleCachePath()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(db))
	if err != nil || len(entries) != 1 || entries[0].Name() != "rules.db" {
		t.Fatal(entries, err)
	}
}

func TestLegacyRuleMigrationPreservesAgeAndFiles(t *testing.T) {
	isolateRuleCache(t)
	cfg := filepath.Join(t.TempDir(), "client.json")
	text := []byte("DOMAIN,legacy.example,REJECT\n")
	ad := []byte("ad.example\n")
	textHash := fmt.Sprintf("%x", sha256.Sum256(text))
	const url = "https://example.invalid/rules"
	adKey := fmt.Sprintf("%x.txt", sha256.Sum256([]byte(url)))
	old := time.Now().Add(-2 * adRefreshInterval).Truncate(time.Second)
	files := map[string][]byte{
		filepath.Join(filepath.Dir(cfg), routingCache, textHash+".txt"): text,
		filepath.Join(filepath.Dir(cfg), adCache, adKey):                ad,
	}
	for name, data := range files {
		if err := os.Mkdir(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(name, old, old); err != nil {
			t.Fatal(err)
		}
	}
	s := RouteSettings{Cache: textHash, Sources: "REJECT " + url}
	load := func() {
		t.Helper()
		r, _, err := buildRouting(t.Context(), cfg, s, false, nil)
		if err != nil || r.match("legacy.example").action != "REJECT" {
			t.Fatal("offline routing", err)
		}
		ads, stale, err := loadAdRules(t.Context(), cfg, []string{url}, false)
		if err != nil || !stale || !ads.match("ad.example").blocked {
			t.Fatal("offline ad blocking", err, stale)
		}
	}
	load()
	for name, data := range files {
		got, err := os.ReadFile(name)
		if err != nil || string(got) != string(data) {
			t.Fatal("migration changed original", name, err)
		}
		if err := os.Remove(name); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Dir(name)); err != nil {
			t.Fatal(err)
		}
	}
	load()
	assertNoCacheDirs(t, cfg)
	other := filepath.Join(t.TempDir(), "client.json")
	if _, e := readRuleCache(other, adCache, adKey, maxAdBytes); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("unexpected cache access from separate config", e)
	}
}
