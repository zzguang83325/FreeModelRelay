//go:build windows

package kit

import (
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows/registry"
)

const winInetKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// 系统代理缓存: 注册表读取有一定开销, 缓存 3 秒。
var sysProxyCache struct {
	sync.Mutex
	at      time.Time
	enabled bool
	server  string
}

// readWinInetProxy 读取当前用户的 WinINET 系统代理设置。
// ProxyEnable != 0 时 ProxyServer 才有意义。
func readWinInetProxy() (enabled bool, server string) {
	sysProxyCache.Lock()
	defer sysProxyCache.Unlock()
	if time.Since(sysProxyCache.at) < 3*time.Second {
		return sysProxyCache.enabled, sysProxyCache.server
	}
	enabled, server = queryWinInet()
	sysProxyCache.enabled, sysProxyCache.server, sysProxyCache.at = enabled, server, time.Now()
	return enabled, server
}

func queryWinInet() (bool, string) {
	k, err := registry.OpenKey(registry.CURRENT_USER, winInetKey, registry.QUERY_VALUE)
	if err != nil {
		return false, ""
	}
	defer k.Close()

	enableVal, _, err := k.GetIntegerValue("ProxyEnable")
	if err != nil || enableVal == 0 {
		return false, ""
	}
	server, _, err := k.GetStringValue("ProxyServer")
	if err != nil || strings.TrimSpace(server) == "" {
		return false, ""
	}
	return true, server
}

// systemProxyFor 根据 WinINET 的 ProxyServer 串解析出目标协议对应的代理地址。
// ProxyServer 支持两种格式:
//   - 单地址: "127.0.0.1:7890" (http 与 https 共用)
//   - 分协议: "http=127.0.0.1:7890;https=127.0.0.1:7890"
func systemProxyFor(scheme, hostname string) *url.URL {
	enabled, server := readWinInetProxy()
	if !enabled {
		return nil
	}
	raw := pickProxyServer(server, scheme)
	if raw == "" {
		return nil
	}
	u, err := ParseProxyURL(raw)
	if err != nil || u == nil {
		return nil
	}
	return u
}

// pickProxyServer 从 ProxyServer 串里取出该协议对应的地址。
// 见 proxy.go 中的实现(与平台无关)。
// SystemProxyInfo 返回系统代理的展示信息: 地址与是否启用。
func SystemProxyInfo() (string, bool) {
	enabled, server := readWinInetProxy()
	if !enabled {
		return "", false
	}
	return server, true
}
