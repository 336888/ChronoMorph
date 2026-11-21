//go:build !windows

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"slacker/internal/bridgeconfig"
	"slacker/internal/realitytransport"
)

func bridgePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	p := filepath.Join(filepath.Dir(exe), "cm-reality")
	info, e := os.Stat(p)
	if errors.Is(e, os.ErrNotExist) {
		return "", fmt.Errorf("缺少 REALITY 组件 %s。请重新安装完整的 Debian 软件包", p)
	}
	if e != nil {
		return "", fmt.Errorf("无法访问 REALITY 组件: %w", e)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("REALITY 组件路径不是普通文件: %s", p)
	}
	return p, nil
}

type realityProcess struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	output io.ReadCloser
	once   sync.Once
}

func startRealityProcess(cfg bridgeconfig.Config) (*realityProcess, error) {
	path, err := bridgePath()
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(path)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		in.Close()
		out.Close()
		return nil, err
	}
	p := &realityProcess{cmd: cmd, input: in, output: out}
	if _, err = in.Write(append(b, '\n')); err != nil {
		p.stop()
		out.Close()
		_ = cmd.Wait()
		return nil, err
	}
	return p, nil
}
func (p *realityProcess) stop() { p.once.Do(func() { p.input.Close(); _ = p.cmd.Process.Kill() }) }
func (p *realityProcess) wait() { _ = p.cmd.Wait() }

type realityPipe struct {
	net.Conn
	child *realityProcess
}

func (c *realityPipe) Close() error { err := c.Conn.Close(); c.child.stop(); return err }

func checkRealitySupport() error {
	_, err := bridgePath()
	return err
}

func (cm *ClientMux) dialRealityTransport(ctx context.Context, cfg bridgeconfig.Config) (net.Conn, error) {
	if cm.entry != nil {
		raw, err := cm.dialNodeTCP(ctx, cfg.Address)
		if err != nil {
			return nil, err
		}
		return realitytransport.Client(ctx, raw, cfg)
	}
	p, err := startRealityProcess(cfg)
	if err != nil {
		return nil, err
	}
	local, pump := net.Pipe()
	conn := &realityPipe{Conn: local, child: p}
	go func() { _, _ = io.Copy(p.input, pump); p.stop() }()
	go func() { _, _ = io.Copy(pump, p.output); pump.Close(); p.stop(); p.wait() }()
	return conn, nil
}

func runRealityListener(ctx context.Context, cfg bridgeconfig.Config) error {
	p, e := startRealityProcess(cfg)
	if e != nil {
		return e
	}
	stop := context.AfterFunc(ctx, p.stop)
	defer stop()
	rd := bufio.NewReader(p.output)
	b, e := rd.ReadString('\n')
	if e != nil || b != "READY\n" {
		p.stop()
		p.wait()
		return errors.New("REALITY 入口未启动，请检查监听端口和组件")
	}
	_, _ = io.Copy(io.Discard, rd)
	p.stop()
	p.wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.New("REALITY 入口已停止")
}
