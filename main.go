package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
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
	TLSNode
	TLSListen     string        `json:"tls_listen,omitempty"`
	Listen        string        `json:"listen"`
	ServerIP      string        `json:"server_ip,omitempty"`
	MasterKey     string        `json:"master_key,omitempty"`
	Users         []UserEntry   `json:"users,omitempty"`
	Profiles      []NodeProfile `json:"profiles,omitempty"`
	ActiveIndex   int           `json:"active_index,omitempty"`
	DirectDomains []string      `json:"direct_domains,omitempty"`
	BlockDomains  []string      `json:"block_domains,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		if runtime.GOOS == "windows" {
			RunWindowsGUI("client.json")
			return
		}
		fmt.Println("用法: chronomorph <server | client | keygen | version> [-c config.json]")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "version":
		fmt.Println(VersionString())
	case "restore-proxy":
		if runtime.GOOS != "windows" {
			os.Exit(2)
		}
		if err := restoreSystemProxy(); err != nil {
			log.Fatal(err)
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
		_ = fs.Parse(os.Args[2:])
		cert, err := serverIdentity(*dir, *create)
		if err != nil {
			log.Fatal(err)
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

	rawPC, err := listenTunnelUDP("udp", cfg.Listen)
	if err != nil {
		log.Fatalf("[Server] 监听 UDP %s 失败: %v", cfg.Listen, err)
	}

	rtpPC := NewServerRTPPacketConn(rawPC, multiEng, guard)
	ln, err := kcp.ServeConn(nil, 10, 3, rtpPC)
	if err != nil {
		log.Fatalf("[Server] 启动 KCP 监听失败: %v", err)
	}
	log.Printf("[Server] ChronoMorph %s 已启动，监听 %s (用户数: %d)", AppVersion, cfg.Listen, len(users))

	if cfg.TLSListen != "" {
		cert, err := serverIdentity(filepath.Dir(cfgPath), false)
		if err != nil {
			log.Fatalf("[TLS] 无法读取服务端证书: %v", err)
		}
		tcpListener, err := net.Listen("tcp", cfg.TLSListen)
		if err != nil {
			log.Fatalf("[TLS] 无法监听 %s: %v", cfg.TLSListen, err)
		}
		defer tcpListener.Close()
		go serveTLS(tcpListener, cert, multiEng)
		log.Printf("[TLS] TLS 1.3/TCP 已启动，监听 %s", tcpListener.Addr())
	}
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		select {
		case <-sig:
		case <-stopCh:
			return
		}
		signal.Stop(sig)
		_ = ln.Close()
		_ = rawPC.Close()
	}()

	for {
		conn, err := ln.AcceptKCP()
		if err != nil {
			close(stopCh)
			_ = rawPC.Close()
			<-trafficSaved
			return
		}
		tuneKCP(conn, 1150)
		go handleServerSession(conn)
	}
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

func handleServerStream(stream *smux.Stream) {
	if !serverLimiter.Acquire() {
		_ = stream.Close()
		return
	}
	defer serverLimiter.Release()
	defer stream.Close()

	var cmd [1]byte
	if _, err := io.ReadFull(stream, cmd[:]); err != nil {
		return
	}
	if cmd[0] == CmdCapabilities {
		_, _ = stream.Write([]byte{CmdCapabilities, capDialAck | capDialProgress})
		return
	}
	if cmd[0] == CmdPing {
		_, _ = stream.Write([]byte{CmdPing})
		return
	}
	if cmd[0] == CmdUDP {
		handleServerUDPStream(stream)
		return
	}
	ack := cmd[0] == CmdDialTCP || cmd[0] == CmdDialReady
	if cmd[0] != CmdTCP && !ack {
		return
	}

	var lenBuf [1]byte
	if _, err := io.ReadFull(stream, lenBuf[:]); err != nil {
		return
	}
	addrBuf := make([]byte, lenBuf[0])
	if _, err := io.ReadFull(stream, addrBuf); err != nil {
		return
	}

	if cmd[0] == CmdDialReady {
		if _, err := stream.Write([]byte{CmdDialReady}); err != nil {
			return
		}
	}
	targetConn, err := net.DialTimeout("tcp", string(addrBuf), 8*time.Second)
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
	}

	pipeBidirectional(stream, targetConn)
}

type ClientMux struct {
	lastFailure   string
	lastFailureAt time.Time
	lastLogged    time.Time
	tlsConfig     *tls.Config
	tlsNode       TLSNode
	closed        bool
	mu            sync.Mutex
	serverIP      net.IP
	sess          *smux.Session
	udpPath       *RTPPacketConn
	dialMode      byte
	retryAfter    time.Time
	lastDialErr   error
	packetEngine  *ChronoPacketEngine
	udpRelay      *ClientMux
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
	if node.TLSPort != 0 {
		cm.tlsConfig, err = clientTLSConfig(key, node)
	} else {
		cm.packetEngine = NewChronoPacketEngine(key)
	}
	return cm, err
}

func (cm *ClientMux) OpenStream() (*smux.Stream, error) {
	stream, _, _, err := cm.openSessionStream()
	return stream, err
}

// Only the dial and first round trip are shared. The short negative cache also
// prevents queued callers from serially probing the same unreachable node.
func (cm *ClientMux) openSessionStream() (*smux.Stream, *smux.Session, byte, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.closed {
		return nil, nil, 0, net.ErrClosed
	}
	if cm.sess != nil {
		if !cm.sess.IsClosed() {
			if st, err := cm.sess.OpenStream(); err == nil {
				return st, cm.sess, cm.dialMode, nil
			}
		}
		cm.closeLocked()
	}
	if time.Now().Before(cm.retryAfter) {
		return nil, nil, 0, cm.lastDialErr
	}
	var conn net.Conn
	var err error
	if cm.tlsConfig != nil {
		cm.dialMode = CmdDialTCP
		conn, err = cm.dialTLS()
	} else {
		conn, err = cm.dialUDP()
	}
	if err == nil {
		cm.sess, err = smux.Client(conn, smuxTransportConfig(conn))
		if err != nil {
			_ = conn.Close()
		}
	}
	if err == nil && cm.tlsConfig == nil {
		cm.dialMode, err = confirmUDP(cm.sess)
		if err != nil {
			err = fmt.Errorf("UDP 节点未应答，请检查 UDP 放行、密钥和两端时间: %w", err)
		}
	}
	if err == nil {
		var st *smux.Stream
		st, err = cm.sess.OpenStream()
		if err == nil {
			cm.lastDialErr = nil
			cm.retryAfter = time.Time{}
			return st, cm.sess, cm.dialMode, nil
		}
	}
	cm.closeLocked()
	cm.lastDialErr, cm.retryAfter = err, time.Now().Add(500*time.Millisecond)
	return nil, nil, 0, err
}

// A late timeout on an old stream must not close a replacement session or a
// session which is receiving authenticated packets for unrelated streams.
func (cm *ClientMux) discardSilentUDP(session *smux.Session, since time.Time) bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if session == nil || cm.tlsConfig != nil || cm.closed {
		return false
	}
	if cm.sess != session {
		return true
	}
	if cm.udpPath == nil || cm.udpPath.lastReply.Load() >= since.UnixNano() {
		return false
	}
	cm.closeLocked()
	return true
}

// Give game and voice associations their own transport and congestion queue.
// All UDP associations on a node share it, and it is created on first use.
func (cm *ClientMux) openUDPRelayStream() (*smux.Stream, error) {
	cm.mu.Lock()
	if cm.closed {
		cm.mu.Unlock()
		return nil, net.ErrClosed
	}
	if cm.udpRelay == nil {
		cm.udpRelay = &ClientMux{serverIP: cm.serverIP, tlsNode: cm.tlsNode, tlsConfig: cm.tlsConfig, packetEngine: cm.packetEngine}
	}
	relay := cm.udpRelay
	cm.mu.Unlock()
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

func (cm *ClientMux) dialUDP() (net.Conn, error) {
	network := "udp4"
	if cm.serverIP.To4() == nil {
		network = "udp6"
	}
	rawPC, err := listenTunnelUDP(network, ":0")
	if err != nil {
		return nil, err
	}

	rtpPC := NewClientRTPPacketConn(rawPC, cm.serverIP, cm.packetEngine)

	kcpConn, err := kcp.NewConn2(&net.UDPAddr{IP: cm.serverIP, Port: PortMin}, nil, 10, 3, rtpPC)
	if err != nil {
		_ = rawPC.Close()
		return nil, err
	}
	tuneKCP(kcpConn, 1150)

	cm.udpPath = rtpPC
	return kcpConn, nil
}

func (cm *ClientMux) Reset() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.closeLocked()
	if cm.udpRelay != nil {
		cm.udpRelay.Close()
		cm.udpRelay = nil
	}
}

func (cm *ClientMux) Close() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.closed = true
	cm.closeLocked()
	if cm.udpRelay != nil {
		cm.udpRelay.Close()
		cm.udpRelay = nil
	}
}

func (cm *ClientMux) closeLocked() {
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

	if cfg.TLSPort == 0 {
		go SyncTimeOffset()
	}
	cm, err := newNodeMux(cfg.ServerIP, cfg.MasterKey, cfg.TLSNode)
	if err != nil {
		log.Fatalf("[Client] 初始化失败: %v", err)
	}
	defer cm.Close()

	router := NewRouter(cfg.DirectDomains, cfg.BlockDomains)
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		log.Fatalf("[Client] 监听 %s 失败: %v", cfg.Listen, err)
	}
	log.Printf("[Client] ChronoMorph %s 已启动，混合代理监听 %s", AppVersion, cfg.Listen)

	for {
		c, err := ln.Accept()
		if err != nil {
			continue
		}
		go handleMixedProxy(c, cm, router)
	}
}

func tuneKCP(conn *kcp.UDPSession, mtu int) {
	conn.SetNoDelay(1, 20, 2, 1)
	conn.SetWindowSize(256, 256)
	conn.SetMtu(mtu)
	conn.SetACKNoDelay(true)
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
	if c, ok := conn.(*tlsMeterConn); ok && c.muxV2 {
		cfg.Version = 2
		cfg.MaxStreamBuffer = 256 * 1024
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
	var stop sync.Once
	abort := func() { stop.Do(func() { _ = a.Close(); _ = b.Close() }) }
	var wg sync.WaitGroup
	wg.Add(2)
	copyTo := func(dst, src io.ReadWriteCloser) {
		defer wg.Done()
		if _, err := io.Copy(dst, src); err != nil {
			abort()
			return
		}
		if err := closeWrite(dst); err != nil {
			abort()
		}
	}
	go copyTo(a, b)
	go copyTo(b, a)
	wg.Wait()
}
