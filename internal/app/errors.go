package app

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
)

// ============================================================================
// 上游错误透传
// 之前所有上游错误都被压成 500/502, 客户端无法按语义重试(402 欠费 / 429 限流 /
// 400 参数错误 全变成 api_error)。这里保留上游状态码与响应体, 按需回写。
// ============================================================================

// upstreamError 携带上游 HTTP 状态码与响应体。
type upstreamError struct {
	Status int
	Body   string
	Msg    string
}

func (e *upstreamError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	if e.Body != "" {
		return e.Body
	}
	return http.StatusText(e.Status)
}

// newUpstreamError 构造可透传的上游错误。
func newUpstreamError(status int, body string) *upstreamError {
	return &upstreamError{Status: status, Body: strings.TrimSpace(body)}
}

// clientStatusForUpstream 决定回写给客户端的 HTTP 状态码。
// 401 不透传: 那是我们的账号 token 失效, 与客户端 API Key 无关,
// 直接回 401 会让客户端误判自己的密钥无效而停止重试。
func clientStatusForUpstream(status int) int {
	switch {
	case status == http.StatusUnauthorized:
		return http.StatusBadGateway
	case status >= 400 && status < 500:
		return status
	case status == http.StatusServiceUnavailable, status == http.StatusTooManyRequests:
		return status
	default:
		return http.StatusBadGateway
	}
}

// upstreamErrorStatus 取用于统计/回写的状态码。
func upstreamErrorStatus(err error, fallback int) int {
	var ue *upstreamError
	if errors.As(err, &ue) {
		return clientStatusForUpstream(ue.Status)
	}
	if errors.Is(err, context.Canceled) {
		return 499 // client closed request
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}
	return fallback
}

// writeUpstreamError 按上游语义回写错误; 非 upstreamError 一律 502。
func writeUpstreamError(w http.ResponseWriter, err error) {
	var ue *upstreamError
	if !errors.As(err, &ue) {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]string{"message": err.Error(), "type": "api_error"},
		})
		return
	}
	status := clientStatusForUpstream(ue.Status)
	if ue.Body != "" && json.Valid([]byte(ue.Body)) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if _, werr := w.Write([]byte(ue.Body)); werr != nil {
			log.Printf("  write upstream error body failed: %v", werr)
		}
		return
	}
	msg := ue.Body
	if msg == "" {
		msg = http.StatusText(ue.Status)
	}
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"message": msg, "type": "upstream_error"},
	})
}