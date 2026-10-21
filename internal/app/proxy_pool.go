package app

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"FreeModelRelay/internal/kit"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
	"golang.org/x/net/proxy"
)

var (
	zenHTTPClient  = &http.Client{Transport: buildZenTransport()}
	zenProxyCount  atomic.Uint64
	zenTransportMu sync.Mutex

	// 冷却按代理 URL 记账(而非列表索引): 订阅节点随刷新/健康检测进出出口池,
	// 索引会错位, URL 是稳定键。
	zenProxyCooldowns   = map[string]time.Time{} // 代理URL -> 冷却截止
	zenProxyCooldownsMu sync.Mutex
)

// cooldownZenProxy 标记某出口代理冷却,冷却期内轮询跳过
func cooldownZenProxy(purl string, d time.Duration) {
	if purl == "" {
		return
	}
	if d <= 0 {
		d = 10 * time.Minute
	}
	zenProxyCooldownsMu.Lock()
	zenProxyCooldowns[purl] = time.Now().Add(d)
	zenProxyCooldownsMu.Unlock()
}

// clearZenProxyCooldown 清除某代理的冷却(封禁/解封后避免残留展示)
func clearZenProxyCooldown(purl string) {
	if purl == "" {
		return
	}
	zenProxyCooldownsMu.Lock()
	delete(zenProxyCooldowns, purl)
	zenProxyCooldownsMu.Unlock()
}

func zenProxyAvailable(purl string) bool {
	zenProxyCooldownsMu.Lock()
	defer zenProxyCooldownsMu.Unlock()
	until, ok := zenProxyCooldowns[purl]
	if !ok {
		return true
	}
	if time.Now().After(until) {
		delete(zenProxyCooldowns, purl)
		return true
	}
	return false
}

// zenProxyCooldownStatus 冷却中的出口(代理URL -> 冷却截止时刻)
func zenProxyCooldownStatus() map[string]string {
	zenProxyCooldownsMu.Lock()
	defer zenProxyCooldownsMu.Unlock()
	out := map[string]string{}
	for u, until := range zenProxyCooldowns {
		if time.Now().Before(until) {
			out[u] = until.Format("15:04:05")
		}
	}
	return out
}

// rebuildZenTransport 代理池或配置变化时重建 zen 上游 HTTP 客户端
func rebuildZenTransport() {
	zenTransportMu.Lock()
	defer zenTransportMu.Unlock()
	zenHTTPClient = &http.Client{Transport: buildZenTransport()}
}

func getZenHTTPClient() *http.Client {
	zenTransportMu.Lock()
	defer zenTransportMu.Unlock()
	return zenHTTPClient
}

// effectiveZenProxies zen 通道实际可用的出口列表: 手动代理 + 机场订阅节点(本地 socks 端口)。
// 订阅节点由 subscription.go 管理, 自动追加在手动列表之后; 冷却索引基于此合并列表。
func effectiveZenProxies() []string {
	cfg := getZenConfig()
	subs := subscriptionProxyURLs()
	if len(subs) == 0 {
		return cfg.Proxies
	}
	out := make([]string, 0, len(cfg.Proxies)+len(subs))
	out = append(out, cfg.Proxies...)
	return append(out, subs...)
}

// zenStickyProxy 粘性出口记忆(sticky 策略): 最近一次成功请求实际使用的代理 URL。
// 目的: 免费额度按出口 IP 记账, 一个节点能用就应该一直用, 而不是每次轮询
// 换节点反复撞其他节点的限流。节点失败进入冷却后自动切换, 冷却结束自动回归。
var zenStickyMu sync.Mutex
var zenStickyProxy string

func getZenStickyProxy() string {
	zenStickyMu.Lock()
	defer zenStickyMu.Unlock()
	return zenStickyProxy
}

func setZenStickyProxy(u string) {
	if u == "" {
		return
	}
	zenStickyMu.Lock()
	zenStickyProxy = u
	zenStickyMu.Unlock()
}

// pickZenProxy 按策略选择代理,返回 (代理URL, 索引);无代理返回 ("", -1)。
// 跳过冷却中的代理;全部冷却时返回最早恢复的近似(轮询位)。
// 每次调用递增计数,保证 round_robin 顺序与日志索引一致。
// 代理来源: 手动列表(网络设置页) + 机场订阅节点(自动注入)。
func pickZenProxy() (string, int) {
	proxies := effectiveZenProxies()
	n := len(proxies)
	if n == 0 {
		return "", -1
	}
	idx := int(zenProxyCount.Add(1)-1) % n
	switch getZenConfig().ProxyStrategy {
	case "random":
		idx = int(kit.RandIntn(n))
	case "fill":
		idx = 0
	case "sticky":
		// 优先队列(手工大模型检测通过的节点): 按加入顺序找第一个可用出口。
		// 队列节点全部冷却/失效时才继续走粘性与常规选择。
		for _, s := range subPreferredSnapshot() {
			for i, p := range proxies {
				if p == s && zenProxyAvailable(p) {
					return p, i
				}
			}
		}
		// 粘住最近成功的出口: 仍在池内且未冷却则直接复用;
		// 粘性失效(被删/封禁/冷却)时走下面的探测循环另选, 选中者成为新粘性。
		if s := getZenStickyProxy(); s != "" {
			for i, p := range proxies {
				if p == s {
					if zenProxyAvailable(p) {
						return p, i
					}
					break
				}
			}
		}
	}
	// 冷却跳过:线性探测下一个可用代理
	for i := 0; i < n; i++ {
		if zenProxyAvailable(proxies[idx]) {
			break
		}
		idx = (idx + 1) % n
	}
	if getZenConfig().ProxyStrategy == "sticky" && idx >= 0 && idx < n {
		setZenStickyProxy(proxies[idx])
	}
	return proxies[idx], idx
}

// zenProxyPick 记录某次请求实际拨号使用的代理, 通过请求上下文传给 callZenAPI。
// pickZenProxy 在每次拨号(zenDialContext)时写入, 请求结束后由调用方读取用于
// 精确冷却/封禁(按 url 记账)。连接被复用(keep-alive)时本次请求不会拨号,
// 此时 url 保持空, 调用方不做冷却。
type zenProxyPick struct {
	idx int    // 池内索引, 仅日志用
	url string // 实际拨号代理 URL, 冷却/封禁记账键
}

type zenPickKey struct{}

// lastZenProxyIdx 最近一次选择的代理索引(日志用)
func lastZenProxyIdx() int {
	v := int64(zenProxyCount.Load())
	if v <= 0 {
		return -1
	}
	return int((v - 1) % int64(max(1, len(effectiveZenProxies()))))
}

func maskProxyURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = url.User("***")
	return u.String()
}

func buildZenTransport() *http.Transport {
	t := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
	}
	t.DialContext = zenDialContext
	// https: uTLS Chrome 指纹 + 强制 HTTP/1.1。
	// 2026-10-14 实测: 同 body 同头下 h2(Go x/net/http2 SETTINGS 帧指纹) 403,
	// h1.1 放行 —— 网关对 h2 帧序做指纹检测, Go http2 特征被识别。
	t.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		raw, err := zenDialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			raw.Close()
			return nil, err
		}
		uconn, err := zenUTLSConn(raw, host)
		if err != nil {
			raw.Close()
			return nil, err
		}
		if err := uconn.HandshakeContext(ctx); err != nil {
			uconn.Close()
			return nil, err
		}
		return uconn, nil
	}
	return t
}

// zenUTLSConn 构造 Chrome_133 指纹且 ALPN 仅含 http/1.1 的 uTLS 连接。
// 注意: 指纹模板自带 ALPN 扩展(h2,http/1.1), 会覆盖 Config.NextProtos,
// 必须改写 spec 内的 ALPNExtension 才能真正降级到 HTTP/1.1
// (网关对 Go x/net/http2 的 SETTINGS 帧指纹做检测, h2 上必 403)。
func zenUTLSConn(raw net.Conn, host string) (*utls.UConn, error) {
	spec, err := utls.UTLSIdToSpec(utls.HelloChrome_133)
	if err != nil {
		return nil, err
	}
	for _, e := range spec.Extensions {
		if alpn, ok := e.(*utls.ALPNExtension); ok {
			alpn.AlpnProtocols = []string{"http/1.1"}
			break
		}
	}
	uconn := utls.UClient(raw, &utls.Config{ServerName: host}, utls.HelloCustom)
	if err := uconn.ApplyPreset(&spec); err != nil {
		return nil, err
	}
	return uconn, nil
}

func zenHTTP2Transport() *http2.Transport {
	return &http2.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			raw, err := zenDialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				raw.Close()
				return nil, err
			}
			// Chrome_120 指纹已于 2026-10 被 opencode 网关识别拉黑(curl/Go 同 body 可复现 403,
			// Bun/BoringSSL 同 body 200), 升级为 Chrome_133
			uconn := utls.UClient(raw, &utls.Config{
				ServerName: host,
				NextProtos: []string{"h2", "http/1.1"},
			}, utls.HelloChrome_133)
			if err := uconn.HandshakeContext(ctx); err != nil {
				raw.Close()
				return nil, err
			}
			return uconn, nil
		},
	}
}

func zenDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	p, idx := pickZenProxy()
	// 记录本次请求实际拨号使用的代理, 供调用方精确冷却(连接未复用才有拨号)
	if pk, ok := ctx.Value(zenPickKey{}).(*zenProxyPick); ok {
		pk.idx = idx
		pk.url = p
	}
	if p == "" {
		// zen 专用代理池为空: 回退到统一出口代理(手动 > 系统代理 > 环境变量 > 直连)
		if u, _ := kit.ResolveEgressProxy("https", addr); u != nil {
			p = u.String()
		}
	}
	if p == "" {
		d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
		return d.DialContext(ctx, network, addr)
	}
	return dialViaProxy(ctx, p, network, addr)
}

// dialViaProxy 统一拨号:http/https 走 CONNECT,socks5 走 SOCKS5 握手
func dialViaProxy(ctx context.Context, raw, network, addr string) (net.Conn, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("bad proxy url: %w", err)
	}
	switch u.Scheme {
	case "http", "https":
		return dialHTTPProxy(ctx, u, network, addr)
	case "socks5", "socks5h":
		auth := &proxy.Auth{}
		if u.User != nil {
			auth.User = u.User.Username()
			auth.Password, _ = u.User.Password()
		}
		d, err := proxy.SOCKS5("tcp", u.Host, auth, proxy.Direct)
		if err != nil {
			return nil, err
		}
		type ctxDialer interface {
			DialContext(context.Context, string, string) (net.Conn, error)
		}
		if cd, ok := d.(ctxDialer); ok {
			return cd.DialContext(ctx, network, addr)
		}
		// 旧接口无 ctx:包装
		type result struct {
			c   net.Conn
			err error
		}
		ch := make(chan result, 1)
		go func() {
			c, err := d.Dial(network, addr)
			ch <- result{c, err}
		}()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case r := <-ch:
			return r.c, r.err
		}
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q", u.Scheme)
	}
}

// dialHTTPProxy 通过 http(s) 代理建立 CONNECT 隧道
func dialHTTPProxy(ctx context.Context, u *url.URL, network, addr string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	rawConn, err := d.DialContext(ctx, "tcp", u.Host)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "https" {
		tlsConn := tls.Client(rawConn, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: u.Hostname()})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			rawConn.Close()
			return nil, err
		}
		rawConn = tlsConn
	}

	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: addr},
		Host:   addr,
		Header: make(http.Header),
	}
	if u.User != nil {
		cred := base64.StdEncoding.EncodeToString([]byte(u.User.String()))
		req.Header.Set("Proxy-Authorization", "Basic "+cred)
	}
	if err := req.Write(rawConn); err != nil {
		rawConn.Close()
		return nil, err
	}

	br := bufio.NewReader(rawConn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		rawConn.Close()
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		rawConn.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("proxy CONNECT %s: %s %s", u.Host, resp.Status, strings.TrimSpace(string(b)))
	}
	return rawConn, nil
}
