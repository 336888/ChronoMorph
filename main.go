package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/xtaci/kcp-go/v5"
	"github.com/xtaci/smux"
)

const (
	CmdTCP          byte = 0x01
	CmdUDP          byte = 0x02
	CmdPing         byte = 0x03
	CmdDialTCP      byte = 0x04
	CmdCapabilities byte = 0x05
	capDialAck      byte = 1
	capDialProgress byte = 2
	CmdDialReady    byte = 0x06
)

type Config struct {
	ActiveMode      string          `json:"active_traffic_mode,omitempty"`
	Network         *NetworkOptions `json:"network,omitempty"`
	LockEntryType   bool            `json:"lock_entry_type,omitempty"`
	TUNBlockIPv6    bool            `json:"tun_block_ipv6,omitempty"`
	DNSUpstreams    []string        `json:"dns_upstreams,omitempty"`
	ChainEntry      string          `json:"chain_entry,omitempty"`
	AdSubscriptions []string        `json:"ad_subscriptions,omitempty"`
	AdAllowDomains  []string        `json:"ad_allow_domains,omitempty"`
	AdPaused        bool            `json:"ad_paused,omitempty"`
	TLSNode
	Reality       *RealityConfig `json:"reality,omitempty"`
	Website       *WebsiteConfig `json:"website,omitempty"`
	TLSOnly       bool           `json:"tls_only,omitempty"`
	TLSListen     string         `json:"tls_listen,omitempty"`
	Listen        string         `json:"listen"`
	ServerIP      string         `json:"server_ip,omitempty"`
	MasterKey     string         `json:"master_key,omitempty"`
	Users         []UserEntry    `json:"users,omitempty"`
	Profiles      []NodeProfile  `json:"profiles,omitempty"`
	ActiveIndex   int            `json:"active_index,omitempty"`
	Routing       RouteSettings  `json:"routing,omitempty"`
	DirectDomains []string       `json:"direct_domains,omitempty"`
	BlockDomains  []string       `json:"block_domains,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		if runtime.GOOS == "windows" {
			RunWindowsGUI("client.json")
			return
		}
		fmt.Println("用法: slacker <server | client | keygen | version> [-c config.json]")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "licenses":
		if err := printLicenses(os.Stdout); err != nil {
			log.Fatal(err)
		}
	case "version":
		fmt.Println(VersionString())
	case "restore-proxy":
		if runtime.GOOS != "windows" {
			os.Exit(2)
		}
		if err := restoreSystemProxy(); err != nil {
			log.Fatal(err)
		}
	case "reality":
		fs := flag.NewFlagSet("reality", flag.ExitOnError)
		path := fs.String("c", "/etc/chronomorph/server.json", "server configuration")
		create := fs.Bool("create", false, "create missing server identity")
		export := fs.Bool("export", false, "export public transport metadata")
		check := fs.Bool("check", false, "authenticate and ping the local REALITY listener")
		_ = fs.Parse(os.Args[2:])
		if err := realityAction(*path, *create, *export, *check); err != nil {
			log.Fatal(err)
		}
		if *check && !*export {
			fmt.Println("目标网站证书和 REALITY 节点认证通过")
		}
	case "website-check":
		fs := flag.NewFlagSet("website-check", flag.ExitOnError)
		cfgPath := fs.String("c", "/etc/chronomorph/server.json", "server configuration")
		configOnly := fs.Bool("config-only", false, "validate the local website configuration")
		_ = fs.Parse(os.Args[2:])
		if err := checkWebsite(*cfgPath, *configOnly); err != nil {
			log.Fatal(err)
		}
		if !*configOnly {
			fmt.Println("HTTPS 网站首页和认证隧道均已验证")
		}
	case "tls-check":
		fs := flag.NewFlagSet("tls-check", flag.ExitOnError)
		cfgPath := fs.String("c", "/etc/chronomorph/server.json", "server configuration")
		_ = fs.Parse(os.Args[2:])
		if err := checkLocalTLS(*cfgPath); err != nil {
			log.Fatal(err)
		}
		fmt.Println("TLS/TCP 双向认证和服务端应答正常")
	case "tls-identity":
		fs := flag.NewFlagSet("tls-identity", flag.ExitOnError)
		dir := fs.String("dir", "/etc/chronomorph", "server configuration directory")
		create := fs.Bool("create", false, "create the identity if missing")
		subject := fs.Bool("subject", false, "print the public certificate subject instead of the SHA-256 pin")
		_ = fs.Parse(os.Args[2:])
		cert, err := serverIdentity(*dir, *create)
		if err != nil {
			log.Fatal(err)
		}
		if *subject {
			leaf, err := x509.ParseCertificate(cert.Certificate[0])
			if err != nil {
				log.Fatal(err)
			}
			fmt.Println(leaf.Subject.String())
			return
		}
		fmt.Printf("%x\n", sha256.Sum256(cert.Certificate[0]))
	case "keygen":
		key := make([]byte, 32)
		_, _ = rand.Read(key)
		fmt.Printf("MasterKey: %s\n", hex.EncodeToString(key))
	case "server":
		fs := flag.NewFlagSet("server", flag.ExitOnError)
		cfgPath := fs.String("c", "/etc/chronomorph/server.json", "服务端配置文件路径")
		_ = fs.Parse(os.Args[2:])
		runServer(*cfgPath)
	case "client":
		fs := flag.NewFlagSet("client", flag.ExitOnError)
		cfgPath := fs.String("c", "client.json", "客户端配置文件路径")
		_ = fs.Parse(os.Args[2:])
		runHeadlessClient(*cfgPath)
	default:
		fmt.Printf("未知命令: %s\n", os.Args[1])
		os.Exit(1)
	}
}

var serverLimiter = &StreamLimiter{}

func runServer(cfgPath string) {
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		log.Fatalf("[Server] 读取配置失败: %v", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("[Server] 配置解析失败: %v", err)
	}

	users := cfg.serverUsers()
	multiEng, err := NewMultiUserEngine(users)
	if err != nil {
		log.Fatalf("[Server] 初始化用户引擎失败: %v", err)
	}

	stopCh := make(chan struct{})
	guard := NewAbuseGuard()

	WatchReloadSignal(cfgPath, multiEng)
	trafficSaved := StartTrafficPersistence(filepath.Join(filepath.Dir(cfgPath), "traffic.json"), multiEng, stopCh)
	StartLocalStatsAPI(multiEng, guard)

	var listeners int
	failed := make(chan error, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	webOnly := cfg.Website != nil && cfg.Website.Only
	realityOnly := cfg.Reality != nil && cfg.Reality.Only
	if webOnly && realityOnly {
		log.Fatal("只能选择一个独占入口")
	}
	legacy := !webOnly && !realityOnly
	if cfg.Reality != nil && !webOnly {
		listeners++
		go func() { failed <- serveReality(ctx, cfg.Reality, filepath.Dir(cfgPath), multiEng) }()
	}
	if legacy {
		rawPC, err := listenTunnelUDP("udp", cfg.Listen)
		if err == nil {
			ln, listenErr := kcp.ServeConn(nil, 10, 3, NewServerRTPPacketConn(rawPC, multiEng, guard))
			if listenErr == nil {
				defer rawPC.Close()
				defer ln.Close()
				listeners++
				go func() {
					for {
						conn, err := ln.AcceptKCP()
						if err != nil {
							failed <- err
							return
						}
						go handleServerSession(tunedKCP(conn))
					}
				}()
				log.Printf("[Server] Slacker %s UDP 监听 %s (用户数: %d)", AppVersion, rawPC.LocalAddr(), len(users))
			} else {
				_ = rawPC.Close()
				err = listenErr
			}
		}
		if err != nil {
			log.Printf("[UDP] 无法监听 %s: %v", cfg.Listen, err)
		}
	}
	if legacy && cfg.TLSListen != "" {
		cert, err := serverIdentity(filepath.Dir(cfgPath), false)
		if err == nil {
			var ln net.Listener
			ln, err = net.Listen("tcp", cfg.TLSListen)
			if err == nil {
				defer ln.Close()
				listeners++
				go func() { serveTLS(ln, cert, multiEng); failed <- fmt.Errorf("TLS/TCP 监听器关闭") }()
				log.Printf("[TLS] TLS 1.3/TCP 已启动，监听 %s", ln.Addr())
			}
		}
		if err != nil {
			log.Printf("[TLS] 无法启动 %s: %v", cfg.TLSListen, err)
		}
	}
	if cfg.Website != nil && !realityOnly {
		web, err := cfg.Website.server(multiEng)
		if err == nil {
			var ln net.Listener
			ln, err = net.Listen("tcp", web.Addr)
			if err == nil {
				listeners++
				defer web.Close()
				go func() { failed <- web.ServeTLS(ln, "", "") }()
				log.Printf("[HTTPS] %s 网站入口监听 %s", cfg.Website.Name, ln.Addr())
			}
		}
		if err != nil {
			log.Printf("[HTTPS] 网站入口无法启动: %v", err)
		}
	}
	if listeners == 0 {
		log.Fatal("[Server] 所有传输入口均无法启动")
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sig)
	for listeners > 0 {
		select {
		case <-sig:
			close(stopCh)
			<-trafficSaved
			return
		case err := <-failed:
			log.Printf("[Server] 监听器已停止: %v", err)
			listeners--
		}
	}
	close(stopCh)
	<-trafficSaved
	log.Fatal("[Server] 所有传输入口均已停止")
}

func handleServerSession(conn net.Conn) {
	defer conn.Close()
	sess, err := smux.Server(conn, smuxTransportConfig(conn))
	if err != nil {
		return
	}
	defer sess.Close()

	for {
		stream, err := sess.AcceptStream()
		if err != nil {
			return
		}
		go handleServerStream(stream)
	}
}

func handleServerStream(stream net.Conn) {
	if !serverLimiter.Acquire() {
		_ = stream.Close()
		return
	}
	defer serverLimiter.Release()
	defer stream.Close()

	cmd, target, err := readServerRequest(stream, 10*time.Second)
	if err != nil {
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			serverLimiter.headerTimeouts.Add(1)
		}
		return
	}
	switch cmd {
	case CmdCapabilities:
		stream.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, _ = stream.Write([]byte{CmdCapabilities, capDialAck | capDialProgress})
		return
	case cmdQualityDownload:
		serveQualityBlock(stream)
		return
	case CmdPing:
		stream.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, _ = stream.Write([]byte{CmdPing})
		return
	case CmdUDP:
		handleServerUDPStream(stream)
		return
	case CmdTCP, CmdDialTCP, CmdDialReady:
	default:
		return
	}
	ack := cmd != CmdTCP
	if ack {
		stream.SetWriteDeadline(time.Now().Add(15 * time.Second))
	}
	if cmd == CmdDialReady {
		if _, err := stream.Write([]byte{CmdDialReady}); err != nil {
			return
		}
	}
	targetConn, err := net.DialTimeout("tcp", target, 8*time.Second)
	if err != nil {
		if ack {
			_, _ = stream.Write([]byte{1})
		}
		return
	}
	defer targetConn.Close()
	if ack {
		if _, err := stream.Write([]byte{0}); err != nil {
			return
		}
		stream.SetWriteDeadline(time.Time{})
	}

	if relayBidirectional(stream, targetConn, 2*time.Minute) {
		serverLimiter.drainTimeouts.Add(1)
	}
}

// One deadline covers the entire header, including a partially sent target.
func readServerRequest(conn net.Conn, timeout time.Duration) (byte, string, error) {
	conn.SetReadDeadline(time.Now().Add(timeout))
	defer conn.SetReadDeadline(time.Time{})
	var cmd, size [1]byte
	if _, err := io.ReadFull(conn, cmd[:]); err != nil {
		return 0, "", err
	}
	switch cmd[0] {
	case CmdTCP, CmdDialTCP, CmdDialReady:
		if _, err := io.ReadFull(conn, size[:]); err != nil {
			return 0, "", err
		}
		addr := make([]byte, size[0])
		_, err := io.ReadFull(conn, addr)
		return cmd[0], string(addr), err
	default:
		return cmd[0], "", nil
	}
}

type ClientMux struct {
	entry           *ClientMux
	ownsEntry       bool
	lastFailure     string
	lastFailureAt   time.Time
	lastLogged      time.Time
	webKey          ed25519.PrivateKey
	tlsConfig       *tls.Config
	tlsNode         TLSNode
	closed          bool
	mu              sync.Mutex
	generation      uint64
	dialDone        chan struct{}
	cancelDial      context.CancelFunc
	stopClock       context.CancelFunc
	clockDone       chan struct{}
	serverIP        net.IP
	sess            *smux.Session
	udpPath         *RTPPacketConn
	tcpPath         *tlsMeterConn
	preferUDP       bool
	activeTransport atomic.Int32
	dialMode        byte
	retryAfter      time.Time
	lastDialErr     error
	packetEngine    *ChronoPacketEngine
	udpRelay        *ClientMux
}

func NewClientMux(serverIPStr, masterKeyHex string) (*ClientMux, error) {
	return newNodeMux(serverIPStr, masterKeyHex, TLSNode{})
}

func newNodeMux(serverIPStr, masterKeyHex string, node TLSNode) (*ClientMux, error) {
	ip := net.ParseIP(serverIPStr)
	if ip == nil {
		return nil, fmt.Errorf("无效的服务端 IP: %s", serverIPStr)
	}
	key, err := hex.DecodeString(masterKeyHex)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("MasterKey 必须是 64 位 Hex 字符串")
	}
	if err := node.validate(); err != nil {
		return nil, err
	}
	cm := &ClientMux{serverIP: ip, tlsNode: node}
	if node.WebName != "" {
		cm.webKey = tlsClientKey(key)
		cm.tlsConfig = &tls.Config{ServerName: node.WebName, MinVersion: tls.VersionTLS13, NextProtos: []string{"http/1.1"}}
	} else if node.TLSPort != 0 {
		cm.tlsConfig, err = clientTLSConfig(key, node)
	}
	if node.TLSPort == 0 || node.UDPPort != 0 {
		cm.packetEngine = NewChronoPacketEngine(key)
	}
	return cm, err
}

func (cm *ClientMux) OpenStream() (*smux.Stream, error) {
	stream, _, _, err := cm.openSessionStream()
	return stream, err
}

// Share one handshake per lane. Neither a handshake nor a blocked stream write
// may hold the lock used by Stop, profile changes, or the independent UDP lane.
func (cm *ClientMux) openSessionStream() (*smux.Stream, *smux.Session, byte, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	generation := cm.generation
	retried := false
	for !cm.closed && generation == cm.generation {
		session, mode := cm.sess, cm.dialMode
		if session == nil {
			if err := cm.ensureSessionLocked(); err != nil {
				return nil, nil, 0, err
			}
			continue
		}
		cm.mu.Unlock()
		stream, err := session.OpenStream()
		cm.mu.Lock()
		if cm.sess != session {
			// The displaced session already owns and closes its pending streams.
			continue
		}
		if err == nil {
			return stream, session, mode, nil
		}
		cm.closeLocked()
		if retried {
			cm.cacheDialErrorLocked(err)
			return nil, nil, 0, err
		}
		retried = true
	}
	return nil, nil, 0, net.ErrClosed
}

// Called with mu held and no installed session. Waiters recheck the current
// generation, session and cached error after the attempt finishes.
func (cm *ClientMux) ensureSessionLocked() error {
	if time.Now().Before(cm.retryAfter) {
		return cm.lastDialErr
	}
	if pending := cm.dialDone; pending != nil {
		cm.mu.Unlock()
		<-pending
		cm.mu.Lock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	cm.dialDone, cm.cancelDial = done, cancel
	cm.mu.Unlock()
	path, err := cm.connectTransport(ctx)
	cancel()
	cm.mu.Lock()
	if cm.dialDone != done {
		path.close()
		return net.ErrClosed
	}
	close(done)
	cm.dialDone, cm.cancelDial = nil, nil
	if err != nil {
		cm.cacheDialErrorLocked(err)
		return err
	}
	cm.sess, cm.udpPath, cm.tcpPath, cm.dialMode = path.session, path.udp, path.tcp, path.mode
	transport := int32(1)
	if path.tcp != nil {
		transport = 2
	}
	cm.activeTransport.Store(transport)
	cm.lastDialErr, cm.retryAfter = nil, time.Time{}
	return nil
}

func (cm *ClientMux) cacheDialErrorLocked(err error) {
	cm.lastDialErr, cm.retryAfter = err, time.Now().Add(500*time.Millisecond)
}

// A late timeout on an old stream must not close a replacement session or a
// session which is receiving authenticated packets for unrelated streams.
func (cm *ClientMux) discardSilentPath(session *smux.Session, since time.Time) bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if session == nil || (cm.tlsConfig != nil && cm.tlsNode.UDPPort == 0) || cm.closed {
		return false
	}
	if cm.sess != session {
		return true
	}
	var last *atomic.Int64
	if cm.udpPath != nil {
		last = &cm.udpPath.lastReply
	} else if cm.tcpPath != nil {
		last = &cm.tcpPath.lastReply
	}
	if last == nil || last.Load() >= since.UnixNano() {
		return false
	}
	cm.closeLocked()
	return true
}

// Give game and voice associations their own transport and congestion queue.
// All UDP associations on a node share it, and it is created on first use.
func (cm *ClientMux) gameLane() (*ClientMux, error) {
	cm.mu.Lock()
	if cm.closed {
		cm.mu.Unlock()
		return nil, net.ErrClosed
	}
	if cm.udpRelay == nil {
		cm.udpRelay = &ClientMux{serverIP: cm.serverIP, tlsNode: cm.tlsNode, tlsConfig: cm.tlsConfig, webKey: cm.webKey, packetEngine: cm.packetEngine, preferUDP: true, entry: cm.entry}
	}
	relay := cm.udpRelay
	cm.mu.Unlock()
	return relay, nil
}
func (cm *ClientMux) openUDPRelayStream() (*smux.Stream, error) {
	relay, err := cm.gameLane()
	if err != nil {
		return nil, err
	}
	if cm.tlsNode.UDPPort != 0 {
		// A UDP association gets a SOCKS success only after the selected path answers.
		if _, err := MeasureTunnelLatency(relay); err != nil {
			return nil, err
		}
	}
	return relay.OpenStream()
}

func listenTunnelUDP(network, addr string) (net.PacketConn, error) {
	pc, err := net.ListenPacket(network, addr)
	if err == nil {
		if udp, ok := pc.(*net.UDPConn); ok {
			const bufferSize = 4 << 20
			_ = udp.SetReadBuffer(bufferSize)
			_ = udp.SetWriteBuffer(bufferSize)
		}
	}
	return pc, err
}

func (cm *ClientMux) dialUDP() (net.Conn, *RTPPacketConn, error) {
	network := "udp4"
	if cm.serverIP.To4() == nil {
		network = "udp6"
	}
	rawPC, err := listenTunnelUDP(network, ":0")
	if err != nil {
		return nil, nil, err
	}

	rtpPC := NewClientRTPPacketConn(rawPC, cm.serverIP, cm.packetEngine)

	rtpPC.fixedPort = cm.tlsNode.UDPPort
	kcpConn, err := kcp.NewConn2(&net.UDPAddr{IP: cm.serverIP, Port: PortMin}, nil, 10, 3, rtpPC)
	if err != nil {
		_ = rawPC.Close()
		return nil, nil, err
	}
	return tunedKCP(kcpConn), rtpPC, nil
}

func (cm *ClientMux) Reset() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.resetLocked()
}

func (cm *ClientMux) Close() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.closed = true
	if cm.ownsEntry {
		cm.entry.Close()
	}
	if cm.stopClock != nil {
		cm.stopClock()
		<-cm.clockDone
		cm.stopClock = nil
	}
	cm.resetLocked()
}

func (cm *ClientMux) resetLocked() {
	cm.generation++
	if cm.dialDone != nil {
		cm.cancelDial()
		close(cm.dialDone)
		cm.dialDone, cm.cancelDial = nil, nil
	}
	cm.lastDialErr, cm.retryAfter = nil, time.Time{}
	cm.closeLocked()
	if cm.udpRelay != nil {
		cm.udpRelay.Close()
		cm.udpRelay = nil
	}
}

func (cm *ClientMux) closeLocked() {
	cm.activeTransport.Store(0)
	cm.tcpPath = nil
	if cm.udpPath != nil {
		_ = cm.udpPath.Close()
		cm.udpPath = nil
	}
	if cm.sess != nil {
		_ = cm.sess.Close()
		cm.sess = nil
	}
}

func runHeadlessClient(cfgPath string) {
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		log.Fatalf("[Client] 读取配置失败: %v", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("[Client] 配置解析失败: %v", err)
	}

	cm, err := cfg.nodeMux(cfg.ServerIP, cfg.MasterKey, cfg.TLSNode)
	if err != nil {
		log.Fatalf("[Client] 初始化失败: %v", err)
	}
	defer cm.Close()
	cm.startClockSync()

	router := cfg.newRouter()
	router.restoreRoutes(cfgPath, cfg.Routing)
	if len(cfg.AdSubscriptions) > 0 {
		sources, e := adSources(cfg.AdSubscriptions)
		if e != nil {
			log.Fatal(e)
		}
		rules, _, e := loadAdRules(context.Background(), cfgPath, sources, false)
		if e != nil {
			log.Fatal(e)
		}
		router.ads.Store(rules)
		go func() {
			ticker := time.NewTicker(adRefreshInterval)
			defer ticker.Stop()
			for range ticker.C {
				if rules, _, err := loadAdRules(context.Background(), cfgPath, sources, true); err == nil {
					router.ads.Store(rules)
				} else {
					log.Printf("[去广告] %v", err)
				}
			}
		}()
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		log.Fatalf("[Client] 监听 %s 失败: %v", cfg.Listen, err)
	}
	log.Printf("[Client] Slacker %s 已启动，混合代理监听 %s", AppVersion, cfg.Listen)

	for {
		c, err := ln.Accept()
		if err != nil {
			continue
		}
		go handleMixedProxy(c, cm, router)
	}
}

func smuxConfig() *smux.Config {
	cfg := smux.DefaultConfig()
	cfg.KeepAliveInterval = 10 * time.Second
	cfg.KeepAliveTimeout = 30 * time.Second
	cfg.MaxReceiveBuffer = 4 * 1024 * 1024
	return cfg
}

func smuxTransportConfig(conn net.Conn) *smux.Config {
	cfg := smuxConfig()
	if c, ok := conn.(*tlsMeterConn); ok && c.profile != tlsProfileV1 {
		cfg.Version = 2
		// A 200 ms path at 100 Mbit/s needs about 2.5 MB in flight.
		// Retain room for other streams when a single receiver pauses.
		cfg.MaxStreamBuffer = 4 << 20
		cfg.MaxReceiveBuffer = 16 << 20
	}
	return cfg
}

func closeWrite(dst io.Closer) error {
	if half, ok := dst.(interface{ CloseWrite() error }); ok {
		return half.CloseWrite()
	}
	return dst.Close()
}

func pipeBidirectional(a, b io.ReadWriteCloser) {
	relayBidirectional(a, b, 0)
}

type relayProgressWriter struct {
	io.Writer
	start time.Time
	last  *atomic.Int64
}

func (w relayProgressWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if n > 0 && w.last.Load() >= 0 {
		w.last.Store(int64(time.Since(w.start)))
	}
	return n, err
}

// After one clean EOF, let the reverse direction finish while it makes
// progress. A zero drainIdle retains the unrestricted client-side behavior.
func relayBidirectional(a, b io.ReadWriteCloser, drainIdle time.Duration) bool {
	start := time.Now()
	var last atomic.Int64
	last.Store(-1)
	done := make(chan error, 2)
	copyTo := func(dst, src io.ReadWriteCloser) {
		var w io.Writer = dst
		if drainIdle > 0 {
			w = relayProgressWriter{dst, start, &last}
		}
		_, err := io.Copy(w, src)
		if err == nil {
			err = closeWrite(dst)
		}
		done <- err
	}
	go copyTo(a, b)
	go copyTo(b, a)
	closeBoth := func() {
		_ = a.Close()
		_ = b.Close()
	}
	if err := <-done; err != nil {
		closeBoth()
		<-done
		return false
	}

	var timer *time.Timer
	var deadline <-chan time.Time
	if drainIdle > 0 {
		last.Store(int64(time.Since(start)))
		timer = time.NewTimer(drainIdle)
		defer timer.Stop()
		deadline = timer.C
	}
	for {
		select {
		case err := <-done:
			if err != nil {
				closeBoth()
			}
			return false
		case <-deadline:
			remaining := drainIdle - (time.Since(start) - time.Duration(last.Load()))
			if remaining > 0 {
				timer.Reset(remaining)
				continue
			}
			closeBoth()
			<-done
			return true
		}
	}
}
