package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsLoadsTrayImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tray.ico")
	if err := os.WriteFile(path, trayIcon, 0600); err != nil {
		t.Fatal(err)
	}
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	user32 := windows.NewLazySystemDLL("user32.dll")
	for _, size := range []uintptr{16, 32, 48} {
		// Use the same Win32 loader as the systray package.
		handle, _, err := user32.NewProc("LoadImageW").Call(0, uintptr(unsafe.Pointer(pathPtr)), 1, size, size, 0x10)
		if handle == 0 {
			t.Fatalf("LoadImageW(%d): %v", size, err)
		}
		user32.NewProc("DestroyIcon").Call(handle)
	}
}

func TestWindowsSwitchesImportedNode(t *testing.T) {
	controller := &GUIController{cfgPath: filepath.Join(t.TempDir(), "client.json"), cfg: &Config{Listen: "127.0.0.1:0", ServerIP: "127.0.0.1", MasterKey: strings.Repeat("01", 32)}, router: NewRouter(nil, nil)}
	if err := controller.StartTunnel(); err != nil {
		t.Fatal(err)
	}
	defer controller.StopTunnel()
	oldMux := controller.cm
	originalListener := controller.ln
	node := &ShareProfile{ServerIP: "127.0.0.2", MasterKey: strings.Repeat("02", 32), Name: "second node"}
	if err := controller.importProfile(node); err != nil {
		t.Fatal(err)
	}
	if controller.ln != originalListener {
		t.Fatal("switch released the local proxy listener")
	}
	if controller.cm == oldMux || controller.cm.serverIP.String() != node.ServerIP {
		t.Fatal("old endpoint remained active")
	}
	if err := controller.importProfile(node); err != nil {
		t.Fatal(err)
	}
	if len(controller.cfg.Profiles) != 1 {
		t.Fatal("duplicate import created another profile")
	}
}

func TestWindowsImportPreservesOtherTransports(t *testing.T) {
	dir := t.TempDir()
	r := RealityConfig{Name: "example.com", Target: "example.com:443", Listen: ":443"}
	reality, err := r.node(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	p := ShareProfile{ServerIP: "192.0.2.1", MasterKey: strings.Repeat("ab", 32), TLSNode: reality}
	legacy := p
	legacy.RealityNode = RealityNode{}
	g := &GUIController{cfgPath: filepath.Join(dir, "client.json"), cfg: &Config{}}
	for _, profile := range []*ShareProfile{&legacy, &p, &p} {
		if err := g.importProfile(profile); err != nil {
			t.Fatal(err)
		}
	}
	if len(g.cfg.Profiles) != 2 || g.cfg.Profiles[0].TLSNode != legacy.TLSNode || g.cfg.Profiles[1].TLSNode != p.TLSNode || g.cfg.ActiveIndex != 1 {
		t.Fatal("transport overwritten or duplicate created")
	}
}

// The release workflow supplies the just-built GUI executable.
func TestWindowsGUIProcess(t *testing.T) {
	exe := os.Getenv("CHRONOMORPH_TEST_EXE")
	if exe == "" {
		t.Skip("release executable not supplied")
	}
	cmd := exec.Command(exe)
	cmd.Dir = t.TempDir()
	// A cached subscription with an offline origin reproduces a v1.4.3
	// configuration. Saving a chain or correcting a domain must work offline.
	src := "https://127.0.0.1:1/unreachable.txt"
	reserve, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxyAddr := reserve.Addr().String()
	reserve.Close()
	if err := SaveConfigAtomic(filepath.Join(cmd.Dir, "client.json"), &Config{Listen: proxyAddr, AdSubscriptions: []string{src}}); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(cmd.Dir, "adblock-cache")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, fmt.Sprintf("%x.txt", sha256.Sum256([]byte(src)))), []byte("ads.example\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var childErr bytes.Buffer
	cmd.Stderr = &childErr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	deadline := time.Now().Add(25 * time.Second)
	var native uintptr
	for time.Now().Before(deadline) {
		native = existingDesktop()
		if native != 0 && uc("IsWindowVisible", native) != 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if native == 0 || uc("IsWindowVisible", native) == 0 {
		t.Fatal("native desktop window did not appear")
	}
	var owner uint32
	uc("GetWindowThreadProcessId", native, uintptr(unsafe.Pointer(&owner)))
	if owner != uint32(cmd.Process.Pid) {
		t.Fatal("unexpected native window owner")
	}
	if uc("SendMessageW", native, 0x7f, 1, 0) == 0 && uc("GetClassLongPtrW", native, uintptr(^uint(13))) == 0 {
		t.Fatal("native desktop has no window icon")
	}
	click := func(id int) {
		h := uc("GetDlgItem", native, uintptr(id))
		if h == 0 {
			t.Fatalf("missing control %d", id)
		}
		uc("SendMessageW", h, 0x00f5, 0, 0)
	}
	click(importAction)
	field := uc("GetDlgItem", native, linkEdit)
	if field == 0 || uc("IsWindowVisible", field) == 0 {
		t.Fatal("native import form did not open")
	}
	cert, err := serverIdentity(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	master := strings.Repeat("21", 32)
	users, err := NewMultiUserEngine([]UserEntry{{Name: "native-test", KeyHex: master}})
	if err != nil {
		t.Fatal(err)
	}
	go serveTLS(listener, cert, users)
	profile := ShareProfile{ServerIP: "127.0.0.1", MasterKey: master, Name: "本机测试节点", TLSNode: TLSNode{UDPPort: listener.Addr().(*net.TCPAddr).Port, TLSPort: listener.Addr().(*net.TCPAddr).Port, TLSPin: fmt.Sprintf("%x", sha256.Sum256(cert.Certificate[0]))}}
	link, err := profile.encode()
	if err != nil {
		t.Fatal(err)
	}
	linkPtr := utf(link)
	uc("SendMessageW", field, 0x000c, 0, uintptr(unsafe.Pointer(linkPtr)))
	runtime.KeepAlive(linkPtr)
	length := int(uc("SendMessageW", field, 0x000e, 0, 0))
	if length != len(link) {
		t.Fatalf("native import field length %d, want %d", length, len(link))
	}
	click(saveImport)
	wait := func(label string, valid func() bool) {
		t.Helper()
		limit := time.Now().Add(8 * time.Second)
		for !valid() {
			if time.Now().After(limit) {
				file, readErr := os.ReadFile(filepath.Join(cmd.Dir, "client.json"))
				var c Config
				_ = json.Unmarshal(file, &c)
				h := uc("GetDlgItem", native, toggleAction)
				var b [128]uint16
				uc("SendMessageW", h, 0x000d, uintptr(len(b)), uintptr(unsafe.Pointer(&b[0])))
				var status [512]uint16
				notice := uc("GetDlgItem", native, noticeControl)
				uc("SendMessageW", notice, 0x000d, uintptr(len(status)), uintptr(unsafe.Pointer(&status[0])))
				t.Logf("Native UI feedback: %s", windows.UTF16ToString(status[:]))
				t.Fatalf("%s; saved=%v endpoint=%s tls=%d home-visible=%d toggle=%q import-visible=%d", label, readErr == nil, c.ServerIP, c.TLSPort, uc("IsWindowVisible", h), windows.UTF16ToString(b[:]), uc("IsWindowVisible", field))
			}
			time.Sleep(70 * time.Millisecond)
		}
	}
	toggle := uc("GetDlgItem", native, toggleAction)
	controlText := func(h uintptr) string {
		var b [128]uint16
		uc("SendMessageW", h, 0x000d, uintptr(len(b)), uintptr(unsafe.Pointer(&b[0])))
		return windows.UTF16ToString(b[:])
	}
	wait("import did not select the dual node", func() bool { return uc("IsWindowVisible", toggle) != 0 && controlText(toggle) == "开始加速" })
	click(navPrefs)
	click(tlsOnlyAction)
	policy := uc("GetDlgItem", native, tlsOnlyAction)
	wait("TLS-only preference was not applied", func() bool { return controlText(policy) == "已开启" })
	click(navHome)
	click(toggleAction)
	wait("native start did not enable the local listener", func() bool { return controlText(toggle) == "停止加速" })
	click(pingAction)
	wait("native ping did not reach the authenticated server", func() bool { return users.users[0].traffic.totals(trafficMonth(time.Now()))[1] > 0 })
	if path := os.Getenv("CHRONOMORPH_PREVIEW"); path != "" {
		time.Sleep(250 * time.Millisecond)
		if err := captureDesktopWindow(native, path); err != nil {
			t.Fatal(err)
		}
	}
	// A separate application holds one real TCP connection across GUI refreshes.
	echo, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		if c, e := echo.Accept(); e == nil {
			defer c.Close()
			_, _ = io.Copy(c, c)
		}
	}()
	c, err := net.DialTimeout("tcp4", proxyAddr, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	port := echo.Addr().(*net.TCPAddr).Port
	_, err = c.Write(append([]byte{4, 1, byte(port >> 8), byte(port), 127, 0, 0, 1, 0}, []byte("hello")...))
	if err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 13)
	if _, err = io.ReadFull(c, reply); err != nil || reply[1] != 90 || string(reply[8:]) != "hello" {
		t.Fatalf("SOCKS4 echo: %x %v", reply, err)
	}
	click(navLive)
	flows := uc("GetDlgItem", native, flowList)
	wait("live TCP row did not appear", func() bool { return uc("SendMessageW", flows, 0x18b, 0, 0) == 1 })
	setQuery := func(q string) {
		p := utf(q)
		uc("SendMessageW", uc("GetDlgItem", native, flowQuery), 0x000c, 0, uintptr(unsafe.Pointer(p)))
		runtime.KeepAlive(p)
	}
	setQuery("never-matches.example")
	wait("filter left wrong row", func() bool { return uc("SendMessageW", flows, 0x18b, 0, 0) == 0 })
	setQuery(fmt.Sprintf("(%d)", os.Getpid()))
	wait("could not filter by calling process", func() bool { return uc("SendMessageW", flows, 0x18b, 0, 0) == 1 })
	uc("SendMessageW", flows, 0x186, 0, 0)
	detail := uc("GetDlgItem", native, flowDetail)
	wait("native byte counts or target incorrect", func() bool {
		s := controlText(detail)
		return strings.Count(s, "5 B") == 2 && strings.Contains(s, echo.Addr().String())
	})
	setQuery("")
	if path := os.Getenv("CHRONOMORPH_LIVE_PREVIEW"); path != "" {
		if err := captureDesktopWindow(native, path); err != nil {
			t.Fatal(err)
		}
	}
	click(dropFlow)
	if _, err := c.Read(reply[:1]); err == nil {
		t.Fatal("disconnected socket still usable")
	}
	wait("closed connection remained visible", func() bool { return uc("SendMessageW", flows, 0x18b, 0, 0) == 0 })
	t.Log("Live TCP process filtering, destination, counters, specific disconnect and row removal verified")
	click(navNodes)
	click(openBench)
	benchField := uc("GetDlgItem", native, benchEdit)
	wait("batch latency results missing", func() bool {
		s := controlText(benchField)
		return strings.Contains(s, "本机测试节点") && strings.Contains(s, " ms")
	})
	click(navHome)
	click(toggleAction)
	wait("native stop did not release the listener", func() bool { return controlText(toggle) == "开始加速" })
	t.Log("Native dual node import, start, authenticated ping and stop verified")
	click(navNodes)
	del := uc("GetDlgItem", native, deleteNode)
	if del == 0 || uc("IsWindowVisible", del) == 0 {
		t.Fatal("native delete action missing")
	}
	// Post the click because a modal confirmation runs on the GUI thread.
	confirmDeletion := func(choice uintptr) {
		uc("PostMessageW", del, 0x00f5, 0, 0)
		var dialog, button uintptr
		wait("delete confirmation button did not appear", func() bool {
			dialog = uc("FindWindowW", uintptr(unsafe.Pointer(utf("#32770"))), uintptr(unsafe.Pointer(utf("删除选中节点"))))
			if dialog == 0 || uc("GetWindow", dialog, 4) != native {
				return false
			}
			// The top-level window can exist before MessageBox creates its buttons.
			button = uc("GetDlgItem", dialog, choice)
			return button != 0 && uc("IsWindowVisible", button) != 0 && uc("IsWindowEnabled", button) != 0
		})
		uc("SendMessageW", button, 0x00f5, 0, 0)
		wait("confirmation stayed open", func() bool { return uc("IsWindow", dialog) == 0 })
	}
	confirmDeletion(7) // IDNO
	list := uc("GetDlgItem", native, nodesList)
	if uc("SendMessageW", list, 0x18b, 0, 0) != 1 {
		t.Fatal("canceled deletion removed a node")
	}
	confirmDeletion(6) // IDYES
	wait("last node still displayed", func() bool { return uc("SendMessageW", list, 0x18b, 0, 0) == 0 })
	contents, err := os.ReadFile(filepath.Join(cmd.Dir, "client.json"))
	var after Config
	if err != nil || json.Unmarshal(contents, &after) != nil || len(after.Profiles) != 0 || after.ServerIP != "" || after.MasterKey != "" {
		t.Fatal("deleted node still on disk", err)
	}

	// Take the full Debian output row through the actual edit control, system
	// clipboard and native paste button; verify all saved transport fields.
	rc := RealityConfig{Name: "example.com", Target: "example.com:443", Listen: ":443"}
	realNode, err := rc.node(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	realProfile := ShareProfile{TLSNode: realNode, ServerIP: "2001:db8::1", MasterKey: master, Name: "测试节点-REALITY-v6"}
	realLink, err := realProfile.encode()
	if err != nil {
		t.Fatal(err)
	}
	click(importAction)
	row := "[IPv6 REALITY] " + realLink[:76] + "\r\n  " + realLink[76:]
	putText := func(s string) {
		buffer := utf(s)
		uc("SendMessageW", field, 0x000c, 0, uintptr(unsafe.Pointer(buffer)))
		runtime.KeepAlive(buffer)
	}
	putText(row)
	uc("SendMessageW", field, 0x00b1, 0, ^uintptr(0))
	uc("SendMessageW", field, 0x0301, 0, 0) // WM_COPY in the GUI process
	putText("previous selection")
	click(pasteImport)
	click(saveImport)
	wait("native paste did not save the REALITY node", func() bool {
		data, err := os.ReadFile(filepath.Join(cmd.Dir, "client.json"))
		var c Config
		if err != nil || json.Unmarshal(data, &c) != nil || len(c.Profiles) != 1 || c.ActiveIndex != 0 {
			return false
		}
		saved := c.Profiles[0]
		return saved.TLSNode == realNode && c.TLSNode == realNode && saved.ServerIP == realProfile.ServerIP && saved.MasterKey == master && saved.Name == realProfile.Name && uc("IsWindowVisible", toggle) != 0
	})
	t.Log("Native REALITY clipboard import saved all fields from a wrapped Debian export row")

	// With two saved CM nodes the native selector must persist A as entry
	// while the second import remains the active exit.
	click(importAction)
	putText(link)
	click(saveImport)
	wait("second node was not selected as exit", func() bool {
		data, err := os.ReadFile(filepath.Join(cmd.Dir, "client.json"))
		var cfg Config
		return err == nil && json.Unmarshal(data, &cfg) == nil && len(cfg.Profiles) == 2 && cfg.ServerIP == profile.ServerIP
	})
	click(navChain)
	entryList := uc("GetDlgItem", native, chainList)
	if uc("SendMessageW", entryList, 0x18b, 0, 0) != 2 {
		t.Fatal("expected off and one CM entry")
	}
	uc("SendMessageW", entryList, 0x186, 1, 0)
	click(saveChainAction)
	wait("CM chain settings not persisted", func() bool {
		data, err := os.ReadFile(filepath.Join(cmd.Dir, "client.json"))
		var cfg Config
		if err != nil || json.Unmarshal(data, &cfg) != nil {
			return false
		}
		entry, ok := cfg.entryProfile()
		return ok && entry.ServerIP == realProfile.ServerIP && cfg.ServerIP == profile.ServerIP
	})
	notice := uc("GetDlgItem", native, noticeControl)
	wait("chain save did not finish offline", func() bool { return controlText(notice) == "链路已保存" })
	click(navAds)
	subs := uc("GetDlgItem", native, subscriptionEdit)
	if controlText(subs) != src {
		t.Fatal("chain save changed subscription")
	}
	click(addAdSource)
	if uc("SendMessageW", subs, 0x000e, 0, 0) <= uintptr(len(src)) {
		t.Fatal("preset not inserted")
	}
	setField := func(id int, s string) {
		p := utf(s)
		uc("SendMessageW", uc("GetDlgItem", native, uintptr(id)), 0x000c, 0, uintptr(unsafe.Pointer(p)))
		runtime.KeepAlive(p)
	}
	setField(subscriptionEdit, src)
	click(saveSubsAction)
	wait("offline refresh did not report error", func() bool { return strings.Contains(controlText(notice), "下载失败") })
	click(openDomainTools)
	setField(domainEdit, "https://sub.ads.example/page")
	click(checkDomainAction)
	if got := controlText(notice); !strings.Contains(got, "订阅 1") || !strings.Contains(got, "ads.example") {
		t.Fatal("missing matching rule", got)
	}
	click(addAllowedAction)
	wait("allow list save did not finish", func() bool { return controlText(notice) == "放行名单已保存" })
	click(checkDomainAction)
	if got := controlText(notice); !strings.Contains(got, "我的放行名单") {
		t.Fatal("saved exception did not take effect", got)
	}
	setField(allowedEdit, "")
	click(saveAllowedAction)
	wait("allow list removal did not complete", func() bool { return controlText(notice) == "放行名单已保存" })
	click(navAds)
	click(pauseAdsAction)
	pauseButton := uc("GetDlgItem", native, pauseAdsAction)
	wait("filter not paused", func() bool { return controlText(pauseButton) == "恢复过滤" })
	click(openDomainTools)
	click(checkDomainAction)
	if got := controlText(notice); !strings.Contains(got, "订阅过滤已暂停") {
		t.Fatal("paused filter decision incorrect", got)
	}
	click(navAds)
	click(pauseAdsAction)
	wait("filter did not resume", func() bool { return controlText(pauseButton) == "暂停过滤" })
	data, err := os.ReadFile(filepath.Join(cmd.Dir, "client.json"))
	var savedConfig Config
	if err != nil || json.Unmarshal(data, &savedConfig) != nil || savedConfig.AdPaused || len(savedConfig.AdSubscriptions) != 1 || savedConfig.AdSubscriptions[0] != src || savedConfig.ChainEntry == "" || len(savedConfig.AdAllowDomains) != 0 {
		t.Fatal("independent settings not persisted")
	}
	t.Log("Native chain saved offline; failed refresh retained rules; domain lookup, allow, remove, pause and resume verified")
	click(navRoutes)
	setField(routeManual, "DOMAIN-SUFFIX,example.net,DIRECT")
	click(routeSave)
	wait("native routing edit did not apply", func() bool { return strings.Contains(controlText(notice), "已应用 1 条") })
	setField(routeTarget, "www.example.net")
	click(routeTest)
	if got := controlText(notice); !strings.Contains(got, "DIRECT") {
		t.Fatal("route lookup", got)
	}
	click(navResources)
	for i, resource := range resourceCards {
		click(editSite + i)
		// Read foreign-process EDIT controls via WM_GETTEXT.
		if got := controlText(uc("GetDlgItem", native, resourceURL)); got != geoRelease+resource.name {
			t.Fatal(resource.name, got)
		}
		click(cancelResource)
	}
	if path := os.Getenv("SLACKER_RESOURCES_PREVIEW"); path != "" {
		if err := captureDesktopWindow(native, path); err != nil {
			t.Fatal(err)
		}
	}
	click(editSite)
	setField(resourceURL, "https://127.0.0.1:1/geo")
	setField(resourceOverrides, "cn,!sample.example")
	click(saveResource)
	wait("resource edits not saved", func() bool {
		b, e := os.ReadFile(filepath.Join(cmd.Dir, "client.json"))
		var cfg Config
		return e == nil && json.Unmarshal(b, &cfg) == nil && cfg.Routing.Resources[geoSiteFile].Overrides == "cn,!sample.example"
	})
	click(cancelResource)
	click(syncSite)
	wait("failed sync did not show feedback", func() bool { return strings.Contains(controlText(notice), "下载失败") })
	click(editSite)
	if got := controlText(uc("GetDlgItem", native, resourceOverrides)); got != "cn,!sample.example" {
		t.Fatal("lost edits", got)
	}
	click(defaultResourceURL)
	click(saveResource)
	wait("default resource URL not restored", func() bool {
		b, e := os.ReadFile(filepath.Join(cmd.Dir, "client.json"))
		var cfg Config
		return e == nil && json.Unmarshal(b, &cfg) == nil && cfg.Routing.Resources[geoSiteFile].URL == ""
	})
	click(cancelResource)
	click(navModes)
	click(modeNew)
	setField(modeRemark, "游戏")
	setField(modeFile, "games")
	setField(modeHandle, "game.exe")
	setField(modeBypass, "updater.exe")
	click(modeOptions)
	click(modeChildren)
	click(modeOptsBack)
	click(modeVerify)
	if got := controlText(notice); got != "规则验证通过" {
		t.Fatal("mode validation", got)
	}
	click(modeSave)
	click(modeUse)
	wait("saved mode not activated", func() bool {
		b, _ := os.ReadFile(filepath.Join(cmd.Dir, "client.json"))
		var c Config
		_ = json.Unmarshal(b, &c)
		return c.ActiveMode == "games.json"
	})
	click(modeEdit)
	if controlText(uc("GetDlgItem", native, modeBypass)) != "updater.exe" {
		t.Fatal("rules did not survive reopen")
	}
	click(modeBack)
	click(modeNewRoute)
	setField(modeRemark, "IP routes")
	setField(modeHandle, "192.0.2.0/24")
	click(modeSave)
	m, err := readMode(filepath.Join(cmd.Dir, "client.json"), "IP routes")
	if err != nil || m.Kind != routeMode {
		t.Fatal(m, err)
	}
	t.Log("Slacker navigation, manual routing preview, named process and IP modes verified")

	click(navPrefs)
	click(openNetwork)
	setField(netHeartbeat, "12")
	click(saveNetwork)
	click(tabProcess)
	setField(procNames, "game.exe")
	click(procChildren)
	click(procEcho)
	setField(procDelay, "18")
	click(saveNetwork)
	click(tabAdapter)
	setField(netAddress, "10.0.236.10")
	setField(netGateway, "10.0.236.1")
	setField(netBypass, "192.0.2.0/24")
	click(saveNetwork)
	click(tabDNS)
	setField(dnsChina, "tcp://223.5.5.5:53")
	setField(dnsDomains, "example.com")
	click(saveNetwork)
	wait("network options did not persist", func() bool {
		data, _ := os.ReadFile(filepath.Join(cmd.Dir, "client.json"))
		var cfg Config
		if json.Unmarshal(data, &cfg) != nil || cfg.Network == nil {
			return false
		}
		o := cfg.Network
		return o.Heartbeat == 12 && o.Children && o.Echo && o.EchoDelay == 18 && len(o.Processes) == 1 && o.Gateway == "10.0.236.1" && len(o.Bypass) == 1 && o.ChinaDNS == "tcp://223.5.5.5:53" && len(o.ChinaDomains) == 1
	})
	click(tabAdapter)
	setField(netMask, "255.0.255.0")
	click(saveNetwork)
	if !strings.Contains(controlText(notice), "掩码") {
		t.Fatal("invalid mask accepted", controlText(notice))
	}
	click(navHome)

	uc("SendMessageW", native, 0x10, 0, 0)
	if uc("IsWindowVisible", native) != 0 {
		t.Fatal("close did not hide window to tray")
	}
	second := exec.Command(exe)
	second.Dir = cmd.Dir
	if err := second.Run(); err != nil {
		t.Fatal(err)
	}
	for time.Now().Before(deadline) && uc("IsWindowVisible", native) == 0 {
		time.Sleep(50 * time.Millisecond)
	}
	if uc("IsWindowVisible", native) == 0 {
		t.Fatal("second launch did not restore the original window")
	}
	t.Log("Native window, application icon, import form, close-to-tray and second-launch restore verified")
	user32 := windows.NewLazySystemDLL("user32.dll")
	if shell, _, _ := user32.NewProc("GetShellWindow").Call(); shell == 0 {
		t.Log("No interactive Explorer shell in runner session; tray rectangle unavailable")
		return
	}
	className, _ := windows.UTF16PtrFromString("SystrayClass")
	// The tray backend registers notification ID 100 on its hidden top-level window.
	type iconID struct {
		Size   uint32
		Window uintptr
		ID     uint32
		GUID   windows.GUID
	}
	var lastHRESULT uintptr
	for time.Now().Before(deadline) {
		var after uintptr
		for {
			hwnd, _, _ := user32.NewProc("FindWindowExW").Call(0, after, uintptr(unsafe.Pointer(className)), 0)
			if hwnd == 0 {
				break
			}
			after = hwnd
			var pid uint32
			user32.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&pid)))
			if pid != uint32(cmd.Process.Pid) {
				continue
			}
			id := iconID{Window: hwnd, ID: 100}
			id.Size = uint32(unsafe.Sizeof(id))
			var rect [4]int32
			lastHRESULT, _, _ = windows.NewLazySystemDLL("shell32.dll").NewProc("Shell_NotifyIconGetRect").Call(uintptr(unsafe.Pointer(&id)), uintptr(unsafe.Pointer(&rect)))
			if int32(lastHRESULT) >= 0 && rect[2] > rect[0] && rect[3] > rect[1] {
				t.Logf("Explorer returned a tray rectangle %v", rect)
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal(fmt.Sprintf("Explorer did not report a tray icon for the running EXE, HRESULT 0x%x", uint32(lastHRESULT)))
}

func TestFailedDashboardStartPreservesConfig(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	path := filepath.Join(t.TempDir(), "client.json")
	old := &Config{Listen: "127.0.0.1:0", ServerIP: "127.0.0.1", MasterKey: strings.Repeat("ab", 32)}
	if err := SaveConfigAtomic(path, old); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctrl := &GUIController{cfgPath: path, cfg: old, router: NewRouter(nil, nil)}
	candidate := *old
	candidate.Listen = ln.Addr().String()
	if err := ctrl.startTunnelLocked(&candidate, true); err == nil {
		t.Fatal("started on an occupied port")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) || ctrl.cfg != old || ctrl.running {
		t.Fatalf("failed start changed config, read error: %v", err)
	}
}
