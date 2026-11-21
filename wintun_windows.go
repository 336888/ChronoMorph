package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/windows"
)

// Original signed binaries from https://www.wintun.net/builds/wintun-0.14.1.zip.
//
//go:embed assets/wintun-0.14.1.zip
var wintunArchive []byte

var errWintunContents = errors.New("Wintun 驱动文件校验失败")

func bundledWintun() ([]byte, error) {
	arch := runtime.GOARCH
	if arch == "386" {
		arch = "x86"
	}
	bundle, err := zip.NewReader(bytes.NewReader(wintunArchive), int64(len(wintunArchive)))
	if err != nil {
		return nil, err
	}
	dll, err := bundle.Open("wintun/bin/" + arch + "/wintun.dll")
	if err != nil {
		return nil, err
	}
	defer dll.Close()
	return io.ReadAll(dll)
}

// Keep the verified file locked against writes and replacement until Windows
// has mapped it. Dependencies of the signed DLL are loaded from System32.
func verifiedWintun(path string, size int, digest [sha256.Size]byte) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(h), path)
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(f, int64(size)+1))
	if err == nil && (n != int64(size) || !bytes.Equal(hash.Sum(nil), digest[:])) {
		err = errWintunContents
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func installWintun(dir string, payload []byte) (*os.File, error) {
	path := filepath.Join(dir, "wintun.dll")
	digest := sha256.Sum256(payload)
	f, err := verifiedWintun(path, len(payload), digest)
	if err == nil || (!errors.Is(err, os.ErrNotExist) && !errors.Is(err, errWintunContents)) {
		return f, err
	}
	if err = os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(dir, "wintun-*.tmp")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(payload)
	closeErr := tmp.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		// Another instance may already be using the correct DLL.
		if existing, checkErr := verifiedWintun(path, len(payload), digest); checkErr == nil {
			return existing, nil
		}
		return nil, err
	}
	return verifiedWintun(path, len(payload), digest)
}

func loadBundledWintun() (windows.Handle, error) {
	programFiles, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if err != nil {
		return 0, err
	}
	data, err := bundledWintun()
	if err != nil {
		return 0, err
	}
	file, err := installWintun(filepath.Join(programFiles, "ChronoMorph", "Wintun", "0.14.1", runtime.GOARCH), data)
	if err != nil {
		return 0, fmt.Errorf("准备内置 Wintun 驱动失败: %w", err)
	}
	defer file.Close()
	h, err := windows.LoadLibraryEx(file.Name(), 0, windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return 0, fmt.Errorf("加载内置 Wintun 驱动失败: %w", err)
	}
	// The Go Wintun binding resolves this basename on first use. Ensure it
	// refers to the verified module before handing control to tun2socks.
	var selected windows.Handle
	name, _ := windows.UTF16PtrFromString("wintun.dll")
	if err := windows.GetModuleHandleEx(windows.GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT, name, &selected); err != nil || selected != h {
		windows.FreeLibrary(h)
		return 0, errors.New("进程已加载其他版本的 Wintun，请彻底退出客户端后重新启动")
	}
	return h, nil
}
