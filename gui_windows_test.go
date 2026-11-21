package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
	node := &ShareProfile{ServerIP: "127.0.0.2", MasterKey: strings.Repeat("02", 32), Name: "second node"}
	if err := controller.importProfile(node); err != nil {
		t.Fatal(err)
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
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(25 * time.Second)
	for {
		res, err := client.Get("http://127.0.0.1:18080/")
		if err == nil {
			page, err := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if err == nil && res.StatusCode == 200 && bytes.Contains(page, []byte("<title>ChronoMorph</title>")) {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the Windows GUI did not open its local panel")
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Log("Windows GUI executable served its dashboard")
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
