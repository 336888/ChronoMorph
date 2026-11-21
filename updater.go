package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const githubRepo = "336888/Slacker"

type UpdateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	HasUpdate bool   `json:"has_update"`
	URL       string `json:"url"`
}

func CheckLatestRelease() UpdateInfo {
	info := UpdateInfo{Current: AppVersion}
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", "https://api.github.com/repos/"+githubRepo+"/releases/latest", nil)
	if err != nil {
		return info
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Slacker-Client")

	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		return info
	}
	defer resp.Body.Close()

	var rel struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return info
	}
	info.Latest = rel.TagName
	info.URL = rel.HTMLURL
	info.HasUpdate = rel.TagName != "" && !strings.EqualFold(rel.TagName, AppVersion)
	return info
}
