package kit

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestParseProxyURL(t *testing.T) {
	cases := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{raw: "", want: ""},
		{raw: "http://127.0.0.1:7890", want: "http://127.0.0.1:7890"},
		{raw: "127.0.0.1:7890", want: "http://127.0.0.1:7890"},
		{raw: "socks5://127.0.0.1:1080", want: "socks5://127.0.0.1:1080"},
		{raw: "socks5h://127.0.0.1:1080", want: "socks5h://127.0.0.1:1080"},
		{raw: "https://proxy.example.com:443", want: "https://proxy.example.com:443"},
		{raw: "127.0.0.1", want: "http://127.0.0.1:80"},
		{raw: "socks5://127.0.0.1", want: "socks5://127.0.0.1:1080"},
		{raw: "ftp://127.0.0.1:21", wantErr: true},
		{raw: "http://", wantErr: true},
	}
	for _, c := range cases {
		u, err := ParseProxyURL(c.raw)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseProxyURL(%q) 期望报错, 实际返回 %v", c.raw, u)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseProxyURL(%q) 报错: %v", c.raw, err)
			continue
		}
		if c.want == "" {
			if u != nil {
				t.Errorf("ParseProxyURL(%q) 期望 nil, 实际 %v", c.raw, u)
			}
			continue
		}
		if u.String() != c.want {
			t.Errorf("ParseProxyURL(%q) = %q, 期望 %q", c.raw, u.String(), c.want)
		}
	}
}

func TestPickProxyServer(t *testing.T) {
	cases := []struct {
		server string
		scheme string
		want   string
	}{
		{server: "127.0.0.1:7890", scheme: "http", want: "127.0.0.1:7890"},
		{server: "127.0.0.1:7890", scheme: "https", want: "127.0.0.1:7890"},
		{server: "http=127.0.0.1:7890;https=127.0.0.1:7891", scheme: "http", want: "127.0.0.1:7890"},
		{server: "http=127.0.0.1:7890;https=127.0.0.1:7891", scheme: "https", want: "127.0.0.1:7891"},
		{server: "http=127.0.0.1:7890", scheme: "https", want: "127.0.0.1:7890"},
		{server: "socks=127.0.0.1:1080", scheme: "https", want: "127.0.0.1:1080"},
		{server: "socks=127.0.0.1:1080", scheme: "http", want: "127.0.0.1:1080"},
		{server: "http=127.0.0.1:7890;https=127.0.0.1:7891", scheme: "ftp", want: "127.0.0.1:7891"},
		{server: "", scheme: "https", want: ""},
	}
	for _, c := range cases {
		if got := pickProxyServer(c.server, c.scheme); got != c.want {
			t.Errorf("pickProxyServer(%q, %q) = %q, 期望 %q", c.server, c.scheme, got, c.want)
		}
	}
}

func TestIsLoopbackHost(t *testing.T) {
	loopback := []string{"127.0.0.1", "127.0.0.53", "localhost", "LOCALHOST", "::1", "[::1]", "0.0.0.0"}
	for _, h := range loopback {
		if !isLoopbackHost(h) {
			t.Errorf("isLoopbackHost(%q) = false, 期望 true", h)
		}
	}
	remote := []string{"example.com", "10.0.0.1", "8.8.8.8"}
	for _, h := range remote {
		if isLoopbackHost(h) {
			t.Errorf("isLoopbackHost(%q) = true, 期望 false", h)
		}
	}
}

// withProxyEnv 临时替换系统代理与环境变量代理解析器, 返回恢复函数。
func withProxyEnv(sys *url.URL, env *url.URL) func() {
	oldSys, oldEnv := systemProxyResolver, envProxyResolver
	systemProxyResolver = func(string, string) *url.URL { return sys }
	envProxyResolver = func(*url.URL) (*url.URL, error) { return env, nil }
	return func() {
		systemProxyResolver, envProxyResolver = oldSys, oldEnv
	}
}

func TestResolveEgressProxyPriority(t *testing.T) {
	sysProxy := mustParse(t, "http://127.0.0.1:7890")
	envProxy := mustParse(t, "http://127.0.0.1:1080")

	restore := withProxyEnv(sysProxy, envProxy)
	defer restore()

	// 手动指定优先级最高
	if err := SetEgressProxy("http://127.0.0.1:9999", true); err != nil {
		t.Fatalf("SetEgressProxy 报错: %v", err)
	}
	u, src := ResolveEgressProxy("https", "example.com:443")
	if src != ProxySourceManual || u.String() != "http://127.0.0.1:9999" {
		t.Errorf("手动指定时 = %v/%s, 期望 manual/http://127.0.0.1:9999", u, src)
	}

	// 其次是系统代理
	if err := SetEgressProxy("", true); err != nil {
		t.Fatalf("SetEgressProxy 报错: %v", err)
	}
	u, src = ResolveEgressProxy("https", "example.com:443")
	if src != ProxySourceSystem || u.String() != "http://127.0.0.1:7890" {
		t.Errorf("系统代理时 = %v/%s, 期望 system/http://127.0.0.1:7890", u, src)
	}

	// 关闭跟随系统代理后走环境变量
	if err := SetEgressProxy("", false); err != nil {
		t.Fatalf("SetEgressProxy 报错: %v", err)
	}
	u, src = ResolveEgressProxy("https", "example.com:443")
	if src != ProxySourceEnv || u.String() != "http://127.0.0.1:1080" {
		t.Errorf("环境变量时 = %v/%s, 期望 env/http://127.0.0.1:1080", u, src)
	}

	// 都没有则直连
	restore2 := withProxyEnv(nil, nil)
	defer restore2()
	u, src = ResolveEgressProxy("https", "example.com:443")
	if src != ProxySourceDirect || u != nil {
		t.Errorf("直连时 = %v/%s, 期望 direct/nil", u, src)
	}
}

func TestResolveEgressProxyLoopbackAlwaysDirect(t *testing.T) {
	restore := withProxyEnv(mustParse(t, "http://127.0.0.1:7890"), mustParse(t, "http://127.0.0.1:1080"))
	defer restore()

	if err := SetEgressProxy("http://127.0.0.1:9999", true); err != nil {
		t.Fatalf("SetEgressProxy 报错: %v", err)
	}
	for _, host := range []string{"127.0.0.1:9999", "localhost:9999", "[::1]:9999"} {
		u, src := ResolveEgressProxy("http", host)
		if u != nil || src != ProxySourceDirect {
			t.Errorf("回环地址 %s = %v/%s, 期望 direct/nil", host, u, src)
		}
	}
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := ParseProxyURL(raw)
	if err != nil {
		t.Fatalf("ParseProxyURL(%q) 报错: %v", raw, err)
	}
	return u
}

// TestHTTPTransportUsesEgressProxy 验证共享 Transport 真的按解析结果走代理。
func TestHTTPTransportUsesEgressProxy(t *testing.T) {
	// 起一个假的 HTTP 代理, 收到请求直接回一个标记头。
	proxyHits := make(chan string, 4)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits <- r.Host
		w.Header().Set("X-Via-Proxy", "1")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("proxied"))
	}))
	defer proxy.Close()

	restore := withProxyEnv(mustParse(t, proxy.URL), nil)
	defer restore()

	if err := SetEgressProxy("", true); err != nil {
		t.Fatalf("SetEgressProxy 报错: %v", err)
	}

	client := NewHTTPClient(5 * time.Second)
	resp, err := client.Get("http://upstream.example.com/path")
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "proxied" {
		t.Fatalf("响应体 = %q, 期望 proxied", string(body))
	}
	select {
	case host := <-proxyHits:
		if host != "upstream.example.com" {
			t.Errorf("代理收到的 Host = %q, 期望 upstream.example.com", host)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("代理未收到请求")
	}
}

// TestHTTPTransportLoopbackDirect 验证回环地址不会被送进代理。
func TestHTTPTransportLoopbackDirect(t *testing.T) {
	proxyHits := make(chan string, 4)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits <- r.Host
		w.WriteHeader(http.StatusOK)
	}))
	defer proxy.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("direct"))
	}))
	defer origin.Close()

	restore := withProxyEnv(mustParse(t, proxy.URL), nil)
	defer restore()

	if err := SetEgressProxy("", true); err != nil {
		t.Fatalf("SetEgressProxy 报错: %v", err)
	}

	client := NewHTTPClient(5 * time.Second)
	resp, err := client.Get(origin.URL)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "direct" {
		t.Fatalf("响应体 = %q, 期望 direct", string(body))
	}
	select {
	case h := <-proxyHits:
		t.Errorf("回环请求被送进了代理: %s", h)
	case <-time.After(300 * time.Millisecond):
	}
}
