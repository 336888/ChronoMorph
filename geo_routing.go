package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"

	mmdb "github.com/oschwald/maxminddb-golang/v2"
	"github.com/xtls/xray-core/common/geodata"
	"google.golang.org/protobuf/proto"
)

const geoRelease = "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/"
const geoSiteFile = "geosite.dat"
const geoIPFile = "geoip.metadb"
const geoASNFile = "GeoLite2-ASN.mmdb"

var geoFilenames = [...]string{geoSiteFile, geoIPFile, geoASNFile}

type siteRoute struct {
	edits    map[string]bool
	selector string
	hit      routeHit
	matcher  geodata.DomainMatcher
}

type geoRoutes struct {
	missing          error
	overrides        geoOverrides
	downloaded       []string
	files            map[string]string
	sites            []siteRoute
	siteSelectors    map[string]bool
	countries        map[string]routeHit
	asns             map[uint32]routeHit
	countryDB, asnDB *mmdb.Reader
}

func (g *geoRoutes) require(name string) {
	if g.files == nil {
		g.files = make(map[string]string)
	}
	g.files[name] = ""
}

func geoLabel(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

func (g *geoRoutes) add(kind, value string, hit routeHit) error {
	switch kind {
	case "GEOSITE":
		for _, part := range strings.Split(value, "@") {
			if !geoLabel(part) {
				return fmt.Errorf("无效的 GeoSite 分类 %q", value)
			}
		}
		g.require(geoSiteFile)
		if g.siteSelectors[value] {
			return nil
		}
		if g.siteSelectors == nil {
			g.siteSelectors = make(map[string]bool)
		}
		g.siteSelectors[value] = true
		g.sites = append(g.sites, siteRoute{selector: value, hit: hit})
	case "GEOIP":
		if !geoLabel(value) {
			return fmt.Errorf("无效的 GeoIP 标签 %q", value)
		}
		g.require(geoIPFile)
		if g.countries == nil {
			g.countries = make(map[string]routeHit)
		}
		if _, ok := g.countries[value]; !ok {
			g.countries[value] = hit
		}
	case "IP-ASN":
		n, err := strconv.ParseUint(strings.TrimPrefix(value, "as"), 10, 32)
		if err != nil || n == 0 {
			return fmt.Errorf("无效的 ASN %q", value)
		}
		g.require(geoASNFile)
		if g.asns == nil {
			g.asns = make(map[uint32]routeHit)
		}
		if _, ok := g.asns[uint32(n)]; !ok {
			g.asns[uint32(n)] = hit
		}
	}
	return nil
}

func (g *geoRoutes) matchIP(ip netip.Addr, take func(routeHit)) {
	if override, found := prefixOverride(g.overrides.countries, ip); found {
		take(g.countries[override])
	} else if g.countryDB != nil && len(g.countries) > 0 {
		var codes any
		if g.countryDB.Lookup(ip).Decode(&codes) == nil {
			switch v := codes.(type) {
			case string:
				take(g.countries[strings.ToLower(v)])
			case []any:
				for _, item := range v {
					if s, ok := item.(string); ok {
						take(g.countries[strings.ToLower(s)])
					}
				}
			}
		}
	}
	if override, found := prefixOverride(g.overrides.asns, ip); found {
		take(g.asns[override])
	} else if g.asnDB != nil && len(g.asns) > 0 {
		var asn uint32
		if g.asnDB.Lookup(ip).DecodePath(&asn, "autonomous_system_number") == nil {
			take(g.asns[asn])
		}
	}
}

func (g *geoRoutes) loadSites(data []byte) error {
	var list geodata.GeoSiteList
	if err := proto.Unmarshal(data, &list); err != nil {
		return err
	}
	if len(list.Entry) == 0 {
		return fmt.Errorf("GeoSite 数据库为空")
	}
	groups := make(map[string]*geodata.GeoSite, len(list.Entry))
	for _, group := range list.Entry {
		groups[strings.ToLower(group.Code)] = group
	}
	var factory geodata.MphDomainMatcherFactory
	for i, rule := range g.sites {
		g.sites[i].edits = g.overrides.sites[rule.selector]
		key := rule.selector
		var attrs []string
		if groups[key] == nil {
			parts := strings.Split(key, "@")
			key, attrs = parts[0], parts[1:]
		}
		group := groups[key]
		if group == nil {
			if len(g.sites[i].edits) > 0 {
				continue
			}
			return fmt.Errorf("找不到 GeoSite 分类 %q", key)
		}
		var rules []*geodata.DomainRule
		for _, domain := range group.Domain {
			if !hasSiteAttrs(domain, attrs) {
				continue
			}
			rules = append(rules, &geodata.DomainRule{Value: &geodata.DomainRule_Custom{Custom: domain}})
		}
		if len(rules) == 0 {
			if len(g.sites[i].edits) > 0 {
				continue
			}
			return fmt.Errorf("GeoSite 分类 %q 没有匹配条目", rule.selector)
		}
		m, err := factory.BuildMatcher(rules)
		if err != nil {
			return fmt.Errorf("%s: %w", rule.selector, err)
		}
		g.sites[i].matcher = m
	}
	return nil
}

func hasSiteAttrs(domain *geodata.Domain, attrs []string) bool {
	for _, attr := range attrs {
		found := false
		for _, a := range domain.Attribute {
			if strings.EqualFold(a.Key, attr) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (g *geoRoutes) decode(name string, data []byte, verify bool) error {
	if name == geoSiteFile {
		return g.loadSites(data)
	}
	db, err := mmdb.OpenBytes(data)
	if err != nil {
		return err
	}
	want := "Meta-geoip0"
	if name == geoASNFile {
		want = "GeoLite2-ASN"
	}
	if db.Metadata.DatabaseType != want {
		return fmt.Errorf("数据库类型 %q，预期 %q", db.Metadata.DatabaseType, want)
	}
	if verify {
		var err error
		if name == geoIPFile {
			err = verifyMetaCountries(db)
		} else {
			err = db.Verify()
		}
		if err != nil {
			return err
		}
	}
	if name == geoIPFile {
		g.countryDB = db
	} else {
		g.asnDB = db
	}
	return nil
}

// Meta-geoip0 omits the descriptive metadata required by the generic
// verifier. Walk the networks and validate each distinct record once.
func verifyMetaCountries(db *mmdb.Reader) error {
	seen := make(map[uintptr]bool)
	for rec := range db.Networks() {
		if err := rec.Err(); err != nil {
			return err
		}
		if seen[rec.Offset()] {
			continue
		}
		seen[rec.Offset()] = true
		var value any
		if err := rec.Decode(&value); err != nil {
			return err
		}
		switch v := value.(type) {
		case string:
			if v == "" {
				return fmt.Errorf("空 GeoIP 标签")
			}
		case []any:
			for _, code := range v {
				if s, ok := code.(string); !ok || s == "" {
					return fmt.Errorf("无效的 GeoIP 标签")
				}
			}
		default:
			return fmt.Errorf("无效的 Meta-geoip0 记录类型 %T", value)
		}
	}
	if len(seen) == 0 {
		return fmt.Errorf("GeoIP 数据库为空")
	}
	return nil
}

func (g *geoRoutes) load(ctx context.Context, path string, settings RouteSettings, refresh bool, selected []string, client *http.Client, pending *routeSnapshots) error {
	for _, name := range geoFilenames {
		if _, ok := g.files[name]; !ok {
			continue
		}
		resource := settings.Resources[name]
		source, err := resource.source(name)
		if err != nil {
			return err
		}
		fetch := refresh && selectedResource(selected, name)
		digest := settings.GeoFiles[name]
		const maxBytes = 64 << 20
		var item cachedRule
		var data []byte
		if fetch {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
			if err != nil {
				return err
			}
			resp, err := client.Do(req)
			if err != nil {
				return fmt.Errorf("%s 下载失败: %w", name, err)
			}
			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				return fmt.Errorf("%s 下载失败 (HTTP %d)", name, resp.StatusCode)
			}
			data, err = io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
			resp.Body.Close()
			if err != nil || len(data) > maxBytes {
				return fmt.Errorf("%s 读取失败或超过 64 MiB", name)
			}
		} else {
			sum, cacheErr := hex.DecodeString(digest)
			if cacheErr != nil || len(sum) != sha256.Size {
				cacheErr = errMissingRouteData
			} else {
				item, cacheErr = readRuleCache(path, routingCache, digest+"-"+name, maxBytes)
			}
			if cacheErr != nil {
				failure := fmt.Errorf("%s 缓存不可用，请同步资源: %w", name, cacheErr)
				if selected == nil || !errors.Is(cacheErr, errMissingRouteData) && !errors.Is(cacheErr, os.ErrNotExist) {
					return failure
				}
				g.missing = failure
				delete(g.files, name)
				continue
			}
			data = item.data
		}
		if !fetch && fmt.Sprintf("%x", sha256.Sum256(data)) != digest {
			return fmt.Errorf("%s 缓存校验失败", name)
		}
		if err := g.decode(name, data, fetch); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if fetch {
			digest = pending.add(data, "-"+name)
			g.downloaded = append(g.downloaded, name)
		}
		if item.legacy {
			*pending = append(*pending, item)
		}
		g.files[name] = digest
	}
	return nil
}
