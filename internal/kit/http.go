package kit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

var ExecCommand = exec.Command

var HTTPTransport = &http.Transport{
	MaxIdleConns:          100,
	MaxIdleConnsPerHost:   10,
	IdleConnTimeout:       90 * time.Second,
	DisableCompression:    false,
	ResponseHeaderTimeout: 120 * time.Second, // 上游 2 分钟未返回响应头则中断，防止无限等待
	// 动态代理解析: 逐请求按当前配置决定走代理还是直连。
	// 配置变更后由 SetEgressProxy 调用 CloseIdleConnections, 新请求立即生效。
	Proxy: EgressProxyFor,
}

var HTTPClient = &http.Client{
	Transport: HTTPTransport,
}

// NewHTTPClient 创建使用统一出口代理的客户端。
// 所有上游请求都应通过它创建, 以保证代理行为一致。
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: HTTPTransport,
		Timeout:   timeout,
	}
}

func HTTPPostForm(rawURL string, form url.Values) (*http.Response, error) {
	req, err := http.NewRequest("POST", rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return HTTPClient.Do(req)
}

func HTTPPostJSON(rawURL string, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", rawURL, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return HTTPClient.Do(req)
}

func ReadBody(resp *http.Response) string {
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Sprintf("<read error: %v>", err)
	}
	return string(data)
}

func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func RunCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	return cmd.Start()
}
