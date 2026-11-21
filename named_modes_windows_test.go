package main

import (
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestWindowsModeSocketOwner(t *testing.T) {
	if target := os.Getenv("SLACKER_TEST_MODE_PEER"); target != "" {
		c, err := net.DialTimeout("tcp4", target, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		c.SetReadDeadline(time.Now().Add(15 * time.Second))
		io.Copy(io.Discard, c)
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	cmd := exec.Command(exe, "-test.run=^TestWindowsModeSocketOwner$")
	cmd.Env = append(os.Environ(), "SLACKER_TEST_MODE_PEER="+l.Addr().String())
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	l.SetDeadline(time.Now().Add(10 * time.Second))
	peer, err := l.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	m := newTrafficMode(processMode, (&Config{}).networkOptions())
	m.Remark = "named"
	m.Handle = []string{exe}
	m.Loopback = true
	for _, except := range []bool{false, true} {
		if except {
			m.Bypass = []string{filepath.Base(exe)}
		}
		rules, e := compileMode(m)
		if e != nil {
			t.Fatal(e)
		}
		selected, e := modeProxyOwner(peer, rules)
		if e != nil || selected == except {
			t.Fatalf("loopback owner selected=%v exclude=%v err=%v", selected, except, e)
		}
	}
}
