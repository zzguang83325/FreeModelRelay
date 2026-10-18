package app

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"FreeModelRelay/internal/kit"
)

// RequestLog 单条大模型请求记录（仅对话/补全调用，含请求与回复内容摘要）
type RequestLog struct {
	Seq      int64     `json:"seq"` // 自增序号，保证展示顺序稳定
	Time     time.Time `json:"time"`
	Client   string    `json:"client"`
	Method   string    `json:"method"`
	Path     string    `json:"path"`
	Model    string    `json:"model,omitempty"`
	Route    string    `json:"route"` // zen | cline
	Status   int       `json:"status"`
	Duration int64     `json:"durationMs"`
	Note     string    `json:"note,omitempty"`
	ReqBody  string    `json:"reqBody,omitempty"`
	RespBody string    `json:"respBody,omitempty"`
}

const (
	maxReqLogs     = 100       // 仅内存保留最近 100 条，重启即清空
	maxBodyCapture = 128 << 10 // 请求/响应内容捕获上限 128KB
)

var (
	reqLogsMu sync.Mutex
	reqLogs   []RequestLog
	logSeq    int64
)

// allocLogSeq 在请求进入时分配序号，保证日志按请求到达顺序排列（而非完成顺序）
func allocLogSeq() int64 {
	reqLogsMu.Lock()
	defer reqLogsMu.Unlock()
	logSeq++
	return logSeq
}

// ============ 诊断文件日志（所有 HTTP 请求，用于排查客户端连通问题） ============

var (
	diagLogOnce sync.Once
	diagLogger  *log.Logger
)

// diagLog 惰性打开 data/all-requests.log，写不进也不影响服务
func diagLog() *log.Logger {
	diagLogOnce.Do(func() {
		f, err := os.OpenFile(kit.ResolveDataPath("all-requests.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err == nil {
			diagLogger = log.New(io.MultiWriter(os.Stderr, f), "", log.LstdFlags|log.Lmicroseconds)
		}
	})
	return diagLogger
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// logDiagREQ 记录大模型请求进入（后台/管理请求不记录）
func logDiagREQ(r *http.Request) {
	if lg := diagLog(); lg != nil {
		lg.Printf("REQ  %-4s %-40s from=%-15s UA=%q", r.Method, r.URL.Path, clientIP(r), r.UserAgent())
	}
}

// logDiagRESP 记录请求结束
func logDiagRESP(r *http.Request, status int, dur time.Duration, note string) {
	if lg := diagLog(); lg != nil {
		lg.Printf("RESP %-4s %-40s status=%-3d dur=%-8s %s", r.Method, r.URL.Path, status, dur.Truncate(time.Millisecond), note)
	}
}

// appendReqLog 记录一条请求日志（仅内存，不落盘），seq 由请求进入时分配
func appendReqLog(seq int64, l RequestLog) {
	reqLogsMu.Lock()
	l.Seq = seq
	reqLogs = append(reqLogs, l)
	if len(reqLogs) > maxReqLogs {
		reqLogs = reqLogs[len(reqLogs)-maxReqLogs:]
	}
	reqLogsMu.Unlock()
}

// LoadRequestLogs 返回最近的请求日志(底层切片拷贝, 防止返回后与并发 append 竞态)
func LoadRequestLogs() []RequestLog {
	reqLogsMu.Lock()
	defer reqLogsMu.Unlock()
	out := make([]RequestLog, len(reqLogs))
	copy(out, reqLogs)
	return out
}

// ClearRequestLogs 清空请求日志（序号继续递增，避免新旧记录混淆）
func ClearRequestLogs() {
	reqLogsMu.Lock()
	reqLogs = nil
	reqLogsMu.Unlock()
}

// ============ 请求日志中间件 ============

type statusWriter struct {
	http.ResponseWriter
	status  int
	respBuf []byte // 响应体捕获(上限 maxBodyCapture)
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	// 捕获只复制副本, 绝不修改/截断原始 b —— 否则发给客户端的数据会被截断
	if len(w.respBuf) < maxBodyCapture {
		room := maxBodyCapture - len(w.respBuf)
		tail := b
		if len(tail) > room {
			tail = tail[:room]
		}
		w.respBuf = append(w.respBuf, tail...)
	}
	return w.ResponseWriter.Write(b)
}

// Flush 透传底层 Flusher，否则包装器会丢失 http.Flusher 接口，
// 导致所有 SSE 流式响应被判为 "streaming not supported for client" 而返回空体。
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// isLLMPath 是否大模型请求(对话/补全)，仅这类请求记入请求日志
func isLLMPath(p string) bool {
	switch p {
	case "/v1/chat/completions", "/v1/messages", "/v1/responses":
		return true
	}
	return false
}

// requestLogMiddleware 仅记录大模型请求（/v1/chat/completions、/v1/messages、/v1/responses），
// 管理接口、模型列表、健康检查等一律不入库；捕获请求与响应内容用于详情查看。
// 同时做 free-gate 兼容路径归一化: /openai/v1/*、/anthropic/v1/*、/codex/v1/* → /v1/*，
// 使原本配置 http://host:port/openai/v1 的客户端可直接切换到本代理。
func requestLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		for _, prefix := range []string{"/openai", "/anthropic", "/codex"} {
			if strings.HasPrefix(p, prefix+"/v1") {
				r.URL.Path = p[len(prefix):]
				break
			}
		}
		start := time.Now()
		if !isLLMPath(r.URL.Path) {
			// 后台/管理/模型列表等请求不记任何日志，直接放行
			next.ServeHTTP(w, r)
			return
		}
		// 诊断日志：仅大模型请求落盘，用于排查客户端连通性
		logDiagREQ(r)
		seq := allocLogSeq()
		sw := &statusWriter{ResponseWriter: w}

		// 读取请求体提取模型，并放回，避免影响后续处理
		model := ""
		bodyBytes, _ := io.ReadAll(r.Body)
		if len(bodyBytes) > 0 {
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			var probe struct {
				Model string `json:"model"`
			}
			if json.Unmarshal(bodyBytes, &probe) == nil {
				model = probe.Model
			}
		}
		reqPreview := bodyBytes
		if len(reqPreview) > maxBodyCapture {
			reqPreview = reqPreview[:maxBodyCapture]
		}

		next.ServeHTTP(sw, r)

		if sw.status == 0 {
			sw.status = http.StatusOK
		}
		// 按实际生效模型判定路由(空/未知模型在处理器中已回退到默认模型)
		trimmedModel := strings.TrimSpace(model)
		logModel := trimmedModel
		if logModel == "" || !isKnownModel(logModel) {
			logModel = getDefaultModel()
		}
		// 发生回退时，在原始模型名后括号注明实际生效的模型
		displayModel := trimmedModel
		switch {
		case logModel == "":
			// 无默认模型可用，保持原样
		case trimmedModel == "":
			displayModel = "(" + logModel + ")" // 未指定模型
		case trimmedModel != logModel:
			displayModel = trimmedModel + " (" + logModel + ")"
		}
		route := "cline"
		if _, ok := resolveZenFreeModel(logModel); ok {
			route = "zen"
		} else if p := providerRoute(logModel); p != "" {
			// 第三方 provider (workbuddy / workbuddy-ai / qoder):
			// 不标记的话后台日志会把它们显示成 cline, 排查问题时误导。
			route = p
		}
		client := r.RemoteAddr
		if host, _, err := net.SplitHostPort(client); err == nil {
			client = host
		}
		appendReqLog(seq, RequestLog{
			Time:     start,
			Client:   client,
			Method:   r.Method,
			Path:     r.URL.Path,
			Model:    displayModel,
			Route:    route,
			Status:   sw.status,
			Duration: time.Since(start).Milliseconds(),
			ReqBody:  string(reqPreview),
			RespBody: summarizeResponse(sw),
		})
		logDiagRESP(r, sw.status, time.Since(start), "model="+displayModel+" route="+route)
	})
}

// summarizeResponse 从捕获的响应体提取回复文本：
// SSE 流式提取增量文本；JSON 提取 message/content 或错误信息；其余原样截断。
func summarizeResponse(sw *statusWriter) string {
	ct := sw.Header().Get("Content-Type")
	if strings.Contains(ct, "text/event-stream") {
		return kit.Truncate(extractSSEText(sw.respBuf), maxBodyCapture)
	}
	var m map[string]any
	if json.Unmarshal(sw.respBuf, &m) != nil {
		return kit.Truncate(string(sw.respBuf), 32768)
	}
	if em, ok := m["error"]; ok {
		switch e := em.(type) {
		case map[string]any:
			if msg, ok := e["message"].(string); ok {
				return "错误: " + msg
			}
		case string:
			return "错误: " + e
		}
		return "错误: " + kit.Truncate(string(sw.respBuf), 8192)
	}
	if choices, ok := m["choices"].([]any); ok && len(choices) > 0 {
		if c, ok := choices[0].(map[string]any); ok {
			if msg, ok := c["message"].(map[string]any); ok {
				if txt := extractContentText(msg); txt != "" {
					return txt
				}
			}
		}
	}
	if blocks, ok := m["content"].([]any); ok {
		var sb strings.Builder
		for _, b := range blocks {
			if bm, ok := b.(map[string]any); ok && bm["type"] == "text" {
				if t, ok := bm["text"].(string); ok {
					sb.WriteString(t)
				}
			}
		}
		if sb.Len() > 0 {
			return sb.String()
		}
	}
	return kit.Truncate(string(sw.respBuf), 32768)
}

// extractContentText 提取 message.content(字符串或分块数组)
func extractContentText(msg map[string]any) string {
	var sb strings.Builder
	switch c := msg["content"].(type) {
	case string:
		sb.WriteString(c)
	case []any:
		for _, p := range c {
			if pm, ok := p.(map[string]any); ok && pm["type"] == "text" {
				if t, ok := pm["text"].(string); ok {
					sb.WriteString(t)
				}
			}
		}
	}
	if sb.Len() == 0 {
		if rc, ok := msg["reasoning_content"].(string); ok {
			sb.WriteString("[推理] " + rc)
		}
	}
	return sb.String()
}

// extractSSEText 从捕获的 SSE 帧中提取增量文本(content/reasoning_content)
func extractSSEText(buf []byte) string {
	var sb strings.Builder
	for _, line := range strings.Split(string(buf), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		if payload == "[DONE]" {
			break
		}
		var m map[string]any
		if json.Unmarshal([]byte(payload), &m) != nil {
			continue
		}
		if em, ok := m["error"]; ok {
			if e, ok := em.(map[string]any); ok {
				if msg, ok := e["message"].(string); ok {
					sb.WriteString("\n[错误] " + msg)
				}
			}
			continue
		}
		choices, _ := m["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		cm, _ := choices[0].(map[string]any)
		if cm == nil {
			continue
		}
		d, _ := cm["delta"].(map[string]any)
		if d == nil {
			continue
		}
		if c, ok := d["content"].(string); ok {
			sb.WriteString(c)
		}
		if c, ok := d["reasoning"].(string); ok {
			sb.WriteString(c)
		}
		if c, ok := d["reasoning_content"].(string); ok {
			sb.WriteString(c)
		}
	}
	return sb.String()
}
