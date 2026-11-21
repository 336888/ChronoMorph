package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const appleRouteURL = "https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Surge/Apple/Apple_All_No_Resolve.list"

type RouteSettings struct {
	Manual  string `json:"manual,omitempty"`
	Sources string `json:"sources,omitempty"`
	Cache   string `json:"snapshot,omitempty"`
}
type routeHit struct {
	order        int
	action, rule string
}
type keywordRoute struct {
	term string
	hit  routeHit
}
type routeIndex struct {
	exact, suffix  map[string]routeHit
	prefix         map[netip.Prefix]routeHit
	keywords       []keywordRoute
	count, skipped int
}

func newRouteIndex() *routeIndex {
	return &routeIndex{exact: map[string]routeHit{}, suffix: map[string]routeHit{}, prefix: map[netip.Prefix]routeHit{}}
}
func routeAction(s string) bool { return s == "DIRECT" || s == "PROXY" || s == "REJECT" }
func (r *routeIndex) add(text, source, action string) error {
	return r.parse(text, source, action, nil)
}
func (r *routeIndex) parse(text, source, action string, cache *bytes.Buffer) error {
	scan := bufio.NewScanner(strings.NewReader(text))
	scan.Buffer(nil, 8192)
	for row := 1; scan.Scan(); row++ {
		line := strings.TrimSpace(strings.TrimPrefix(scan.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") || strings.HasPrefix(line, ";") {
			continue
		}
		p := strings.Split(line, ",")
		for i := range p {
			p[i] = strings.TrimSpace(p[i])
		}
		errLine := func(msg string) error { return fmt.Errorf("%s 第 %d 行：%s", source, row, msg) }
		if len(p) == 1 && action != "" {
			domain := p[0]
			kind := "DOMAIN"
			if strings.HasPrefix(domain, ".") {
				domain = strings.TrimPrefix(domain, ".")
				kind = "DOMAIN-SUFFIX"
			}
			p = []string{kind, domain}
		}
		if len(p) < 2 {
			return errLine("需要规则类型和匹配值")
		}
		kind, value := strings.ToUpper(p[0]), strings.ToLower(p[1])
		if action != "" && (kind == "PROCESS-NAME" || kind == "USER-AGENT" || kind == "URL-REGEX" || kind == "GEOIP" || kind == "IP-ASN" || kind == "DOMAIN-WILDCARD") {
			r.skipped++
			continue
		}
		a := action
		tail := p[2:]
		if a == "" {
			if len(tail) == 0 {
				return errLine("请指定 DIRECT、PROXY 或 REJECT")
			}
			a = strings.ToUpper(tail[0])
			tail = tail[1:]
		}
		if !routeAction(a) || len(tail) > 1 || len(tail) == 1 && !strings.EqualFold(tail[0], "no-resolve") {
			return errLine("动作或参数无效")
		}
		if r.count >= 180000 {
			return errLine("规则总数超过 180000 条")
		}
		h := routeHit{r.count + 1, a, kind + "," + value}
		switch kind {
		case "DOMAIN", "DOMAIN-SUFFIX":
			value = strings.TrimSuffix(value, ".")
			if !validWebsiteName(value) && (strings.Contains(value, ".") || !validWebsiteName(value+".test")) {
				return errLine("域名无效")
			}
			m := r.exact
			if kind == "DOMAIN-SUFFIX" {
				m = r.suffix
			}
			if _, ok := m[value]; !ok {
				m[value] = h
			}
		case "DOMAIN-KEYWORD":
			if value == "" || len(value) > 253 || strings.ContainsAny(value, " /\\:") {
				return errLine("关键字无效")
			}
			r.keywords = append(r.keywords, keywordRoute{value, h})
		case "IP-CIDR", "IP-CIDR6":
			n, e := netip.ParsePrefix(value)
			if e != nil || n.Addr().Is4() != (kind == "IP-CIDR") {
				return errLine("CIDR 地址或地址族无效")
			}
			n = n.Masked()
			if _, ok := r.prefix[n]; !ok {
				r.prefix[n] = h
			}
		default:
			return errLine("暂不支持 " + kind)
		}
		r.count++
		if cache != nil {
			fmt.Fprintf(cache, "%s,%s,%s\n", kind, value, a)
		}
	}
	return scan.Err()
}
func (r *routeIndex) match(target string) (hit routeHit) {
	if r == nil {
		return
	}
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		host = target
	}
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	take := func(h routeHit) {
		if h.order > 0 && (hit.order == 0 || h.order < hit.order) {
			hit = h
		}
	}
	if ip, e := netip.ParseAddr(host); e == nil {
		ip = ip.Unmap()
		if len(r.prefix) > 0 {
			for bits := ip.BitLen(); bits >= 0; bits-- {
				take(r.prefix[netip.PrefixFrom(ip, bits).Masked()])
			}
		}
		return
	}
	take(r.exact[host])
	for s := host; s != ""; {
		take(r.suffix[s])
		_, s, _ = strings.Cut(s, ".")
	}
	for _, k := range r.keywords {
		if strings.Contains(host, k.term) {
			take(k.hit)
		}
	}
	return
}

type routeFeed struct{ action, url string }

func routeFeeds(text string) ([]routeFeed, error) {
	var feeds []routeFeed
	for i, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 || !routeAction(strings.ToUpper(fields[0])) {
			return nil, fmt.Errorf("订阅第 %d 行需要 动作 HTTPS直链", i+1)
		}
		u, e := url.Parse(fields[1])
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return nil, fmt.Errorf("订阅第 %d 行地址无效", i+1)
		}
		if len(feeds) >= 12 {
			return nil, errors.New("最多 12 个分流订阅")
		}
		feeds = append(feeds, routeFeed{strings.ToUpper(fields[0]), u.String()})
	}
	return feeds, nil
}
func routeSnapshot(path string, raw []byte) (string, error) {
	sum := fmt.Sprintf("%x", sha256.Sum256(raw))
	dir := filepath.Join(filepath.Dir(path), "route-cache")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	f, e := os.CreateTemp(dir, ".rules-*")
	if e != nil {
		return "", e
	}
	defer os.Remove(f.Name())
	_, e = f.Write(raw)
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e == nil {
		e = os.Rename(f.Name(), filepath.Join(dir, sum+".txt"))
	}
	return sum, e
}
func buildRouting(ctx context.Context, path string, s RouteSettings, refresh bool, client *http.Client) (*routeIndex, string, error) {
	r := newRouteIndex()
	if e := r.add(s.Manual, "本地规则", ""); e != nil {
		return nil, "", e
	}
	feeds, e := routeFeeds(s.Sources)
	if e != nil {
		return nil, "", e
	}
	if len(feeds) == 0 {
		return r, "", nil
	}
	if !refresh {
		b, e := hex.DecodeString(s.Cache)
		if e != nil || len(b) != 32 {
			return nil, "", errors.New("缺少分流订阅缓存，请更新规则")
		}
		file, e := os.Open(filepath.Join(filepath.Dir(path), "route-cache", s.Cache+".txt"))
		if e != nil {
			return nil, "", e
		}
		defer file.Close()
		raw, e := io.ReadAll(io.LimitReader(file, (16<<20)+1))
		if e != nil || len(raw) > 16<<20 || fmt.Sprintf("%x", sha256.Sum256(raw)) != s.Cache {
			return nil, "", errors.New("分流订阅缓存校验失败")
		}
		return r, s.Cache, r.add(string(raw), "缓存", "")
	}
	var merged bytes.Buffer
	for i, f := range feeds {
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
		if e != nil {
			return nil, "", e
		}
		res, e := client.Do(req)
		if e != nil {
			return nil, "", fmt.Errorf("订阅 %d 下载失败", i+1)
		}
		data, e := io.ReadAll(io.LimitReader(res.Body, (8<<20)+1))
		res.Body.Close()
		if e != nil || len(data) > 8<<20 || res.StatusCode != 200 {
			return nil, "", fmt.Errorf("订阅 %d 下载失败或文件过大 (HTTP %d)", i+1, res.StatusCode)
		}
		label := fmt.Sprintf("订阅 %d", i+1)
		before := r.count
		if e = r.parse(string(data), label, f.action, &merged); e != nil {
			return nil, "", e
		}
		if before == r.count {
			return nil, "", fmt.Errorf("%s 没有规则", label)
		}
		if merged.Len() > 16<<20 {
			return nil, "", errors.New("分流订阅总大小超过限制")
		}
	}
	hash, e := routeSnapshot(path, merged.Bytes())
	return r, hash, e
}
func routeHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) > 8 {
			return errors.New("无效重定向")
		}
		return nil
	}}
}
