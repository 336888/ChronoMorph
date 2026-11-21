package main

import (
	"encoding/json"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestImportDebianRealityExport(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Debian export uses Bash and jq")
	}
	for _, bin := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip(bin + " unavailable")
		}
	}
	cfg := RealityConfig{Name: "example.com", Listen: ":443", Target: "example.com:443"}
	node, err := cfg.node(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("21", 32)
	for _, ip := range []string{"192.0.2.1", "2001:db8::1"} {
		cmd := exec.Command("bash", "-c", `source ./cm.sh; CM_PUBLIC_IP="$1" gen_share_link "$2" '测试节点' 0 '' 0 '' 0 true "$3"`, "_", ip, key, string(data))
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		line := strings.TrimSpace(string(out))
		_, link, ok := strings.Cut(line, "] ")
		if !ok {
			t.Fatal("missing export label")
		}
		for _, example := range []struct{ name, input string }{
			{"bare", link}, {"full row", line}, {"wrapped row", line[:85] + "\r\n  " + line[85:]},
		} {
			t.Run(ip+"/"+example.name, func(t *testing.T) {
				profile, err := DecodeShareLink(example.input)
				if err != nil {
					t.Fatal(err)
				}
				if profile.TLSNode != node || profile.ServerIP != ip || profile.MasterKey != key || !strings.HasPrefix(profile.Name, "测试节点-REALITY-v") {
					t.Fatal("node data changed during import")
				}
			})
		}
		if _, err := DecodeShareLink(line + "\r\n" + line); err == nil {
			t.Fatal("accepted multiple nodes as one")
		}
		if _, err := DecodeShareLink(strings.Replace(line, "cm+reality://", "cm+tls://", 1)); err == nil {
			t.Fatal("accepted mismatched transport")
		}
		if _, err := DecodeShareLink(line[:len(line)/2]); err == nil {
			t.Fatal("accepted truncated export")
		}
	}
}
