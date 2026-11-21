package main

import (
	"os/exec"
	"syscall"
)

func hideBridgeWindow(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }
