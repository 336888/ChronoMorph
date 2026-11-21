package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
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

// The release workflow supplies the just-built GUI executable.
func TestWindowsGUIProcess(t *testing.T) {
	exe := os.Getenv("CHRONOMORPH_TEST_EXE")
	if exe == "" {
		t.Skip("release executable not supplied")
	}
	cmd := exec.Command(exe)
	cmd.Dir = t.TempDir()
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
	wait("import did not select the dual node", func() bool { return uc("IsWindowVisible", toggle) != 0 && controlText(toggle) == "启动连接" })
	click(toggleAction)
	wait("native start did not enable the local listener", func() bool { return controlText(toggle) == "断开连接" })
	click(pingAction)
	wait("native ping did not reach the authenticated server", func() bool { return users.users[0].txByte.Load() > 0 })
	if path := os.Getenv("CHRONOMORPH_PREVIEW"); path != "" {
		time.Sleep(250 * time.Millisecond)
		if err := captureDesktopWindow(native, path); err != nil {
			t.Fatal(err)
		}
	}
	click(toggleAction)
	wait("native stop did not release the listener", func() bool { return controlText(toggle) == "启动连接" })
	t.Log("Native dual node import, start, authenticated ping and stop verified")
	click(navNodes)
	del := uc("GetDlgItem", native, deleteNode)
	if del == 0 || uc("IsWindowVisible", del) == 0 {
		t.Fatal("native delete action missing")
	}
	// Post the click because a modal confirmation runs on the GUI thread.
	confirmDeletion := func(choice uintptr) {
		uc("PostMessageW", del, 0x00f5, 0, 0)
		var dialog uintptr
		wait("delete confirmation did not appear", func() bool {
			dialog = uc("FindWindowW", uintptr(unsafe.Pointer(utf("#32770"))), uintptr(unsafe.Pointer(utf("删除选中节点"))))
			return dialog != 0 && uc("GetWindow", dialog, 4) == native
		})
		button := uc("GetDlgItem", dialog, choice)
		if button == 0 {
			t.Fatal("missing confirmation option")
		}
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
