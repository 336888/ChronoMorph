package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"strings"
)

type ShareProfile struct {
	TLSNode
	ServerIP  string `json:"s"`
	MasterKey string `json:"k"`
	Name      string `json:"n,omitempty"`
}

func EncodeShareLink(serverIP, masterKey, name string) (string, error) {
	return (ShareProfile{
		ServerIP:  serverIP,
		MasterKey: masterKey,
		Name:      name,
	}).encode()
}

func (p ShareProfile) encode() (string, error) {
	if err := p.TLSNode.validate(); err != nil {
		return "", err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	prefix := "cm://"
	if p.TLSPort != 0 {
		prefix = "cm+tls://"
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

func DecodeShareLink(link string) (*ShareProfile, error) {
	link = strings.TrimSpace(link)
	prefix, encoded, ok := strings.Cut(link, "://")
	if !ok || (prefix != "cm" && prefix != "cm+tls") {
		return nil, errors.New("请输入 cm:// 或 cm+tls:// 节点链接")
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("链接 Base64 解码失败")
	}
	var p ShareProfile
	if err := json.Unmarshal(raw, &p); err != nil || p.ServerIP == "" || len(p.MasterKey) != 64 {
		return nil, errors.New("节点参数不完整或密钥长度错误")
	}
	if net.ParseIP(p.ServerIP) == nil {
		return nil, errors.New("链接中的服务器 IP 无效，请从 Debian 重新导出")
	}
	if _, err := hex.DecodeString(p.MasterKey); err != nil {
		return nil, errors.New("链接中的密钥不是十六进制")
	}
	if err := p.TLSNode.validate(); err != nil {
		return nil, err
	}
	if (prefix == "cm+tls") != (p.TLSPort != 0) {
		return nil, errors.New("节点类型与 TLS 参数不一致")
	}
	return &p, nil
}
