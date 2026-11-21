//go:build !windows

package main

import "os/exec"

func hideBridgeWindow(*exec.Cmd) {}
