package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// llm_utils_chat 请求体
//
// 这里固定住四个"改错了就整条链路 400"的约定:
//  1. 必须带 config_name, 且是 wire id(不是展示 id —— 拿展示 id 当
//     config_name 发, 上游按"这个 function 不认识的名字"回 4001)
//  2. 必须带 function, 且必须是能聊天的那个(花名册分组里的 solo_agent /
//     builder_v3 只答目录、拒绝聊天)
//  3. content 必须是数组(否则上游 Go 反序列化直接报
//     "cannot unmarshal string into ... []*LLMRawMessageContent")
//  4. 干净的字段只有 messages / tools / reasoning_effort —— temperature、
//     max_tokens 之类一律会被上游校验拒掉
//
// 第 1 条这里曾经是反的("禁带 config_name"), 依据是"带上就 4001"。那次实测
// 是把展示 id 当 config_name 发; 真正的修法是填 wire id, 见 traeBuildBody。
// ---------------------------------------------------------------------------

func TestTraeBuildBodySendsWireConfigName(t *testing.T) {
	m := traeModelInfo{Key: "glm-5", Function: "solo_coder", WireFunction: "solo_coder"}
	body := traeBuildBody(map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}, m)

	// 展示 id 就是 wire id 时, 两个字段都填展示 id
	if body["config_name"] != "glm-5" {
		t.Fatalf("config_name 应为 glm-5, 得到 %v", body["config_name"])
	}
	if body["model"] != "glm-5" {
		t.Fatalf("model 应为 glm-5, 得到 %v", body["model"])
	}
	if body["function"] != "solo_coder" {
		t.Fatalf("function 应取自模型所属目录函数, 得到 %v", body["function"])
	}
	if body["stream"] != true {
		t.Fatalf("stream 应为 true, 得到 %v", body["stream"])
	}
}

// 展示 id ≠ wire id 时, 两个字段都必须换成 wire id —— 只换 model 不换
// config_name 会拿到 4001, 反之亦然。
func TestTraeBuildBodyUsesWireIDWhenDifferent(t *testing.T) {
	m := traeModelInfo{
		Key: "DeepSeek-V4-Flash", WireID: "DeepSeek-V4-Flash-Official",
		Function: "solo_agent", WireFunction: "solo_coder",
	}
	body := traeBuildBody(map[string]any{}, m)
	if body["model"] != "DeepSeek-V4-Flash-Official" {
		t.Fatalf("model 应是 wire id, 得到 %v", body["model"])
	}
	if body["config_name"] != "DeepSeek-V4-Flash-Official" {
		t.Fatalf("config_name 应是 wire id, 得到 %v", body["config_name"])
	}
}

// 花名册分组名里有 solo_agent / builder_v3: 它们能答目录却拒绝聊天。
// 没有 wire 行时不能直接拿它去发聊天, 必须退到默认聊天函数。
func TestTraeBuildBodySkipsNonChatFunction(t *testing.T) {
	cases := map[string]string{
		"solo_agent":     traeSoloFunction,
		"builder_v3":     traeSoloFunction,
		"solo_coder":     "solo_coder",
		"chat_v3":        "chat_v3",
		"solo_work_lite": traeSoloFunction,
	}
	for fn, want := range cases {
		body := traeBuildBody(map[string]any{}, traeModelInfo{Key: "glm-5", Function: fn})
		if body["function"] != want {
			t.Fatalf("Function=%s 时 function 应为 %s, 得到 %v", fn, want, body["function"])
		}
	}
	// wire 给的函数优先于 remote 的分组名
	body := traeBuildBody(map[string]any{}, traeModelInfo{
		Key: "glm-5", Function: "solo_coder", WireFunction: "solo_work_remote",
	})
	if body["function"] != "solo_work_remote" {
		t.Fatalf("wire 的 function 应优先, 得到 %v", body["function"])
	}
}

func TestTraeBuildBodyClientOverrideFunction(t *testing.T) {
	m := traeModelInfo{Key: "glm-5", Function: "solo_coder"}
	body := traeBuildBody(map[string]any{"function": "chat_v3"}, m)
	if body["function"] != "chat_v3" {
		t.Fatalf("客户端显式指定 function 时应优先, 得到 %v", body["function"])
	}
}

func TestTraeBuildBodyNormalizesMessageContent(t *testing.T) {
	m := traeModelInfo{Key: "glm-5", Function: "chat_v3"}
	in := []any{
		map[string]any{"role": "developer", "content": "env"},
		map[string]any{"role": "user", "content": "hello"},
	}
	body := traeBuildBody(map[string]any{"messages": in}, m)
	msgs, ok := body["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("messages 应为 2 条数组, 得到 %v", body["messages"])
	}

	first := msgs[0].(map[string]any)
	if first["role"] != "system" {
		t.Fatalf("developer 应折叠为 system, 得到 %v", first["role"])
	}
	// content 必须是 [{type,text}] 数组, 字符串会被上游 Go 结构体拒绝
	cc, ok := first["content"].([]any)
	if !ok || len(cc) != 1 {
		t.Fatalf("content 应为单元素数组, 得到 %#v", first["content"])
	}
	part := cc[0].(map[string]any)
	if part["type"] != "text" || part["text"] != "env" {
		t.Fatalf("content 元素形态不对: %#v", part)
	}
	// 不能就地改动调用方的 params: 重试路径会复用同一份
	if raw, _ := in[0].(map[string]any)["content"].(string); raw != "env" {
		t.Fatalf("入参被就地修改: %v", in[0])
	}
}

func TestTraeBuildBodyDoesNotStripTools(t *testing.T) {
	m := traeModelInfo{Key: "glm-5", Function: "chat_v3"}
	body := traeBuildBody(map[string]any{
		"tools": []any{map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":       "get_weather",
				"parameters": map[string]any{"type": "object"},
			},
		}},
	}, m)
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools 应保留, 得到 %v", body["tools"])
	}
	// 上游要求 parameters 是 JSON 字符串而不是对象
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if _, isStr := fn["parameters"].(string); !isStr {
		t.Fatalf("parameters 应序列化为字符串, 得到 %#v", fn["parameters"])
	}
}

// ---------------------------------------------------------------------------
// SSE 帧转换
// ---------------------------------------------------------------------------

func decodeChunk(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("chunk 不是合法 JSON: %v (%s)", err, raw)
	}
	return obj
}

func chunkText(t *testing.T, chunk map[string]any) string {
	t.Helper()
	choices, _ := chunk["choices"].([]any)
	if len(choices) == 0 {
		return ""
	}
	delta, _ := choices[0].(map[string]any)["delta"].(map[string]any)
	if delta == nil {
		return ""
	}
	s, _ := delta["content"].(string)
	return s
}

func TestTraeSSEExtractOutputFrames(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		wantOK  bool
		want    string
	}{
		{
			name:    "content 增量",
			payload: `{"response":"你","reasoning_content":null,"tool_calls":null,"phase":null}`,
			wantOK:  true,
			want:    "你",
		},
		{
			name:    "元数据帧跳过",
			payload: `{"model":"","session_id":"x","prompt_completion_id":0}`,
			wantOK:  false,
		},
		{
			name:    "耗时统计跳过",
			payload: `{"name":"llm_raw_chat_v2","server_processing_time":108}`,
			wantOK:  false,
		},
		{
			name:    "裸字符串心跳",
			payload: `"Processing_1792387505"`,
			wantOK:  false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, ok, done := traeSSEExtract([]byte(c.payload))
			if done {
				t.Fatalf("普通帧不应标记结束")
			}
			if ok != c.wantOK {
				t.Fatalf("ok=%v 期望 %v (%s)", ok, c.wantOK, raw)
			}
			if !c.wantOK {
				if len(raw) != 0 {
					t.Fatalf("跳过的帧不应产出 chunk: %s", raw)
				}
				return
			}
			if got := chunkText(t, decodeChunk(t, raw)); got != c.want {
				t.Fatalf("content=%q 期望 %q", got, c.want)
			}
		})
	}
}

func TestTraeSSEExtractReasoningDelta(t *testing.T) {
	raw, ok, done := traeSSEExtract([]byte(`{"response":"","reasoning_content":"推理中"}`))
	if done || !ok {
		t.Fatalf("推理帧应产出 chunk: ok=%v done=%v", ok, done)
	}
	chunk := decodeChunk(t, raw)
	delta := chunk["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)
	if delta["reasoning_content"] != "推理中" {
		t.Fatalf("推理增量丢失: %#v", delta)
	}
	if _, has := delta["content"]; has {
		t.Fatalf("推理帧不应同时带 content: %#v", delta)
	}
}

func TestTraeSSEExtractDoneAndError(t *testing.T) {
	// 结束帧要把 finish_reason 交给客户端, 不能直接当成 done 丢掉
	// (done=true 时 relayProviderSSE 只写 "[DONE]" 并返回, 这一帧就没了)。
	// 上游读到 EOF 时会自动补 "[DONE]"。
	raw, ok, done := traeSSEExtract([]byte(`{"finish_reason":"stop"}`))
	if !ok || done {
		t.Fatalf("finish_reason 应产出结束 chunk: ok=%v done=%v", ok, done)
	}
	chunk := decodeChunk(t, raw)
	fr := chunk["choices"].([]any)[0].(map[string]any)["finish_reason"]
	if fr != "stop" {
		t.Fatalf("finish_reason 应透传, 得到 %v", fr)
	}

	// 错误帧必须变成可读的 error 信封, 否则客户端只看到"空回答"
	raw, ok, done = traeSSEExtract([]byte(`{"code":2001,"message":"function is empty, cannot resolve model by usage=","extra":null}`))
	if !ok || done {
		t.Fatalf("错误帧应产出 chunk: ok=%v done=%v", ok, done)
	}
	chunk = decodeChunk(t, raw)
	e, _ := chunk["error"].(map[string]any)
	if e == nil {
		t.Fatalf("错误帧应带 error 信封: %s", raw)
	}
	if msg, _ := e["message"].(string); !strings.Contains(msg, "function is empty") {
		t.Fatalf("错误信息应保留上游原文: %v", msg)
	}
	if kind, _ := e["type"].(string); kind != "invalid_request_error" {
		t.Fatalf("业务码 2001 是请求侧问题, 不该标成上游故障: %v", kind)
	}
}

func TestTraeErrorType(t *testing.T) {
	cases := []struct {
		code any
		want string
	}{
		{401, "auth_error"},
		{403, "auth_error"},
		{402, "api_error"},
		{404, "invalid_request_error"},
		{429, "upstream_error"},
		{500, "upstream_error"},
		{2001, "invalid_request_error"},
		{4001, "invalid_request_error"},
		{"4001", "invalid_request_error"},
		{"abc", "upstream_error"},
		{nil, "upstream_error"},
	}
	for _, c := range cases {
		if got := traeErrorType(c.code); got != c.want {
			t.Fatalf("traeErrorType(%v)=%q 期望 %q", c.code, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// 官方模型目录
//
// get_detail_param 会把用户自配的自定义模型一并列出(其中几个 base_url 指回
// 本服务自己), 所以花名册必须来自 /api/remote/v1/models, get_detail_param
// 只用来补倍率。
// ---------------------------------------------------------------------------

func TestTraeFetchRemoteModels(t *testing.T) {
	var gotPath, gotRawQuery, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotRawQuery, gotMethod = r.URL.Path, r.URL.RawQuery, r.Method
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{"list": []any{
				map[string]any{
					"function": "solo_coder",
					"models": []any{
						map[string]any{
							"name":                  "glm-5",
							"display_name":          "GLM-5",
							"context_window_tokens": map[string]any{"dev": 200000, "max": 200000},
							"features":              `{"consumption_rate":{"enable":true,"data":{"rate":0.83}}}`,
							"reasoning_effort_config": map[string]any{
								"support_thinking": true,
							},
							"is_default": true,
						},
						map[string]any{
							"name":          "free-model",
							"features":      `{"consumption_rate":{"enable":false}}`,
							"config_source": 1,
						},
					},
				},
			}},
		})
	}))
	defer srv.Close()

	// header 里要带 token, 避免账号为空时 panic
	got, err := traeFetchRemoteModels(&traeAccount{AccessToken: "t"}, srv.URL)
	if err != nil {
		t.Fatalf("拉取官方目录失败: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("应为 GET, 得到 %s", gotMethod)
	}
	if gotPath != "/api/remote/v1/models" {
		t.Fatalf("路径不对: %s", gotPath)
	}
	// 2026-10-19 实测: 不带 ?functions= 上游只回 solo_coder 一个分组(12 条),
	// 带上并集才拿到完整花名册(7 个分组, 去重后 32 个模型)。少了这个参数
	// 花名册会悄悄缩到 1/3, 所以这里显式锁住。
	if !strings.HasPrefix(gotRawQuery, "functions=") {
		t.Fatalf("必须带 ?functions= 并集参数, 得到 query %q", gotRawQuery)
	}
	for _, fn := range traeRemoteDirectoryFunctions {
		if !strings.Contains(gotRawQuery, fn) {
			t.Fatalf("query %q 缺分组 %q", gotRawQuery, fn)
		}
	}
	if len(got) != 2 {
		t.Fatalf("应解出 2 个模型, 得到 %d", len(got))
	}

	byKey := map[string]traeModelInfo{}
	for _, m := range got {
		byKey[m.Key] = m
	}
	glm := byKey["glm-5"]
	if glm.Function != "solo_coder" {
		t.Fatalf("function 应来自分组, 得到 %q", glm.Function)
	}
	if glm.DisplayName != "GLM-5" || glm.Context != 200000 || glm.IsDefault != true {
		t.Fatalf("基础字段不对: %+v", glm)
	}
	if !glm.Priced || glm.Multiplier != 0.83 {
		t.Fatalf("features 里的 rate 应解析为倍率: %+v", glm)
	}
	if !glm.Reasoning {
		t.Fatalf("support_thinking 应映射到 Reasoning: %+v", glm)
	}

	// enable:false 表示不计费 —— 但要标成"未标价", 不能当成免费(0)暴露
	free := byKey["free-model"]
	if free.Priced {
		t.Fatalf("consumption_rate.enable=false 应为未标价: %+v", free)
	}
}

func TestTraeSyncModelsSkipsCustomRows(t *testing.T) {
	// 官方目录只有 glm-5; get_detail_param 同时给出一个自定义模型(指回本服务)
	// 和 glm-5 的打折倍率。最终只应保留 glm-5, 且倍率取 wire 的 0.06。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/remote/v1/models" {
			json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{"list": []any{
					map[string]any{
						"function": "solo_coder",
						"models":   []any{map[string]any{"name": "glm-5", "display_name": "GLM-5"}},
					},
				}},
			})
			return
		}
		// get_detail_param
		json.NewEncoder(w).Encode(map[string]any{
			"config_info_list": []any{
				map[string]any{
					"config_name":    "self-loop",
					"config_source":  3,
					"display_config": map[string]any{"is_custom_model": true},
					"model_detail_list": []any{map[string]any{
						"custom_config": map[string]any{"base_url": "http://127.0.0.1:9999/v1/chat/completions"},
					}},
				},
				map[string]any{
					"config_name":            "glm-5",
					"config_source":          1,
					"display_contact_config": `{"consumption_rate":{"enable":true,"data":{"rate":0.06}}}`,
				},
			},
		})
	}))
	defer srv.Close()

	orig := traeRegionGateways["cn"]
	traeRegionGateways["cn"] = struct {
		remote string
		chat   string
		pay    string
		origin string
	}{srv.URL, srv.URL, srv.URL, "https://www.trae.cn"}
	defer func() { traeRegionGateways["cn"] = orig }()

	acc := &traeAccount{ID: "trae_test", Region: "cn", AccessToken: "t", UserID: "1"}
	traeAccMu.Lock()
	old := traeAccounts
	traeAccounts = []*traeAccount{acc}
	traeAccMu.Unlock()
	defer func() {
		traeAccMu.Lock()
		traeAccounts = old
		traeAccMu.Unlock()
	}()

	n, err := traeSyncModels()
	if err != nil {
		t.Fatalf("同步失败: %v", err)
	}
	if n != 1 {
		t.Fatalf("只应保留官方花名册里的 1 个模型, 得到 %d", n)
	}
	m, ok := traeResolveModel("glm-5")
	if !ok {
		t.Fatalf("glm-5 未找到: %v", traeModelSnapshot())
	}
	if m.Function != "solo_coder" {
		t.Fatalf("function 应保留官方目录的归属, 得到 %q", m.Function)
	}
	if m.Multiplier != 0.06 || !m.Priced {
		t.Fatalf("倍率应取 get_detail_param 的 wire 值(打折后): %+v", m)
	}
}
