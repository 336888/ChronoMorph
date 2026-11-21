//go:build windows

package main

import "syscall"

func RegisterShutdownCleanup(onCleanup func()) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	setCtrlHandler := kernel32.NewProc("SetConsoleCtrlHandler")
	cb := syscall.NewCallback(func(ctrlType uint32) uintptr {
		if ctrlType == 2 || ctrlType == 5 || ctrlType == 6 {
			onCleanup()
		}
		return 0
	})
	_, _, _ = setCtrlHandler.Call(cb, 1)
}
