package app

import (
	"bytes"
	"FreeModelRelay/internal/kit"
	"compress/gzip"
	"compress/zlib"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/andybalholm/brotli"
)

// ZenModel opencode zen 免费模型定义
type ZenModel struct {
	ID      string   `json:"id"`
	Aliases []string `json:"aliases,omitempty"`
	Context int      `json:"context"`
	Output  int      `json:"output"`
	Source  string   `json:"source"` // seed=内置 / synced=动态同步
}

// 免费模型 seed 兜底列表(仅官方目录不可用时生效; 在线以 models.opencode.ai 为准)。
// 2026-10-14 以 bash+read 指纹实测: 前 6 个 200 可用, 后 2 个限流中(429);
// 旧 seed 中的 deepseek-v4-flash-free/mimo-v2.5-free/ling-3.0-flash-free 已 410 下架。
var zenSeedModels = []ZenModel{
	{"big-pickle", nil, 200000, 32000, "seed"},
	{"nemotron-3.5-lightning-free", []string{"nemotron-3.5-lightning"}, 200000, 32768, "seed"},
	{"longcat-2.5-preview-free", []string{"longcat-2.5-preview"}, 200000, 32768, "seed"},
	{"space-bunny-free", []string{"space-bunny"}, 200000, 32768, "seed"},
	{"mimo-v2.6-flash-free", []string{"mimo-v2.6-flash"}, 200000, 32768, "seed"},
	{"nemotron-3-ultra-free", []string{"nemotron-3-ultra"}, 1000000, 128000, "seed"},
	{"step-5-preview-free", []string{"step-5-preview"}, 200000, 32768, "seed"},
	{"ling-3.1-flash-free", []string{"ling-3.1-flash"}, 200000, 32768, "seed"},
}

var (
	zenModelsMu sync.RWMutex
	zenModels   = make(map[string]*ZenModel) // 主表:ID
	zenAliases  = make(map[string]*ZenModel) // 别名表
)

// 真实 CLI 走 /inference/v1 网关(2026-10-14 抓包); 旧 /zen/v1 已被 inference-proxy
// 转发到 BYOK 路径, 匿名 key 会被 401 AuthError 拒绝。
const zenAPIBase = "https://opencode.ai/inference/v1"

func initZenModels() {
	zenModelsMu.Lock()
	defer zenModelsMu.Unlock()
	if len(zenModels) > 0 {
		return
	}
	for _, m := range zenSeedModels {
		cp := m
		zenModels[cp.ID] = &cp
		for _, a := range cp.Aliases {
			zenAliases[a] = &cp
		}
	}
}

// resolveZenModel 解析模型名到 zen 模型。支持 "opencode/<id>" 前缀、展示层的
// "opencode-zen/<id> · free" 形态(与 " · x倍率" 计费段)以及别名。
// 别名优先: 同步来的付费同名模型(如 deepseek-v4-flash)不会覆盖 free 别名解析。
func resolveZenModel(id string) (*ZenModel, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, false
	}
	// 展示层装饰(分组前缀 + 计费段)先归一, 客户端从 /v1/models 原样选来也能命中
	id = providerNormalizeDisplayID(id)
	if id == "" {
		return nil, false
	}
	zenModelsMu.RLock()
	defer zenModelsMu.RUnlock()
	if m, ok := zenAliases[id]; ok {
		return m, true
	}
	if strings.HasPrefix(id, "opencode/") {
		short := strings.TrimPrefix(id, "opencode/")
		if m, ok := zenAliases[short]; ok {
			return m, true
		}
		if m, ok := zenModels[short]; ok {
			return m, true
		}
	}
	if m, ok := zenModels[id]; ok {
		return m, true
	}
	return nil, false
}

// isZenFreeModel 免费判定:
// 官方目录(models.opencode.ai)可用时以它为准 — 与 opencode CLI 同规则
// (cost.input==0 且非 deprecated/alpha), 保证与 opencode 工具看到的免费阵容一致;
// 目录不可用时回退启发式: seed 白名单 或 ID 带 -free 后缀。
func isZenFreeModel(m *ZenModel) bool {
	if m == nil {
		return false
	}
	if catalogLoaded() {
		return catalogIsFree(m.ID)
	}
	return m.Source == "seed" || strings.HasSuffix(m.ID, "-free")
}

// resolveZenFreeModel 只解析免费 zen 模型
func resolveZenFreeModel(id string) (*ZenModel, bool) {
	m, ok := resolveZenModel(id)
	if !ok || !isZenFreeModel(m) {
		return nil, false
	}
	return m, true
}

// routeModel 决定请求走哪个上游: "zen" / "cline" / "reject"
// zen 免费模型 -> zen; zen 付费模型 -> reject(400); 其他 -> cline
// 故障转移: zen 连续失败期间,zen 免费模型请求临时路由到 cline 账号池
func routeModel(id string) string {
	id = strings.TrimSpace(id)
	initZenModels()
	cfg := getZenConfig()
	if zm, ok := resolveZenModel(id); ok {
		if isZenFreeModel(zm) {
			// 与 cline 模型表冲突时(几乎不可能)走 cline
			initModelsCache()
			modelsMu.Lock()
			_, inCline := modelsCache[id]
			modelsMu.Unlock()
			if !inCline {
				if cfg.Failover && zenFailedNow() {
					log.Printf("  failover: zen degraded, %q routed to cline pool", id)
					return "cline"
				}
				return "zen"
			}
		} else {
			return "reject"
		}
	}
	if strings.HasPrefix(id, "opencode/") {
		short := strings.TrimPrefix(id, "opencode/")
		if zm, ok := resolveZenModel(short); ok {
			if isZenFreeModel(zm) {
				if cfg.Failover && zenFailedNow() {
					log.Printf("  failover: zen degraded, %q routed to cline pool", id)
					return "cline"
				}
				return "zen"
			}
			return "reject"
		}
	}
	return "cline"
}

// ============ zen 配置 ============

type zenCompactConfig struct {
	Auto         bool   `json:"auto"`         // 官方风格摘要压缩开关
	Buffer       int    `json:"buffer"`       // 预留输出缓冲 token,默认 20000
	KeepTokens   int    `json:"keepTokens"`   // 尾部保留 token 预算,默认 8000
	SummaryModel string `json:"summaryModel"` // 摘要模型,空=用请求模型
	MaxSummary   int    `json:"maxSummary"`   // 摘要最大输出 token,默认 4096
}

// zenRTKConfig RTK 工具输出预压缩配置 (9Router RTK 保守移植):
// 上游请求前对超长 tool_result 按安全类型过滤/截断, 节省输入 token。
// level: 0=关闭(默认), 1=仅处理文件列表/git status 等安全类型, 2=加入 git diff 截断
type zenRTKConfig struct {
	Level       int  `json:"level"`       // 0=off 1=safe 2=+git-diff
	MinChars    int  `json:"minChars"`    // 触发阈值,输出超过该字符数才处理,默认 20000
	KeepHead    int  `json:"keepHead"`    // 截断保留头部行数,默认 30
	KeepTail    int  `json:"keepTail"`    // 截断保留尾部行数,默认 15
	GrepDedup   bool `json:"grepDedup"`   // 去相邻重复行
	FilterNoise bool `json:"filterNoise"` // 过滤 node_modules/lock 等噪音行
}

type zenConfigData struct {
	Enabled         bool             `json:"enabled"`
	Key             string           `json:"key"`
	BaseURL         string           `json:"baseURL"`
	Proxies         []string         `json:"proxies"`         // http(s)/socks5 代理,轮询出口
	ProxyStrategy   string           `json:"proxyStrategy"`   // round_robin / random / fill
	MaxConcurrency  int              `json:"maxConcurrency"`  // zen 上游最大并发,防 worker 瞬时超限,默认 8
	Retries         int              `json:"retries"`         // 限流/网络错误重试次数,默认 3
	Failover        bool             `json:"failover"`        // zen 连续失败后故障转移到 cline 账号池,默认 true
	FailoverCount   int              `json:"failoverCount"`   // 触发故障转移的连续失败次数,默认 3
	FailoverMinutes int              `json:"failoverMinutes"` // 故障转移窗口(分钟),默认 5
	Compaction      zenCompactConfig `json:"compaction"`
	RTK             zenRTKConfig     `json:"rtk"`
}

func defaultZenConfig() *zenConfigData {
	return &zenConfigData{
		Enabled:         true,
		Key:             "public",
		BaseURL:         zenAPIBase,
		ProxyStrategy:   "round_robin",
		MaxConcurrency:  8,
		Retries:         3,
		Failover:        true,
		FailoverCount:   3,
		FailoverMinutes: 5,
		Compaction: zenCompactConfig{
			Auto:       true,
			Buffer:     20000,
			KeepTokens: 8000,
			MaxSummary: 4096,
		},
		RTK: zenRTKConfig{
			Level:       0, // 默认关闭,后台可开
			MinChars:    20000,
			KeepHead:    30,
			KeepTail:    15,
			GrepDedup:   true,
			FilterNoise: true,
		},
	}
}

var (
	zenConfig   = loadZenConfig()
	zenConfigMu sync.Mutex
)

// ============ 限流防御状态机 ============

var (
	zenSem       chan struct{} // 并发信号量
	zenFailCount int           // 连续失败计数
	zenFailUntil time.Time     // 故障转移截止时间
	zenStateMu   sync.Mutex
)

func init() {
	rebuildZenSem()
}

func rebuildZenSem() {
	cfg := getZenConfig()
	n := cfg.MaxConcurrency
	if n <= 0 {
		n = 8
	}
	zenStateMu.Lock()
	zenSem = make(chan struct{}, n)
	zenStateMu.Unlock()
}

func markZenSuccess() {
	zenStateMu.Lock()
	zenFailCount = 0
	zenFailUntil = time.Time{}
	zenStateMu.Unlock()
}

func markZenFail() {
	cfg := getZenConfig()
	thr := cfg.FailoverCount
	if thr <= 0 {
		thr = 3
	}
	window := cfg.FailoverMinutes
	if window <= 0 {
		window = 5
	}
	zenStateMu.Lock()
	zenFailCount++
	if zenFailCount >= thr {
		zenFailUntil = time.Now().Add(time.Duration(window) * time.Minute)
	}
	zenStateMu.Unlock()
}

// zenFailedNow zen 是否处于故障转移状态
func zenFailedNow() bool {
	zenStateMu.Lock()
	defer zenStateMu.Unlock()
	if zenFailUntil.IsZero() {
		return false
	}
	if time.Now().After(zenFailUntil) {
		zenFailCount = 0
		zenFailUntil = time.Time{}
		return false
	}
	return true
}

// isRateLimited 限流信号识别: 429/503 直接命中; 502/403 按错误体关键词
func isRateLimited(status int, body string) bool {
	if status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
		return true
	}
	// 裸 500(响应体为空)是上游网关的瞬时故障, 实测重试一次就能成功;
	// 有响应体的 500 往往是模型侧确定性错误, 交给调用方直接失败, 不空转。
	if status == http.StatusInternalServerError && strings.TrimSpace(body) == "" {
		return true
	}
	if status == http.StatusBadGateway || status == http.StatusForbidden {
		low := strings.ToLower(body)
		for _, kw := range []string{"resourceexhausted", "limit reached", "rate limit", "too many", "overloaded", "busy"} {
			if strings.Contains(low, kw) {
				return true
			}
		}
	}
	return false
}

func loadZenConfig() *zenConfigData {
	path := kit.ResolveDataPath(".zen-config.json")
	cfg := defaultZenConfig()
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			log.Printf("zen config parse failed: %v", err)
		}
	}
	if cfg.Key == "" {
		cfg.Key = "public"
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = zenAPIBase
	}
	// 迁移: 旧配置可能保存了已失效的 /zen/v1 端点(匿名 key 会 401)。
	// 直接写文件而非 saveZenConfig, 避免初始化循环(zenConfig→load→save→zenConfig)。
	if strings.Contains(cfg.BaseURL, "/zen/v1") {
		cfg.BaseURL = zenAPIBase
		if data, err := json.MarshalIndent(cfg, "", "  "); err == nil {
			_ = os.WriteFile(kit.ResolveDataPath(".zen-config.json"), data, 0600)
		}
		log.Printf("zen baseURL migrated to %s", zenAPIBase)
	}
	return cfg
}

func saveZenConfig() {
	zenConfigMu.Lock()
	defer zenConfigMu.Unlock()
	data, _ := json.MarshalIndent(zenConfig, "", "  ")
	if err := os.WriteFile(kit.ResolveDataPath(".zen-config.json"), data, 0600); err != nil {
		log.Printf("zen config save failed: %v", err)
	}
}

func getZenConfig() *zenConfigData {
	zenConfigMu.Lock()
	defer zenConfigMu.Unlock()
	return zenConfig
}

func setZenConfig(c *zenConfigData) {
	zenConfigMu.Lock()
	zenConfig = c
	zenConfigMu.Unlock()
	saveZenConfig()
	rebuildZenTransport()
	rebuildZenSem()
}

// validateProxyList 校验代理列表格式: 支持 http/https/socks5/socks5h, 必须包含 host:port。
func validateProxyList(proxies []string) error {
	for _, p := range proxies {
		line := strings.TrimSpace(p)
		if line == "" {
			continue
		}
		u, err := url.Parse(line)
		if err != nil {
			return fmt.Errorf("代理格式无效 %q: %v", line, err)
		}
		switch u.Scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			return fmt.Errorf("代理 %q 协议不受支持（支持 http/https/socks5/socks5h）", line)
		}
		if u.Host == "" {
			return fmt.Errorf("代理 %q 缺少 host:port", line)
		}
		if _, _, err := net.SplitHostPort(u.Host); err != nil {
			return fmt.Errorf("代理 %q 缺少端口: %v", line, err)
		}
	}
	return nil
}

// ============ zen 上游调用 ============

// ============ 免费通道请求体指纹 ============
// 2026-10-15 实测(bun 裸放 /inference/v1, 50+ 次消歧): FreeTierError 403 判据为
// 多要素组合且随服务端灰度漂移 —— tools 须含描述/参数像真实客户端的 bash+read
// (空 schema 或 "永不调用" 式占位描述必 403; 客户端自带的同名同类工具可保留),
// stream 必须为 true(false 恒 403), 需若干 x-opencode-* 头。
// 真实 CLI 恒带官方时间戳形 ID(descending)与 max_tokens/stream_options, bun 200
// 基线均含这些要素; 本实现按官方 id.ts 算法生成 ID 并补齐字段, 全维度贴近真实请求。
// 注: 判据随灰度漂移(2026-10-15 上午还启用过 system prompt 白名单), 若 403 回潮
// 先用真实 CLI 抓包复核当时规则形态再改指纹。

//go:embed zen_free_tools.json
var zenFreeToolsJSON []byte

var (
	zenRealToolsOnce sync.Once
	zenRealTools     []map[string]any
)

// zenRealToolDefs 官方工具定义(嵌入自 1.19.0 主对话抓包), 按名注入缺失项。
func zenRealToolDefs() []map[string]any {
	zenRealToolsOnce.Do(func() {
		var defs []map[string]any
		if err := json.Unmarshal(zenFreeToolsJSON, &defs); err != nil {
			log.Printf("zen free tools embed parse failed: %v", err)
			return
		}
		zenRealTools = defs
	})
	return zenRealTools
}

// applyFreeTierFingerprint 补齐请求体指纹: 强制 stream:true, 按缺失名注入官方
// 工具定义, 匿名 key 下按官方 SDK 惯例附带用量统计。客户端已声明的同名工具
// 保留不动(同名同类定义实测可过, 且避免重名工具调用歧义)。
func applyFreeTierFingerprint(body map[string]any) {
	body["stream"] = true
	existing := map[string]bool{}
	tools, _ := body["tools"].([]any)
	for _, t := range tools {
		tm, _ := t.(map[string]any)
		if tm == nil {
			continue
		}
		if fn, ok := tm["function"].(map[string]any); ok {
			if n, ok := fn["name"].(string); ok {
				existing[n] = true
			}
		}
	}
	for _, def := range zenRealToolDefs() {
		fn, _ := def["function"].(map[string]any)
		if fn == nil {
			continue
		}
		name, _ := fn["name"].(string)
		if name == "" || existing[name] {
			continue
		}
		tools = append(tools, def)
		existing[name] = true
	}
	body["tools"] = tools
	// 上游恒流式; 真实 CLI 流式请求恒带 include_usage(聚合器可容忍 usage-only chunk)
	if getZenConfig().Key == "public" {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
}

// ============ 会话身份: 与真实 opencode 一致, 同一对话复用同一 session ============

// canonicalZenSession 已移除: sha 派生的时间段 hex 不含真实时间戳, 与官方
// id.ts 时间戳形 ID 不符, 实测无法通过免费通道校验。改用 kit.ZenOfficialID。

// zenConversationSeed 提取对话稳定种子: 客户端会话信号(metadata.session_id)优先,
// 其次首条用户消息内容。同一会话内后续请求(带完整历史)种子保持一致。
func zenConversationSeed(params map[string]any) string {
	if meta, ok := params["metadata"].(map[string]any); ok {
		if sid, _ := meta["session_id"].(string); sid != "" {
			return sid
		}
	}
	msgs, ok := params["messages"].([]any)
	if !ok {
		return ""
	}
	for _, m := range msgs {
		mm, ok := m.(map[string]any)
		if !ok || mm["role"] != "user" {
			continue
		}
		encoded, _ := json.Marshal(mm["content"])
		if len(encoded) > 0 && string(encoded) != "null" {
			return string(encoded)
		}
	}
	return ""
}

var (
	zenSessMu    sync.Mutex
	zenSessCache = map[string]string{} // 对话种子 -> 官方格式 session ID
)

// zenSessionID 会话 ID: 官方算法生成, 同一对话复用同一 session(贴近真实客户端)。
func zenSessionID(params map[string]any) string {
	seed := zenConversationSeed(params)
	if seed == "" {
		return kit.ZenOfficialID("ses")
	}
	zenSessMu.Lock()
	defer zenSessMu.Unlock()
	if id, ok := zenSessCache[seed]; ok {
		return id
	}
	if len(zenSessCache) >= 512 {
		zenSessCache = map[string]string{}
	}
	id := kit.ZenOfficialID("ses")
	zenSessCache[seed] = id
	return id
}

// buildZenBody 构造 zen 请求体:只带 OpenAI 兼容字段,改写模型为 zen ID
func buildZenBody(params map[string]any, stream bool) map[string]any {
	body := map[string]any{}
	for _, key := range passThroughKeys {
		if val, ok := params[key]; ok {
			body[key] = val
		}
	}
	for _, key := range []string{"model", "messages", "max_tokens", "max_completion_tokens", "stream"} {
		if val, ok := params[key]; ok {
			body[key] = val
		}
	}
	if stream {
		body["stream"] = true
	}
	if model, ok := params["model"].(string); ok {
		if m, ok := resolveZenModel(model); ok {
			body["model"] = m.ID
		}
	}
	// 真实 CLI 恒带 max_tokens(免费通道 200 基线均含); 客户端未传时按官方缺省
	// 32000 补齐, 上限不超过模型输出限额。
	if _, ok := body["max_tokens"]; !ok {
		if _, ok := body["max_completion_tokens"]; !ok {
			limit := 32000
			if model, ok := body["model"].(string); ok {
				if m, ok := resolveZenModel(model); ok && m.Output > 0 && m.Output < limit {
					limit = m.Output
				}
			}
			body["max_tokens"] = limit
		}
	}
	delete(body, "reasoning_effort")
	delete(body, "reasoningEffort")
	return body
}

// decodeZenResponse 按上游 Content-Encoding 包装解压流。
// 免费通道指纹要求 Accept-Encoding 显式含 br(Go 不自动解压), 此处统一还原为
// 明文, 下游 SSE 转发与流聚合均无需感知压缩; 响应头同步剥离压缩标记。
func decodeZenResponse(resp *http.Response) *http.Response {
	switch strings.ToLower(resp.Header.Get("Content-Encoding")) {
	case "gzip":
		if zr, err := gzip.NewReader(resp.Body); err == nil {
			resp.Body = zr
			resp.Header.Del("Content-Encoding")
			resp.Header.Del("Content-Length")
		}
	case "deflate":
		if zr, err := zlib.NewReader(resp.Body); err == nil {
			resp.Body = zr
			resp.Header.Del("Content-Encoding")
			resp.Header.Del("Content-Length")
		}
	case "br":
		resp.Body = io.NopCloser(brotli.NewReader(resp.Body))
		resp.Header.Del("Content-Encoding")
		resp.Header.Del("Content-Length")
	}
	return resp
}

// callZenAPI 调用 zen 上游,带限流防御: 并发信号量 + 指数退避重试 + 代理冷却 + 故障计数
// 上游免费通道强制校验 stream:true + tools 含 bash/read 指纹, 故恒以流式请求上游;
// stream=false 的调用方由本函数在内部把 SSE 聚合为完整 chat.completion JSON 后返回,
// 调用方无需感知上游流式。
// 返回 (响应, 命中限流次数, 错误)
func callZenAPI(params map[string]any, stream bool) (*http.Response, int, error) {
	cfg := getZenConfig()
	body := buildZenBody(params, true) // 上游恒流式(指纹校验强制)
	applyFreeTierFingerprint(body)

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal zen body: %w", err)
	}

	endpoint := strings.TrimRight(cfg.BaseURL, "/") + "/chat/completions"

	zenStateMu.Lock()
	sem := zenSem
	sem <- struct{}{}
	zenStateMu.Unlock()
	defer func() { <-sem }()

	retries := cfg.Retries
	if retries <= 0 {
		retries = 3
	}
	delay := time.Second
	rateLimited := 0

	// 请求级代理追踪: 拨号时写入实际选中的代理, 供冷却精确定位
	pick := &zenProxyPick{idx: -1}
	ctx := context.WithValue(context.Background(), zenPickKey{}, pick)

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyJSON))
		if err != nil {
			return nil, rateLimited, fmt.Errorf("create zen request: %w", err)
		}
		// 客户端身份: 对齐真实 opencode CLI(2026-10-15 抓包) —— ID 按官方 id.ts
		// descending 算法生成(时间戳形, 服务端可解码校验), 同一对话复用同一
		// session ID, request ID 每次请求新生成; UA 为 SDK 拼接格式。
		// 注意: 真实 CLI 不发送 x-session-affinity/X-Session-Id/x-opencode-model,
		// 多余头不发送以贴近真实客户端。
		sess := zenSessionID(params)
		user := kit.ZenOfficialID("msg")
		req.Header.Set("Authorization", "Bearer "+cfg.Key)
		req.Header.Set("Content-Type", "application/json")
		// 指纹要求: accept-encoding 必须含 br(浏览器级压缩), 仅 gzip/identity 会被
		// FreeTierError 403 拒绝(2026-10-14 实测)。显式设置后 Go 不再自动解压,
		// 响应由 decodeZenResponse 统一还原明文。
		req.Header.Set("Accept-Encoding", "gzip, deflate, br")
		req.Header.Set("User-Agent", kit.ZenUserAgent())
		req.Header.Set("x-opencode-project", "global")
		req.Header.Set("x-opencode-session", sess)
		req.Header.Set("x-opencode-session-id", sess)
		req.Header.Set("x-opencode-request", user)
		req.Header.Set("x-opencode-client", "cli")
		log.Printf("  zen upstream: model=%s stream=%v msgs=%d via=%s attempt=%d session=%s",
			body["model"], body["stream"], getMsgCount(params), describeZenProxy(), attempt+1, kit.Truncate(sess, 24))

		resp, err := getZenHTTPClient().Do(req)
		if err != nil {
			// 网络错误:退避重试(不计入故障转移,瞬时可恢复)
			if attempt < retries {
				log.Printf("  zen network error (%v), retry %d/%d after %v", err, attempt+1, retries, delay)
				time.Sleep(kit.WithRetryJitter(delay))
				delay *= 2
				continue
			}
			return nil, rateLimited, fmt.Errorf("zen request: %w", err)
		}
		resp = decodeZenResponse(resp)
		if resp.StatusCode == http.StatusOK {
			markZenSuccess()
			if stream {
				return resp, rateLimited, nil
			}
			// 非流式调用方: 聚合上游 SSE 为完整 chat.completion JSON
			agg := collectStreamResponse(resp)
			resp.Body.Close()
			out, merr := json.Marshal(agg)
			if merr != nil {
				return nil, rateLimited, fmt.Errorf("zen stream buffer marshal: %w", merr)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(bytes.NewReader(out)),
			}, rateLimited, nil
		}

		bodyBytes := kit.ReadBody(resp)
		resp.Body.Close()
		reason := fmt.Sprintf("zen API %d: %s", resp.StatusCode, kit.Truncate(bodyBytes, 500))

		if isRateLimited(resp.StatusCode, bodyBytes) {
			rateLimited++
			// 冷却本次请求实际拨号使用的代理; 未拨号(连接复用)时回退到计数法
			idx := pick.idx
			if idx < 0 {
				idx = lastZenProxyIdx()
			}
			if idx >= 0 {
				d := parseRetryAfter(resp.Header.Get("Retry-After"))
				if d <= 0 {
					d = 10 * time.Minute
				}
				cooldownZenProxy(idx, d)
				log.Printf("  zen rate limited (%d), proxy cooldown %v", resp.StatusCode, d)
			}
			// 裸 500 只补一次: 瞬时抖动重试一次基本就能过; 如果连续 500 说明
			// 上游整体故障, 继续退避重试只会把每个请求拖到 9 秒, 不如尽快失败
			// 让 markZenFail 累计到阈值后 failover 到 Cline 账号池。
			maxTry := retries
			if resp.StatusCode == http.StatusInternalServerError {
				maxTry = 1
			}
			if attempt < maxTry {
				wait := delay
				if retryAfter := parseRetryAfter(resp.Header.Get("Retry-After")); retryAfter > wait {
					wait = retryAfter
				}
				log.Printf("  zen rate limited (%d), retry %d/%d after %v", resp.StatusCode, attempt+1, retries, wait)
				time.Sleep(kit.WithRetryJitter(wait))
				delay *= 2
				continue
			}
			markZenFail()
			return nil, rateLimited, fmt.Errorf("%s", reason)
		}

		markZenFail()
		return nil, rateLimited, fmt.Errorf("%s", reason)
	}
}

func describeZenProxy() string {
	cfg := getZenConfig()
	proxies := cfg.Proxies
	if len(proxies) == 0 {
		return "direct"
	}
	idx := lastZenProxyIdx()
	if idx < 0 {
		idx = 0
	}
	idx %= len(proxies)
	return fmt.Sprintf("proxy[%d]=%s", idx+1, kit.Truncate(maskProxyURL(proxies[idx]), 60))
}

func zenModelList() []map[string]any {
	initZenModels()
	zenModelsMu.RLock()
	out := make([]map[string]any, 0, len(zenModels))
	for _, m := range zenModels {
		if !isZenFreeModel(m) {
			continue
		}
		cp := *m
		out = append(out, map[string]any{
			"id":      cp.ID,
			"context": cp.Context,
			"output":  cp.Output,
			"source":  cp.Source,
		})
	}
	zenModelsMu.RUnlock()
	return out
}

// syncZenModels 拉取 zen /v1/models,动态合并到模型表
func syncZenModels() (int, error) {
	initZenModels()
	cfg := getZenConfig()
	endpoint := strings.TrimRight(cfg.BaseURL, "/") + "/models"
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Key)
	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0, err
	}

	zenModelsMu.Lock()
	defer zenModelsMu.Unlock()
	added := 0
	for _, item := range payload.Data {
		id := item.ID
		if id == "" {
			continue
		}
		if _, ok := zenModels[id]; ok {
			continue
		}
		// 跳过与免费模型别名冲突的 ID(如付费的 deepseek-v4-flash),保证别名解析不被覆盖
		if _, conflict := zenAliases[id]; conflict {
			continue
		}
		// 新模型:默认按 200K 上下文接入,输出按 32K
		zenModels[id] = &ZenModel{
			ID:      id,
			Context: 200000,
			Output:  32768,
			Source:  "synced",
		}
		added++
	}
	// 官方目录合并: 补齐目录中的免费模型 + 用官方元数据修正限额(目录可用时)
	added += mergeCatalogModelsLocked()
	return added, nil
}

// startZenModelsRefresher 定时同步 zen 模型列表(默认 10 分钟)
func startZenModelsRefresher() {
	go func() {
		if _, err := syncZenModels(); err != nil {
			log.Printf("zen model sync: failed (%v), using seed list", err)
		}
		ticker := time.NewTicker(10 * time.Minute)
		for range ticker.C {
			cfg := getZenConfig()
			if !cfg.Enabled {
				continue
			}
			if added, err := syncZenModels(); err != nil {
				log.Printf("zen model sync: failed (%v)", err)
			} else if added > 0 {
				log.Printf("zen model sync: %d new models from official feed", added)
			}
		}
	}()
}
