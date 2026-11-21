package main

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"
)

func (g *GUIController) saveChain(entryID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	cfg := *g.cfg
	if cfg.ChainEntry == entryID {
		return nil
	}
	cfg.ChainEntry = entryID
	if entryID != "" {
		if _, ok := cfg.entryProfile(); !ok {
			return fmt.Errorf("前置节点已变更，请重新选择")
		}
	}
	if err := g.useConfigLocked(&cfg); err != nil {
		return err
	}
	for probe := range g.probes {
		probe.Close()
	}
	return nil
}

// Only subscription downloads share this mutex. Changing the chain and
// resolving false positives remain local even when a feed is unreachable.
func (g *GUIController) saveSubscriptions(sourcesText string) (int, error) {
	src, err := adSources(strings.Split(sourcesText, "\n"))
	if err != nil {
		return 0, err
	}
	g.rulesMu.Lock()
	defer g.rulesMu.Unlock()
	rules, _, err := loadAdRules(context.Background(), g.cfgPath, src, true)
	if err != nil {
		return 0, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	cfg := *g.cfg
	cfg.AdSubscriptions = src
	if err = g.installConfigLocked(&cfg, nil); err != nil {
		return 0, err
	}
	g.router.ads.Store(rules)
	return len(rules.blocks), nil
}

func (g *GUIController) saveAllowList(text string) error {
	domains, err := allowDomains(text)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	cfg := *g.cfg
	cfg.AdAllowDomains = domains
	return g.saveFilterPreferencesLocked(&cfg)
}

func (g *GUIController) toggleAdPause() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	cfg := *g.cfg
	cfg.AdPaused = !cfg.AdPaused
	return g.saveFilterPreferencesLocked(&cfg)
}

func (g *GUIController) saveFilterPreferencesLocked(cfg *Config) error {
	if err := g.installConfigLocked(cfg, nil); err != nil {
		return err
	}
	g.router.setAdPreferences(cfg.AdAllowDomains, cfg.AdPaused)
	return nil
}

func (g *GUIController) startSubscriptions(stop <-chan struct{}) {
	refresh := func(force bool) {
		g.rulesMu.Lock()
		defer g.rulesMu.Unlock()
		g.mu.Lock()
		src := slices.Clone(g.cfg.AdSubscriptions)
		g.mu.Unlock()
		if len(src) == 0 {
			return
		}
		sources, err := adSources(src)
		if err != nil {
			log.Printf("[去广告] %v", err)
			return
		}
		r, stale, err := loadAdRules(context.Background(), g.cfgPath, sources, force)
		if err == nil {
			g.router.ads.Store(r)
			if stale {
				r, _, err = loadAdRules(context.Background(), g.cfgPath, sources, true)
				if err == nil {
					g.router.ads.Store(r)
				}
			}
		}
		if err != nil {
			log.Printf("[去广告] %v", err)
		}
	}
	go func() {
		refresh(false)
		ticker := time.NewTicker(adRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				refresh(true)
			}
		}
	}()
}
