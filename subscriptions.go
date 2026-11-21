package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const defaultAdSource = "https://raw.githubusercontent.com/217heidai/adblockfilters/dist/adblockdomain.txt"
const maxAdBytes = 32 << 20
const adRefreshInterval = 8 * time.Hour

type adRules struct{ blocks, exceptions map[string]bool }

func (r *adRules) blocksHost(host string) bool {
	if r == nil {
		return false
	}
	block := false
	for {
		if r.exceptions[host] {
			return false
		}
		block = block || r.blocks[host]
		_, rest, more := strings.Cut(host, ".")
		if !more {
			return block
		}
		host = rest
	}
}

func parseAdSource(data []byte, r *adRules) error {
	if len(data) > maxAdBytes {
		return errors.New("单个订阅超过 32 MiB")
	}
	found := false
	if bytes.IndexByte(data, 0) >= 0 {
		return errors.New("订阅文件不是文本域名表")
	}
	scan := bufio.NewScanner(bytes.NewReader(data))
	for scan.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scan.Text(), "\uFEFF"))
		if line == "" || strings.HasPrefix(line, "!") || strings.HasPrefix(line, "#") {
			continue
		}
		target := r.blocks
		if strings.HasPrefix(line, "@@||") {
			target = r.exceptions
			line = line[2:]
		}
		if strings.HasPrefix(line, "||") {
			var ok bool
			line, ok = strings.CutSuffix(line[2:], "^")
			if !ok {
				continue
			}
		}
		host := strings.ToLower(strings.TrimSuffix(line, "."))
		if validWebsiteName(host) {
			target[host] = true
			found = true
		}
	}
	if err := scan.Err(); err != nil {
		return err
	}
	if !found {
		return errors.New("订阅中没有域名规则。请使用纯域名列表或 AdGuard DNS 域名规则直链")
	}
	return nil
}

func adSources(lines []string) ([]string, error) {
	var sources []string
	for _, line := range lines {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		if strings.TrimSuffix(s, "/") == "https://github.com/217heidai/adblockfilters" {
			s = defaultAdSource
		}
		u, err := url.Parse(s)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return nil, errors.New("订阅需要有效的 HTTPS 直链")
		}
		if !slices.Contains(sources, s) {
			sources = append(sources, s)
		}
	}
	if len(sources) > 12 {
		return nil, errors.New("最多可添加 12 个订阅")
	}
	return sources, nil
}

func loadAdRules(ctx context.Context, config string, sources []string, force bool) (*adRules, bool, error) {
	r := &adRules{blocks: map[string]bool{}, exceptions: map[string]bool{}}
	client := http.Client{Timeout: 35 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) > 8 {
			return errors.New("订阅重定向无效")
		}
		return nil
	}}
	stale := false
	for i, src := range sources {
		path := filepath.Join(filepath.Dir(config), "adblock-cache", fmt.Sprintf("%x.txt", sha256.Sum256([]byte(src))))
		info, err := os.Stat(path)
		validCache := err == nil && info.Mode().IsRegular() && info.Size() <= maxAdBytes
		if !force && validCache {
			data, e := os.ReadFile(path)
			if e == nil && parseAdSource(data, r) == nil {
				stale = stale || time.Since(info.ModTime()) > adRefreshInterval
				continue
			}
		}
		req, err := http.NewRequestWithContext(ctx, "GET", src, nil)
		if err != nil {
			return nil, false, fmt.Errorf("订阅 %d 地址无效", i+1)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, false, fmt.Errorf("订阅 %d 下载失败，请检查网络或链接", i+1)
		}
		data, e := io.ReadAll(io.LimitReader(resp.Body, maxAdBytes+1))
		resp.Body.Close()
		if e != nil || resp.StatusCode != 200 {
			return nil, false, fmt.Errorf("订阅 %d 下载失败 (HTTP %d)", i+1, resp.StatusCode)
		}
		if err = parseAdSource(data, r); err != nil {
			return nil, false, fmt.Errorf("订阅 %d: %w", i+1, err)
		}
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, false, err
		}
		f, err := os.CreateTemp(filepath.Dir(path), ".rules-*")
		if err != nil {
			return nil, false, err
		}
		_, err = f.Write(data)
		ce := f.Close()
		if err == nil {
			err = ce
		}
		if err == nil {
			err = os.Rename(f.Name(), path)
		}
		_ = os.Remove(f.Name())
		if err != nil {
			return nil, false, err
		}
	}
	return r, stale, nil
}
