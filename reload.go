package main

import (
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"syscall"
)

type MultiUserServerConfig struct {
	Listen string      `json:"listen"`
	Users  []UserEntry `json:"users"`
}

func (m *MultiUserEngine) ReplaceUsers(cfgs []UserEntry) error {
	next, err := NewMultiUserEngine(cfgs)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.users = next.users
	m.mu.Unlock()
	return nil
}

func WatchReloadSignal(cfgPath string, engine *MultiUserEngine) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)

	go func() {
		for range ch {
			data, err := os.ReadFile(cfgPath)
			if err != nil {
				log.Printf("[Reload] 读取配置失败: %v", err)
				continue
			}
			var cfg MultiUserServerConfig
			if err := json.Unmarshal(data, &cfg); err != nil {
				log.Printf("[Reload] JSON 解析失败: %v", err)
				continue
			}
			if err := engine.ReplaceUsers(cfg.Users); err != nil {
				log.Printf("[Reload] 密钥校验失败，保持旧配置: %v", err)
				continue
			}
			log.Printf("[Reload] 热重载成功，当前生效用户数: %d", len(cfg.Users))
		}
	}()
}
