package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// TLSNode is carried in TLS node links. Both fields are mandatory for this transport.
type TLSNode struct {
	TLSPort int    `json:"tls_port,omitempty"`
	TLSPin  string `json:"tls_pin,omitempty"`
}

func (n TLSNode) validate() error {
	if n == (TLSNode{}) {
		return nil
	}
	pin, err := hex.DecodeString(n.TLSPin)
	if n.TLSPort < 1 || n.TLSPort > 65535 || err != nil || len(pin) != sha256.Size {
		return errors.New("TLS 节点需要有效的 TCP 端口和 SHA-256 证书指纹，请重新导入链接")
	}
	return nil
}

func tlsClientKey(master []byte) ed25519.PrivateKey {
	mac := hmac.New(sha256.New, master)
	mac.Write([]byte("ChronoMorph TLS client Ed25519 identity v1"))
	return ed25519.NewKeyFromSeed(mac.Sum(nil))
}

func issueCertificate(key ed25519.PrivateKey, usage x509.ExtKeyUsage) ([]byte, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	cert := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "ChronoMorph"},
		NotBefore: now.Add(-24 * time.Hour), NotAfter: now.AddDate(5, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
		BasicConstraintsValid: true,
	}
	return x509.CreateCertificate(rand.Reader, cert, cert, key.Public(), key)
}

const tlsIdentityFile = "transport.pem"
const tlsMuxV2 = "chronomorph-smux/2"

// Publish the entire key pair atomically and never replace an existing server identity.
func serverIdentity(dir string, create bool) (tls.Certificate, error) {
	path := filepath.Join(dir, tlsIdentityFile)
	pair, err := tls.LoadX509KeyPair(path, path)
	if err == nil || !create || !os.IsNotExist(err) {
		return pair, err
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return pair, err
	}
	der, err := issueCertificate(key, x509.ExtKeyUsageServerAuth)
	if err != nil {
		return pair, err
	}
	pk, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return pair, err
	}
	tmp, err := os.CreateTemp(dir, ".transport-*")
	if err != nil {
		return pair, err
	}
	defer os.Remove(tmp.Name())
	var buf bytes.Buffer
	_ = pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	_ = pem.Encode(&buf, &pem.Block{Type: "PRIVATE KEY", Bytes: pk})
	if _, err = tmp.Write(buf.Bytes()); err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return pair, err
	}
	if closeErr != nil {
		return pair, closeErr
	}
	if err = os.Link(tmp.Name(), path); err != nil && !os.IsExist(err) {
		return pair, err
	}
	return tls.LoadX509KeyPair(path, path)
}

func clientTLSConfig(master []byte, node TLSNode) (*tls.Config, error) {
	if err := node.validate(); err != nil {
		return nil, err
	}
	pin, err := hex.DecodeString(node.TLSPin)
	if err != nil || len(pin) != sha256.Size {
		return nil, errors.New("TLS 证书指纹缺失")
	}
	key := tlsClientKey(master)
	der, err := issueCertificate(key, x509.ExtKeyUsageClientAuth)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{tlsMuxV2},
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		// The node link pins the complete server certificate. There is no DNS name or public CA.
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) != 1 {
				return errors.New("TLS 服务端证书链与节点不符")
			}
			cert := cs.PeerCertificates[0]
			sum := sha256.Sum256(cert.Raw)
			if !hmac.Equal(pin, sum[:]) {
				return errors.New("TLS 服务端证书指纹不匹配")
			}
			roots := x509.NewCertPool()
			roots.AddCert(cert)
			_, err := cert.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
			return err
		},
	}, nil
}

func (cm *ClientMux) dialTLS() (net.Conn, error) {
	addr := net.JoinHostPort(cm.serverIP.String(), strconv.Itoa(cm.tlsNode.TLSPort))
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", addr, cm.tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("TLS/TCP %s: %w", addr, err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var ack [1]byte
	_, err = io.ReadFull(conn, ack[:])
	if err != nil || ack[0] != 1 {
		_ = conn.Close()
		return nil, errors.New("TLS 服务端未确认客户端身份，请检查节点密钥和配额")
	}
	_ = conn.SetReadDeadline(time.Time{})
	return &tlsMeterConn{Conn: conn, muxV2: conn.ConnectionState().NegotiatedProtocol == tlsMuxV2}, nil
}

type tlsMeterConn struct {
	net.Conn
	muxV2 bool
	users *MultiUserEngine
	user  *UserEntry
}

func (c *tlsMeterConn) allowed() bool {
	if c.user == nil {
		return true
	}
	c.users.mu.RLock()
	defer c.users.mu.RUnlock()
	return !c.user.retired.Load() && !c.user.IsOverQuota(c.user.LimitGB)
}
func (c *tlsMeterConn) Read(p []byte) (int, error) {
	if !c.allowed() {
		return 0, net.ErrClosed
	}
	n, err := c.Conn.Read(p)
	if n > 0 {
		if !c.allowed() {
			return 0, net.ErrClosed
		}
		if c.user != nil {
			c.user.rxByte.Add(uint64(n))
		} else {
			LiveStats.AddRx(uint64(n))
		}
	}
	return n, err
}
func (c *tlsMeterConn) Write(p []byte) (int, error) {
	if !c.allowed() {
		return 0, net.ErrClosed
	}
	n, err := c.Conn.Write(p)
	if n > 0 {
		if c.user != nil {
			c.user.txByte.Add(uint64(n))
		} else {
			LiveStats.AddTx(uint64(n))
		}
	}
	return n, err
}

func serveTLS(ln net.Listener, cert tls.Certificate, users *MultiUserEngine) {
	active := make(chan struct{}, 128)
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		select {
		case active <- struct{}{}:
			go func() { defer func() { <-active }(); acceptTLS(c, cert, users) }()
		default:
			_ = c.Close()
		}
	}
}

func acceptTLS(raw net.Conn, cert tls.Certificate, users *MultiUserEngine) {
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(6 * time.Second))
	var user *UserEntry
	conn := tls.Server(raw, &tls.Config{
		NextProtos: []string{tlsMuxV2},
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert},
		ClientAuth: tls.RequireAnyClientCert, SessionTicketsDisabled: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) != 1 {
				return errors.New("invalid client identity")
			}
			key, ok := cs.PeerCertificates[0].PublicKey.(ed25519.PublicKey)
			if !ok {
				return errors.New("invalid client key type")
			}
			users.mu.RLock()
			defer users.mu.RUnlock()
			for _, u := range users.users {
				if bytes.Equal(key, u.tlsPublic) && !u.IsOverQuota(u.LimitGB) {
					user = u
					return nil
				}
			}
			return errors.New("unregistered TLS client or exhausted quota")
		},
	})
	if conn.Handshake() != nil || user == nil {
		return
	}
	if _, err := conn.Write([]byte{1}); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	handleServerSession(&tlsMeterConn{Conn: conn, muxV2: conn.ConnectionState().NegotiatedProtocol == tlsMuxV2, users: users, user: user})
}

func checkLocalTLS(cfgPath string) error {
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		return err
	}
	var cfg Config
	if err = json.Unmarshal(b, &cfg); err != nil {
		return err
	}
	host, portText, err := net.SplitHostPort(cfg.TLSListen)
	if err != nil {
		return err
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	} else if host == "::" {
		host = "::1"
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return err
	}
	cert, err := serverIdentity(filepath.Dir(cfgPath), false)
	if err != nil {
		return err
	}
	users := cfg.serverUsers()
	if len(users) == 0 {
		return errors.New("没有配置 TLS 用户")
	}
	cm, err := newNodeMux(host, users[0].KeyHex, TLSNode{TLSPort: port, TLSPin: fmt.Sprintf("%x", sha256.Sum256(cert.Certificate[0]))})
	if err != nil {
		return err
	}
	defer cm.Close()
	_, err = MeasureTunnelLatency(cm)
	return err
}
