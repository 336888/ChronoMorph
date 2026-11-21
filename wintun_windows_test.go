package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/tun"
)

func TestWintunCache(t *testing.T) {
	data, err := bundledWintun()
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(wintunArchive)); got != "07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51" {
		t.Fatal("archive differs from the official Wintun release")
	}
	dir := filepath.Join(t.TempDir(), "driver cache")
	for _, previous := range []string{"missing", "good", "corrupt"} {
		if previous == "corrupt" {
			if err := os.WriteFile(filepath.Join(dir, "wintun.dll"), []byte("incomplete"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		file, err := installWintun(dir, data)
		if err != nil {
			t.Fatalf("%s: %v", previous, err)
		}
		file.Close()
		got, err := os.ReadFile(filepath.Join(dir, "wintun.dll"))
		if err != nil || !bytes.Equal(data, got) {
			t.Fatalf("%s: signed DLL changed", previous)
		}
	}
}

func TestStandaloneWindowsTUN(t *testing.T) {
	exe := os.Getenv("CHRONOMORPH_TEST_EXE")
	if os.Getenv("CHRONOMORPH_TEST_WINTUN") != "1" || exe == "" {
		t.Skip("Windows release runner with driver installation enabled required")
	}
	raw, err := os.ReadFile(exe)
	if err != nil || !bytes.Contains(raw, wintunArchive) {
		t.Fatal("standalone release EXE does not contain the official Wintun archive", err)
	}
	files, err := os.ReadDir(filepath.Dir(exe))
	if err != nil || len(files) != 1 {
		t.Fatal("expected a directory containing only the standalone EXE", err)
	}
	h, err := loadBundledWintun()
	if err != nil {
		t.Fatal(err)
	}
	defer windows.FreeLibrary(h)
	// This is the same adapter and packet-ring API used by tun2socks. No DNS
	// or default route is changed on the build runner.
	dev, err := tun.CreateTUN("ChronoMorph-CI", 1400)
	if err != nil {
		t.Fatal(err)
	}
	if err := dev.Close(); err != nil {
		t.Fatal(err)
	}
	t.Log("Standalone EXE embeds Wintun; signed driver loaded, adapter and packet ring opened and closed")
}
