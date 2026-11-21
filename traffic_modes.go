package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
)

const processMode = "process"
const routeMode = "route"

type TrafficMode struct {
	Version  int      `json:"version"`
	Kind     string   `json:"kind"`
	Remark   string   `json:"remark"`
	Handle   []string `json:"handle"`
	Bypass   []string `json:"bypass"`
	TCP      bool     `json:"tcp"`
	UDP      bool     `json:"udp"`
	DNS      bool     `json:"dns"`
	OwnDNS   bool     `json:"selected_process_dns"`
	ProxyDNS bool     `json:"proxy_dns"`
	DNSHost  string   `json:"dns_host,omitempty"`
	Children bool     `json:"children"`
	Echo     bool     `json:"icmp"`
	Delay    int      `json:"icmp_delay_ms"`
	LAN      bool     `json:"lan"`
	Loopback bool     `json:"loopback"`
}

func newTrafficMode(kind string, o NetworkOptions) TrafficMode {
	return TrafficMode{Version: 1, Kind: kind, TCP: o.TCP, UDP: o.UDP, DNS: o.DNS, OwnDNS: true, ProxyDNS: o.ProxyDNS, DNSHost: "1.1.1.1:53", Children: o.Children, Echo: o.Echo, Delay: o.EchoDelay, LAN: true}
}

type exeRule struct {
	literal string
	dir     bool
	full    bool
	expr    *regexp.Regexp
}

func executableRule(s string) (exeRule, error) {
	if strings.HasPrefix(s, "re:") {
		pattern := strings.TrimSpace(strings.TrimPrefix(s, "re:"))
		if pattern == "" {
			return exeRule{}, errors.New("空正则表达式")
		}
		rx, e := regexp.Compile("(?i:" + pattern + ")")
		return exeRule{expr: rx, full: true}, e
	}
	s = strings.ReplaceAll(s, "/", `\`)
	full := strings.Contains(s, `\`)
	dir := strings.HasSuffix(s, `\`)
	if full && !(len(s) > 2 && s[1:3] == `:\` || strings.HasPrefix(s, `\\`)) {
		return exeRule{}, errors.New("请填完整路径或 EXE 文件名")
	}
	if strings.ContainsAny(s, "*?\"<>|") || strings.Contains(s, "\x00") || !dir && !strings.HasSuffix(strings.ToLower(s), ".exe") {
		return exeRule{}, errors.New("请输入 EXE 文件名、完整路径、以反斜杠结尾的目录或 re: 表达式")
	}
	return exeRule{literal: strings.ToLower(s), dir: dir, full: full}, nil
}
func (r exeRule) match(name, path string) bool {
	if r.expr != nil {
		return path != "" && r.expr.MatchString(path)
	}
	if r.full {
		if r.dir {
			return strings.HasPrefix(strings.ToLower(path), r.literal)
		}
		return strings.EqualFold(path, r.literal)
	}
	return strings.EqualFold(name, r.literal)
}

type modeRules struct {
	mode            TrafficMode
	include, except []exeRule
	routes, direct  []netip.Prefix
	needsPath       bool
}

func compileMode(m TrafficMode) (*modeRules, error) {
	if m.Version != 1 || m.Kind != processMode && m.Kind != routeMode {
		return nil, fmt.Errorf("不支持的模式类型或版本")
	}
	if strings.TrimSpace(m.Remark) == "" || len([]rune(m.Remark)) > 100 {
		return nil, fmt.Errorf("备注需要 1–100 个字符")
	}
	if m.Kind == processMode {
		if m.Delay < 0 || m.Delay > 2000 {
			return nil, fmt.Errorf("ICMP 延迟须为 0–2000 毫秒")
		}
		if m.DNS {
			a, e := dnsAddresses(m.DNSHost)
			if e != nil || len(a) != 1 {
				return nil, fmt.Errorf("请填写一个有效的 DNS 上游 IP:端口")
			}
		}
	}
	out := &modeRules{mode: m}
	for _, g := range []struct {
		label string
		list  []string
		proc  *[]exeRule
		ips   *[]netip.Prefix
	}{{"处理规则", m.Handle, &out.include, &out.routes}, {"分流规则", m.Bypass, &out.except, &out.direct}} {
		if len(g.list) > 4096 {
			return nil, fmt.Errorf("%s 最多 4096 行", g.label)
		}
		for i, line := range g.list {
			s := strings.TrimSpace(line)
			if s == "" || strings.HasPrefix(s, "#") {
				continue
			}
			var err error
			if len(s) > 2048 {
				err = errors.New("规则太长")
			} else if m.Kind == routeMode {
				var a []netip.Prefix
				a, err = ipRanges([]string{s})
				*g.ips = append(*g.ips, a...)
			} else {
				var r exeRule
				r, err = executableRule(s)
				out.needsPath = out.needsPath || r.full
				*g.proc = append(*g.proc, r)
			}
			if err != nil {
				return nil, fmt.Errorf("%s 第 %d 行：%w", g.label, i+1, err)
			}
		}
	}
	if len(out.include)+len(out.except)+len(out.routes)+len(out.direct) == 0 {
		return nil, fmt.Errorf("至少填写一条处理或分流规则")
	}
	return out, nil
}
func containsIP(list []netip.Prefix, a netip.Addr) bool {
	for _, p := range list {
		if p.Contains(a.Unmap()) {
			return true
		}
	}
	return false
}
func (r *modeRules) routesIP(a netip.Addr) bool {
	return !containsIP(r.direct, a) && (len(r.routes) == 0 || containsIP(r.routes, a))
}

type processImage struct{ name, path string }

func (r *modeRules) selects(chain []processImage) (bool, error) {
	selected := len(r.include) == 0
	for _, p := range chain {
		for _, rule := range r.except {
			if rule.match(p.name, p.path) {
				return false, nil
			}
		}
		for _, rule := range r.include {
			if rule.match(p.name, p.path) {
				selected = true
			}
		}
	}
	if r.needsPath {
		for _, p := range chain {
			if p.path == "" {
				return false, errors.New("无法读取进程路径以判断模式规则")
			}
		}
	}
	return selected, nil
}
func (m TrafficMode) overlay(o NetworkOptions, rules *modeRules) NetworkOptions {
	o.mode = rules
	o.Processes = nil
	if m.Kind == processMode {
		o.TCP = m.TCP
		o.UDP = m.UDP
		o.DNS = m.DNS
		o.SelectedDNS = m.OwnDNS
		o.ProxyDNS = m.ProxyDNS
		o.Children = m.Children
		o.Echo = m.Echo
		o.EchoDelay = m.Delay
		if m.DNS {
			o.CustomDNS = m.DNSHost
		}
	}
	return o
}
func modeFilename(name string) (string, error) {
	name = strings.TrimSpace(name)
	if strings.HasSuffix(strings.ToLower(name), ".json") {
		name = name[:len(name)-5]
	}
	if name == "" || name == "." || name == ".." || len([]rune(name)) > 96 || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") || strings.ContainsAny(name, `/\:*?"<>|`) || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", errors.New("请输入有效的 JSON 文件名")
	}
	stem := strings.ToUpper(strings.Split(name, ".")[0])
	if slices.Contains([]string{"CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$"}, stem) || len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '0' && stem[3] <= '9' {
		return "", errors.New("文件名为 Windows 保留名称")
	}
	return name + ".json", nil
}
func modeDir(cfg string) string { return filepath.Join(filepath.Dir(cfg), "modes") }
func listModes(cfg string) ([]string, error) {
	entries, e := os.ReadDir(modeDir(cfg))
	if errors.Is(e, fs.ErrNotExist) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var out []string
	for _, f := range entries {
		if f.Type().IsRegular() && strings.HasSuffix(strings.ToLower(f.Name()), ".json") {
			if _, e := modeFilename(f.Name()); e == nil {
				out = append(out, f.Name())
			}
		}
	}
	sort.Strings(out)
	return out, nil
}
func readMode(cfg, name string) (TrafficMode, error) {
	var m TrafficMode
	n, e := modeFilename(name)
	if e != nil {
		return m, e
	}
	root, e := os.OpenRoot(modeDir(cfg))
	if e != nil {
		return m, e
	}
	defer root.Close()
	f, e := root.Open(n)
	if e != nil {
		return m, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 2<<20+1))
	if e != nil || len(b) > 2<<20 {
		return m, fmt.Errorf("读取模式失败或文件大于 2 MiB")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if e = dec.Decode(&m); e != nil {
		return m, e
	}
	if e = dec.Decode(new(any)); e != io.EOF {
		return m, errors.New("JSON 文件包含多余内容")
	}
	return m, nil
}
func saveMode(cfg, name string, m TrafficMode, create bool) (string, error) {
	n, e := modeFilename(name)
	if e != nil {
		return "", e
	}
	if _, e = compileMode(m); e != nil {
		return "", e
	}
	b, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		return "", e
	}
	if len(b) > 2<<20 {
		return "", errors.New("模式文件超过 2 MiB")
	}
	dir := modeDir(cfg)
	if e = os.MkdirAll(dir, 0700); e != nil {
		return "", e
	}
	tmp, e := os.CreateTemp(dir, ".writing-*")
	if e != nil {
		return "", e
	}
	defer os.Remove(tmp.Name())
	if _, e = tmp.Write(append(b, '\n')); e != nil {
		tmp.Close()
		return "", e
	}
	if e = tmp.Close(); e != nil {
		return "", e
	}
	target := filepath.Join(dir, n)
	if create {
		e = os.Link(tmp.Name(), target)
	} else {
		if _, e = os.Stat(target); e == nil {
			e = os.Rename(tmp.Name(), target)
		}
	}
	if errors.Is(e, fs.ErrExist) {
		return "", fmt.Errorf("已有同名模式 %s", n)
	}
	return n, e
}
func scanExecutables(dir string) ([]string, error) {
	var out []string
	e := filepath.WalkDir(dir, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type().IsRegular() && strings.EqualFold(filepath.Ext(d.Name()), ".exe") {
			out = append(out, path)
			if len(out) > 4096 {
				return fmt.Errorf("目录超过 4096 个 EXE，请选择更具体的文件夹")
			}
		}
		return nil
	})
	return out, e
}
