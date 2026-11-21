package main

import (
	"encoding/json"
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var proxyNames = [...]string{"ProxyEnable", "ProxyServer", "AutoConfigURL"}

type proxyValue struct {
	Type   uint32
	Text   string
	Number uint32
}
type proxyValues [3]proxyValue
type proxyJournal struct {
	Original proxyValues
	Previous proxyValues
	Applied  proxyValues
}

type proxyRegistry struct{ settings, journal string }

var systemProxy = proxyRegistry{`Software\Microsoft\Windows\CurrentVersion\Internet Settings`, `Software\ChronoMorph`}

func readProxyValues(k registry.Key) (proxyValues, error) {
	var values proxyValues
	for i, name := range proxyNames {
		_, typ, err := k.GetValue(name, nil)
		if errors.Is(err, registry.ErrNotExist) {
			continue
		}
		if err != nil {
			return values, err
		}
		v := proxyValue{Type: typ}
		switch typ {
		case registry.DWORD:
			n, _, err := k.GetIntegerValue(name)
			if err != nil {
				return values, err
			}
			v.Number = uint32(n)
		case registry.SZ, registry.EXPAND_SZ:
			s, _, err := k.GetStringValue(name)
			if err != nil {
				return values, err
			}
			v.Text = s
		default:
			return values, fmt.Errorf("无法保存系统代理项 %s 的类型 %d", name, typ)
		}
		values[i] = v
	}
	return values, nil
}

func (v proxyValue) write(k registry.Key, name string) error {
	switch v.Type {
	case 0:
		err := k.DeleteValue(name)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	case registry.DWORD:
		return k.SetDWordValue(name, v.Number)
	case registry.SZ:
		return k.SetStringValue(name, v.Text)
	case registry.EXPAND_SZ:
		return k.SetExpandStringValue(name, v.Text)
	default:
		return fmt.Errorf("unsupported proxy setting type %d", v.Type)
	}
}

func (p proxyRegistry) load() (*proxyJournal, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, p.journal, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer k.Close()
	s, _, err := k.GetStringValue("ProxyRecovery")
	if errors.Is(err, registry.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var j proxyJournal
	if err = json.Unmarshal([]byte(s), &j); err != nil {
		return nil, err
	}
	return &j, nil
}

// Record both sides before changing any values so startup can recover an interrupted update.
func (p proxyRegistry) apply(addr string, global bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, p.settings, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	current, err := readProxyValues(k)
	if err != nil {
		return err
	}
	old, err := p.load()
	if err != nil {
		return err
	}
	j := proxyJournal{Original: current, Previous: current, Applied: current}
	if old != nil {
		for i := range current {
			if current[i] == old.Applied[i] || current[i] == old.Previous[i] {
				j.Original[i] = old.Original[i]
			}
		}
	}
	j.Applied[0] = proxyValue{Type: registry.DWORD}
	if global {
		j.Applied[0].Number = 1
		j.Applied[1] = proxyValue{Type: registry.SZ, Text: addr}
		j.Applied[2] = proxyValue{}
	} else {
		j.Applied[2] = proxyValue{Type: registry.SZ, Text: "http://127.0.0.1:18080/proxy.pac"}
	}
	state, _, err := registry.CreateKey(registry.CURRENT_USER, p.journal, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer state.Close()
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	if err = state.SetStringValue("ProxyRecovery", string(data)); err != nil {
		return err
	}
	for _, i := range []int{1, 2, 0} {
		if current[i] != j.Applied[i] {
			if err = j.Applied[i].write(k, proxyNames[i]); err != nil {
				return errors.Join(err, p.restore())
			}
		}
	}
	// Once all writes succeed only our final values belong to this process.
	j.Previous = j.Applied
	data, err = json.Marshal(j)
	if err == nil {
		err = state.SetStringValue("ProxyRecovery", string(data))
	}
	if err != nil {
		return errors.Join(err, p.restore())
	}
	return nil
}

func (p proxyRegistry) restore() error {
	j, err := p.load()
	if err != nil || j == nil {
		return err
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, p.settings, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	current, err := readProxyValues(k)
	if err != nil {
		return err
	}
	for _, i := range []int{1, 2, 0} {
		if current[i] != j.Original[i] && (current[i] == j.Applied[i] || current[i] == j.Previous[i]) {
			if err = j.Original[i].write(k, proxyNames[i]); err != nil {
				return err
			}
		}
	}
	state, err := registry.OpenKey(registry.CURRENT_USER, p.journal, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer state.Close()
	return state.DeleteValue("ProxyRecovery")
}

func notifyProxyChange() {
	setOption := windows.NewLazySystemDLL("wininet.dll").NewProc("InternetSetOptionW")
	setOption.Call(0, 39, 0, 0) // INTERNET_OPTION_SETTINGS_CHANGED
	setOption.Call(0, 37, 0, 0) // INTERNET_OPTION_REFRESH
}

func restoreSystemProxy() error {
	j, err := systemProxy.load()
	if err != nil || j == nil {
		return err
	}
	if err = systemProxy.restore(); err != nil {
		return err
	}
	notifyProxyChange()
	return nil
}

func (g *GUIController) setProxyLocked(enable, global bool) error {
	if !enable {
		if err := restoreSystemProxy(); err != nil {
			return err
		}
		g.sysProxyOn = false
		return nil
	}
	if !g.running || g.ln == nil {
		return errors.New("请先启动本地代理")
	}
	if err := systemProxy.apply(g.proxyAddressLocked(), global); err != nil {
		if journal, readErr := systemProxy.load(); readErr == nil {
			g.sysProxyOn = journal != nil
		}
		notifyProxyChange()
		return err
	}
	notifyProxyChange()
	g.sysProxyOn = true
	return nil
}

func (g *GUIController) toggleSystemProxy() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.setProxyLocked(!g.sysProxyOn, g.router.IsGlobalMode())
}

func (g *GUIController) toggleRouteMode() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	next := !g.router.IsGlobalMode()
	if g.sysProxyOn {
		if err := g.setProxyLocked(true, next); err != nil {
			return err
		}
	}
	g.router.SetGlobalMode(next)
	return nil
}
