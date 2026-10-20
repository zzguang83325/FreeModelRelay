package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"FreeModelRelay/internal/kit"
)

// sseErrorText 把 SSE 错误帧里的 error 字段拼成人能读的一句话。
//
// 直接 fmt.Sprint 一个 map[string]any 会吐出 "map[message:xxx type:yyy]" ——
// 那是 Go 的结构体直出, 用户看到的是字段名和方括号而不是错误信息, 排查时还
// 得回头翻日志。error 可能是对象(合成帧 / 上游 OpenAI 形态), 也可能是裸
// 字符串, 两种都要认。
func sseErrorText(e any) string {
	if s, ok := e.(string); ok {
		return s
	}
	m, ok := e.(map[string]any)
	if !ok {
		return fmt.Sprint(e)
	}
	msg, _ := m["message"].(string)
	typ, _ := m["type"].(string)
	code, _ := m["code"].(string)
	parts := make([]string, 0, 3)
	if strings.TrimSpace(msg) != "" {
		parts = append(parts, msg)
	}
	if strings.TrimSpace(typ) != "" {
		parts = append(parts, "type="+typ)
	}
	if strings.TrimSpace(code) != "" {
		parts = append(parts, "code="+code)
	}
	if len(parts) == 0 {
		return fmt.Sprint(e)
	}
	return strings.Join(parts, " ")
}

// ============================================================================
// Provider SSE 中继: 把非标准上游流(wb 的裸 OpenAI chunk / qoder 的双层信封)
// 规整为客户端可直接消费的 OpenAI chat.completion.chunk 流。
// ============================================================================

// sseExtractor 从一条 `data:` 负载中提取对客户端可见的 OpenAI 兼容 chunk。
//   - (chunk, true, false)  转发该 chunk
//   - (nil, false, false)   跳过该帧(心跳/控制/无 body)
//   - (nil, false, true)    上游结束帧
type sseExtractor func(payload []byte) (chunk []byte, ok bool, done bool)

func writeSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
}

// relayProviderSSE 逐行转发上游 SSE。非 `data:` 行(注释心跳 `: heartbeat`、
// `event:finish` 等控制行)一律丢弃, 避免部分客户端解析失败。
func relayProviderSSE(w http.ResponseWriter, upstream *http.Response, extract sseExtractor, onUsage func(map[string]any)) {
	writeSSEHeaders(w)
	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Printf("  streaming not supported for client")
		return
	}

	reader := bufio.NewReader(upstream.Body)
	const idleTimeout = 90 * time.Second
	idle := time.AfterFunc(idleTimeout, func() { upstream.Body.Close() })
	defer idle.Stop()

	for {
		line, err := reader.ReadString('\n')
		idle.Reset(idleTimeout)
		if err != nil {
			if err != io.EOF {
				log.Printf("  upstream stream interrupted: %v", err)
			}
			break
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if !strings.HasPrefix(trimmed, "data:") {
			continue
		}
		payload := strings.TrimSpace(trimmed[5:])
		if payload == "" {
			continue
		}
		chunk, ok, done := extract([]byte(payload))
		if done {
			w.Write([]byte("data: [DONE]\n\n"))
			flusher.Flush()
			return
		}
		if !ok || len(chunk) == 0 {
			continue
		}
		if onUsage != nil {
			var obj map[string]any
			if json.Unmarshal(chunk, &obj) == nil {
				if u, ok := obj["usage"].(map[string]any); ok && len(u) > 0 {
					onUsage(u)
				}
			}
		}
		w.Write([]byte("data: "))
		w.Write(chunk)
		w.Write([]byte("\n\n"))
		flusher.Flush()
	}
	w.Write([]byte("data: [DONE]\n\n"))
	flusher.Flush()
}

// aggregateProviderSSE 把上游 SSE 流聚合成一个完整的 chat.completion 响应,
// 供客户端以非流式方式请求时使用。
//
// 上游偶尔会返回非 SSE 的普通 JSON(单次 completion 或错误信封), 因此这里
// 记录原始文本; 若一帧都没解析到, 就按普通 JSON 兜底, 避免静默返回空内容。
func aggregateProviderSSE(upstream *http.Response, extract sseExtractor, onUsage func(map[string]any)) (map[string]any, error) {
	reader := bufio.NewReader(upstream.Body)
	out := map[string]any{"object": "chat.completion"}
	var content, reasoning strings.Builder
	var toolCalls []any
	finish := ""
	frames := 0
	var aggErr error
	var raw strings.Builder

	// process 处理一条 SSE 行; 返回 true 表示收到上游结束帧。
	process := func(line string) bool {
		trimmed := strings.TrimRight(line, "\r\n")
		if !strings.HasPrefix(trimmed, "data:") {
			return false
		}
		payload := strings.TrimSpace(trimmed[5:])
		if payload == "" {
			return false
		}
		chunk, ok, done := extract([]byte(payload))
		if done {
			return true
		}
		if !ok || len(chunk) == 0 {
			return false
		}
		var obj map[string]any
		if json.Unmarshal(chunk, &obj) != nil {
			return false
		}
		// 错误帧(合成或上游直出)在此终止聚合, 交给调用方返回 502。
		if e, ok := obj["error"]; ok && e != nil {
			aggErr = fmt.Errorf("upstream error: %s", kit.Truncate(sseErrorText(e), 400))
			return true
		}
		frames++
		if id, ok := obj["id"].(string); ok && id != "" {
			out["id"] = id
		}
		if m, ok := obj["model"].(string); ok && m != "" {
			out["model"] = m
		}
		if c, ok := obj["created"].(float64); ok {
			out["created"] = int64(c)
		}
		if u, ok := obj["usage"].(map[string]any); ok && len(u) > 0 {
			out["usage"] = u
			if onUsage != nil {
				onUsage(u)
			}
		}
		choices, _ := obj["choices"].([]any)
		for _, c := range choices {
			cm, _ := c.(map[string]any)
			if cm == nil {
				continue
			}
			if fr, ok := cm["finish_reason"].(string); ok && fr != "" {
				finish = fr
			}
			d, _ := cm["delta"].(map[string]any)
			if d == nil {
				d, _ = cm["message"].(map[string]any)
			}
			if d == nil {
				continue
			}
			if s, ok := d["content"].(string); ok {
				content.WriteString(s)
			}
			if s, ok := d["reasoning_content"].(string); ok {
				reasoning.WriteString(s)
			}
			if tc, ok := d["tool_calls"].([]any); ok && len(tc) > 0 {
				toolCalls = appendToolCallDeltas(toolCalls, tc)
			}
		}
		return false
	}

	for {
		line, err := reader.ReadString('\n')
		// 关键: 末行可能没有换行符(普通 JSON 响应整个 body 就是一行),
		// 必须在 err != nil 之前先处理, 否则会丢掉整段内容。
		if line != "" {
			raw.WriteString(line)
			if process(line) {
				break
			}
		}
		if err != nil {
			break
		}
	}

	if aggErr != nil {
		return nil, aggErr
	}
	if frames == 0 {
		text := strings.TrimSpace(raw.String())
		if text != "" {
			var plain map[string]any
			if json.Unmarshal([]byte(text), &plain) == nil {
				if e, ok := plain["error"]; ok && e != nil {
					return nil, fmt.Errorf("upstream error: %s", kit.Truncate(sseErrorText(e), 300))
				}
				if _, ok := plain["choices"]; ok {
					if _, has := plain["object"]; !has {
						plain["object"] = "chat.completion"
					}
					return plain, nil
				}
			}
			return nil, fmt.Errorf("upstream returned no stream frames: %s", kit.Truncate(text, 300))
		}
		return nil, fmt.Errorf("upstream returned empty response")
	}

	msg := map[string]any{"role": "assistant", "content": content.String()}
	if reasoning.Len() > 0 {
		msg["reasoning_content"] = reasoning.String()
	}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
	}
	if finish == "" {
		finish = "stop"
	}
	out["choices"] = []any{map[string]any{
		"index":         0,
		"message":       msg,
		"finish_reason": finish,
	}}
	if _, ok := out["created"]; !ok {
		out["created"] = time.Now().Unix()
	}
	return out, nil
}

// appendToolCallDeltas 按 index 合并流式 tool_calls 分片(arguments 拼接)。
func appendToolCallDeltas(dst []any, deltas []any) []any {
	for _, raw := range deltas {
		d, _ := raw.(map[string]any)
		if d == nil {
			continue
		}
		idx := 0
		if v, ok := d["index"].(float64); ok {
			idx = int(v)
		}
		for len(dst) <= idx {
			dst = append(dst, map[string]any{
				"index":    len(dst),
				"id":       "",
				"type":     "function",
				"function": map[string]any{"name": "", "arguments": ""},
			})
		}
		slot, _ := dst[idx].(map[string]any)
		if slot == nil {
			continue
		}
		if id, ok := d["id"].(string); ok && id != "" {
			slot["id"] = id
		}
		if t, ok := d["type"].(string); ok && t != "" {
			slot["type"] = t
		}
		fn, _ := slot["function"].(map[string]any)
		dfn, _ := d["function"].(map[string]any)
		if fn == nil || dfn == nil {
			continue
		}
		if n, ok := dfn["name"].(string); ok && n != "" {
			fn["name"] = n
		}
		if a, ok := dfn["arguments"].(string); ok && a != "" {
			cur, _ := fn["arguments"].(string)
			fn["arguments"] = cur + a
		}
	}
	return dst
}

// --- 具体上游的 extractor ---

// wbSSEExtract WorkBuddy 上游帧本身即 OpenAI chat.completion.chunk。
func wbSSEExtract(payload []byte) ([]byte, bool, bool) {
	if string(payload) == "[DONE]" {
		return nil, false, true
	}
	return payload, true, false
}

// qoderSSEExtract Qoder 帧外层为 {headers, body, statusCodeValue, statusCode},
// body 是内层模型 JSON 字符串(或 "[DONE]")。event:finish 帧无 body → 跳过。
// 内层若是 provider_error 之类的错误信封, 合成 OpenAI 风格的 error 帧下发,
// 否则客户端只能看到"空回答"。
func qoderSSEExtract(payload []byte) ([]byte, bool, bool) {
	var env struct {
		Body            string `json:"body"`
		StatusCode      string `json:"statusCode"`
		StatusCodeValue int    `json:"statusCodeValue"`
	}
	if json.Unmarshal(payload, &env) != nil || env.Body == "" {
		return nil, false, false
	}
	if env.Body == "[DONE]" {
		return nil, false, true
	}
	if env.StatusCodeValue != 0 && env.StatusCodeValue != 200 {
		log.Printf("  qoder frame status %d %s", env.StatusCodeValue, env.StatusCode)
		return synthErrorChunk(env.Body), true, false
	}
	// 内层可能是错误信封(无 choices 但有 code/message)
	if errChunk := innerErrorChunk(env.Body); errChunk != nil {
		return errChunk, true, false
	}
	return []byte(env.Body), true, false
}

// innerErrorChunk 识别内层错误信封(如 {"code":"provider_error","message":...}),
// 命中则返回合成的 error chunk, 否则返回 nil。
//
// 注意: 不能用 `strings.Contains(body, `"error"`)` 之类字面量做前置短路 ——
// 上游的错误码形如 `{"code":"provider_error","message":"boom"}`, 其中
// `provider_error` 的 error 前是下划线而非引号, 字面量匹配不到, 于是这种
// 信封会被静默放过, 变成客户端眼中的"空回答"。唯一可靠的短路是
// "含 choices 即正常帧"。
func innerErrorChunk(body string) []byte {
	if strings.Contains(body, `"choices"`) {
		return nil
	}
	var obj map[string]any
	if json.Unmarshal([]byte(body), &obj) != nil {
		return nil
	}
	if _, ok := obj["error"]; ok {
		return synthErrorChunk(body)
	}
	if codeIsError(obj["code"]) {
		return synthErrorChunk(body)
	}
	return nil
}

// codeIsError 判断上游信封里的 code 是否表示失败。
// 字符串形态("ok"/"0"/"" 视为成功)与数字形态(0 视为成功)都要认,
// 因为同一家上游在不同接口上给的形态并不一致。
func codeIsError(code any) bool {
	switch c := code.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(c)) {
		case "", "ok", "0", "success":
			return false
		}
		return true
	case float64:
		return c != 0
	}
	return false
}

// synthErrorChunk 把上游错误体压成 OpenAI 流式错误帧 `{"error":{...}}`。
func synthErrorChunk(body string) []byte {
	msg := body
	var obj map[string]any
	if json.Unmarshal([]byte(body), &obj) == nil {
		if m, ok := obj["message"].(string); ok && m != "" {
			msg = m
			if d, ok := obj["details"].(string); ok && d != "" {
				msg += " | " + d
			}
		}
	}
	out, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"message": kit.Truncate(msg, 500),
			"type":    "api_error",
		},
	})
	return out
}

// chatStreamToResponsesExtract 把 provider 上游的 chat.completions SSE 流转换为
// Responses SSE 流。与 responses.go 的 chatStreamToResponses 只有一点不同:
// 每条 `data:` 负载先过 sseExtractor(qoder 是双层信封, wb 要识别 "[DONE]"),
// 解不出 OpenAI chunk 的帧(心跳/控制行)跳过, 不进转换。
func chatStreamToResponsesExtract(w http.ResponseWriter, upstream *http.Response, onUsage func(map[string]any), extract sseExtractor) {
	s := newResponsesSSE(w)
	s.event("response.created", map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id":         s.respID,
			"object":     "response",
			"created_at": time.Now().Unix(),
			"status":     "in_progress",
			"model":      "",
			"output":     []any{},
		},
	})
	s.event("response.in_progress", map[string]any{"type": "response.in_progress", "response": map[string]any{"id": s.respID}})

	textEmitted := false
	callEmitted := false
	upstreamErr := ""
	var curCallID, curCallName string
	var curArgs strings.Builder
	var outText strings.Builder

	reader := bufio.NewReader(upstream.Body)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			line = strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(line, "data:") {
				payload := strings.TrimSpace(line[5:])
				if payload == "" || payload == "[DONE]" {
					continue
				}
				// provider 差异吸收层: 双层信封解包 / 结束帧识别
				chunk, ok, done := extract([]byte(payload))
				if done {
					break
				}
				if !ok || len(chunk) == 0 {
					continue
				}
				var obj map[string]any
				if json.Unmarshal(chunk, &obj) != nil {
					continue
				}
				if e, ok := obj["error"]; ok && e != nil {
					upstreamErr = kit.Truncate(fmt.Sprint(e), 400)
					log.Printf("  provider responses stream upstream error: %s", upstreamErr)
					break
				}
				if m, ok := obj["model"].(string); ok && m != "" {
					// 记录上游回的 model, 收尾事件里带回给客户端
					s.event("response.in_progress", map[string]any{"type": "response.in_progress", "response": map[string]any{"id": s.respID, "model": m}})
				}
				if onUsage != nil {
					if u, ok := obj["usage"].(map[string]any); ok && len(u) > 0 {
						onUsage(u)
					}
				}
				choices, _ := obj["choices"].([]any)
				if len(choices) == 0 {
					continue
				}
				ch, _ := choices[0].(map[string]any)
				if ch == nil {
					continue
				}
				delta, _ := ch["delta"].(map[string]any)
				if delta == nil {
					delta = ch
				}
				// 文本
				if c, ok := delta["content"].(string); ok && c != "" {
					if !textEmitted {
						textEmitted = true
						s.event("response.output_item.added", map[string]any{
							"type":         "response.output_item.added",
							"output_index": 0,
							"item":         map[string]any{"id": s.msgID, "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}},
						})
						s.event("response.content_part.added", map[string]any{
							"type":          "response.content_part.added",
							"item_id":       s.msgID,
							"output_index":  0,
							"content_index": 0,
							"part":          map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
						})
					}
					outText.WriteString(c)
					s.event("response.output_text.delta", map[string]any{
						"type":          "response.output_text.delta",
						"item_id":       s.msgID,
						"output_index":  0,
						"content_index": 0,
						"delta":         c,
					})
				}
				// 推理
				if r, ok := delta["reasoning_content"].(string); ok && r != "" {
					s.event("response.reasoning_summary_text.delta", map[string]any{
						"type":         "response.reasoning_summary_text.delta",
						"item_id":      s.msgID,
						"output_index": 0,
						"delta":        r,
					})
				}
				// 工具调用
				if tc, ok := delta["tool_calls"].([]any); ok {
					for _, c := range tc {
						cm, ok := c.(map[string]any)
						if !ok {
							continue
						}
						if id, ok := cm["id"].(string); ok && id != "" {
							curCallID = id
						}
						fn, _ := cm["function"].(map[string]any)
						if fn != nil {
							if n, ok := fn["name"].(string); ok && n != "" {
								curCallName = n
							}
							if a, ok := fn["arguments"].(string); ok && a != "" {
								curArgs.WriteString(a)
							}
						}
						if !callEmitted && curCallName != "" {
							callEmitted = true
							s.event("response.output_item.added", map[string]any{
								"type":         "response.output_item.added",
								"output_index": 1,
								"item": map[string]any{
									"type":      "function_call",
									"id":        "fc_" + curCallName,
									"call_id":   curCallID,
									"name":      curCallName,
									"arguments": "",
									"status":    "in_progress",
								},
							})
						}
					}
				}
			}
		}
		if err != nil {
			break
		}
	}

	// 收尾
	if textEmitted {
		s.event("response.output_text.done", map[string]any{"type": "response.output_text.done", "item_id": s.msgID, "output_index": 0, "content_index": 0, "text": outText.String()})
		s.event("response.content_part.done", map[string]any{"type": "response.content_part.done", "item_id": s.msgID, "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": outText.String(), "annotations": []any{}}})
		s.event("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"id": s.msgID, "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": outText.String(), "annotations": []any{}}}}})
	}
	if callEmitted {
		s.event("response.function_call_arguments.done", map[string]any{"type": "response.function_call_arguments.done", "item_id": "fc_" + curCallName, "output_index": 1, "arguments": curArgs.String()})
		s.event("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": 1, "item": map[string]any{"type": "function_call", "id": "fc_" + curCallName, "call_id": curCallID, "name": curCallName, "arguments": curArgs.String(), "status": "completed"}})
	}
	if upstreamErr != "" {
		// 上游在流中途报错(角色不合法 / 配额不足 / 内容审核等)。此前这里直接
		// break 再发 response.completed, 客户端拿到的是"成功的空回答", 真实原因
		// 只留在网关日志里 —— 排查成本极高。改为按协议发 response.failed。
		s.event("response.failed", map[string]any{
			"type": "response.failed",
			"response": map[string]any{
				"id":         s.respID,
				"object":     "response",
				"created_at": time.Now().Unix(),
				"status":     "failed",
				"model":      "",
				"output":     []any{},
				"error":      map[string]any{"code": "upstream_error", "message": upstreamErr},
			},
		})
		return
	}
	s.event("response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":         s.respID,
			"object":     "response",
			"created_at": time.Now().Unix(),
			"status":     "completed",
			"model":      "",
			"output":     []any{},
			"usage":      map[string]any{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0},
		},
	})
}
