package app

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestIsProviderRoute 三个协议入口(chat / anthropic / responses)共用同一路由判定。
// 少判一个分支的后果不是报错而是静默改道: 请求被交给 cline 账号池,
// 用 cline 的额度去打另一个上游, 客户端看不出任何异常。
func TestIsProviderRoute(t *testing.T) {
	yes := []string{wbVariantCN.ID, wbVariantINTL.ID, "qoder"}
	for _, r := range yes {
		if !isProviderRoute(r) {
			t.Errorf("isProviderRoute(%q) = false, 期望 true", r)
		}
	}
	no := []string{"", "zen", "cline", "reject", "workbuddy-cn", "WorkBuddy", "QODER", "openai"}
	for _, r := range no {
		if isProviderRoute(r) {
			t.Errorf("isProviderRoute(%q) = true, 期望 false", r)
		}
	}
}

// TestProviderStripPrefix 前缀解析: 显式前缀必须剥掉, 裸 id 原样透传。
// 返回带前缀的模型名会直接打到上游 404/400。
func TestProviderStripPrefix(t *testing.T) {
	cases := map[string]string{
		"workbuddy/hy3":       "hy3",
		"wb/hy3":              "hy3",
		"workbuddy-ai/hy3":    "hy3",
		"wb-ai/hy3":           "hy3",
		"workbuddyai/hy3":     "hy3",
		"qoder/qmodel":        "qmodel",
		"qd/auto":             "auto",
		"hy3":                 "hy3",
		"glm-5.3":             "glm-5.3",
		"deepseek/v4":         "deepseek/v4", // 非 provider 前缀, 不动
		"openai/gpt-4o":       "openai/gpt-4o",
		"/hy3":                "/hy3", // 空前缀不合法, 原样返回
		"":                    "",
		"workbuddy/hy3/extra": "hy3/extra", // 只剥第一段
		"workbuddy-ai":        "workbuddy-ai",
		"qoder":               "qoder",
	}
	for in, want := range cases {
		if got := providerStripPrefix(in); got != want {
			t.Errorf("providerStripPrefix(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

// TestProviderChatErrorCarriesStatus 上游错误必须带出正确 HTTP 状态:
// 未知模型 400、上游关闭 503。若退化成统一 502, 客户端无法区分
// "我把模型名写错了"(可自纠) 与 "上游临时故障"(可重试)。
func TestProviderChatErrorCarriesStatus(t *testing.T) {
	e := providerErr(400, "invalid_request_error", "model %q bad", "x")
	if e.Status != 400 || e.Type != "invalid_request_error" {
		t.Fatalf("providerErr 状态/类型错误: %+v", e)
	}
	if e.Error() != `model "x" bad` {
		t.Fatalf("providerErr 消息错误: %q", e.Error())
	}
	// 同样数量参数不匹配时必须不 panic(格式化参数交给 fmt)
	e2 := providerErr(503, "api_error", "no args")
	if e2.Error() != "no args" {
		t.Fatalf("providerErr 消息错误: %q", e2.Error())
	}
}

// TestSynthErrorChunkIsValidOpenAIError 合成的错误帧必须能被客户端按
// OpenAI 错误结构解析, 否则前端只能显示"空回答"。
func TestSynthErrorChunkIsValidOpenAIError(t *testing.T) {
	raw := synthErrorChunk(`{"message":"boom","details":"extra detail"}`)
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("合成错误帧不是合法 JSON: %v (%s)", err, raw)
	}
	e, ok := obj["error"].(map[string]any)
	if !ok {
		t.Fatalf("合成错误帧缺少 error 字段: %s", raw)
	}
	msg, _ := e["message"].(string)
	if !strings.Contains(msg, "boom") {
		t.Errorf("错误帧未带上游 message: %q", msg)
	}
	if e["type"] != "api_error" {
		t.Errorf("错误帧 type = %v, 期望 api_error", e["type"])
	}
	// 非 JSON 原文也不能崩, 且要有可读消息
	raw2 := synthErrorChunk("plain text failure")
	var obj2 map[string]any
	if err := json.Unmarshal(raw2, &obj2); err != nil {
		t.Fatalf("非 JSON 错误体应被包成合法帧: %v (%s)", err, raw2)
	}
	if msg2, _ := obj2["error"].(map[string]any)["message"].(string); !strings.Contains(msg2, "plain text failure") {
		t.Errorf("非 JSON 错误体丢失原文: %q", msg2)
	}
}

// TestInnerErrorChunk 内层信封识别: 正常 completion(含 choices) 不能误判成错误,
// 否则每次成功回答都会变成 502。
func TestInnerErrorChunk(t *testing.T) {
	normal := `{"id":"x","choices":[{"delta":{"content":"hi"}}]}`
	if got := innerErrorChunk(normal); got != nil {
		t.Errorf("正常 completion 被误判为错误: %s", got)
	}
	withErr := `{"error":{"message":"quota exceeded"}}`
	if got := innerErrorChunk(withErr); got == nil {
		t.Error("含 error 的信封未被识别")
	}
	withCode := `{"code":"provider_error","message":"boom"}`
	if got := innerErrorChunk(withCode); got == nil {
		t.Error("含非 ok code 的信封未被识别")
	}
	// 上游的 code 形态不统一: 数字 0/非 0、字符串 success/OK(大小写)都出现过
	if got := innerErrorChunk(`{"code":500,"message":"boom"}`); got == nil {
		t.Error("数字形态错误码未被识别")
	}
	for _, body := range []string{
		`{"code":"ok","choices":[]}`,
		`{"code":"0"}`,
		`{"code":"success"}`,
		`{"code":"OK"}`,
		`{"code":0}`,
		`{}`,
	} {
		if got := innerErrorChunk(body); got != nil {
			t.Errorf("innerErrorChunk(%s) 误报: %s", body, got)
		}
	}
}

// TestWBSSEExtract OpenAI 直出帧: 原样透传, [DONE] 作为结束标记。
func TestWBSSEExtract(t *testing.T) {
	payload := []byte(`{"choices":[{"delta":{"content":"a"}}]}`)
	chunk, ok, done := wbSSEExtract(payload)
	if !ok || done {
		t.Fatalf("正常帧处置错误: ok=%v done=%v", ok, done)
	}
	if string(chunk) != string(payload) {
		t.Errorf("正常帧应原样透传, 得到 %s", chunk)
	}
	if _, ok, done := wbSSEExtract([]byte("[DONE]")); !done || ok {
		t.Error("[DONE] 应被识别为结束帧")
	}
}

// TestQoderSSEExtract 双层信封: body 是内层 JSON 字符串, 心跳帧跳过,
// 非 200 帧合成错误 chunk 而不是丢弃(丢弃会让客户端看到空回答)。
func TestQoderSSEExtract(t *testing.T) {
	inner := `{"id":"1","choices":[{"delta":{"content":"hi"}}]}`
	env, _ := json.Marshal(map[string]any{
		"headers":         map[string]any{},
		"body":            inner,
		"statusCode":      "OK",
		"statusCodeValue": 200,
	})
	chunk, ok, done := qoderSSEExtract(env)
	if !ok || done {
		t.Fatalf("正常信封处置错误: ok=%v done=%v", ok, done)
	}
	if string(chunk) != inner {
		t.Errorf("应拆出内层 body, 得到 %s", chunk)
	}

	// 无 body 的 event:finish 心跳 -> 跳过
	if _, ok, done := qoderSSEExtract([]byte(`{"headers":{},"body":""}`)); ok || done {
		t.Error("无 body 的心跳帧应被跳过")
	}

	// body 为 [DONE] -> 结束
	doneEnv, _ := json.Marshal(map[string]any{"body": "[DONE]"})
	if _, ok, done := qoderSSEExtract(doneEnv); !done || ok {
		t.Error("body=[DONE] 应被识别为结束帧")
	}

	// 非 200 帧: 合成 error chunk
	badEnv, _ := json.Marshal(map[string]any{
		"body":            `{"code":"provider_error","message":"boom"}`,
		"statusCode":      "BAD_REQUEST",
		"statusCodeValue": 400,
	})
	chunk, ok, done = qoderSSEExtract(badEnv)
	if !ok || done {
		t.Fatalf("错误信封应产出错误帧: ok=%v done=%v", ok, done)
	}
	var obj map[string]any
	if err := json.Unmarshal(chunk, &obj); err != nil {
		t.Fatalf("错误帧不是合法 JSON: %v", err)
	}
	if _, has := obj["error"]; !has {
		t.Errorf("非 200 帧未合成 error: %s", chunk)
	}

	// 非 JSON payload -> 跳过, 不能 panic
	if _, ok, done := qoderSSEExtract([]byte("not json")); ok || done {
		t.Error("非法 payload 应被跳过")
	}
}

// TestProviderIsAliasModel 占位别名模型(auto/default/default-model)必须从
// 三家目录同步时排除: 它们路由不可预期且与 cline/zen 目录撞名。
func TestProviderIsAliasModel(t *testing.T) {
	for _, id := range []string{"auto", "default", "default-model", "AUTO", "Default", " default ", "DEFAULT-MODEL"} {
		if !providerIsAliasModel(id) {
			t.Errorf("providerIsAliasModel(%q) = false, 期望 true", id)
		}
	}
	for _, id := range []string{"", "hy3", "qmodel_38max", "dfmodel", "auto-reply", "defaultmodel", "glm-5.3"} {
		if providerIsAliasModel(id) {
			t.Errorf("providerIsAliasModel(%q) = true, 期望 false", id)
		}
	}
}

// TestProviderDecorateID 展示装饰: id 前加 owned_by 分组前缀, id 后附加
// 计费段(付费 " · x<倍率>" / 免费 " · free")。/v1/models 的分组与计费信息
// 全靠它, 每个 id 末尾都必须给出确定的计费段。
func TestProviderDecorateID(t *testing.T) {
	cases := []struct {
		group, id, name string
		mult            float64
		free            bool
		want            string
	}{
		{"qoder", "dfmodel", "DeepSeek-V4-Pro", 0.5, false, "qoder/DeepSeek-V4-Pro · x0.5"},
		{"qoder", "qfmodel", "Qwen3.8-Flash", 0, true, "qoder/Qwen3.8-Flash · free"},
		{"workbuddy", "hy3", "", 0.29, false, "workbuddy/hy3 · x0.29"}, // 无显示名: 退回路由 id 作首段
		{"workbuddy", "hy3", "Hunyuan Turbo", 2, false, "workbuddy/Hunyuan-Turbo · x2"},
		{"workbuddy", "hy3", "Hunyuan 3", 1.5, false, "workbuddy/Hunyuan-3 · x1.5"},
		{"workbuddy", "hy3", "Hy3", 0, true, "workbuddy/Hy3 · free"},
		{"workbuddy", "hunyuan-turbos", "Hunyuan-Turbos", 0, false, "workbuddy/Hunyuan-Turbos"}, // 未标价: 不加段
		{"", "hy3", "", 0.29, false, "hy3 · x0.29"},                                             // 无分组: 兼容旧格式
		{"", "hy3", "", 0, false, "hy3"},
		{"workbuddy", "hy3", "  ", 0, false, "workbuddy/hy3"}, // 全空显示名: 原样
	}
	for _, c := range cases {
		if got := providerDecorateID(c.group, c.id, c.name, c.mult, c.free); got != c.want {
			t.Errorf("providerDecorateID(%q,%q,%q,%v,%v) = %q, 期望 %q",
				c.group, c.id, c.name, c.mult, c.free, got, c.want)
		}
	}
}

// TestProviderFreeIDAndNormalize 整池免费来源的计费段与展示 id 归一:
// /v1/models 里每个 id 末尾要么 "x<倍率>" 要么 "free"; 客户端原样发回时
// 必须还原成真实模型 id 才能命中目录。
func TestProviderFreeIDAndNormalize(t *testing.T) {
	if got := providerFreeID("cline-free/mimo-v2.6-flash"); got != "cline-free/mimo-v2.6-flash · free" {
		t.Errorf("providerFreeID = %q", got)
	}
	cases := map[string]string{
		// zen: 分组前缀是纯展示层, 真实 id 是裸名
		"opencode-zen/big-pickle · free":            "big-pickle",
		"opencode-zen/nemotron-3-ultra-free · free": "nemotron-3-ultra-free",
		// cline: "cline-free/" 是真实 id 的一部分, 不能剥
		"cline-free/mimo-v2.6-flash · free": "cline-free/mimo-v2.6-flash",
		// 旧配置的干净 id 原样
		"big-pickle":                 "big-pickle",
		"cline-free/mimo-v2.6-flash": "cline-free/mimo-v2.6-flash",
		"opencode/big-pickle":        "opencode/big-pickle",
		"workbuddy/Hy3 · free":       "workbuddy/Hy3", // provider 前缀由 providerStripPrefix 处理
		"":                           "",
	}
	for in, want := range cases {
		if got := providerNormalizeDisplayID(in); got != want {
			t.Errorf("providerNormalizeDisplayID(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

// TestProviderStripDecorate 路由入口剥离: 按第一个 " · " 截断。
// 客户端从 /v1/models 原样选来的 id 与旧配置的干净 id 必须落到同一上游模型。
func TestProviderStripDecorate(t *testing.T) {
	cases := map[string]string{
		"dfmodel · DeepSeek-V4-Pro":         "dfmodel",
		"hy3 · Hunyuan-Turbo · x2":          "hy3",
		"hy3":                               "hy3", // 无装饰: 原样(向后兼容)
		"qoder/dfmodel · DeepSeek-V4-Pro":   "qoder/dfmodel",
		"workbuddy/hy3 · x0.29":             "workbuddy/hy3",
		"workbuddy/Hy3 · free":              "workbuddy/Hy3",
		"cline-free/mimo-v2.6-flash · free": "cline-free/mimo-v2.6-flash",
		"opencode-zen/big-pickle · free":    "opencode-zen/big-pickle",
		"glm-5.3-flash-free":                "glm-5.3-flash-free",
		"":                                  "",
	}
	for in, want := range cases {
		if got := providerStripDecorate(in); got != want {
			t.Errorf("providerStripDecorate(%q) = %q, 期望 %q", in, got, want)
		}
	}
	// 往返一致: 显示名首段剥掉后是显示名本身(解析靠显示名兜底), 路由 id 首段剥掉后是路由 id
	if got := providerStripDecorate(providerDecorateID("qoder", "dfmodel", "DeepSeek-V4-Pro", 0.5, false)); got != "qoder/DeepSeek-V4-Pro" {
		t.Errorf("装饰往返不一致: %q", got)
	}
	if got := providerStripDecorate(providerDecorateID("workbuddy", "hy3", "", 0.29, false)); got != "workbuddy/hy3" {
		t.Errorf("无显示名装饰往返不一致: %q", got)
	}
}

// TestRouteModelAny 空 model 请求的默认模型兜底必须认识第三方 provider。
// 回归: routeModel 只返回 zen/cline/reject, 把 workbuddy/hy3 交给它会得到
// "cline" —— 请求带着 provider 默认模型进 cline 账号池, 用 cline 的额度请求一个
// 不存在的 cline 模型, 实测回 404(管理页却显示默认模型已设为 workbuddy/hy3)。
func TestRouteModelAny(t *testing.T) {
	if got := routeModelAny("workbuddy/hy3"); got != wbVariantCN.ID {
		t.Errorf("routeModelAny(workbuddy/hy3) = %q, 期望 %q", got, wbVariantCN.ID)
	}
	if got := routeModelAny("workbuddy-ai/GLM-5.3 · x0.79"); got != wbVariantINTL.ID {
		t.Errorf("routeModelAny(workbuddy-ai/...) = %q, 期望 %q", got, wbVariantINTL.ID)
	}
	if got := routeModelAny("qoder/dmodel"); got != "qoder" {
		t.Errorf("routeModelAny(qoder/dmodel) = %q, 期望 qoder", got)
	}
	// 非 provider 的照旧交给 routeModel: zen 免费 -> zen, cline 模型 -> cline
	if got := routeModelAny("big-pickle"); got != "zen" {
		t.Errorf("routeModelAny(big-pickle) = %q, 期望 zen", got)
	}
	if got := routeModelAny("cline-free/mimo-v2.6-flash · free"); got != "cline" {
		t.Errorf("routeModelAny(cline-free/...) = %q, 期望 cline", got)
	}
}

// TestProviderDecorateUnpriced 上游没给价(无 credits / priceFactor)的模型
// 不加计费段。回归: WB CN 的 hunyuan-chat / hunyuan-image-alpha 为了"每个 id 都以
// x<倍率> 或 free 收尾"会被误标 " · free", 但它们实测按量扣积分(约 0.01/2.5k
// token)—— 标 free 等于教用户去用它以为不花钱。
func TestProviderDecorateUnpriced(t *testing.T) {
	// 有显示名但无价: 只有分组前缀 + 显示名, 没有 " · " 段
	if got := providerDecorateID("workbuddy", "hunyuan-chat", "Hunyuan-Turbos", 0, false); got != "workbuddy/Hunyuan-Turbos" {
		t.Errorf("未标价模型被加了计费段: %q", got)
	}
	// 明确免费(上游给了 x0.00 credits)必须仍然带 free 段
	if got := providerDecorateID("workbuddy", "hy3", "Hy3", 0, true); got != "workbuddy/Hy3 · free" {
		t.Errorf("免费模型丢了 free 段: %q", got)
	}
	// 未标价的 id 送回来时仍能被剥干净并路由
	if got := providerDecorateID("workbuddy", "hunyuan-chat", "Hunyuan-Turbos", 0, false); providerStripDecorate(got) != "workbuddy/Hunyuan-Turbos" {
		t.Errorf("未标价模型剥离失败: %q", providerStripDecorate(got))
	}
}
