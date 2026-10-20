package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ============================================================================
// 第三方 provider 的 chat 入口 (WorkBuddy CN / WorkBuddy AI / Qoder)
// ============================================================================

// providerUsageFn 记录第三方 provider 的模型用量(按模型名累计 token)。
// 这些 provider 的账号不在 cline 账号池内, 因此不计入池内记账。
func providerUsageFn(model string) func(map[string]any) {
	return func(u map[string]any) {
		var pt, ct float64
		if v, ok := u["prompt_tokens"].(float64); ok {
			pt = v
		}
		if v, ok := u["completion_tokens"].(float64); ok {
			ct = v
		}
		if pt == 0 && ct == 0 {
			return
		}
		recordClineModelStats(model, int(pt), int(ct))
		if cr, ok := u["credit"].(float64); ok && cr > 0 {
			log.Printf("  provider credit: %.4f model=%s", cr, model)
		}
	}
}

// providerChatError 携带应当返回给客户端的 HTTP 状态, 便于 chat / anthropic /
// responses 三个协议入口共用同一套上游调用逻辑。
type providerChatError struct {
	Status int
	Msg    string
	Type   string
}

func (e *providerChatError) Error() string { return e.Msg }

func providerErr(status int, typ, format string, args ...any) *providerChatError {
	return &providerChatError{Status: status, Msg: fmt.Sprintf(format, args...), Type: typ}
}

// writeProviderError 把 providerChatError 写成 OpenAI 风格错误体。
func writeProviderError(w http.ResponseWriter, err error) {
	var pe *providerChatError
	if !errors.As(err, &pe) {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]string{"message": err.Error(), "type": "api_error"},
		})
		return
	}
	writeJSON(w, pe.Status, map[string]any{
		"error": map[string]string{"message": pe.Msg, "type": pe.Type},
	})
}

// wbPrepareChat 完成 WorkBuddy 侧的模型解析 + 开关校验并发出上游请求。
// 返回的 *http.Response 由调用方负责 Close。
func wbPrepareChat(params map[string]any, variant string) (*http.Response, wbModelInfo, error) {
	model, _ := params["model"].(string)
	name := providerStripPrefix(model)
	var m wbModelInfo
	var ok bool
	if variant != "" {
		m, ok = wbResolveModelIn(variant, name)
	} else {
		m, ok = wbResolveModel(name)
	}
	if !ok {
		if providerIsAliasModel(name) {
			return nil, m, providerErr(http.StatusBadRequest, "invalid_request_error",
				"model %q is a placeholder alias excluded from the catalog; pick a concrete model from /v1/models", model)
		}
		if variant == "" || !wbVariantExists(variant) {
			return nil, m, providerErr(http.StatusBadRequest, "invalid_request_error",
				"model %q is not an available workbuddy model", model)
		}
		m = wbModelInfo{ID: name, Variant: variant}
	}
	if variant != "" {
		m.Variant = variant
	}
	// 注意: 不能用客户端传入的 name 覆盖 m.ID —— wbResolveModel 按显示名兜底
	// 命中后, m.ID 是上游内部路由 id(如 deepseek-v3-2-volc), name 只是展示名,
	// 覆盖会让上游报 11102 "service info not found"。
	if v := wbVariantByID(m.Variant); v != nil && !wbVariantEnabled(v) {
		return nil, m, providerErr(http.StatusServiceUnavailable, "api_error",
			"%s upstream disabled in /admin/ settings", v.DisplayName)
	}

	resp, acc, err := wbCallChat(params, m, requestSessionID(params, nil))
	if err != nil {
		return nil, m, err
	}
	if acc != nil {
		log.Printf("  workbuddy account: %s", maskSecret(acc.Email))
	}
	return resp, m, nil
}

// qoderPrepareChat 完成 Qoder 侧的模型解析 + 令牌换取并发出上游请求。
func qoderPrepareChat(params map[string]any) (*http.Response, qoderModelInfo, error) {
	model, _ := params["model"].(string)
	name := providerStripPrefix(model)
	m, ok := qoderResolveModel(name)
	if !ok {
		if providerIsAliasModel(name) {
			return nil, m, providerErr(http.StatusBadRequest, "invalid_request_error",
				"model %q is a placeholder alias excluded from the catalog; pick a concrete model from /v1/models", model)
		}
		return nil, m, providerErr(http.StatusBadRequest, "invalid_request_error",
			"model %q is not an available qoder model", model)
	}
	acc := qoderPickAccount()
	if acc == nil {
		return nil, m, providerErr(http.StatusServiceUnavailable, "auth_error",
			"no qoder account available; add a personal access token in /admin/")
	}
	if err := qoderEnsureToken(acc); err != nil {
		log.Printf("  qoder token error: %v", err)
		return nil, m, err
	}
	resp, err := qoderCallChat(params, m, acc)
	if err != nil {
		return nil, m, err
	}
	return resp, m, nil
}

// prepareProviderChat 按路由分发到对应 provider 的上游调用。
func prepareProviderChat(route string, params map[string]any) (*http.Response, sseExtractor, string, error) {
	switch route {
	case wbVariantCN.ID, wbVariantINTL.ID:
		if !getWBConfig().Enabled {
			return nil, nil, "", providerErr(http.StatusServiceUnavailable, "api_error",
				"workbuddy upstream disabled in /admin/ settings")
		}
		resp, m, err := wbPrepareChat(params, route)
		if err != nil {
			return nil, nil, "", err
		}
		return resp, wbSSEExtract, m.ID, nil
	case "qoder":
		if !getQoderConfig().Enabled {
			return nil, nil, "", providerErr(http.StatusServiceUnavailable, "api_error",
				"qoder upstream disabled in /admin/ settings")
		}
		resp, m, err := qoderPrepareChat(params)
		if err != nil {
			return nil, nil, "", err
		}
		return resp, qoderSSEExtract, m.Key, nil
	case "trae":
		if !getTraeConfig().Enabled {
			return nil, nil, "", providerErr(http.StatusServiceUnavailable, "api_error",
				"trae upstream disabled in /admin/ settings")
		}
		resp, m, err := traePrepareChat(params)
		if err != nil {
			return nil, nil, "", err
		}
		return resp, traeSSEExtract, m.Key, nil
	case "loomy":
		if !getLoomyConfig().Enabled {
			return nil, nil, "", providerErr(http.StatusServiceUnavailable, "api_error",
				"loomy upstream disabled in /admin/ settings")
		}
		resp, m, acc, err := loomyPrepareChat(params)
		if err != nil {
			return nil, nil, "", err
		}
		return resp, loomySSEExtractFor(acc), m.Key, nil
	}
	return nil, nil, "", providerErr(http.StatusBadRequest, "invalid_request_error",
		"model %q is not a third-party provider model", route)
}

// handleProviderAnthropicMessage 第三方 provider 的 Anthropic Messages 入口。
// Anthropic 与 OpenAI 的请求体形状不同, 但上游只认 OpenAI, 因此调用方已把
// 请求转成 openAIReq; 这里只负责取上游、把 OpenAI 流/响应再转回 Anthropic。
func handleProviderAnthropicMessage(w http.ResponseWriter, route string, req anthropicReq, openAIReq map[string]any, toolSchemas map[string]map[string]bool) {
	resp, extract, modelName, err := prepareProviderChat(route, openAIReq)
	if err != nil {
		log.Printf("  anthropic %s api error: %v", route, err)
		writeProviderError(w, err)
		return
	}
	defer resp.Body.Close()
	log.Printf("  anthropic %s: model=%s stream=%v", route, modelName, req.Stream)

	usageFn := providerUsageFn(modelName)
	if req.Stream {
		// qoder 的帧是双层信封, 必须传 extract; workbuddy 的帧本就是 OpenAI chunk,
		// 但其 extractor 同时负责识别 "[DONE]", 因此统一传 extract 更安全。
		handleAnthropicStreamExtract(w, resp, modelName, toolSchemas, usageFn, extract)
		return
	}
	out, err := aggregateProviderSSE(resp, extract, usageFn)
	if err != nil {
		log.Printf("  anthropic %s aggregate error: %v", route, err)
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]string{"message": err.Error(), "type": "api_error"},
		})
		return
	}
	out = normalizeOpenAIResponse(out)
	anthropicResp := openAIToAnthropicSchemas(out, toolSchemas)
	if tc, ok := getNested(out, "choices", 0, "message", "tool_calls").([]any); ok && len(tc) > 0 {
		anthropicResp["stop_reason"] = "tool_use"
	}
	writeJSON(w, http.StatusOK, anthropicResp)
}

// handleProviderResponses 第三方 provider 的 Responses 协议入口。
func handleProviderResponses(w http.ResponseWriter, route string, chat map[string]any, isStream bool) {
	resp, extract, modelName, err := prepareProviderChat(route, chat)
	if err != nil {
		log.Printf("  responses %s api error: %v", route, err)
		writeProviderError(w, err)
		return
	}
	defer resp.Body.Close()
	log.Printf("  responses %s: model=%s stream=%v", route, modelName, isStream)

	usageFn := providerUsageFn(modelName)
	if isStream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(http.StatusOK)
		chatStreamToResponsesExtract(w, resp, usageFn, extract)
		return
	}
	out, err := aggregateProviderSSE(resp, extract, usageFn)
	if err != nil {
		log.Printf("  responses %s aggregate error: %v", route, err)
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]string{"message": err.Error(), "type": "api_error"},
		})
		return
	}
	out = normalizeOpenAIResponse(out)
	writeJSON(w, http.StatusOK, chatToResponses(out))
}

// handleWBAChat WorkBuddy 变体聊天入口。variant 非空时强制走该变体。
func handleWBAChat(w http.ResponseWriter, r *http.Request, params map[string]any, variant string) {
	if !getWBConfig().Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": map[string]string{"message": "workbuddy upstream disabled in /admin/ settings", "type": "api_error"},
		})
		return
	}
	isStream, _ := params["stream"].(bool)
	resp, m, err := wbPrepareChat(params, variant)
	if err != nil {
		log.Printf("  workbuddy api error: %v", err)
		writeProviderError(w, err)
		return
	}
	defer resp.Body.Close()
	log.Printf("  workbuddy: model=%s variant=%s stream=%v", m.ID, m.Variant, isStream)

	usageFn := providerUsageFn(m.ID)
	if isStream {
		relayProviderSSE(w, resp, wbSSEExtract, usageFn)
		return
	}
	out, err := aggregateProviderSSE(resp, wbSSEExtract, usageFn)
	if err != nil {
		log.Printf("  workbuddy aggregate error: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]string{"message": err.Error(), "type": "api_error"},
		})
		return
	}
	out = normalizeOpenAIResponse(out)
	writeJSON(w, http.StatusOK, out)
}

// handleQoderChat Qoder (china 区) 聊天入口。
func handleQoderChat(w http.ResponseWriter, r *http.Request, params map[string]any) {
	if !getQoderConfig().Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": map[string]string{"message": "qoder upstream disabled in /admin/ settings", "type": "api_error"},
		})
		return
	}
	isStream, _ := params["stream"].(bool)
	resp, m, err := qoderPrepareChat(params)
	if err != nil {
		log.Printf("  qoder api error: %v", err)
		writeProviderError(w, err)
		return
	}
	defer resp.Body.Close()
	log.Printf("  qoder: model=%s stream=%v", m.Key, isStream)

	usageFn := providerUsageFn(m.Key)
	if isStream {
		relayProviderSSE(w, resp, qoderSSEExtract, usageFn)
		return
	}
	out, err := aggregateProviderSSE(resp, qoderSSEExtract, usageFn)
	if err != nil {
		log.Printf("  qoder aggregate error: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]string{"message": err.Error(), "type": "api_error"},
		})
		return
	}
	out = normalizeOpenAIResponse(out)
	writeJSON(w, http.StatusOK, out)
}

func handleTraeChat(w http.ResponseWriter, r *http.Request, params map[string]any) {
	if !getTraeConfig().Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": map[string]string{"message": "trae upstream disabled in /admin/ settings", "type": "api_error"},
		})
		return
	}
	isStream, _ := params["stream"].(bool)
	resp, m, err := traePrepareChat(params)
	if err != nil {
		log.Printf("  trae api error: %v", err)
		writeProviderError(w, err)
		return
	}
	defer resp.Body.Close()
	log.Printf("  trae: model=%s stream=%v", m.Key, isStream)

	usageFn := providerUsageFn(m.Key)
	if isStream {
		relayProviderSSE(w, resp, traeSSEExtract, usageFn)
		return
	}
	out, err := aggregateProviderSSE(resp, traeSSEExtract, usageFn)
	if err != nil {
		log.Printf("  trae aggregate error: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]string{"message": err.Error(), "type": "api_error"},
		})
		return
	}
	out = normalizeOpenAIResponse(out)
	writeJSON(w, http.StatusOK, out)
}

// ============================================================================
// 模型 id 展示装饰 / 别名过滤
// ============================================================================

// providerDecorateSep 装饰分隔符。模型对外 id 形如:
//
//	<分组>/<上游可读显示名> · x<倍率>   (付费)
//	<分组>/<上游可读显示名> · free      (免费)
//
// 分组就是 json 里的 owned_by(cline-free / opencode-zen / workbuddy /
// workbuddy-ai / qoder), 客户端据此按来源归类; 计费段固定在第二位,
// 免费与付费占同一位置, 这样每个 id 都自带"多少钱"的信息。
// 装饰只存在于 /v1/models 列表这一展示层; 路由入口用 providerStripDecorate
// 按第一个分隔符截断, 所以客户端拿列表里的 id 调用、旧配置里的干净 id 调用
// 都能落到同一个上游模型。
const providerDecorateSep = " · "

// providerFreeTag 免费模型的计费段(倍率为 0 时使用), 与 "x0.29" 同一位置。
const providerFreeTag = "free"

// providerFreeID 给整池免费的来源(cline / zen)补上计费段: 它们的 id 本身
// 已带分组前缀(cline-free/...), 或由调用方拼好(zen 用 opencode-zen/<id>)。
// 这样 /v1/models 里每一个 id 末尾要么是 "x<倍率>", 要么是 "free", 不再有
// "没有倍率 = 不知道收不收费" 的空白。
func providerFreeID(id string) string {
	return id + providerDecorateSep + providerFreeTag
}

// providerStripGroup 去掉展示层的分组前缀, 还原路由 id。
// 只处理 zen 这一种情况: 它的真实模型 id 是裸名(cline 的 id 本身就以
// "cline-free/" 开头, 属于真实 id, 不能剥), 分组前缀纯属展示层。
func providerStripGroup(id string) string {
	if strings.HasPrefix(id, "opencode-zen/") {
		return strings.TrimPrefix(id, "opencode-zen/")
	}
	return id
}

// providerNormalizeDisplayID 把客户端传来的展示 id 归一成路由 id:
// 先剥展示装饰, 再剥纯展示的分组前缀(zen)。旧配置的干净 id 原样返回。
func providerNormalizeDisplayID(id string) string {
	return providerStripGroup(providerStripDecorate(strings.TrimSpace(id)))
}

// providerIsAliasModel 判定上游目录里的别名模型(auto / default 等):
// 它们不是真实模型, 只是"让上游自己挑"的占位符 —— 路由不可预期、倍率未知,
// 且与 cline/zen 目录撞名, 从三家目录同步时统一排除。
func providerIsAliasModel(id string) bool {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "auto", "default", "default-model":
		return true
	}
	return false
}

// providerFormatMultiplier 把倍率格式化成 x0.29 形态: 整数不带小数点,
// 小数保留有效位(0.29 → x0.29, 2 → x2, 1.5 → x1.5)。
func providerFormatMultiplier(m float64) string {
	s := strconv.FormatFloat(m, 'f', -1, 64)
	return "x" + s
}

// providerDecorateID 构造对外暴露的模型 id:
//
//	<group>/<上游可读显示名> · x<倍率>   付费
//	<group>/<上游可读显示名> · free      免费
//
// 首段用显示名是因为用户不需要知道内部路由 id; group 是 owned_by 分组
// (workbuddy / workbuddy-ai / qoder), 客户端据此按来源归类。displayName 为空
// (部分 WB 模型上游没给 name)时退回路由 id 作首段, 保证可调用性优先。
// free 为 true 时即使倍率为 0 也补一段 " · free", 让"没有倍率"不再是无信息的
// 空白 —— 每个 id 末尾要么是 x<倍率>, 要么是 free; 既非免费又没标价(上游没给
// credits 字段)的模型不加段, 与条目里的 cost 字段保持一致。
//
// 例外只有 WB CN 的 hunyuan-chat(hunyuan-image-alpha): 上游目录里没有 credits
// 字段, 实测调一次仍扣 credit(约 0.01/2.5k token), 所以既不是免费也不能标 x ——
// 标 " · free" 会教用户去用它以为不花钱, 标一个编出来的倍率更糟。保持无段,
// 并在 /admin 里显式提示"上游未标价, 实测按量扣费", 让用户知道这是上游的缺口。
func providerDecorateID(group, id, displayName string, multiplier float64, free bool) string {
	out := id
	if name := strings.TrimSpace(displayName); name != "" {
		// 显示名里的空格转 -, 避免与 id 中潜在的空格/分隔符语义混淆
		out = strings.ReplaceAll(name, " ", "-")
	}
	switch {
	case multiplier > 0:
		out += providerDecorateSep + providerFormatMultiplier(multiplier)
	case free:
		out += providerDecorateSep + providerFreeTag
	}
	if g := strings.TrimSpace(group); g != "" {
		out = g + "/" + out
	}
	return out
}

// providerStripDecorate 剥掉展示装饰, 还原可路由 id。
// 按第一个 " · " 截断 —— 前半段一定是最初 providerPublicID 生成的路由 id。
// 没有装饰的 id 原样返回, 旧配置完全不受影响。
func providerStripDecorate(id string) string {
	if i := strings.Index(id, providerDecorateSep); i >= 0 {
		return strings.TrimSpace(id[:i])
	}
	return id
}

// providerModelList 汇总所有第三方 provider 的模型条目(/v1/models 用)。
func providerModelList() []map[string]any {
	var out []map[string]any
	if getWBConfig().Enabled {
		// 只列上游真正接入的模型; 未接入的(11102 service info not found)若列出来,
		// 客户端选中就是必然失败的请求。后台 /admin 仍能看到全部条目。
		for _, m := range wbModelListAvailable() {
			cost, _ := m["cost"].(string)
			if cost == "" {
				cost = "free"
			}
			mult, _ := m["multiplier"].(float64)
			// 用 baseId 而不是 id 拼展示名: id 在有歧义时已被加上
			// "<provider>/" 消歧前缀, 再叠加分组会变成 "workbuddy/workbuddy/..."。
			// 消歧在展示层由分组前缀统一承担。
			group, _ := m["owned_by"].(string)
			baseID, _ := m["baseId"].(string)
			name, _ := m["name"].(string)
			out = append(out, map[string]any{
				"id":       providerDecorateID(group, baseID, name, mult, cost == "free"),
				"object":   "model",
				"created":  time.Now().UnixMilli(),
				"owned_by": m["owned_by"],
				"source":   m["source"],
				"status":   "active",
				"cost":     cost,
				"context":  m["context"],
				"output":   m["output"],
			})
		}
	}
	if getQoderConfig().Enabled {
		for _, m := range qoderModelList() {
			cost := "paid"
			mult, _ := m["priceFactor"].(float64)
			if mult == 0 {
				cost = "free"
			}
			name, _ := m["name"].(string)
			baseID, _ := m["baseId"].(string)
			out = append(out, map[string]any{
				"id":       providerDecorateID("qoder", baseID, name, mult, cost == "free"),
				"object":   "model",
				"created":  time.Now().UnixMilli(),
				"owned_by": "qoder",
				"source":   "qoder",
				"status":   "active",
				"cost":     cost,
				"context":  m["context"],
				"output":   m["output"],
			})
		}
	}
	if getTraeConfig().Enabled {
		for _, m := range traeModelList() {
			// 只有上游真的给了 credit_multiplier 才能判免费: 键缺失时 mult 也是 0,
			// 但那是"上游没标价", 照 free 处理会教用户以为能免费用。
			cost := "paid"
			mult, _ := m["priceFactor"].(float64)
			if priced, _ := m["priced"].(bool); priced && mult == 0 {
				cost = "free"
			}
			name, _ := m["name"].(string)
			baseID, _ := m["baseId"].(string)
			out = append(out, map[string]any{
				"id":       providerDecorateID("trae", baseID, name, mult, cost == "free"),
				"object":   "model",
				"created":  time.Now().UnixMilli(),
				"owned_by": "trae",
				"source":   "trae",
				"status":   "active",
				"cost":     cost,
				"context":  m["context"],
				"output":   m["output"],
			})
		}
	}
	if getLoomyConfig().Enabled {
		for _, m := range loomyModelList() {
			// 倍率 0 有两种含义: 显式标 x0.0(限时免费) vs 图片模型没标价(按点数
			// 实扣)。只有前者标 free。
			cost := "paid"
			mult, _ := m["priceFactor"].(float64)
			if free, _ := m["free"].(bool); free {
				cost = "free"
			}
			name, _ := m["name"].(string)
			baseID, _ := m["baseId"].(string)
			item := map[string]any{
				"id":       providerDecorateID("loomy", baseID, name, mult, cost == "free"),
				"object":   "model",
				"created":  time.Now().UnixMilli(),
				"owned_by": "loomy",
				"source":   "loomy",
				"status":   "active",
				"cost":     cost,
				"context":  m["context"],
				"output":   m["output"],
				"imageGen": m["imageGen"],
			}
			out = append(out, item)
		}
	}
	return out
}

// providerRoute 判断模型属于哪个第三方 provider:
// "workbuddy" / "workbuddy-ai" / "qoder" / ""。
// 支持显式前缀 <provider>/<模型> 强制指定(解决同名模型歧义);
// 无前缀时按已同步的目录判定。展示装饰(" · 显示名 · x倍率")先剥掉,
// 客户端从 /v1/models 原样选来的 id 才能正确路由。
func providerRoute(id string) string {
	id = strings.TrimSpace(id)
	if i := strings.Index(id, "/"); i > 0 {
		prefix, rest := id[:i], strings.TrimSpace(id[i+1:])
		if prefix != "opencode" && rest != "" {
			switch prefix {
			case "workbuddy", "wb":
				return wbVariantCN.ID
			case "workbuddy-ai", "wb-ai", "workbuddyai":
				return wbVariantINTL.ID
			case "qoder", "qd":
				return "qoder"
			case "trae", "tr":
				return "trae"
			case "loomy", "xf", "xunfei":
				return "loomy"
			}
		}
	}
	// 完整装饰串精确匹配优先: 显示名跨 provider 冲突时倍率段是唯一区分
	// (如 WB CN "GLM-5.3 · x0.79" 与 Qoder "GLM-5.3 · x0.8"), 剥掉装饰再
	// 裸名解析就会丢失归属。
	if m, ok := wbResolveDecorated(id); ok {
		return m.Variant
	}
	if _, ok := qoderResolveDecorated(id); ok {
		return "qoder"
	}
	if _, ok := traeResolveDecorated(id); ok {
		return "trae"
	}
	if _, ok := loomyResolveDecorated(id); ok {
		return "loomy"
	}
	// 裸名兜底: 内部路由 id / 显示名
	stripped := providerStripDecorate(id)
	if m, ok := wbResolveModel(stripped); ok {
		return m.Variant
	}
	if _, ok := qoderResolveModel(stripped); ok {
		return "qoder"
	}
	if _, ok := traeResolveModel(stripped); ok {
		return "trae"
	}
	if _, ok := loomyResolveModel(stripped); ok {
		return "loomy"
	}
	return ""
}

// providerStripPrefix 去掉显式 provider 前缀与展示装饰, 得到上游模型名。
func providerStripPrefix(id string) string {
	id = providerStripDecorate(id)
	if i := strings.Index(id, "/"); i > 0 {
		switch id[:i] {
		case "workbuddy", "wb", "workbuddy-ai", "wb-ai", "workbuddyai", "qoder", "qd", "trae", "tr", "loomy", "xf", "xunfei":
			return strings.TrimSpace(id[i+1:])
		}
	}
	return id
}

// isProviderRoute 判断路由值是否属于第三方 provider(workbuddy CN/INTL + qoder)。
// chat / anthropic / responses 三个协议入口共用同一判定, 少一处就会把请求
// 静默交给 cline 账号池(用 cline 的额度跑另一个上游, 且不报错)。
func isProviderRoute(route string) bool {
	switch route {
	case wbVariantCN.ID, wbVariantINTL.ID, "qoder", "trae", "loomy":
		return true
	}
	return false
}

// providerNameOf 把内部变体 id 映射成对外 provider 名(命名空间前缀、UI 标签、
// owned_by 共用同一套取值)。
func providerNameOf(variant string) string {
	switch variant {
	case wbVariantCN.ID:
		return wbVariantCN.Provider
	case wbVariantINTL.ID:
		return wbVariantINTL.Provider
	case "qoder":
		return "qoder"
	case "trae":
		return "trae"
	case "loomy":
		return "loomy"
	}
	return variant
}

// providerPublicIDs 计算"裸 id -> 是否必须带 <provider>/ 前缀"。
//
// 裸 id 只有在全程唯一时才可直接路由; 一旦同一 id 出现在多个来源
// (实测: workbuddy 与 qoder 都有 auto; CN 与 INTL 都有 hy3/glm-5.2/glm-5.3),
// 裸 id 只能解析到其中一个, 直接对外暴露就是给客户端埋 500 的坑。
// 与 zen/cline 目录撞名同样必须加前缀, 否则会被那两个上游抢走。
func providerPublicIDs() map[string]bool {
	dup := map[string]bool{}
	seen := map[string]struct{}{}
	wbModelsMu.RLock()
	for _, m := range wbModels {
		if _, ok := seen[m.ID]; ok {
			dup[m.ID] = true
		}
		seen[m.ID] = struct{}{}
	}
	wbModelsMu.RUnlock()
	for _, m := range qoderModelSnapshot() {
		if _, ok := seen[m.Key]; ok {
			dup[m.Key] = true
		}
		seen[m.Key] = struct{}{}
	}
	for _, m := range traeModelSnapshot() {
		if _, ok := seen[m.Key]; ok {
			dup[m.Key] = true
		}
		seen[m.Key] = struct{}{}
	}
	for _, m := range loomyModelSnapshot() {
		if _, ok := seen[m.Key]; ok {
			dup[m.Key] = true
		}
		seen[m.Key] = struct{}{}
	}
	// 与 zen / cline 目录撞名: 那些模型有各自的解析优先级, 裸 id 到不了我们这里。
	initZenModels()
	zenModelsMu.RLock()
	for id := range zenModels {
		dup[id] = true
	}
	zenModelsMu.RUnlock()
	initModelsCache()
	modelsMu.Lock()
	for id := range modelsCache {
		dup[id] = true
	}
	modelsMu.Unlock()
	return dup
}

// providerPublicID 返回该模型对外暴露的 id: 唯一时保持裸 id(不破坏既有调用),
// 有歧义时加 <provider>/ 前缀。两种形态 providerRoute 都能路由。
func providerPublicID(variant, id string) string {
	if !providerPublicIDs()[id] {
		return id
	}
	return providerNameOf(variant) + "/" + id
}

func readJSONBody(r *http.Request, dst any) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if len(strings.TrimSpace(string(body))) == 0 {
		return json.Unmarshal([]byte("{}"), dst)
	}
	return json.Unmarshal(body, dst)
}
