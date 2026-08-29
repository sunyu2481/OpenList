package tool

import (
	"testing"

	_115 "github.com/OpenListTeam/OpenList/v4/drivers/115"
	_115_open "github.com/OpenListTeam/OpenList/v4/drivers/115_open"
	_123 "github.com/OpenListTeam/OpenList/v4/drivers/123"
	_123_open "github.com/OpenListTeam/OpenList/v4/drivers/123_open"
	"github.com/OpenListTeam/OpenList/v4/drivers/guangyapan"
	"github.com/OpenListTeam/OpenList/v4/drivers/pikpak"
	"github.com/OpenListTeam/OpenList/v4/drivers/thunder"
	"github.com/OpenListTeam/OpenList/v4/drivers/thunder_browser"
	"github.com/OpenListTeam/OpenList/v4/drivers/thunderx"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
)

func TestUrlScheme(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		// ed2k 链接里的 "|" 会让 url.Parse 直接报错，必须自己取 scheme
		{"ed2k", "ed2k://|file|movie.avi|733934592|FD8F1B4C5F8B3E7A9C2D1E0F4A6B8C9D|/", "ed2k"},
		{"ed2k uppercase", "ED2K://|file|a.mkv|100|ABCDEF|/", "ed2k"},
		{"ed2k with spaces", "  ed2k://|file|a.mkv|100|ABCDEF|/  ", "ed2k"},
		{"magnet", "magnet:?xt=urn:btih:abcdef", "magnet"},
		{"http", "http://example.com/a.zip", "http"},
		{"https uppercase", "HTTPS://example.com/a.zip", "https"},
		{"scheme with plus", "svn+ssh://example.com/repo", "svn+ssh"},

		// 无法识别 scheme 的情况一律返回空串，交给下游按 http 处理
		{"empty", "", ""},
		{"no scheme", "example.com/a.zip", ""},
		{"relative path", "/tmp/a.zip", ""},
		{"leading colon", ":example.com", ""},
		{"scheme starts with digit", "1http://example.com", ""},
		{"invalid char in scheme", "ht tp://example.com", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := urlScheme(tt.url); got != tt.want {
				t.Errorf("urlScheme(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestEd2kUnsupportedTools(t *testing.T) {
	// 黑名单里的工具确实不支持 ed2k
	for _, name := range []string{"SimpleHttp", "aria2", "qBittorrent", "Transmission"} {
		if _, ok := ed2kUnsupportedTools[name]; !ok {
			t.Errorf("expected %q to be in ed2kUnsupportedTools", name)
		}
	}
	// 网盘系工具把链接透传给服务端，不应被入口拦截
	for _, name := range []string{"115 Cloud", "115 Open", "PikPak", "Thunder", "ThunderX", "ThunderBrowser"} {
		if _, ok := ed2kUnsupportedTools[name]; ok {
			t.Errorf("%q should not be in ed2kUnsupportedTools", name)
		}
	}
}

func TestToolNameForStorage(t *testing.T) {
	tests := []struct {
		name    string
		storage driver.Driver
		want    string
	}{
		{name: "115 Cloud", storage: &_115.Pan115{}, want: "115 Cloud"},
		{name: "115 Open", storage: &_115_open.Open115{}, want: "115 Open"},
		{name: "123Pan", storage: &_123.Pan123{}, want: "123Pan"},
		{name: "123 Open", storage: &_123_open.Open123{}, want: "123 Open"},
		{name: "GuangYaPan", storage: &guangyapan.GuangYaPan{}, want: "GuangYaPan"},
		{name: "PikPak", storage: &pikpak.PikPak{}, want: "PikPak"},
		{name: "Thunder", storage: &thunder.Thunder{}, want: "Thunder"},
		{name: "ThunderX", storage: &thunderx.ThunderX{}, want: "ThunderX"},
		{name: "ThunderBrowser", storage: &thunder_browser.ThunderBrowser{}, want: "ThunderBrowser"},
		{name: "other", storage: nil, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toolNameForStorage(tt.storage); got != tt.want {
				t.Fatalf("toolNameForStorage(%T) = %q, want %q", tt.storage, got, tt.want)
			}
		})
	}
}
