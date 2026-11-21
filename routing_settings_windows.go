package main

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"
)

func (g *GUIController) applyRouting(entryID, sourcesText string) (int, error) {
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
	cfg.ChainEntry, cfg.AdSubscriptions = entryID, src
	if entryID != "" {
		if _, ok := cfg.entryProfile(); !ok {
			return 0, fmt.Errorf("前置节点已变更，请重新选择")
		}
	}
	changed := g.cfg.ChainEntry != entryID
	if err = g.useConfigLocked(&cfg); err != nil {
		return 0, err
	}
	if changed {
		for probe := range g.probes {
			probe.Close()
		}
	}
	g.router.ads.Store(rules)
	return len(rules.blocks), nil
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
