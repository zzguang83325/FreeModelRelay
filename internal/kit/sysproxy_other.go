//go:build !windows

package kit

import "net/url"

// 非 Windows 平台没有 WinINET 系统代理, 只依赖环境变量与手动配置。
func systemProxyFor(scheme, hostname string) *url.URL {
	return nil
}

// SystemProxyInfo 返回系统代理的展示信息: 地址与是否启用。
// 非 Windows 平台无系统代理概念, 恒返回未启用。
func SystemProxyInfo() (string, bool) {
	return "", false
}
