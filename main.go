package main

import (
	"crypto/rand"
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
	CmdTCP  byte = 0x01
	CmdUDP  byte = 0x02
	CmdPing byte = 0x03
)

type Config struct {
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

	rawPC, err := net.ListenPacket("udp", cfg.Listen)
	if err != nil {
		log.Fatalf("[Server] 监听 UDP %s 失败: %v", cfg.Listen, err)
	}

	rtpPC := NewServerRTPPacketConn(rawPC, multiEng, guard)
	ln, err := kcp.ServeConn(nil, 10, 3, rtpPC)
	if err != nil {
		log.Fatalf("[Server] 启动 KCP 监听失败: %v", err)
	}
	log.Printf("[Server] ChronoMorph %s 已启动，监听 %s (用户数: %d)", AppVersion, cfg.Listen, len(users))

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
		go handleServerKCP(conn)
	}
}

func handleServerKCP(conn *kcp.UDPSession) {
	defer conn.Close()
	sess, err := smux.Server(conn, smuxConfig())
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
	if cmd[0] == CmdPing {
		_, _ = stream.Write([]byte{CmdPing})
		return
	}
	if cmd[0] == CmdUDP {
		handleServerUDPStream(stream)
		return
	}
	if cmd[0] != CmdTCP {
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

	targetConn, err := net.DialTimeout("tcp", string(addrBuf), 8*time.Second)
	if err != nil {
		return
	}
	defer targetConn.Close()

	pipeBidirectional(stream, targetConn)
}

type ClientMux struct {
	closed       bool
	mu           sync.Mutex
	serverIP     net.IP
	sess         *smux.Session
	packetConn   net.PacketConn
	packetEngine *ChronoPacketEngine
}

func NewClientMux(serverIPStr, masterKeyHex string) (*ClientMux, error) {
	ip := net.ParseIP(serverIPStr)
	if ip == nil {
		return nil, fmt.Errorf("无效的服务端 IP: %s", serverIPStr)
	}
	key, err := hex.DecodeString(masterKeyHex)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("MasterKey 必须是 64 位 Hex 字符串")
	}
	return &ClientMux{serverIP: ip, packetEngine: NewChronoPacketEngine(key)}, nil
}

func (cm *ClientMux) OpenStream() (*smux.Stream, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.closed {
		return nil, net.ErrClosed
	}
	if cm.sess != nil {
		if !cm.sess.IsClosed() {
			if stream, err := cm.sess.OpenStream(); err == nil {
				return stream, nil
			}
		}
		cm.closeLocked()
	}

	network := "udp4"
	if cm.serverIP.To4() == nil {
		network = "udp6"
	}
	rawPC, err := net.ListenPacket(network, ":0")
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

	sess, err := smux.Client(kcpConn, smuxConfig())
	if err != nil {
		_ = kcpConn.Close()
		_ = rawPC.Close()
		return nil, err
	}
	cm.sess = sess
	cm.packetConn = rawPC
	stream, err := sess.OpenStream()
	if err != nil {
		cm.closeLocked()
	}
	return stream, err
}

func (cm *ClientMux) Reset() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.closeLocked()
}

func (cm *ClientMux) Close() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.closed = true
	cm.closeLocked()
}

func (cm *ClientMux) closeLocked() {
	if cm.packetConn != nil {
		_ = cm.packetConn.Close()
		cm.packetConn = nil
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

	SyncTimeOffset()
	cm, err := NewClientMux(cfg.ServerIP, cfg.MasterKey)
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

func pipeBidirectional(a, b io.ReadWriteCloser) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(a, b)
		_ = a.Close()
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(b, a)
		_ = b.Close()
	}()
	wg.Wait()
}
