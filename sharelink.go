package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

type ShareProfile struct {
	ServerIP  string `json:"s"`
	MasterKey string `json:"k"`
	Name      string `json:"n,omitempty"`
}

func EncodeShareLink(serverIP, masterKey, name string) (string, error) {
	b, err := json.Marshal(ShareProfile{
		ServerIP:  serverIP,
		MasterKey: masterKey,
		Name:      name,
	})
	if err != nil {
		return "", err
	}
	return "cm://" + base64.RawURLEncoding.EncodeToString(b), nil
}

func DecodeShareLink(link string) (*ShareProfile, error) {
	link = strings.TrimSpace(link)
	if !strings.HasPrefix(link, "cm://") {
		return nil, errors.New("无效链接，必须以 cm:// 开头")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(link, "cm://"))
	if err != nil {
		return nil, errors.New("链接 Base64 解码失败")
	}
	var p ShareProfile
	if err := json.Unmarshal(raw, &p); err != nil || p.ServerIP == "" || len(p.MasterKey) != 64 {
		return nil, errors.New("节点参数不完整或密钥长度错误")
	}
	return &p, nil
}
