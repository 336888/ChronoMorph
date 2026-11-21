package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"net"
	"testing"
)

func TestChronoMorphCore(t *testing.T) {
	masterKey := make([]byte, 32)
	_, _ = rand.Read(masterKey)

	clientEng := NewChronoPacketEngine(masterKey)
	serverEng := NewChronoPacketEngine(masterKey)

	orig := []byte("ChronoMorph end-to-end payload")
	pkt, hopPort, err := clientEng.SealAsRTP(orig)
	if err != nil {
		t.Fatalf("SealAsRTP 失败: %v", err)
	}
	if hopPort < PortMin || hopPort >= PortMax {
		t.Fatalf("跳频端口越界: %d", hopPort)
	}
	if pkt[0] != 0x80 || pkt[1] != 0x60 {
		t.Fatalf("外层 RTP 头部不合规: %x %x", pkt[0], pkt[1])
	}

	got, err := serverEng.OpenFromRTP(pkt)
	if err != nil || !bytes.Equal(got, orig) {
		t.Fatalf("OpenFromRTP 失败: err=%v, got=%q", err, got)
	}

	if _, err := serverEng.OpenFromRTP(pkt); err == nil {
		t.Fatal("防重放窗口未能拦截重复数据包")
	}

	tampered := append([]byte(nil), pkt...)
	tampered[4] ^= 0xff
	if _, err := serverEng.OpenFromRTP(tampered); err == nil {
		t.Fatal("篡改 RTP 头部后仍意外通过了 AEAD 校验")
	}
}

func TestSTUNAndShareLink(t *testing.T) {
	req := BuildSTUNBindingRequest()
	if !IsSTUNBindingRequest(req) {
		t.Fatal("未能识别合法 STUN 请求")
	}
	resp := BuildSTUNBindingResponse(req, &net.UDPAddr{IP: net.ParseIP("1.2.3.4"), Port: 12345})
	if len(resp) != 32 || !bytes.Equal(req[8:20], resp[8:20]) {
		t.Fatal("STUN 响应格式或 Transaction ID 不匹配")
	}

	key := make([]byte, 32)
	_, _ = rand.Read(key)
	keyHex := hex.EncodeToString(key)

	link, err := EncodeShareLink("45.32.10.88", keyHex, "Tokyo")
	if err != nil {
		t.Fatalf("编码失败: %v", err)
	}
	p, err := DecodeShareLink(link)
	if err != nil || p.ServerIP != "45.32.10.88" || p.MasterKey != keyHex {
		t.Fatalf("解码结果异常: %+v, err=%v", p, err)
	}
}
