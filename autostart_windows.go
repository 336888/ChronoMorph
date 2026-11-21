//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

const runRegPath = `Software\Microsoft\Windows\CurrentVersion\Run`

func IsAutoStartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runRegPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue("ChronoMorph")
	return err == nil
}

func SetAutoStart(enable bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runRegPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if enable {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return k.SetStringValue("ChronoMorph", `"`+exe+`"`)
	}
	err = k.DeleteValue("ChronoMorph")
	if err == registry.ErrNotExist {
		return nil
	}
	return err
}
