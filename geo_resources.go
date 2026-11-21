package main

import (
	"fmt"
	"maps"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// User edits sit above the downloaded snapshot. Replacing a snapshot keeps
// local corrections and the editor can restore upstream behavior by removing a line.
type GeoResource struct {
	URL       string `json:"url,omitempty"`
	Overrides string `json:"overrides,omitempty"`
	SyncedAt  int64  `json:"synced_at,omitempty"`
	SyncedURL string `json:"synced_url,omitempty"`
}

func (v GeoResource) source(name string) (string, error) {
	s := strings.TrimSpace(v.URL)
	if s == "" {
		return geoRelease + name, nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return "", fmt.Errorf("%s 需要有效的 HTTPS 下载地址", name)
	}
	return s, nil
}

func (s RouteSettings) withResource(name string, v GeoResource) RouteSettings {
	s.Resources = maps.Clone(s.Resources)
	if s.Resources == nil {
		s.Resources = make(map[string]GeoResource)
	}
	s.Resources[name] = v
	return s
}

// Keep installed resources available after their last routing rule is removed.
func (s RouteSettings) withIndex(idx *routeIndex, hash string) RouteSettings {
	s.Cache = hash
	s.GeoFiles = maps.Clone(s.GeoFiles)
	if len(idx.geo.files) > 0 && s.GeoFiles == nil {
		s.GeoFiles = make(map[string]string)
	}
	maps.Copy(s.GeoFiles, idx.geo.files)
	for _, name := range idx.geo.downloaded {
		res := s.Resources[name]
		res.SyncedAt = time.Now().Unix()
		res.SyncedURL, _ = res.source(name)
		s = s.withResource(name, res)
	}
	return s
}

type geoOverrides struct {
	sites     map[string]map[string]bool
	countries map[netip.Prefix]string
	asns      map[netip.Prefix]uint32
}

func (g *geoOverrides) parse(name, text string) error {
	if len(text) > 1<<20 {
		return fmt.Errorf("%s 本地条目超过 1 MiB", name)
	}
	for i, raw := range strings.Split(text, "\n") {
		line := strings.ToLower(strings.TrimSpace(raw))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ",")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		invalid := func() error { return fmt.Errorf("%s 本地条目第 %d 行格式无效", name, i+1) }
		if !ok {
			return invalid()
		}
		if name == geoSiteFile {
			for _, part := range strings.Split(key, "@") {
				if !geoLabel(part) {
					return invalid()
				}
			}
			domain := strings.TrimSuffix(strings.TrimPrefix(value, "!"), ".")
			if !validWebsiteName(domain) {
				return invalid()
			}
			if g.sites == nil {
				g.sites = make(map[string]map[string]bool)
			}
			if g.sites[key] == nil {
				g.sites[key] = make(map[string]bool)
			}
			g.sites[key][domain] = !strings.HasPrefix(value, "!")
			continue
		}
		p, err := netip.ParsePrefix(key)
		if err != nil {
			ip, err := netip.ParseAddr(key)
			if err != nil || ip.Zone() != "" {
				return invalid()
			}
			ip = ip.Unmap()
			p = netip.PrefixFrom(ip, ip.BitLen())
		}
		if p.Addr().Is4In6() {
			return invalid()
		}
		p = p.Masked()
		switch name {
		case geoIPFile:
			if value != "-" && !geoLabel(value) {
				return invalid()
			}
			if g.countries == nil {
				g.countries = make(map[netip.Prefix]string)
			}
			if value == "-" {
				value = ""
			}
			g.countries[p] = value
		case geoASNFile:
			var n uint64
			if value != "-" {
				n, err = strconv.ParseUint(strings.TrimPrefix(value, "as"), 10, 32)
				if err != nil || n == 0 {
					return invalid()
				}
			}
			if g.asns == nil {
				g.asns = make(map[netip.Prefix]uint32)
			}
			g.asns[p] = uint32(n)
		default:
			return invalid()
		}
	}
	return nil
}

func prefixOverride[T any](entries map[netip.Prefix]T, ip netip.Addr) (v T, ok bool) {
	if len(entries) > 0 {
		for n := ip.BitLen(); n >= 0; n-- {
			if v, ok = entries[netip.PrefixFrom(ip, n).Masked()]; ok {
				return
			}
		}
	}
	return
}

func (s siteRoute) matches(host string) bool {
	if len(s.edits) > 0 {
		for d := host; d != ""; {
			if yes, found := s.edits[d]; found {
				return yes
			}
			_, d, _ = strings.Cut(d, ".")
		}
	}
	return s.matcher != nil && s.matcher.MatchAny(host)
}

// nil selects the databases required by the active rules. Resource commands
// pass an explicit set so unrelated subscriptions and databases remain offline.
func selectedResource(names []string, name string) bool {
	return names == nil || slices.Contains(names, name)
}
