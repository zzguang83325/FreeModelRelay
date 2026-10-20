package kit

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/net/http/httpproxy"
)

var errUnsupportedProxyScheme = errors.New("代理协议不受支持（支持 http/https/socks5/socks5h）")
var errProxyMissingHost = errors.New("代理缺少 host:port")

// unsupportedProxyScheme 构造协议不支持的错误。
func unsupportedProxyScheme(scheme string) error {
	return fmt.Errorf("%w: %s", errUnsupportedProxyScheme, scheme)
}

// 出口代理来源标识
const (
	ProxySourceManual = "manual" // 后台手动指定
	ProxySourceSystem = "system" // 操作系统系统代理(Windows WinINET)
	ProxySourceEnv    = "env"    // HTTP_PROXY / HTTPS_PROXY 环境变量
	ProxySourceDirect = "direct" // 直连
)

// egressProxyConfig 当前生效的出口代理配置。
// manual 优先级最高; followSystem 打开后按 Windows 系统代理; 最后回退环境变量; 都没有则直连。
type egressProxyConfig struct {
	manual       *url.URL
	followSystem bool
}

var egressProxyCfg atomic.Pointer[egressProxyConfig]

// systemProxyResolver 解析操作系统系统代理, 便于单元测试替换。
var systemProxyResolver = systemProxyFor

// envProxyFunc 读取 HTTP_PROXY / HTTPS_PROXY / NO_PROXY。
// Go 标准库不读 ALL_PROXY, 这里同样不读。
var envProxyFunc = sync.OnceValue(func() func(*url.URL) (*url.URL, error) {
	return httpproxy.FromEnvironment().ProxyFunc()
})

// envProxyResolver 解析环境变量代理, 便于单元测试替换。
var envProxyResolver = func(u *url.URL) (*url.URL, error) {
	return envProxyFunc()(u)
}

// ParseProxyURL 校验并解析代理地址, 支持 http/https/socks5/socks5h。
// 缺协议时按 http:// 处理, 缺端口时补默认端口。
func ParseProxyURL(raw string) (*url.URL, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, nil
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return nil, err
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, unsupportedProxyScheme(u.Scheme)
	}
	if u.Host == "" {
		return nil, errProxyMissingHost
	}
	if _, _, err := net.SplitHostPort(u.Host); err != nil {
		if u.Scheme == "socks5" || u.Scheme == "socks5h" {
			u.Host = net.JoinHostPort(u.Host, "1080")
		} else {
			u.Host = net.JoinHostPort(u.Host, "80")
		}
	}
	return u, nil
}

// SetEgressProxy 更新出口代理配置。
// manual 非空时启用手动代理; followSystem 控制是否跟随操作系统系统代理。
// 配置变化后关闭空闲连接, 避免旧连接继续复用旧出口。
func SetEgressProxy(manual string, followSystem bool) error {
	u, err := ParseProxyURL(manual)
	if err != nil {
		return err
	}
	egressProxyCfg.Store(&egressProxyConfig{manual: u, followSystem: followSystem})
	HTTPTransport.CloseIdleConnections()
	return nil
}

// EgressProxyFor 供 http.Transport.Proxy 使用: 逐请求决定是否走代理, 返回 nil 即直连。
func EgressProxyFor(req *http.Request) (*url.URL, error) {
	if req == nil || req.URL == nil {
		return nil, nil
	}
	u, _ := ResolveEgressProxy(req.URL.Scheme, req.URL.Host)
	return u, nil
}

// ResolveEgressProxy 按目标协议与主机解析当前生效的出口代理。
// 返回 (代理地址, 来源); 代理地址为 nil 表示直连。
// 本机回环地址永远直连, 不会被送进代理。
func ResolveEgressProxy(scheme, hostPort string) (*url.URL, string) {
	hostname := hostPort
	if h, _, err := net.SplitHostPort(hostPort); err == nil {
		hostname = h
	}
	hostname = strings.Trim(hostname, "[]")
	if hostname == "" || isLoopbackHost(hostname) {
		return nil, ProxySourceDirect
	}

	cfg := egressProxyCfg.Load()
	if cfg == nil {
		cfg = &egressProxyConfig{}
	}
	if cfg.manual != nil {
		return cfg.manual, ProxySourceManual
	}
	if cfg.followSystem {
		if u := systemProxyResolver(scheme, hostname); u != nil {
			return u, ProxySourceSystem
		}
	}
	if u, err := envProxyResolver(&url.URL{Scheme: scheme, Host: hostPort}); err == nil && u != nil {
		return u, ProxySourceEnv
	}
	return nil, ProxySourceDirect
}

// EgressProxyStatus 返回当前生效的出口代理状态, 供后台展示。
func EgressProxyStatus() (proxyURL string, source string, systemURL string, systemEnabled bool) {
	systemURL, systemEnabled = SystemProxyInfo()
	if u, src := ResolveEgressProxy("https", "example.com:443"); u != nil {
		return u.String(), src, systemURL, systemEnabled
	}
	return "", ProxySourceDirect, systemURL, systemEnabled
}

// pickProxyServer 从 WinINET 的 ProxyServer 串里取出该协议对应的地址。
// 支持 "127.0.0.1:7890" 单地址与 "http=...;https=...;socks=..." 分协议两种格式。
func pickProxyServer(server, scheme string) string {
	s := strings.TrimSpace(server)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "=") {
		return s
	}
	var httpAddr, httpsAddr string
	for _, seg := range strings.Split(s, ";") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		k, v, ok := strings.Cut(seg, "=")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "http":
			httpAddr = strings.TrimSpace(v)
		case "https":
			httpsAddr = strings.TrimSpace(v)
		case "socks":
			// socks 键对所有协议兜底
			if httpAddr == "" {
				httpAddr = strings.TrimSpace(v)
			}
			if httpsAddr == "" {
				httpsAddr = strings.TrimSpace(v)
			}
		}
	}
	switch strings.ToLower(scheme) {
	case "http":
		return httpAddr
	case "https":
		if httpsAddr != "" {
			return httpsAddr
		}
		return httpAddr
	default:
		if httpsAddr != "" {
			return httpsAddr
		}
		return httpAddr
	}
}

// isLoopbackHost 判断是否本机回环地址。
func isLoopbackHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	h = strings.Trim(h, "[]")
	if h == "localhost" || h == "localhost.localdomain" {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback() || ip.IsUnspecified()
	}
	return false
}
