//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

type GUIController struct {
	rulesMu    sync.Mutex
	mu         sync.Mutex
	cfgPath    string
	cfg        *Config
	cm         *ClientMux
	probes     map[*ClientMux]struct{}
	ln         net.Listener
	router     *Router
	running    bool
	sysProxyOn bool
	pacErr     error
}

func (g *GUIController) StartTunnel() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.startTunnelLocked(g.cfg, false)
}

func (g *GUIController) startTunnelLocked(cfg *Config, save bool) error {
	if g.running {
		return nil
	}
	cm, err := cfg.nodeMux(cfg.ServerIP, cfg.MasterKey, cfg.TLSNode)
	if err != nil {
		return err
	}
	listen := cfg.Listen
	if listen == "" {
		listen = "127.0.0.1:1080"
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		cm.Close()
		return err
	}
	if save {
		if err := SaveConfigAtomic(g.cfgPath, cfg); err != nil {
			_ = ln.Close()
			cm.Close()
			return fmt.Errorf("保存客户端配置失败: %w", err)
		}
	}
	g.cfg = cfg
	cm.startClockSync()
	g.cm = cm
	g.ln = ln
	g.running = true

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			g.mu.Lock()
			active := g.cm
			listener := g.ln
			g.mu.Unlock()
			if active == nil || listener != ln {
				_ = c.Close()
				continue
			}
			go handleMixedProxy(c, active, g.router)
		}
	}()
	log.Printf("[GUI] 加密隧道已启动，监听 %s", listen)
	return nil
}

func (g *GUIController) StopTunnel() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stopTunnelLocked()
}

func (g *GUIController) stopTunnelLocked() error {
	GlobalTUN.StopTUN()
	if g.sysProxyOn {
		if err := g.setProxyLocked(false, false); err != nil {
			return err
		}
	}
	if !g.running {
		return nil
	}
	if g.ln != nil {
		_ = g.ln.Close()
		g.ln = nil
	}
	if g.cm != nil {
		g.cm.Close()
		g.cm = nil
	}
	g.running = false
	log.Println("[GUI] 加密隧道已断开")
	return nil
}

func (g *GUIController) importProfile(p *ShareProfile) error {
	g.mu.Lock()
	cfg := *g.cfg
	cfg.ServerIP, cfg.MasterKey, cfg.TLSNode = p.ServerIP, p.MasterKey, p.TLSNode
	cfg.Profiles = append([]NodeProfile(nil), cfg.Profiles...)
	idx := len(cfg.Profiles)
	for i, node := range cfg.Profiles {
		sameTransport := node.scheme() == p.scheme() && node.WebName == p.WebName && node.RealityName == p.RealityName
		if sameTransport && node.ServerIP == p.ServerIP && node.MasterKey == p.MasterKey && node.TLSPort == p.TLSPort {
			idx = i
			break
		}
	}
	node := NodeProfile{TLSNode: p.TLSNode, Name: p.Name, ServerIP: p.ServerIP, MasterKey: p.MasterKey}
	if idx == len(cfg.Profiles) {
		cfg.Profiles = append(cfg.Profiles, node)
	} else {
		cfg.Profiles[idx] = node
	}
	cfg.ActiveIndex = idx
	defer g.mu.Unlock()
	return g.useConfigLocked(&cfg)
}

func RunWindowsGUI(cfgPath string) {
	setDesktopDPI()
	name, _ := windows.UTF16PtrFromString(`Local\ChronoMorph.Desktop.Singleton`)
	mutex, mutexErr := windows.CreateMutex(nil, false, name)
	if mutex != 0 {
		defer windows.CloseHandle(mutex)
	}
	if mutexErr == windows.ERROR_ALREADY_EXISTS {
		for i := 0; i < 40; i++ {
			if hwnd := existingDesktop(); hwnd != 0 {
				notifyWindow(hwnd, msgShow)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		return
	}
	if mutexErr != nil {
		txt, _ := windows.UTF16PtrFromString(mutexErr.Error())
		_, _ = windows.MessageBox(0, txt, utf("ChronoMorph"), windows.MB_ICONERROR)
		return
	}
	// The native window works independently of the optional PAC and compatibility API.
	ln, pacErr := net.Listen("tcp4", "127.0.0.1:18080")
	if ln != nil {
		defer ln.Close()
	}
	log.SetOutput(AppLogs)
	if err := restoreSystemProxy(); err != nil {
		log.Printf("[GUI] 恢复此前的系统代理失败: %v", err)
	}
	cfg := &Config{Listen: "127.0.0.1:1080"}
	if b, err := os.ReadFile(cfgPath); err == nil {
		_ = json.Unmarshal(b, cfg)
		cfg.normalizeSavedNodes()
	}

	ctrl := &GUIController{
		cfgPath: cfgPath,
		pacErr:  pacErr,
		cfg:     cfg,
		router:  cfg.newRouter(),
	}

	stopCh := make(chan struct{})
	ctrl.startSubscriptions(stopCh)
	ctrl.StartTunnelWatchdog(stopCh)
	ctrl.StartAutoFailover(stopCh)

	cleanup := func() {
		if err := ctrl.StopTunnel(); err != nil {
			log.Printf("[GUI] 退出时恢复代理失败: %v", err)
		}
	}
	RegisterShutdownCleanup(cleanup)

	token := InitGUIToken()
	mux := http.NewServeMux()
	RegisterPACRoute(mux, ctrl)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(strings.ReplaceAll(guiHTML, "{{TOKEN}}", token)))
	})

	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		ctrl.mu.Lock()
		resp := map[string]any{
			"version":       AppVersion,
			"tls_port":      ctrl.cfg.TLSPort,
			"tls_only":      ctrl.cfg.TLSOnly,
			"udp_port":      ctrl.cfg.UDPPort,
			"transport":     ctrl.cm.transportName(),
			"running":       ctrl.running,
			"server_ip":     ctrl.cfg.ServerIP,
			"master_key":    ctrl.cfg.MasterKey,
			"listen":        ctrl.cfg.Listen,
			"sys_proxy":     ctrl.sysProxyOn,
			"global_mode":   ctrl.router.IsGlobalMode(),
			"auto_start":    IsAutoStartEnabled(),
			"tun_mode":      GlobalTUN.IsActive(),
			"active_port":   LiveStats.activePort.Load(),
			"hop_countdown": HopCountdownSec(),
			"tx_kbps":       LiveStats.txSpeedBps.Load() / 1024,
			"rx_kbps":       LiveStats.rxSpeedBps.Load() / 1024,
		}
		ctrl.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/api/toggle", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ServerIP  string `json:"server_ip"`
			MasterKey string `json:"master_key"`
			Listen    string `json:"listen"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "配置格式无效", http.StatusBadRequest)
			return
		}
		ctrl.mu.Lock()
		if ctrl.running {
			ctrl.mu.Unlock()
			if err := ctrl.StopTunnel(); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		} else {
			cfg := *ctrl.cfg
			cfg.ServerIP, cfg.MasterKey = req.ServerIP, req.MasterKey
			if req.Listen != "" {
				cfg.Listen = req.Listen
			}
			err := ctrl.startTunnelLocked(&cfg, true)
			ctrl.mu.Unlock()
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		w.WriteHeader(200)
	})

	mux.HandleFunc("/api/sysproxy", func(w http.ResponseWriter, r *http.Request) {
		if err := ctrl.toggleSystemProxy(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.WriteHeader(200)
	})

	mux.HandleFunc("/api/mode", func(w http.ResponseWriter, r *http.Request) {
		if err := ctrl.toggleRouteMode(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(200)
	})

	mux.HandleFunc("/api/autostart", func(w http.ResponseWriter, r *http.Request) {
		_ = SetAutoStart(!IsAutoStartEnabled())
		w.WriteHeader(200)
	})

	mux.HandleFunc("/api/tun", func(w http.ResponseWriter, r *http.Request) {
		if GlobalTUN.IsActive() {
			GlobalTUN.StopTUN()
			w.WriteHeader(200)
			return
		}
		ctrl.mu.Lock()
		running, listen, ip, cm := ctrl.running, ctrl.proxyAddressLocked(), ctrl.cfg.ServerIP, ctrl.cm
		ctrl.mu.Unlock()
		if !running {
			http.Error(w, "请先启动加密隧道", 400)
			return
		}
		if err := GlobalTUN.StartTUN(listen, ip, cm, ctrl.router); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(200)
	})

	mux.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) {
		ctrl.mu.Lock()
		cm := ctrl.cm
		ctrl.mu.Unlock()
		if cm == nil {
			http.Error(w, "未连接", 400)
			return
		}
		ms, err := MeasureTunnelLatency(cm)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]int64{"ms": ms})
	})

	mux.HandleFunc("/api/import", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Link string `json:"link"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "格式错误", 400)
			return
		}
		p, err := DecodeShareLink(req.Link)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := ctrl.importProfile(p); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(200)
	})

	mux.HandleFunc("/api/export", func(w http.ResponseWriter, r *http.Request) {
		ctrl.mu.Lock()
		p := ShareProfile{ServerIP: ctrl.cfg.ServerIP, MasterKey: ctrl.cfg.MasterKey, TLSNode: ctrl.cfg.TLSNode, Name: "ChronoMorph"}
		ctrl.mu.Unlock()
		link, err := p.encode()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_, _ = w.Write([]byte(link))
	})

	mux.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(AppLogs.Snapshot())
	})

	mux.HandleFunc("/api/diag", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(ctrl.BuildDiagReport()))
	})

	mux.HandleFunc("/api/update", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CheckLatestRelease())
	})

	if pacErr == nil {
		go func() { _ = http.Serve(ln, ProtectLocalAPI(mux)) }()
	} else {
		log.Printf("[PAC] 本地 18080 端口不可用: %v", pacErr)
	}

	if cfg.ServerIP != "" && len(cfg.MasterKey) == 64 {
		if err := ctrl.StartTunnel(); err != nil {
			log.Printf("[GUI] 启动本地代理失败: %v", err)
		}
	}
	ui, err := startDesktop(ctrl)
	if err != nil {
		close(stopCh)
		cleanup()
		text, _ := windows.UTF16PtrFromString(err.Error())
		title, _ := windows.UTF16PtrFromString("ChronoMorph")
		_, _ = windows.MessageBox(0, text, title, windows.MB_ICONERROR)
		return
	}
	RunTray(ctrl, func() { notifyWindow(ui.hwnd, msgShow) }, func() {
		notifyWindow(ui.hwnd, msgQuit)
		close(stopCh)
		cleanup()
	})
}

const guiHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<title>ChronoMorph</title>
<style>
  * { box-sizing: border-box; font-family: "Segoe UI", system-ui, sans-serif; }
  body { margin: 0; background: #161618; color: #dcdfe4; padding: 20px; user-select: none; max-width: 460px; margin: 0 auto; }
  .card { background: #1f1f23; border: 1px solid #2c2c32; border-radius: 14px; padding: 20px; }
  .header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 16px; }
  .title { font-size: 20px; font-weight: 600; }
  .badge { font-size: 12px; padding: 4px 10px; border-radius: 6px; background: #2c2c32; color: #9ca3af; }
  .badge.on { background: #1b3a2d; color: #4ade80; }
  label { display: block; font-size: 12px; color: #9ca3af; margin: 12px 0 6px; }
  input { width: 100%; padding: 10px 12px; border-radius: 8px; border: 1px solid #323238; background: #161618; color: #dcdfe4; outline: none; font-size: 14px; }
  .btn { width: 100%; margin-top: 16px; padding: 12px; border: none; border-radius: 8px; font-size: 15px; font-weight: 600; cursor: pointer; }
  .btn-start { background: #dcdfe4; color: #161618; }
  .btn-stop { background: #3f1d1d; color: #f87171; border: 1px solid #7f1d1d; }
  .row { display: flex; justify-content: space-between; align-items: center; margin-top: 12px; padding-top: 12px; border-top: 1px solid #2c2c32; font-size: 14px; }
  .pill { padding: 6px 12px; border-radius: 6px; border: 1px solid #3a3a42; background: #161618; color: #dcdfe4; cursor: pointer; font-size: 12px; }
  .pill.active { background: #dcdfe4; color: #161618; font-weight: 600; }
  .stats { display: flex; justify-content: space-between; font-size: 12px; color: #9ca3af; margin-top: 12px; padding: 10px 12px; background: #161618; border-radius: 8px; border: 1px solid #2c2c32; }
  .err { color: #f87171; font-size: 12px; margin-top: 8px; min-height: 16px; }
  pre { margin: 12px 0 0; background: #111113; border: 1px solid #2c2c32; border-radius: 8px; padding: 10px; height: 96px; overflow-y: auto; font-size: 11px; color: #8b949e; white-space: pre-wrap; font-family: Consolas, monospace; }
</style>
</head>
<body>
<div class="card">
  <div class="header">
    <div class="title">ChronoMorph <small id="ver" style="font-size:12px;color:#6b7280"></small></div>
    <span id="badge" class="badge">已断开</span>
  </div>
  <label>服务端 IP</label>
  <input id="server_ip" placeholder="例如 45.32.10.88">
  <label>MasterKey (64位 Hex)</label>
  <input id="master_key" type="password" placeholder="粘贴服务端生成的 MasterKey">
  <label>本地混合代理 (SOCKS5 + HTTP)</label>
  <input id="listen" value="127.0.0.1:1080">
  <button id="toggleBtn" class="btn btn-start" onclick="toggleTunnel()">启动加密隧道</button>
  <div id="err" class="err"></div>
  <div class="stats">
    <span>传输 <b id="port">--</b><span id="cd"></span></span>
    <span>↑ <b id="tx">0</b>K · ↓ <b id="rx">0</b>K</span>
  </div>
  <div class="row"><span>Windows 系统代理</span><button id="proxyBtn" class="pill" onclick="post('/api/sysproxy')">未开启</button></div>
  <div class="row"><span>路由模式</span><button id="modeBtn" class="pill" onclick="post('/api/mode')">智能分流</button></div>
  <div class="row"><span>TUN 虚拟网卡 (IPv4，需管理员)</span><button id="tunBtn" class="pill" onclick="post('/api/tun')">未开启</button></div>
  <div class="row"><span>开机自启</span><button id="autoBtn" class="pill" onclick="post('/api/autostart')">已关闭</button></div>
  <div class="row">
    <span>服务端延迟: <b id="pingVal">-- ms</b></span>
    <div>
      <button class="pill" onclick="testPing()">测速</button>
      <button class="pill" onclick="importLink()">导入</button>
      <button class="pill" onclick="exportLink()">导出</button>
    </div>
  </div>
  <pre id="logBox">等待日志...</pre>
</div>
<script>
const H = { 'X-CM-Token': '{{TOKEN}}' };
let editingConfig = false;
for (const input of document.querySelectorAll('input')) {
  input.addEventListener('input', () => { editingConfig = true; });
}
async function post(url) {
  const res = await fetch(url, { method: 'POST', headers: H });
  document.getElementById('err').textContent = res.ok ? '' : await res.text();
  refresh();
}
async function refresh() {
  try {
    const d = await (await fetch('/api/status', { headers: H })).json();
    document.getElementById('ver').textContent = d.version || '';
    if (!editingConfig) {
      document.getElementById('server_ip').value = d.server_ip || '';
      document.getElementById('master_key').value = d.master_key || '';
      document.getElementById('listen').value = d.listen || '127.0.0.1:1080';
    }
    const badge = document.getElementById('badge'), btn = document.getElementById('toggleBtn');
    badge.textContent = d.running ? '本地代理已启动' : '已断开';
    badge.className = 'badge' + (d.running ? ' on' : '');
    btn.textContent = d.running ? '断开隧道' : '启动加密隧道';
    btn.className = 'btn ' + (d.running ? 'btn-stop' : 'btn-start');
    setPill('proxyBtn', d.sys_proxy, '已接管', '未开启');
    setPill('modeBtn', d.global_mode, '全局代理', '智能分流');
    setPill('tunBtn', d.tun_mode, '已开启', '未开启');
    setPill('autoBtn', d.auto_start, '已开启', '已关闭');
    document.getElementById('port').textContent = d.udp_port ? 'TCP + UDP · ' + d.transport : d.tls_port ? 'TLS/TCP ' + d.tls_port : 'UDP ' + (d.active_port || '—');
    document.getElementById('cd').textContent = d.tls_port ? '' : ' (' + d.hop_countdown + 's)';
    document.getElementById('tx').textContent = d.tx_kbps ?? 0;
    document.getElementById('rx').textContent = d.rx_kbps ?? 0;
  } catch (e) {}
}
function setPill(id, on, tOn, tOff) {
  const el = document.getElementById(id);
  el.textContent = on ? tOn : tOff;
  el.className = 'pill' + (on ? ' active' : '');
}
async function toggleTunnel() {
  document.getElementById('err').textContent = '';
  const body = { server_ip: document.getElementById('server_ip').value.trim(), master_key: document.getElementById('master_key').value.trim(), listen: document.getElementById('listen').value.trim() };
  const res = await fetch('/api/toggle', { method: 'POST', headers: H, body: JSON.stringify(body) });
  if (res.ok) editingConfig = false;
  else document.getElementById('err').textContent = await res.text();
  refresh();
}
async function testPing() {
  document.getElementById('pingVal').textContent = '测速中...';
  const res = await fetch('/api/ping', { headers: H });
  document.getElementById('pingVal').textContent = res.ok ? ((await res.json()).ms + ' ms') : '失败';
  document.getElementById('err').textContent = res.ok ? '' : await res.text();
}
async function importLink() {
  const link = prompt('粘贴一个节点链接，也可复制 Debian 菜单 4 中的一整行');
  if (!link) return;
  const res = await fetch('/api/import', { method: 'POST', headers: H, body: JSON.stringify({ link: link.trim() }) });
  if (res.ok) editingConfig = false;
  document.getElementById('err').textContent = res.ok ? '导入成功' : await res.text();
  refresh();
}
async function exportLink() {
  const res = await fetch('/api/export', { headers: H });
  if (res.ok) { await navigator.clipboard.writeText(await res.text()); document.getElementById('err').textContent = '已复制节点链接'; }
}
async function refreshLogs() {
  try {
    const lines = await (await fetch('/api/logs', { headers: H })).json();
    const box = document.getElementById('logBox');
    box.textContent = lines.length ? lines.join('\n') : '暂无日志';
    box.scrollTop = box.scrollHeight;
  } catch (e) {}
}
refresh(); setInterval(refresh, 1000); setInterval(refreshLogs, 2000);
</script>
</body>
</html>`
