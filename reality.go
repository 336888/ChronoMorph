package main

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"slacker/internal/bridgeconfig"
)

// RealityNode is public transport metadata. Each user's existing master key
// and the pinned inner server identity still authenticate the private session.
type RealityNode struct {
	RealityName string `json:"reality_name,omitempty"`
	RealityKey  string `json:"reality_key,omitempty"`
	RealityID   string `json:"reality_id,omitempty"`
}
type RealityConfig struct {
	Name   string `json:"name"`
	Target string `json:"target"`
	Listen string `json:"listen"`
	Only   bool   `json:"only,omitempty"`
}

func (r RealityNode) identity() ([]byte, []byte, error) {
	key, err := base64.RawURLEncoding.Strict().DecodeString(r.RealityKey)
	sid, e := hex.DecodeString(r.RealityID)
	if !validWebsiteName(r.RealityName) || err != nil || len(key) != 32 || e != nil || len(sid) != 8 || bytes.Equal(key, make([]byte, 32)) {
		return nil, nil, errors.New("REALITY 节点域名、公钥或短 ID 无效")
	}
	return key, sid, nil
}
func endpoint(address string) (string, int, error) {
	h, p, e := net.SplitHostPort(address)
	n, err := strconv.Atoi(p)
	if e != nil || err != nil || n < 1 || n > 65535 {
		return "", 0, errors.New("请输入有效的主机和端口")
	}
	return h, n, nil
}
func (r *RealityConfig) validate() error {
	if r == nil || !validWebsiteName(r.Name) {
		return errors.New("REALITY 需要有效的 TLS 服务器名称")
	}
	h, _, err := endpoint(r.Listen)
	if err != nil || (h != "" && net.ParseIP(h) == nil) {
		return errors.New("REALITY 监听地址无效")
	}
	h, _, err = endpoint(r.Target)
	if err != nil || (!validWebsiteName(h) && net.ParseIP(h) == nil) {
		return errors.New("REALITY 目标地址无效")
	}
	return nil
}

// Store the two random values together, without exposing a half-written key or
// rotating it during concurrent upgrades. They are never logged or exported.
func realityKeys(dir string, create bool) (*ecdh.PrivateKey, []byte, error) {
	path := filepath.Join(dir, "reality.key")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && create {
		key, e := ecdh.X25519().GenerateKey(rand.Reader)
		if e != nil {
			return nil, nil, e
		}
		b = append(key.Bytes(), make([]byte, 8)...)
		if _, e = rand.Read(b[32:]); e != nil {
			return nil, nil, e
		}
		f, e := os.CreateTemp(dir, ".reality-key-*")
		if e != nil {
			return nil, nil, e
		}
		defer os.Remove(f.Name())
		if _, e = f.Write(b); e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e != nil {
			return nil, nil, e
		}
		if ce != nil {
			return nil, nil, ce
		}
		if e = os.Link(f.Name(), path); e != nil && !errors.Is(e, os.ErrExist) {
			return nil, nil, e
		}
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, nil, err
	}
	if len(b) != 40 {
		return nil, nil, errors.New("REALITY 私钥文件损坏")
	}
	k, err := ecdh.X25519().NewPrivateKey(b[:32])
	return k, b[32:], err
}

func (r *RealityConfig) node(dir string, create bool) (TLSNode, error) {
	if e := r.validate(); e != nil {
		return TLSNode{}, e
	}
	key, sid, e := realityKeys(dir, create)
	if e != nil {
		return TLSNode{}, e
	}
	pair, e := serverIdentity(dir, create)
	if e != nil {
		return TLSNode{}, e
	}
	_, port, _ := endpoint(r.Listen)
	return TLSNode{RealityNode: RealityNode{RealityName: r.Name, RealityKey: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), RealityID: hex.EncodeToString(sid)}, TLSPort: port, TLSPin: fmt.Sprintf("%x", sha256.Sum256(pair.Certificate[0]))}, nil
}

func (cm *ClientMux) dialReality(ctx context.Context, address string) (*tls.Conn, error) {
	key, sid, err := cm.tlsNode.identity()
	if err != nil {
		return nil, err
	}
	handshake, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	conn, err := cm.dialRealityTransport(handshake, bridgeconfig.Config{Mode: "client", Address: address, Name: cm.tlsNode.RealityName, Key: key, ShortID: sid})
	if err != nil {
		return nil, err
	}
	secured := tls.Client(conn, cm.tlsConfig)
	if err := secured.HandshakeContext(handshake); err != nil {
		conn.Close()
		return nil, fmt.Errorf("REALITY 认证失败或目标不可达: %w", err)
	}
	return secured, nil
}

func serveReality(ctx context.Context, r *RealityConfig, dir string, users *MultiUserEngine) error {
	if e := r.validate(); e != nil {
		return e
	}
	cert, e := serverIdentity(dir, true)
	if e != nil {
		return e
	}
	key, sid, e := realityKeys(dir, true)
	if e != nil {
		return e
	}
	ln, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		return e
	}
	defer ln.Close()
	go serveTLS(ln, cert, users)
	return runRealityListener(ctx, bridgeconfig.Config{Mode: "server", Address: r.Listen, Name: r.Name, Key: key.Bytes(), ShortID: sid, Target: r.Target, Backend: ln.Addr().String()})
}

func realityAction(path string, create, export, check bool) error {
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	var cfg Config
	if e = json.Unmarshal(b, &cfg); e != nil {
		return e
	}
	node, e := cfg.Reality.node(filepath.Dir(path), create)
	if e != nil {
		return e
	}
	if export {
		return json.NewEncoder(os.Stdout).Encode(node)
	}
	if e = checkRealitySupport(); e != nil {
		return e
	}
	if !check {
		return nil
	}
	host, _, _ := endpoint(cfg.Reality.Listen)
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	} else if host == "::" {
		host = "::1"
	}
	public, err := tls.DialWithDialer(&net.Dialer{Timeout: 8 * time.Second}, "tcp",
		net.JoinHostPort(host, strconv.Itoa(node.TLSPort)),
		&tls.Config{ServerName: node.RealityName, MinVersion: tls.VersionTLS13, NextProtos: []string{"h2", "http/1.1"}})
	if err != nil {
		return fmt.Errorf("普通 HTTPS 访问未能验证目标网站证书: %w", err)
	}
	public.Close()
	users := cfg.serverUsers()
	if len(users) == 0 {
		return errors.New("需要至少一个节点用户")
	}
	cm, e := newNodeMux(host, users[0].KeyHex, node)
	if e != nil {
		return e
	}
	defer cm.Close()
	_, e = MeasureTunnelLatency(cm)
	return e
}
