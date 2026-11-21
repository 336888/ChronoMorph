package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
)

func TestProxyRestoresOnlyOwnedSettings(t *testing.T) {
	base := fmt.Sprintf(`Software\ChronoMorph-Test-%d`, time.Now().UnixNano())
	p := proxyRegistry{base + `\settings`, base + `\state`}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, p.settings, registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		k.Close()
		registry.DeleteKey(registry.CURRENT_USER, p.settings)
		registry.DeleteKey(registry.CURRENT_USER, p.journal)
		registry.DeleteKey(registry.CURRENT_USER, base)
	})
	if err = k.SetDWordValue("ProxyEnable", 1); err != nil {
		t.Fatal(err)
	}
	if err = k.SetExpandStringValue("AutoConfigURL", `%USERPROFILE%\original.pac`); err != nil {
		t.Fatal(err)
	}
	before, err := readProxyValues(k)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.restore(); err != nil {
		t.Fatal(err)
	}
	if err = p.apply("127.0.0.1:1080", false); err != nil {
		t.Fatal(err)
	}
	if err = p.apply("127.0.0.1:1080", true); err != nil {
		t.Fatal(err)
	}
	current, err := readProxyValues(k)
	if err != nil || current[0].Number != 1 || current[1].Text != "127.0.0.1:1080" || current[2].Type != 0 {
		t.Fatalf("manual proxy state: %+v, %v", current, err)
	}
	if err = p.restore(); err != nil {
		t.Fatal(err)
	}
	restored, err := readProxyValues(k)
	if err != nil || restored != before {
		t.Fatalf("restored %+v, want %+v", restored, before)
	}
	if err = p.apply("127.0.0.1:1080", true); err != nil {
		t.Fatal(err)
	}
	if err = k.SetStringValue("ProxyServer", "new-setting:8080"); err != nil {
		t.Fatal(err)
	}
	// A new instance can recover after the process was terminated.
	otherProcess := proxyRegistry{p.settings, p.journal}
	if err = otherProcess.restore(); err != nil {
		t.Fatal(err)
	}
	current, err = readProxyValues(k)
	if err != nil || current[1].Text != "new-setting:8080" || current[2] != before[2] {
		t.Fatalf("user settings overwritten: %+v", current)
	}
}
func TestPACQuotesAndDomainBoundaries(t *testing.T) {
	script := GeneratePAC("127.0.0.1:1080", []string{"Example.COM", `quoted"name`})
	if !strings.Contains(script, `var directList = ["example.com","quoted\"name"];`) || strings.Contains(script, "dnsDomainIs(host, directList[i])") {
		t.Fatal("malformed PAC or unbounded domain suffix")
	}
}
