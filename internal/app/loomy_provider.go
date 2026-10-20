package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"FreeModelRelay/internal/kit"
	"FreeModelRelay/internal/loomy"
)

// ============================================================================
// 科大讯飞 Loomy 上游 provider
//
// 与 qoder/trae 同构的多账号池: 登录 session 即 Bearer token, 14 天有效、
// 无 refresh token, 但有密码即可全自动重登。所有出站请求走 kit.HTTPClient,
// 出口代理遵循统一配置(手动 > 系统代理 > 环境变量 > 直连), 不做 provider
// 级特判。
// ============================================================================

const (
	loomyGroup            = "loomy"
	loomyCooldownExhaust  = 10 * time.Minute // 402/点数耗尽
	loomyCooldownRate     = 60 * time.Second // 429/限流
	loomySessionRenewDays = 3                // session 剩余不足该天数时自动重登
)

// ============ 配置 ============

type loomyProviderConfig struct {
	Enabled    bool `json:"enabled"`
	MaxRetries int  `json:"maxRetries"`
}

func defaultLoomyConfig() *loomyProviderConfig {
	return &loomyProviderConfig{Enabled: true, MaxRetries: 1}
}

func loomyConfigPath() string { return kit.ResolveDataPath(".loomy-config.json") }

func loadLoomyConfig() *loomyProviderConfig {
	cfg := defaultLoomyConfig()
	data, err := os.ReadFile(loomyConfigPath())
	if err != nil {
		return cfg
	}
	if json.Unmarshal(data, cfg) != nil {
		return defaultLoomyConfig()
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 1
	}
	return cfg
}

func saveLoomyConfig() {
	data, err := json.MarshalIndent(loomyCfg, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(loomyConfigPath(), data, 0600)
}

func getLoomyConfig() *loomyProviderConfig {
	loomyCfgMu.Lock()
	defer loomyCfgMu.Unlock()
	c := *loomyCfg
	return &c
}

func setLoomyConfig(c *loomyProviderConfig) {
	loomyCfgMu.Lock()
	loomyCfg = c
	loomyCfgMu.Unlock()
	saveLoomyConfig()
}

// ============ 账号 ============

// loomyAccount 一个讯飞账号。Session 是真实凭据(网关 Bearer token), 必须落盘;
// Password 可为空 —— 空即 session-only 账号, session 过期后只能人工换新。
type loomyAccount struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	LoginID    string          `json:"loginId,omitempty"`
	Password   string          `json:"password,omitempty"`
	Enabled    bool            `json:"enabled"`
	Session    string          `json:"session,omitempty"`
	UserID     string          `json:"userId,omitempty"`
	Identity   *loomy.Identity `json:"identity,omitempty"`
	ObtainedAt int64           `json:"obtainedAt,omitempty"` // unix 秒
	ExpireAt   int64           `json:"expireAt,omitempty"`   // unix 秒

	// 点数缓存(运行态, 不落盘)
	balance        float64
	dailyBalance   float64
	available      float64
	quotaUpdatedAt int64

	// 运行态
	cooldownUntil time.Time
	lastError     string
	requests      int64
	pointsUsed    float64
	failures      int
}

func (a *loomyAccount) sessionValid() bool {
	return a.Session != "" && (a.ExpireAt == 0 || a.ExpireAt > time.Now().Unix())
}

func (a *loomyAccount) sessionDaysLeft() float64 {
	if a.ExpireAt == 0 {
		return -1 // 未知(导入的裸 session 未记录过期时间)
	}
	return float64(a.ExpireAt-time.Now().Unix()) / 86400
}

func (a *loomyAccount) inCooldown() bool {
	return a.cooldownUntil.After(time.Now())
}

func (a *loomyAccount) markCooldown(d time.Duration, reason string) {
	if d < time.Second {
		d = time.Second
	}
	a.cooldownUntil = time.Now().Add(d)
	a.failures++
	if reason != "" {
		a.lastError = reason
	}
}

func (a *loomyAccount) publicID() string { return a.ID }

var (
	loomyCfg      = loadLoomyConfig()
	loomyCfgMu    sync.Mutex
	loomyAccounts []*loomyAccount
	loomyAccMu    sync.RWMutex
	loomyIdx      int
	loomyModels   = map[string]loomyModelInfo{}
	loomyModelsMu sync.RWMutex
	loomyUpstream = loomy.NewClient(kit.HTTPClient)
)

func loomyAccountsFilePath() string { return kit.ResolveDataPath(".loomy-accounts.json") }

func loomyLoadAccountsLocked() []*loomyAccount {
	if loomyAccounts != nil {
		return loomyAccounts
	}
	if data, err := os.ReadFile(loomyAccountsFilePath()); err == nil {
		var list []*loomyAccount
		if json.Unmarshal(data, &list) == nil {
			loomyAccounts = list
		}
	}
	if loomyAccounts == nil {
		loomyAccounts = []*loomyAccount{}
	}
	for _, a := range loomyAccounts {
		if a.Identity != nil {
			a.Identity.Normalize()
		}
	}
	return loomyAccounts
}

func loomyLoadAccounts() []*loomyAccount {
	loomyAccMu.Lock()
	defer loomyAccMu.Unlock()
	return loomyLoadAccountsLocked()
}

func loomySaveAccountsLocked() {
	data, err := json.MarshalIndent(loomyAccounts, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(loomyAccountsFilePath(), data, 0600)
}

func loomySaveAccounts() {
	loomyAccMu.Lock()
	defer loomyAccMu.Unlock()
	loomySaveAccountsLocked()
}

// loomyPickAccount 轮询挑一个可用账号: enabled、(session 有效 或 有密码可重登)、
// 不在冷却中。
func loomyPickAccount() *loomyAccount {
	list := loomyLoadAccounts()
	var candidates []*loomyAccount
	for _, a := range list {
		if !a.Enabled {
			continue
		}
		if a.inCooldown() {
			continue
		}
		if !a.sessionValid() && a.Password == "" {
			continue
		}
		candidates = append(candidates, a)
	}
	if len(candidates) == 0 {
		return nil
	}
	loomyAccMu.Lock()
	idx := loomyIdx % len(candidates)
	loomyIdx++
	loomyAccMu.Unlock()
	return candidates[idx]
}

func loomyFindAccount(id string) *loomyAccount {
	for _, a := range loomyLoadAccounts() {
		if a.ID == id {
			return a
		}
	}
	return nil
}

// loomyEnsureSession 保证账号 session 可用: 有效即过; 失效且有密码则自动重登。
func loomyEnsureSession(a *loomyAccount) error {
	if a.sessionValid() {
		return nil
	}
	if a.Password == "" {
		return fmt.Errorf("账号 %s 的 session 已过期且未保存密码, 请重新添加", a.displayName())
	}
	sess, err := loomyUpstream.LoginByPassword(a.LoginID, a.Password, a.Identity)
	if err != nil {
		a.markCooldown(5*time.Minute, "重登失败: "+err.Error())
		return fmt.Errorf("账号 %s 重登失败: %w", a.displayName(), err)
	}
	a.Session = sess.Session
	a.UserID = sess.UserID
	a.ObtainedAt = time.Now().Unix()
	a.ExpireAt = a.ObtainedAt + loomy.SessionExpireSeconds
	a.lastError = ""
	loomySaveAccounts()
	log.Printf("  loomy: account %s session renewed (until %s)",
		maskSecret(a.LoginID), time.Unix(a.ExpireAt, 0).Format("2006-01-02"))
	return nil
}

func (a *loomyAccount) displayName() string {
	if a.Name != "" {
		return a.Name
	}
	if a.LoginID != "" {
		return maskSecret(a.LoginID)
	}
	return a.ID
}

// ============ 模型目录 ============

type loomyModelInfo struct {
	Key             string
	DisplayName     string
	Context         int
	Output          int
	Multiplier      float64 // 点数倍率, 从显示名 "(x0.8)"/"（x4.0）" 解析
	Priced          bool    // 显示名里有没有倍率段: 图片模型没有(按点数实扣), 不能当免费
	Free            bool    // 显式标价 x0.0 (限时免费)
	Reasoning       bool
	Vision          bool
	Streaming       bool
	FunctionCalling bool
	ImageGen        bool
}

// 倍率段两种形态: "Flash(x2.0)" 半角 与 "M3 （x4.0）" 全角括号
var loomyMultRe = regexp.MustCompile("[\\(（]x([0-9]+(?:\\.[0-9]+)?)[\\)）]")

// loomyIsImageModel 生图判定: 优先上游 type=image / output_modalities,
// 名称关键词兜底(上游改版丢字段时仍能识别)。
func loomyIsImageModel(id, name string, m loomy.ModelInfo) bool {
	if m.IsImageModel() {
		return true
	}
	s := strings.ToLower(id + " " + name)
	return strings.Contains(s, "image") || strings.Contains(s, "seedream") ||
		strings.Contains(s, "wanx") || strings.Contains(s, "flux")
}

// loomyMultiplierFromName 从显示名提取倍率: "Spark X2.5 (x0.1)" → 0.1。
// 提取不到返回 0 并标记 priced=false(上游未标价, 不能当免费处理)。
func loomyMultiplierFromName(name string) (float64, bool) {
	m := loomyMultRe.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	var v float64
	if _, err := fmt.Sscanf(m[1], "%g", &v); err != nil {
		return 0, false
	}
	return v, true
}

func loomySyncModels() (int, error) {
	// 找任一可用 session 同步目录(目录是账号无关的)
	var sess string
	for _, a := range loomyLoadAccounts() {
		if a.Session != "" {
			sess = a.Session
			break
		}
	}
	if sess == "" {
		return 0, fmt.Errorf("无可用 Loomy session, 请先添加账号")
	}
	list, err := loomyUpstream.FetchModels(sess)
	if err != nil {
		return 0, err
	}
	next := map[string]loomyModelInfo{}
	for _, m := range list {
		// auto 是客户端的"自动均衡"路由别名, 不是真实模型; 其余要求有 id
		if m.ID == "" || m.ID == "auto" || providerIsAliasModel(m.ID) {
			continue
		}
		mult, priced := loomyMultiplierFromName(m.Name)
		next[m.ID] = loomyModelInfo{
			Key: m.ID, DisplayName: m.Name, Context: m.ContextLength,
			Output: m.MaxOutputTokens, Multiplier: mult, Priced: priced,
			Free:      priced && mult == 0,
			Reasoning: m.Capabilities.Reasoning, Vision: m.Capabilities.Vision,
			Streaming: m.Capabilities.Streaming, FunctionCalling: m.Capabilities.FunctionCalling,
			ImageGen: loomyIsImageModel(m.ID, m.Name, m) && !m.Capabilities.FunctionCalling,
		}
	}
	if len(next) == 0 {
		return 0, fmt.Errorf("上游未返回可用模型")
	}
	loomyModelsMu.Lock()
	loomyModels = next
	loomyModelsMu.Unlock()
	log.Printf("  loomy: %d models synced", len(next))
	return len(next), nil
}

func loomyModelList() []map[string]any {
	loomyModelsMu.RLock()
	keys := make([]string, 0, len(loomyModels))
	for k := range loomyModels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	list := make([]loomyModelInfo, 0, len(keys))
	for _, k := range keys {
		list = append(list, loomyModels[k])
	}
	loomyModelsMu.RUnlock()
	dup := providerPublicIDs()
	out := make([]map[string]any, 0, len(list))
	for _, m := range list {
		publicID := m.Key
		if dup[m.Key] {
			publicID = loomyGroup + "/" + m.Key
		}
		image, video := applyModalityProbe(modalityKey(loomyGroup, m.Key), m.Vision, nil)
		out = append(out, map[string]any{
			"id":          publicID,
			"baseId":      m.Key,
			"name":        m.DisplayName,
			"context":     m.Context,
			"output":      m.Output,
			"price":       m.Multiplier,
			"priceFactor": m.Multiplier,
			"priced":      m.Priced,
			"free":        m.Free,
			"reason":      m.Reasoning,
			"image":       image,
			"video":       video,
			// 生图标志: 调用走 /v1/images/generations 而非 chat
			"imageGen": m.ImageGen,
		})
	}
	return out
}

func loomyModelSnapshot() []loomyModelInfo {
	loomyModelsMu.RLock()
	defer loomyModelsMu.RUnlock()
	out := make([]loomyModelInfo, 0, len(loomyModels))
	for _, m := range loomyModels {
		out = append(out, m)
	}
	return out
}

func loomyResolveDecorated(id string) (loomyModelInfo, bool) {
	loomyModelsMu.RLock()
	defer loomyModelsMu.RUnlock()
	for _, m := range loomyModels {
		free := m.Multiplier == 0
		if providerDecorateID(loomyGroup, m.Key, m.DisplayName, m.Multiplier, free) == id {
			return m, true
		}
		if providerDecorateID("", m.Key, m.DisplayName, m.Multiplier, false) == id {
			return m, true
		}
	}
	return loomyModelInfo{}, false
}

func loomyResolveModel(id string) (loomyModelInfo, bool) {
	loomyModelsMu.RLock()
	defer loomyModelsMu.RUnlock()
	if m, ok := loomyModels[id]; ok {
		return m, true
	}
	for _, m := range loomyModels {
		if strings.TrimSpace(m.DisplayName) == id {
			return m, true
		}
	}
	return loomyModelInfo{}, false
}

// ============ chat ============

// loomyBuildBody 上游请求体: 强制 stream(统一走 SSE 管道), 透传对话相关字段。
func loomyBuildBody(params map[string]any, key string) map[string]any {
	body := map[string]any{"model": key, "stream": true}
	for _, k := range []string{"messages", "tools", "tool_choice", "temperature",
		"top_p", "max_tokens", "stop", "response_format", "reasoning_effort"} {
		if v, ok := params[k]; ok && v != nil {
			body[k] = v
		}
	}
	if _, ok := body["messages"]; !ok {
		body["messages"] = []any{}
	}
	if msgs, ok := body["messages"].([]any); ok {
		body["messages"] = cleanMessages(msgs)
	}
	return body
}

// loomyCallChat 发起一次上游 chat。401 丢 session 重登一次; 402/429 按类型冷却。
func loomyCallChat(params map[string]any, m loomyModelInfo, a *loomyAccount) (*http.Response, error) {
	if err := loomyEnsureSession(a); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(loomyBuildBody(params, m.Key))
	if err != nil {
		return nil, err
	}

	cfg := getLoomyConfig()
	attempts := cfg.MaxRetries
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for i := 0; i <= attempts; i++ {
		req, err := loomyUpstream.NewUpstreamRequest(a.Session, http.MethodPost, "chat/completions", raw)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "text/event-stream")

		resp, err := kit.HTTPClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("上游请求失败: %w", err)
		} else if resp.StatusCode == http.StatusOK {
			a.requests++
			return resp, nil
		} else {
			errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d %s", resp.StatusCode, kit.Truncate(strings.TrimSpace(string(errBody)), 300))
			switch resp.StatusCode {
			case http.StatusUnauthorized, http.StatusForbidden:
				// session 失效: 丢掉, 下轮 ensureSession 重登
				a.Session = ""
				if a.Password == "" {
					a.markCooldown(30*time.Minute, "session 失效且无密码")
					return nil, lastErr
				}
			case http.StatusPaymentRequired:
				a.markCooldown(loomyCooldownExhaust, "点数不足")
			case http.StatusTooManyRequests:
				a.markCooldown(loomyCooldownRate, "上游限流")
			}
		}
		if i < attempts {
			time.Sleep(kit.WithRetryJitter(time.Duration(500+400*i) * time.Millisecond))
			if err := loomyEnsureSession(a); err != nil {
				return nil, err
			}
		}
	}
	a.markCooldown(time.Minute, lastErr.Error())
	return nil, lastErr
}

// loomyPrepareChat 选账号 → 换 session → 发上游请求。
func loomyPrepareChat(params map[string]any) (*http.Response, loomyModelInfo, *loomyAccount, error) {
	model, _ := params["model"].(string)
	name := providerStripPrefix(model)
	m, ok := loomyResolveModel(name)
	if !ok {
		if providerIsAliasModel(name) {
			return nil, m, nil, providerErr(http.StatusBadRequest, "invalid_request_error",
				"model %q is a placeholder alias excluded from the catalog; pick a concrete model from /v1/models", model)
		}
		return nil, m, nil, providerErr(http.StatusBadRequest, "invalid_request_error",
			"model %q is not an available loomy model", model)
	}
	if m.ImageGen {
		return nil, m, nil, providerErr(http.StatusBadRequest, "invalid_request_error",
			"model %q is an image generation model; use /v1/images/generations", m.Key)
	}
	acc := loomyPickAccount()
	if acc == nil {
		return nil, m, nil, providerErr(http.StatusServiceUnavailable, "auth_error",
			"no loomy account available; add one in /admin/ (phone + password or session)")
	}
	resp, err := loomyCallChat(params, m, acc)
	if err != nil {
		return nil, m, acc, err
	}
	return resp, m, acc, nil
}

// loomyRecordUsage 从 SSE chunk 抽 usage, 记账 token 与点数(points_consumed)。
// 闭包捕获账号, 三个协议入口(OpenAI/Anthropic/Responses)共用。
func loomyRecordUsage(a *loomyAccount) func(payload []byte) {
	if a == nil {
		return func([]byte) {}
	}
	return func(payload []byte) {
		var chunk struct {
			Usage struct {
				PointsConsumed float64 `json:"points_consumed"`
			} `json:"usage"`
		}
		if json.Unmarshal(payload, &chunk) != nil {
			return
		}
		if chunk.Usage.PointsConsumed <= 0 {
			return
		}
		loomyAccMu.Lock()
		a.pointsUsed += chunk.Usage.PointsConsumed
		loomyAccMu.Unlock()
	}
}

// loomySSEExtractFor 把 loomy 的账号记账闭包叠到标准 OpenAI extractor 上。
// 上游帧本就是 OpenAI chat.completion.chunk, 复用 wbSSEExtract。
func loomySSEExtractFor(a *loomyAccount) sseExtractor {
	record := loomyRecordUsage(a)
	return func(payload []byte) ([]byte, bool, bool) {
		out, emit, done := wbSSEExtract(payload)
		if emit {
			record(payload)
		}
		return out, emit, done
	}
}

func handleLoomyChat(w http.ResponseWriter, r *http.Request, params map[string]any) {
	if !getLoomyConfig().Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": map[string]string{"message": "loomy upstream disabled in /admin/ settings", "type": "api_error"},
		})
		return
	}
	isStream, _ := params["stream"].(bool)
	resp, m, acc, err := loomyPrepareChat(params)
	if err != nil {
		log.Printf("  loomy api error: %v", err)
		writeProviderError(w, err)
		return
	}
	defer resp.Body.Close()
	log.Printf("  loomy: model=%s account=%s stream=%v", m.Key, maskSecret(acc.displayName()), isStream)

	usageFn := providerUsageFn(m.Key)
	extract := loomySSEExtractFor(acc)
	if isStream {
		relayProviderSSE(w, resp, extract, usageFn)
		return
	}
	out, err := aggregateProviderSSE(resp, extract, usageFn)
	if err != nil {
		log.Printf("  loomy aggregate error: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]string{"message": err.Error(), "type": "api_error"},
		})
		return
	}
	out = normalizeOpenAIResponse(out)
	writeJSON(w, http.StatusOK, out)
}

// ============ 图片生成 ============

// handleImagesGenerations OpenAI Images API 入口。目前只有 loomy 上游提供
// 图片生成模型; 其他 provider 的模型走这里直接 400 并提示。
func handleImagesGenerations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var params map[string]any
	if err := readJSONBody(r, &params); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"message": err.Error(), "type": "parse_error"},
		})
		return
	}
	model, _ := params["model"].(string)
	if model == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"message": "model is required", "type": "invalid_request_error"},
		})
		return
	}
	_, route := resolveRouteModel(model)
	if route != loomyGroup {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{
				"message": fmt.Sprintf("model %q does not support image generation; loomy is the only image-capable upstream", model),
				"type":    "invalid_request_error",
			},
		})
		return
	}
	if !getLoomyConfig().Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": map[string]string{"message": "loomy upstream disabled in /admin/ settings", "type": "api_error"},
		})
		return
	}
	name := providerStripPrefix(model)
	m, ok := loomyResolveModel(name)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"message": fmt.Sprintf("model %q is not an available loomy model", model), "type": "invalid_request_error"},
		})
		return
	}
	acc := loomyPickAccount()
	if acc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": map[string]string{"message": "no loomy account available; add one in /admin/", "type": "auth_error"},
		})
		return
	}
	log.Printf("  images: model=%s account=%s", m.Key, maskSecret(acc.displayName()))
	status, out, err := loomyCallImages(params, m, acc)
	if err != nil {
		log.Printf("  images error: %v", err)
		writeJSON(w, status, map[string]any{
			"error": map[string]string{"message": err.Error(), "type": "api_error"},
		})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// loomyCallImages 透传 /v1/images/generations。上游是标准 OpenAI images API,
// 非流式 JSON 响应。计费按点数(响应无 usage 字段), 本地记次数。
func loomyCallImages(params map[string]any, m loomyModelInfo, a *loomyAccount) (int, map[string]any, error) {
	if err := loomyEnsureSession(a); err != nil {
		return 0, nil, err
	}
	body := map[string]any{"model": m.Key}
	for _, k := range []string{"prompt", "n", "size", "quality", "style", "response_format"} {
		if v, ok := params[k]; ok && v != nil {
			body[k] = v
		}
	}
	if _, ok := body["prompt"]; !ok {
		return http.StatusBadRequest, nil, fmt.Errorf("images 请求缺少 prompt")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, nil, err
	}
	req, err := loomyUpstream.NewUpstreamRequest(a.Session, http.MethodPost, "images/generations", raw)
	if err != nil {
		return 0, nil, err
	}
	resp, err := kit.HTTPClient.Do(req)
	if err != nil {
		return http.StatusBadGateway, nil, fmt.Errorf("上游请求失败: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusPaymentRequired {
			a.markCooldown(loomyCooldownExhaust, "点数不足")
		}
		return resp.StatusCode, nil, fmt.Errorf("HTTP %d %s", resp.StatusCode, kit.Truncate(strings.TrimSpace(string(respBody)), 300))
	}
	var out map[string]any
	if err := json.Unmarshal(respBody, &out); err != nil {
		return http.StatusBadGateway, nil, fmt.Errorf("图片响应解析失败: %w", err)
	}
	loomyAccMu.Lock()
	a.requests++
	loomyAccMu.Unlock()
	// 上游可能静默回退模型, 以响应里的 model 字段为准告警
	if outModel, _ := out["model"].(string); outModel != "" && outModel != m.Key {
		log.Printf("  loomy images: requested %s but upstream served %s", m.Key, outModel)
	}
	return http.StatusOK, out, nil
}

// ============ 点数 / 状态 ============

// loomyRefreshPoints 拉一次点数余额并缓存到账号。
func loomyRefreshPoints(a *loomyAccount) error {
	if a.Session == "" {
		return fmt.Errorf("无 session")
	}
	p, err := loomyUpstream.FetchPoints(a.Session)
	if err != nil {
		return err
	}
	loomyAccMu.Lock()
	a.balance = p.Balance
	a.dailyBalance = p.DailyBalance
	a.available = p.AvailableBalance
	a.quotaUpdatedAt = time.Now().UnixMilli()
	loomyAccMu.Unlock()
	return nil
}

func loomyStatusPayload() map[string]any {
	cfg := getLoomyConfig()
	accounts := loomyLoadAccounts()
	list := make([]map[string]any, 0, len(accounts))
	for _, a := range accounts {
		loomyAccMu.RLock()
		item := map[string]any{
			"id":             a.ID,
			"name":           a.Name,
			"loginId":        a.LoginID,
			"userId":         a.UserID,
			"enabled":        a.Enabled,
			"hasPassword":    a.Password != "",
			"sessionTail":    loomyTailSecret(a.Session),
			"daysLeft":       a.sessionDaysLeft(),
			"expireAtText":   loomySecToText(a.ExpireAt),
			"balance":        a.balance,
			"dailyBalance":   a.dailyBalance,
			"available":      a.available,
			"quotaUpdatedAt": a.quotaUpdatedAt,
			"requests":       a.requests,
			"pointsUsed":     a.pointsUsed,
			"failures":       a.failures,
			"inCooldown":     a.inCooldown(),
			"lastError":      a.lastError,
			"identityBound":  a.Identity != nil,
			"devid":          identityDevid(a.Identity),
		}
		loomyAccMu.RUnlock()
		list = append(list, item)
	}
	models := loomyModelList()
	return map[string]any{
		"id":          loomyGroup,
		"displayName": "科大讯飞 Loomy",
		"region":      "china",
		"chatBase":    loomy.DefaultModelBase,
		"accountBase": loomy.DefaultAccountBase,
		"enabled":     cfg.Enabled,
		"maxRetries":  cfg.MaxRetries,
		"importable":  false,
		"accounts":    list,
		"models":      models,
		"modelCount":  len(models),
	}
}

func loomyCreditsPayload() map[string]any {
	accounts := loomyLoadAccounts()
	total := 0.0
	perAcc := make([]map[string]any, 0, len(accounts))
	for _, a := range accounts {
		if !a.Enabled {
			continue
		}
		loomyAccMu.RLock()
		if a.available > 0 || a.balance > 0 || a.dailyBalance > 0 {
			perAcc = append(perAcc, map[string]any{
				"name":      a.displayName(),
				"available": a.available,
				"balance":   a.balance,
				"daily":     a.dailyBalance,
			})
			total += a.available
		}
		loomyAccMu.RUnlock()
	}
	// 与 wb/qoder/trae 的 credits 结构对齐, 仪表盘与 tab 的通用渲染器直接可用;
	// 讯飞无签到活动(每日赠送点数自动发放), checkin.active=false 即可。
	credits := map[string]any{"unit": "points", "planName": "点数余额（永久+每日赠送）"}
	if len(perAcc) > 0 {
		credits["remain"] = total
		credits["total"] = total
	} else {
		credits["remain"] = 0.0
		credits["total"] = 0.0
	}
	return map[string]any{
		"credits":  credits,
		"checkin":  map[string]any{"active": false},
		"accounts": perAcc,
	}
}

// loomyRefreshAccount 手动刷新: 可用时重登(session 快到期) + 拉点数。
func loomyRefreshAccount(a *loomyAccount) error {
	if a.Password != "" {
		a.Session = "" // 强制重登
		if err := loomyEnsureSession(a); err != nil {
			return err
		}
	}
	return loomyRefreshPoints(a)
}

// loomySessionKeeper 后台协程: 启动先跑一轮(点数缓存是内存态, 重启即失),
// 之后每 6 小时检查一次 —— session 剩余不足 3 天且有密码的账号自动重登
// (14 天 session, 无 refresh token), 顺带刷新点数缓存。
func loomySessionKeeper() {
	for round := 0; ; round++ {
		if round > 0 {
			time.Sleep(6 * time.Hour)
		}
		// 每轮先跟随本机客户端: 客户端重新登录后 auth-session.json 原地更新,
		// 失效的 session-only 账号自动换成新 session
		if imp, upd, note := loomySyncLocalSessions(); imp > 0 || upd > 0 {
			log.Printf("  loomy keeper: local sync +%d imported, %d updated (%s)", imp, upd, note)
		}
		for _, a := range loomyLoadAccounts() {
			if !a.Enabled {
				continue
			}
			if a.Password != "" && a.ExpireAt > 0 &&
				a.ExpireAt-time.Now().Unix() < loomySessionRenewDays*86400 {
				if err := loomyEnsureSession(a); err != nil {
					log.Printf("  loomy keeper: %v", err)
					continue
				}
			}
			if err := loomyRefreshPoints(a); err != nil {
				log.Printf("  loomy keeper: %s 点数刷新失败: %v", maskSecret(a.displayName()), err)
			}
			time.Sleep(3 * time.Second) // 账号间错峰
		}
	}
}

// ============ 账号增删 ============

// loomyAddByPassword 手机号+密码登录并入库。按 loginid 去重(存在则更新密码重登)。
func loomyAddByPassword(loginid, password, name string) (*loomyAccount, error) {
	loginid = strings.TrimSpace(loginid)
	if loginid == "" || password == "" {
		return nil, fmt.Errorf("手机号与密码不能为空")
	}
	list := loomyLoadAccounts()
	var acc *loomyAccount
	for _, a := range list {
		if a.LoginID == loginid {
			acc = a
			break
		}
	}
	if acc == nil {
		acc = &loomyAccount{
			ID:   "loomy_" + randHex(6),
			Name: name,
		}
		loomyAccMu.Lock()
		loomyAccounts = append(loomyAccounts, acc)
		loomyAccMu.Unlock()
	}
	if acc.Identity == nil {
		acc.Identity = loomy.NewIdentity()
	}
	acc.LoginID = loginid
	acc.Password = password
	if name != "" {
		acc.Name = name
	}
	sess, err := loomyUpstream.LoginByPassword(loginid, password, acc.Identity)
	if err != nil {
		acc.lastError = err.Error()
		loomySaveAccounts()
		return acc, err
	}
	acc.Session = sess.Session
	acc.UserID = sess.UserID
	acc.ObtainedAt = time.Now().Unix()
	acc.ExpireAt = acc.ObtainedAt + loomy.SessionExpireSeconds
	acc.lastError = ""
	acc.Enabled = true
	loomySaveAccounts()
	loomyRefreshPoints(acc)
	return acc, nil
}

// loomyAddBySession 导入裸 session(无密码): 从本机客户端或他处拿到的登录态。
func loomyAddBySession(session, name string) (*loomyAccount, error) {
	session = strings.TrimSpace(session)
	if session == "" {
		return nil, fmt.Errorf("session 不能为空")
	}
	c := loomy.NewClient(kit.HTTPClient)
	if _, err := c.FetchModels(session); err != nil {
		return nil, fmt.Errorf("session 校验失败: %w", err)
	}
	acc := &loomyAccount{
		ID:       "loomy_" + randHex(6),
		Name:     name,
		Session:  session,
		Identity: loomy.NewIdentity(),
		Enabled:  true,
	}
	loomyAccMu.Lock()
	loomyAccounts = append(loomyAccounts, acc)
	loomyAccMu.Unlock()
	loomySaveAccounts()
	loomyRefreshPoints(acc)
	return acc, nil
}

// loomyAddBySMS 短信验证码登录并入库(两步式: /sms/send 先拿到 msgid)。
func loomyAddBySMS(phone, code, msgid, name string) (*loomyAccount, error) {
	list := loomyLoadAccounts()
	var acc *loomyAccount
	for _, a := range list {
		if a.LoginID == phone {
			acc = a
			break
		}
	}
	if acc == nil {
		acc = &loomyAccount{
			ID:       "loomy_" + randHex(6),
			Identity: loomy.NewIdentity(),
		}
		loomyAccMu.Lock()
		loomyAccounts = append(loomyAccounts, acc)
		loomyAccMu.Unlock()
	}
	if acc.Identity == nil {
		acc.Identity = loomy.NewIdentity()
	}
	sess, err := loomyUpstream.LoginBySMS(phone, code, msgid, acc.Identity)
	if err != nil {
		acc.lastError = err.Error()
		loomySaveAccounts()
		return acc, err
	}
	acc.LoginID = phone
	acc.Session = sess.Session
	acc.UserID = sess.UserID
	acc.ObtainedAt = time.Now().Unix()
	acc.ExpireAt = acc.ObtainedAt + loomy.SessionExpireSeconds
	acc.lastError = ""
	acc.Enabled = true
	if name != "" {
		acc.Name = name
	}
	loomySaveAccounts()
	loomyRefreshPoints(acc)
	return acc, nil
}

func loomyDeleteAccount(id string) bool {
	loomyAccMu.Lock()
	defer loomyAccMu.Unlock()
	list := loomyLoadAccountsLocked()
	out := list[:0]
	removed := false
	for _, a := range list {
		if a.ID == id {
			removed = true
			continue
		}
		out = append(out, a)
	}
	loomyAccounts = out
	if removed {
		loomySaveAccountsLocked()
	}
	return removed
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func loomyTailSecret(s string) string {
	if len(s) <= 10 {
		return ""
	}
	return "…" + s[len(s)-6:]
}

func loomySecToText(sec int64) string {
	if sec <= 0 {
		return ""
	}
	return time.Unix(sec, 0).Format("2006-01-02 15:04:05")
}

func identityDevid(id *loomy.Identity) string {
	if id == nil {
		return ""
	}
	return id.Devid
}

// ============ 本机客户端导入 ============

// loomyLocalSession 官方客户端落盘的登录态文件(userData/auth-session.json):
// session 明文 + userid + 脱敏手机号。客户端重新登录后文件原地更新。
type loomyLocalSession struct {
	Session   string `json:"session"`
	UserID    string `json:"userid"`
	Phone     string `json:"phone"`
	UpdatedAt int64  `json:"updatedAt"`
}

// loomyLocalSessionGlobs 客户端数据目录。Windows 是 C:\Users\Public\Loomy\<sha256(user)[:12]>\userData\,
// macOS 按 Electron 惯例在 ~/Library/Application Support/Loomy/ 下。
func loomyLocalSessionGlobs() []string {
	globs := []string{
		filepath.Join("C:", "Users", "Public", "Loomy", "*", "userData", "auth-session.json"),
	}
	if public := os.Getenv("PUBLIC"); public != "" {
		globs = append(globs, filepath.Join(public, "Loomy", "*", "userData", "auth-session.json"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		globs = append(globs, filepath.Join(home, "Library", "Application Support", "Loomy", "*", "userData", "auth-session.json"))
	}
	return globs
}

func loomyReadLocalSessions() []loomyLocalSession {
	var out []loomyLocalSession
	seen := map[string]bool{}
	for _, pattern := range loomyLocalSessionGlobs() {
		files, _ := filepath.Glob(pattern)
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			var ls loomyLocalSession
			if json.Unmarshal(data, &ls) != nil || ls.Session == "" || seen[ls.Session] {
				continue
			}
			seen[ls.Session] = true
			out = append(out, ls)
		}
	}
	return out
}

// loomyLocalSyncMu boot 与 keeper 可能并发跑本机同步, 互斥避免同一 session
// 双导入(dup 检查与 append 之间有窗口)。
var loomyLocalSyncMu sync.Mutex

// loomySyncLocalSessions 检测本机 Loomy 客户端登录态并同步进账号池:
//   - 全新 session → 导入为新账号(session-only, 带客户端给的 userid/手机号)
//   - 已入库的 session-only 账号失效且客户端重新登录过(同 userid) → 跟随更新
//   - 已存在相同 session → 跳过
//
// 每个候选 session 先打一次点数接口验证有效性, 过期登录态不入库。
// 返回 (导入数, 跟随更新数, 说明)。
func loomySyncLocalSessions() (int, int, string) {
	sessions := loomyReadLocalSessions()
	if len(sessions) == 0 {
		return 0, 0, "本机未检测到 Loomy 客户端登录态"
	}
	loomyLocalSyncMu.Lock()
	defer loomyLocalSyncMu.Unlock()
	imported, updated, skipped := 0, 0, 0
	var notes []string
	validator := loomy.NewClient(kit.HTTPClient)
	for _, ls := range sessions {
		// 客户端自己维护的登录态一般是活的, 但也可能过期闲置 —— 先验证再入库
		if _, err := validator.FetchPoints(ls.Session); err != nil {
			notes = append(notes, fmt.Sprintf("%s 的 session 已失效", loomyMaskPhone(ls.Phone)))
			continue
		}
		accounts := loomyLoadAccounts()
		dup := false
		for _, a := range accounts {
			if a.Session == ls.Session {
				dup = true
				break
			}
		}
		if dup {
			skipped++
			continue
		}
		// 同 userid 的失效 session-only 账号 → 跟随客户端重新登录
		if ls.UserID != "" {
			matched := false
			for _, a := range accounts {
				if a.UserID == ls.UserID && a.Password == "" && a.Session != ls.Session {
					a.Session = ls.Session
					a.lastError = ""
					loomySaveAccounts()
					loomyRefreshPoints(a)
					updated++
					matched = true
					notes = append(notes, fmt.Sprintf("%s 的 session 已跟随客户端更新", a.displayName()))
					break
				}
			}
			if matched {
				continue
			}
		}
		acc := &loomyAccount{
			ID:       "loomy_" + randHex(6),
			Name:     loomyLocalAccountName(ls),
			LoginID:  ls.Phone,
			UserID:   ls.UserID,
			Session:  ls.Session,
			Identity: loomy.NewIdentity(),
			Enabled:  true,
		}
		loomyAccMu.Lock()
		loomyAccounts = append(loomyAccounts, acc)
		loomyAccMu.Unlock()
		loomySaveAccounts()
		loomyRefreshPoints(acc)
		imported++
		notes = append(notes, fmt.Sprintf("已导入 %s (可用点数 %.0f)", acc.displayName(), acc.available))
	}
	if skipped > 0 && imported == 0 && updated == 0 {
		notes = append(notes, fmt.Sprintf("%d 个本机 session 已在账号池", skipped))
	}
	return imported, updated, strings.Join(notes, "; ")
}

func loomyLocalAccountName(ls loomyLocalSession) string {
	if ls.Phone != "" {
		return "本机 " + ls.Phone
	}
	return "本机 Loomy 客户端"
}

func loomyMaskPhone(p string) string {
	if p == "" {
		return "未知账号"
	}
	return p
}

// ============ 后台 API handlers ============

// handleLoomyAccountAdd 三种添加方式, 按 body 里的 mode 分发:
//
//	password — {mode:"password", phone, password, name?} 服务端直接登录验证
//	session  — {mode:"session", session, name?} 导入裸登录态(本机客户端/他处)
//	sms      — 走 /sms/send + /sms/verify 两步, 不在此入口
func handleLoomyAccountAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	var req struct {
		Mode     string `json:"mode"`
		Phone    string `json:"phone"`
		Password string `json:"password"`
		Session  string `json:"session"`
		Name     string `json:"name"`
	}
	if err := readJSONBody(r, &req); err != nil {
		writeAPI(w, http.StatusBadRequest, apiResponse{Error: "invalid JSON: " + err.Error()})
		return
	}
	switch req.Mode {
	case "", "password":
		acc, err := loomyAddByPassword(req.Phone, req.Password, req.Name)
		if err != nil {
			writeAPI(w, http.StatusBadGateway, apiResponse{
				Error:   "登录失败: " + err.Error(),
				Message: "请确认手机号与密码(需在讯飞账号中心设置过密码), 或改用短信验证码/导入 session",
			})
			return
		}
		msg := fmt.Sprintf("已添加 Loomy 账号 %s, session 有效期至 %s",
			acc.displayName(), loomySecToText(acc.ExpireAt))
		if n, err := loomySyncModels(); err == nil {
			msg += fmt.Sprintf(", 已同步 %d 个模型", n)
		}
		writeAPI(w, http.StatusOK, apiResponse{Success: true, Message: msg})
	case "session":
		acc, err := loomyAddBySession(req.Session, req.Name)
		if err != nil {
			writeAPI(w, http.StatusBadGateway, apiResponse{
				Error:   err.Error(),
				Message: "session 可从本机 Loomy 客户端 %USERPROFILE% 相关目录或其它部署导出",
			})
			return
		}
		writeAPI(w, http.StatusOK, apiResponse{Success: true,
			Message: fmt.Sprintf("已导入 Loomy 账号 %s (session-only, 过期后需重新导入)", acc.displayName())})
	default:
		writeAPI(w, http.StatusBadRequest, apiResponse{Error: "unknown mode: " + req.Mode})
	}
}

// handleLoomyAccountDelete POST {id}
func handleLoomyAccountDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	readJSONBody(r, &req)
	if !loomyDeleteAccount(req.ID) {
		writeAPI(w, http.StatusNotFound, apiResponse{Error: "账号不存在"})
		return
	}
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Message: "已删除账号"})
}

// handleLoomySMSSend POST {phone} → {msgid}。发码不建立会话, msgid 由前端
// 保存并在 /sms/verify 时回传。
func handleLoomySMSSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	var req struct {
		Phone string `json:"phone"`
	}
	if err := readJSONBody(r, &req); err != nil {
		writeAPI(w, http.StatusBadRequest, apiResponse{Error: "invalid JSON: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.Phone) == "" {
		writeAPI(w, http.StatusBadRequest, apiResponse{Error: "手机号不能为空"})
		return
	}
	msgid, err := loomyUpstream.SendSMSCode(strings.TrimSpace(req.Phone), loomy.NewIdentity())
	if err != nil {
		writeAPI(w, http.StatusBadGateway, apiResponse{Error: "发送验证码失败: " + err.Error()})
		return
	}
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Message: "验证码已发送", Data: map[string]string{"msgid": msgid}})
}

// handleLoomySMSVerify POST {phone, code, msgid, name?} → 验证码登录并入库。
func handleLoomySMSVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	var req struct {
		Phone string `json:"phone"`
		Code  string `json:"code"`
		MsgID string `json:"msgid"`
		Name  string `json:"name"`
	}
	if err := readJSONBody(r, &req); err != nil {
		writeAPI(w, http.StatusBadRequest, apiResponse{Error: "invalid JSON: " + err.Error()})
		return
	}
	phone := strings.TrimSpace(req.Phone)
	if phone == "" || req.Code == "" || req.MsgID == "" {
		writeAPI(w, http.StatusBadRequest, apiResponse{Error: "phone/code/msgid 均不能为空"})
		return
	}
	acc, err := loomyAddBySMS(phone, req.Code, req.MsgID, req.Name)
	if err != nil {
		writeAPI(w, http.StatusBadGateway, apiResponse{Error: "验证失败: " + err.Error()})
		return
	}
	msg := fmt.Sprintf("已添加 Loomy 账号 %s, session 有效期至 %s",
		acc.displayName(), loomySecToText(acc.ExpireAt))
	if n, err := loomySyncModels(); err == nil {
		msg += fmt.Sprintf(", 已同步 %d 个模型", n)
	}
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Message: msg})
}

// handleLoomyRefresh POST {id}(可空) — 强制重登(session 快到期/异常时) + 刷新点数。
// id 为空时刷新全部账号。
func handleLoomyRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	readJSONBody(r, &req)

	targets := []*loomyAccount{}
	if req.ID != "" {
		if a := loomyFindAccount(req.ID); a != nil {
			targets = append(targets, a)
		}
	} else {
		targets = loomyLoadAccounts()
	}
	if len(targets) == 0 {
		writeAPI(w, http.StatusBadGateway, apiResponse{Error: "没有可刷新的 Loomy 账号"})
		return
	}
	ok, fail := 0, 0
	var errs []string
	for _, a := range targets {
		if err := loomyRefreshAccount(a); err != nil {
			fail++
			errs = append(errs, maskSecret(a.displayName())+": "+err.Error())
			continue
		}
		ok++
	}
	resp := apiResponse{Success: fail == 0, Message: fmt.Sprintf("刷新成功 %d 个, 失败 %d 个", ok, fail)}
	if len(errs) > 0 {
		resp.Error = strings.Join(errs, "; ")
	}
	writeAPI(w, http.StatusOK, resp)
}

// handleLoomyAccountToggle POST {id, enabled} — 单账号启停。
func handleLoomyAccountToggle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	var req struct {
		ID      string `json:"id"`
		Enabled *bool  `json:"enabled"`
	}
	if err := readJSONBody(r, &req); err != nil {
		writeAPI(w, http.StatusBadRequest, apiResponse{Error: "invalid JSON: " + err.Error()})
		return
	}
	a := loomyFindAccount(req.ID)
	if a == nil {
		writeAPI(w, http.StatusNotFound, apiResponse{Error: "账号不存在"})
		return
	}
	if req.Enabled != nil {
		a.Enabled = *req.Enabled
		loomySaveAccounts()
	}
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Message: "已更新账号状态"})
}
