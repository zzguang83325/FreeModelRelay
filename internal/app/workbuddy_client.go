package app

import (
	"bytes"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"FreeModelRelay/internal/kit"
)

// ============================================================================
// WorkBuddy 上游客户端: 凭据池 + 出站头指纹 + 模型目录 + chat
// ============================================================================

type wbProviderConfig struct {
	Enabled    bool     `json:"enabled"`
	Proxies    []string `json:"proxies"`
	MaxRetries int      `json:"maxRetries"`
	CN         bool     `json:"cn"`
	INTL       bool     `json:"intl"`
}

func defaultWBConfig() *wbProviderConfig {
	return &wbProviderConfig{Enabled: true, MaxRetries: 2, CN: true, INTL: true}
}

var (
	wbCfg      = loadWBConfig()
	wbCfgMu    sync.Mutex
	wbAccounts []*wbAccount
	wbAccMu    sync.RWMutex
	wbIdx      int
	wbModels   = map[string]wbModelInfo{}
	wbModelsMu sync.RWMutex
)

// wbModelKey 模型表的键: CN 与 INTL 目录存在同名模型(如 hy3), 必须按变体隔离,
// 否则后同步的变体会覆盖先同步的。
func wbModelKey(variant, id string) string { return variant + "/" + id }

type wbModelInfo struct {
	ID         string
	Name       string
	Variant    string
	Context    int
	Output     int
	Credits    string
	Multiplier float64 // credits 倍率, 0 = 免费
	Reason     bool
	Tools      bool
	IsDefault  bool
	// Available 由可用性探测写入。上游目录不区分"已接入/未接入" —— 9 个模型
	// (glm-4.6 / minimax-m2.5 / kimi-k2-thinking 等)在目录里但调用必回 11102
	// "service info not found", 所以只能在同步后实调一次才能判定。
	Available bool
	ProbeNote string
	ProbedAt  int64
	// Multimodal 来自上游目录的 disabledMultimodal 取反。上游只有一个总开关,
	// 不区分图片与视频: true 只能证明"支持多模态输入"(图片按支持处理,
	// 视频仍记为未知); false 则两者都不支持。
	Multimodal bool
}

func wbConfigPath() string { return kit.ResolveDataPath(".workbuddy-config.json") }

func loadWBConfig() *wbProviderConfig {
	cfg := defaultWBConfig()
	data, err := os.ReadFile(wbConfigPath())
	if err != nil {
		return cfg
	}
	if json.Unmarshal(data, cfg) != nil {
		return cfg
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 2
	}
	return cfg
}

func saveWBConfig() {
	data, err := json.MarshalIndent(wbCfg, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(wbConfigPath(), data, 0600)
}

func getWBConfig() *wbProviderConfig {
	wbCfgMu.Lock()
	defer wbCfgMu.Unlock()
	return &wbProviderConfig{
		Enabled: wbCfg.Enabled, Proxies: append([]string{}, wbCfg.Proxies...),
		MaxRetries: wbCfg.MaxRetries, CN: wbCfg.CN, INTL: wbCfg.INTL,
	}
}

func setWBConfig(c *wbProviderConfig) {
	wbCfgMu.Lock()
	wbCfg = c
	wbCfgMu.Unlock()
	saveWBConfig()
}

func wbVariantEnabled(v *wbVariant) bool {
	cfg := getWBConfig()
	if v.Region == "cn" {
		return cfg.CN
	}
	return cfg.INTL
}

// ============ 账号池 ============

// wbLoadAccounts 从 data 目录加载已保存账号, 首次为空时自动从本机桌面端导入。
func wbLoadAccounts() []*wbAccount {
	wbAccMu.Lock()
	defer wbAccMu.Unlock()
	if wbAccounts != nil {
		return wbAccounts
	}
	wbAccounts = wbReadAccountsFile()
	if len(wbAccounts) == 0 {
		imported, notes := wbReadLocalAccounts()
		for _, n := range notes {
			log.Printf("  workbuddy import: %s", n)
		}
		if len(imported) > 0 {
			wbAccounts = imported
			wbSaveAccountsLocked()
			for _, a := range imported {
				log.Printf("  workbuddy import: %s %s", a.Variant, maskSecret(a.Email))
			}
		}
	}
	return wbAccounts
}

func wbAccountsFilePath() string { return kit.ResolveDataPath(".workbuddy-accounts.json") }

func wbReadAccountsFile() []*wbAccount {
	data, err := os.ReadFile(wbAccountsFilePath())
	if err != nil {
		return nil
	}
	var list []*wbAccount
	if json.Unmarshal(data, &list) != nil {
		return nil
	}
	return list
}

func wbSaveAccountsLocked() {
	data, err := json.MarshalIndent(wbAccounts, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(wbAccountsFilePath(), data, 0600)
}

func wbSaveAccounts() {
	wbAccMu.Lock()
	defer wbAccMu.Unlock()
	wbSaveAccountsLocked()
}

func wbActiveAccounts(variant string) []*wbAccount {
	wbLoadAccounts()
	wbAccMu.RLock()
	defer wbAccMu.RUnlock()
	var out []*wbAccount
	for _, a := range wbAccounts {
		if variant == "" || a.Variant == variant {
			out = append(out, a)
		}
	}
	return out
}

func wbPickAccount(variant string) *wbAccount {
	list := wbActiveAccounts(variant)
	if len(list) == 0 {
		return nil
	}
	wbAccMu.Lock()
	idx := wbIdx % len(list)
	wbIdx++
	wbAccMu.Unlock()
	acc := list[idx]
	if acc.AccessToken == "" || (acc.ExpiresAt > 0 && time.Now().UnixMilli() >= acc.ExpiresAt-60000) {
		if err := wbRefreshAccount(acc); err != nil {
			log.Printf("  workbuddy refresh for %s failed: %v", maskSecret(acc.Email), err)
		}
	}
	return acc
}

// wbRefreshToken 调用官方 plugin 刷新端点换新 accessToken。
func wbRefreshAccount(a *wbAccount) error {
	v := wbVariantByID(a.Variant)
	if v == nil || a.RefreshToken == "" {
		return fmt.Errorf("no refresh token")
	}
	url := v.ChatBase + "/v2/plugin/auth/token/refresh"
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	wbCommonHeaders(req, a, v)
	req.Header.Set("X-Refresh-Token", a.RefreshToken)
	req.Header.Set("X-Auth-Refresh-Source", "plugin")

	client := kit.NewHTTPClient(30 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		a.LastError = err.Error()
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		a.LastError = fmt.Sprintf("HTTP %d %s", resp.StatusCode, kit.Truncate(strings.TrimSpace(string(body)), 200))
		return fmt.Errorf("%s", a.LastError)
	}
	// 上游把令牌包在 {code,msg,requestId,data:{...}} 信封里;
	// 老版本参考实现读的是顶层字段, 这里两种形态都兼容。
	var raw struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int64  `json:"expiresIn"`
		Domain       string `json:"domain"`
		Data         struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
			ExpiresIn    int64  `json:"expiresIn"`
			Domain       string `json:"domain"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		a.LastError = "刷新响应解析失败"
		return err
	}
	out := raw
	if out.AccessToken == "" && raw.Data.AccessToken != "" {
		out.AccessToken = raw.Data.AccessToken
		out.RefreshToken = raw.Data.RefreshToken
		out.ExpiresIn = raw.Data.ExpiresIn
		out.Domain = raw.Data.Domain
	}
	if out.AccessToken == "" {
		a.LastError = fmt.Sprintf("刷新响应缺少 accessToken: %s", kit.Truncate(strings.TrimSpace(string(body)), 200))
		return fmt.Errorf("%s", a.LastError)
	}
	a.AccessToken = out.AccessToken
	if out.RefreshToken != "" {
		a.RefreshToken = out.RefreshToken
	}
	if out.ExpiresIn > 0 {
		a.ExpiresAt = time.Now().UnixMilli() + out.ExpiresIn*1000
	}
	a.LastError = ""
	wbSaveAccounts()
	log.Printf("  workbuddy token refreshed for %s", maskSecret(a.Email))
	return nil
}

// ============ 出站请求头(官方桌面端指纹) ============

func wbRandomHexBytes(n int) string {
	b := make([]byte, n)
	crand.Read(b)
	return hex.EncodeToString(b)
}

func wbUserAgent(v *wbVariant) string {
	return "WorkBuddy/" + wbClientVersion + " " + v.Platform + "/" + wbClientVersion + " CLI/" + wbCliVersion
}

// wbCommonHeaders 共享头: Origin/Referer/UA/本地化。global 送错平台段会 403 11140。
func wbCommonHeaders(req *http.Request, a *wbAccount, v *wbVariant) {
	h := req.Header
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json, text/event-stream")
	h.Set("X-Requested-With", "XMLHttpRequest")
	h.Set("Origin", v.Origin)
	h.Set("Referer", v.Origin+"/")
	h.Set("User-Agent", wbUserAgent(v))
	h.Set("X-CodeBuddy-Request", "1")
	h.Set("Accept-Language", v.AcceptLanguage)
	h.Set("X-Domain", strings.TrimPrefix(strings.TrimPrefix(v.Origin, "https://"), "http://"))
	if a != nil && a.AccessToken != "" {
		h.Set("Authorization", "Bearer "+a.AccessToken)
	}
	if a != nil && a.UID != "" {
		h.Set("X-User-Id", a.UID)
	}
	if a != nil && a.MachineID != "" {
		h.Set("X-Device-Token", a.MachineID)
	}
}

// wbChatHeaders 追加归属/追踪头(伪造官方桌面端 citation 指纹)。
func wbChatHeaders(req *http.Request, a *wbAccount, v *wbVariant, conversationID string) {
	wbCommonHeaders(req, a, v)
	h := req.Header
	name := v.Platform
	convReqID := wbRandomHexBytes(16)
	messageID := wbRandomHexBytes(16)
	h.Set("X-Agent-Purpose", "conversation")
	h.Set("X-IDE-Name", name)
	h.Set("X-IDE-Type", name)
	h.Set("X-IDE-Version", wbClientVersion)
	h.Set("X-Product", name)
	h.Set("X-Conversation-Request-ID", convReqID)
	h.Set("X-Conversation-Message-ID", messageID)
	h.Set("X-Request-ID", messageID)
	h.Set("X-Root-Request-ID", convReqID)
	h.Set("X-Trace-ID", convReqID)
	h.Set("X-B3-TraceId", convReqID)
	h.Set("X-B3-SpanId", messageID[:16])
	h.Set("X-B3-Sampled", "1")
	h.Set("X-No-Enterprise-Id", "1")
	if conversationID != "" {
		h.Set("X-Conversation-ID", conversationID)
	}
}

// ============ 模型目录 ============

type wbModelsResponse struct {
	Code int `json:"code"`
	Data struct {
		Endpoint string `json:"endpoint"`
		Models   []struct {
			ID                 string `json:"id"`
			Name               string `json:"name"`
			Credits            string `json:"credits"`
			MaxInputTokens     int    `json:"maxInputTokens"`
			MaxOutputTokens    int    `json:"maxOutputTokens"`
			SupportsReasoning  bool   `json:"supportsReasoning"`
			SupportsToolCall   bool   `json:"supportsToolCall"`
			IsDefault          bool   `json:"isDefault"`
			DisabledMultimodal bool   `json:"disabledMultimodal"`
		} `json:"models"`
	} `json:"data"`
}

// wbSyncModels 拉取两变体完整模型目录(含付费), 并合并可用性探测缓存。
// 目录条目会立即生效(默认视为可用), 探测在后台补齐 —— 否则手动同步要等 33 次
// 实调, /admin 的 fetch 会先超时。
func wbSyncModels() (int, error) {
	added := 0
	var lastErr error
	probe := wbProbeCacheLoad()
	for _, v := range wbVariants {
		if !wbVariantEnabled(v) {
			continue
		}
		acc := wbPickAccount(v.ID)
		if acc == nil {
			lastErr = fmt.Errorf("%s: 无可用账号", v.DisplayName)
			continue
		}
		models, err := wbFetchModels(v, acc)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", v.DisplayName, err)
			continue
		}
		stale := make([]wbModelInfo, 0, len(models))
		for i := range models {
			models[i].Available = true
			if e, ok := probe[wbModelKey(v.ID, models[i].ID)]; ok && time.Now().UnixMilli()-e.CheckedAt < wbProbeTTLMillis {
				models[i].Available = e.Available
				models[i].ProbeNote = e.Note
				models[i].ProbedAt = e.CheckedAt
			} else {
				stale = append(stale, models[i])
			}
		}
		wbModelsMu.Lock()
		// 清掉该变体的旧模型后重填(键为 variant/id, 不会误删另一变体)
		for k, m := range wbModels {
			if m.Variant == v.ID {
				delete(wbModels, k)
			}
		}
		for _, m := range models {
			wbModels[wbModelKey(v.ID, m.ID)] = m
			added++
		}
		wbModelsMu.Unlock()
		nFree, nAvail := 0, 0
		for _, m := range models {
			if m.Multiplier == 0 {
				nFree++
			}
			if m.Available {
				nAvail++
			}
		}
		log.Printf("  workbuddy %s: %d models (%d free, %d available)", v.ID, len(models), nFree, nAvail)
		if len(stale) > 0 {
			go wbProbeModelsAsync(v, acc, stale, false)
		}
	}
	return added, lastErr
}

func wbFetchModels(v *wbVariant, a *wbAccount) ([]wbModelInfo, error) {
	paths := []string{"/v2/enterprises/personal/models", "/console/enterprises/personal/models"}
	var lastErr error
	for _, p := range paths {
		url := v.ChatBase + p
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		wbCommonHeaders(req, a, v)
		client := kit.NewHTTPClient(25 * time.Second)
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			lastErr = fmt.Errorf("HTTP %d %s", resp.StatusCode, kit.Truncate(strings.TrimSpace(string(body)), 160))
			continue
		}
		var parsed wbModelsResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			lastErr = err
			continue
		}
		var out []wbModelInfo
		for _, m := range parsed.Data.Models {
			if m.ID == "" {
				continue
			}
			// auto / default 等是"让上游自己挑"的占位别名: 路由不可预期、
			// 倍率未知, 且与 cline/zen 目录撞名, 同步时直接排除。
			if providerIsAliasModel(m.ID) {
				continue
			}
			out = append(out, wbModelInfo{
				ID: m.ID, Name: m.Name, Variant: v.ID,
				Context: m.MaxInputTokens, Output: m.MaxOutputTokens,
				Credits: m.Credits, Multiplier: wbCreditsMultiplier(m.Credits),
				Reason:     m.SupportsReasoning,
				Tools:      m.SupportsToolCall,
				IsDefault:  m.IsDefault,
				Multimodal: !m.DisabledMultimodal,
			})
		}
		return out, nil
	}
	return nil, lastErr
}

// wbCreditsMultiplier 解析 credits 倍率。形如 "x0.00 credits" / "x0.29"；
// 缺字段(CN 的 auto / hunyuan-chat / hunyuan-image-alpha)返回 0 且由调用方
// 结合 wbIsFreeCredits 区分"免费"与"未标价"。
func wbCreditsMultiplier(credits string) float64 {
	num := ""
	seenDot := false
	for _, r := range credits {
		if r >= '0' && r <= '9' {
			num += string(r)
			continue
		}
		if r == '.' && !seenDot {
			seenDot = true
			num += "."
			continue
		}
		if num != "" {
			break
		}
	}
	if num == "" {
		return 0
	}
	f, err := strconv.ParseFloat(strings.TrimSuffix(num, "."), 64)
	if err != nil {
		return 0
	}
	return f
}

// wbIsFreeCredits credits 形如 "x0.00 credits" / "x0.29"; 倍数 0 视为免费。
// 缺少 credits 字段(如 CN 的 auto)不算免费模型。
func wbIsFreeCredits(credits string) bool {
	if !wbHasCreditsField(credits) {
		return false
	}
	return wbCreditsMultiplier(credits) == 0
}

// wbHasCreditsField 报告 credits 字段是否带数字(区分 "x0.00" 与空/无倍率)。
func wbHasCreditsField(credits string) bool {
	for _, r := range credits {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

func wbModelList() []map[string]any {
	wbModelsMu.RLock()
	keys := make([]string, 0, len(wbModels))
	for k := range wbModels {
		keys = append(keys, k)
	}
	list := make([]wbModelInfo, 0, len(keys))
	for _, k := range keys {
		list = append(list, wbModels[k])
	}
	wbModelsMu.RUnlock()

	sort.Slice(list, func(i, j int) bool {
		if list[i].Variant != list[j].Variant {
			return list[i].Variant < list[j].Variant
		}
		return list[i].ID < list[j].ID
	})
	// 裸 id 只要在任一来源重复(跨变体、跨 provider、与 zen/cline 撞名)就必须消歧,
	// 否则客户端按裸 id 调用会落到另一个计费池。
	dup := providerPublicIDs()
	out := make([]map[string]any, 0, len(list))
	for _, m := range list {
		v := wbVariantByID(m.Variant)
		provider := m.Variant
		if v != nil {
			provider = v.Provider
		}
		// 被 /admin 关掉的变体不在目录里出现(避免客户端选到必然 503 的模型)
		if v != nil && !wbVariantEnabled(v) {
			continue
		}
		publicID := m.ID
		if dup[m.ID] {
			publicID = provider + "/" + m.ID
		}
		cost := "paid"
		if m.Multiplier == 0 && wbHasCreditsField(m.Credits) {
			cost = "free"
		}
		// 模态: 上游只有一个 multimodal 总开关, 关了就是图片/视频都不支持;
		// 开着只能确认图片, 视频留 null(未知)交给探测/人工判断。
		var image, video any
		if m.Multimodal {
			image = true
		} else {
			image = false
			video = false
		}
		image, video = applyModalityProbe(modalityKey(provider, m.ID), image, video)
		out = append(out, map[string]any{
			"id":         publicID,
			"baseId":     m.ID,
			"variant":    m.Variant,
			"name":       m.Name,
			"source":     "workbuddy",
			"owned_by":   provider,
			"context":    m.Context,
			"output":     m.Output,
			"credits":    m.Credits,
			"multiplier": m.Multiplier,
			"cost":       cost,
			"available":  m.Available,
			"probeNote":  m.ProbeNote,
			"tools":      m.Tools,
			"reasoning":  m.Reason,
			"image":      image,
			"video":      video,
		})
	}
	return out
}

// wbModelListAvailable 只为 /v1/models 提供: 过滤掉上游未接入的模型。
// 后台仍通过 wbModelList() 看到全部条目(含不可用), 便于排查"为什么某个模型没了"。
func wbModelListAvailable() []map[string]any {
	all := wbModelList()
	out := make([]map[string]any, 0, len(all))
	for _, m := range all {
		if avail, ok := m["available"].(bool); ok && !avail {
			continue
		}
		out = append(out, m)
	}
	return out
}

// wbResolveModel 解析裸模型 id 或上游可读显示名。内部路由 id 优先, 显示名兜底
// —— /v1/models 暴露的首段是显示名(如 "GLM-5.3 · x0.8" 剥掉装饰后剩 GLM-5.3)。
// 同名模型跨变体冲突时(如 CN 与 INTL 对外都叫 GLM-5.3)优先取已启用的变体, 再按
// wbVariants 顺序取第一个命中的变体, 保证路由确定且不会静默落到 cline 池;
// 需要指定另一变体时用 <provider>/<id> 前缀。
func wbResolveModel(id string) (wbModelInfo, bool) {
	wbModelsMu.RLock()
	defer wbModelsMu.RUnlock()
	for _, v := range wbVariants {
		if !wbVariantEnabled(v) {
			continue
		}
		if m, ok := wbModels[wbModelKey(v.ID, id)]; ok {
			return m, true
		}
	}
	for _, v := range wbVariants {
		if m, ok := wbModels[wbModelKey(v.ID, id)]; ok {
			return m, true
		}
	}
	// 显示名兜底: 两轮扫描, 先只在已启用变体里找, 再全变体兜底
	for _, enabledOnly := range []bool{true, false} {
		for _, v := range wbVariants {
			if enabledOnly && !wbVariantEnabled(v) {
				continue
			}
			if m, ok := wbResolveByNameIn(v.ID, id); ok {
				return m, true
			}
		}
	}
	return wbModelInfo{}, false
}

// wbResolveByNameIn 在单个变体内按上游可读显示名解析模型(TrimSpace 后精确匹配)。
func wbResolveByNameIn(variant, name string) (wbModelInfo, bool) {
	for _, m := range wbModels {
		if m.Variant != variant {
			continue
		}
		if strings.TrimSpace(m.Name) == name {
			return m, true
		}
	}
	return wbModelInfo{}, false
}

// wbVariantExists 校验变体 id 是否存在(用于区分"前缀写错"与"模型不在目录")。
func wbVariantExists(id string) bool { return wbVariantByID(id) != nil }

// wbResolveModelIn 在指定变体内解析模型 id: 内部路由 id 优先, 上游可读显示名
// 兜底 —— /v1/models 暴露的首段是显示名(如 "DeepSeek-V3.2 · x0.29" 剥掉装饰
// 后剩 DeepSeek-V3.2), 旧配置的干净内部 id 继续可用。
func wbResolveModelIn(variant, id string) (wbModelInfo, bool) {
	wbModelsMu.RLock()
	defer wbModelsMu.RUnlock()
	if m, ok := wbModels[wbModelKey(variant, id)]; ok {
		return m, true
	}
	return wbResolveByNameIn(variant, id)
}

// wbResolveDecorated 按完整装饰串精确解析(/v1/models 暴露的原样 id)。
// 显示名跨变体冲突时倍率段可以区分(如 CN "GLM-5.3 · x0.79" 与 Qoder
// "GLM-5.3 · x0.8"), 因此路由判定必须先于剥装饰的裸名兜底; 先查已启用变体。
// 同时接受两种历史形态: 带分组前缀的当前格式("workbuddy/Hy3 · free")与
// 早期无分组的格式("Hy3 · x0.29" / 免费时的裸 "Hy3"), 旧配置才不会失效。
func wbResolveDecorated(id string) (wbModelInfo, bool) {
	wbModelsMu.RLock()
	defer wbModelsMu.RUnlock()
	for _, enabledOnly := range []bool{true, false} {
		for _, v := range wbVariants {
			if enabledOnly && !wbVariantEnabled(v) {
				continue
			}
			for _, m := range wbModels {
				if m.Variant != v.ID {
					continue
				}
				free := m.Multiplier == 0 && wbHasCreditsField(m.Credits)
				if providerDecorateID(v.Provider, m.ID, m.Name, m.Multiplier, free) == id {
					return m, true
				}
				if providerDecorateID("", m.ID, m.Name, m.Multiplier, false) == id {
					return m, true
				}
			}
		}
	}
	return wbModelInfo{}, false
}

// ============ chat ============

// wbBuildBody 归一化请求体: 强制 stream + include_usage, 首条必须是 system,
// 并按模型目录裁剪 max_tokens。
func wbBuildBody(params map[string]any, m wbModelInfo) map[string]any {
	body := map[string]any{}
	for k, v := range params {
		if k == "stream" || k == "stream_options" {
			continue
		}
		body[k] = v
	}
	body["model"] = m.ID
	body["stream"] = true
	if _, ok := body["stream_options"]; !ok {
		body["stream_options"] = map[string]any{"include_usage": true}
	} else {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	// 上游风控要求 messages 首条为 system prompt。
	// 先做角色归一: Codex 的 developer 角色若不折叠成 system, hasSystem 判为
	// false, 补一条 system 后 developer 仍在原地 —— 上游照样 400。
	if msgs, ok := body["messages"].([]any); ok {
		msgs = cleanMessages(msgs)
		body["messages"] = msgs
		hasSystem := false
		for _, raw := range msgs {
			if mm, ok := raw.(map[string]any); ok && mm["role"] == "system" {
				hasSystem = true
				break
			}
		}
		if !hasSystem {
			body["messages"] = append([]any{map[string]any{
				"role": "system", "content": "You are a helpful assistant.",
			}}, msgs...)
		}
	} else {
		body["messages"] = []any{map[string]any{"role": "system", "content": "You are a helpful assistant."}}
	}
	// 上游 tool_choice 只接受字符串
	if tc, ok := body["tool_choice"]; ok {
		switch v := tc.(type) {
		case string:
			if v == "none" {
				delete(body, "tool_choice")
			}
		case map[string]any:
			if v["type"] == "function" {
				if fn, ok := v["function"].(map[string]any); ok {
					if n, ok := fn["name"].(string); ok && n != "" {
						body["tool_choice"] = n
					} else {
						body["tool_choice"] = "auto"
					}
				} else {
					body["tool_choice"] = "auto"
				}
			} else {
				delete(body, "tool_choice")
			}
		default:
			delete(body, "tool_choice")
		}
	}
	// max_tokens 只做上限裁剪, 客户端没给就不主动添加(交给上游默认)
	if m.Output > 0 {
		if mt, ok := body["max_tokens"].(float64); ok && int(mt) > m.Output {
			body["max_tokens"] = m.Output
		}
	}
	return body
}

// wbCallChat 发出上游请求。返回 resp 与所用账号。
func wbCallChat(params map[string]any, m wbModelInfo, conversationID string) (*http.Response, *wbAccount, error) {
	v := wbVariantByID(m.Variant)
	if v == nil {
		return nil, nil, fmt.Errorf("未知 WorkBuddy 变体 %q", m.Variant)
	}
	if !wbVariantEnabled(v) {
		return nil, nil, fmt.Errorf("%s 上游已在 /admin/ 关闭", v.DisplayName)
	}
	cfg := getWBConfig()
	attempts := cfg.MaxRetries
	if attempts <= 0 {
		attempts = 2
	}

	body := wbBuildBody(params, m)
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}

	var lastErr error
	for i := 0; i <= attempts; i++ {
		acc := wbPickAccount(v.ID)
		if acc == nil {
			return nil, nil, fmt.Errorf("%s 无可用账号, 请在 /admin/ 导入本机已登录账号", v.DisplayName)
		}
		for _, path := range wbChatPaths(v) {
			url := v.ChatBase + path
			req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
			if err != nil {
				return nil, nil, err
			}
			wbChatHeaders(req, acc, v, conversationID)
			client := kit.NewHTTPClient(10 * time.Minute)
			resp, err := client.Do(req)
			if err != nil {
				lastErr = err
				continue
			}
			if resp.StatusCode == 200 {
				return resp, acc, nil
			}
			errBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d %s", resp.StatusCode, kit.Truncate(strings.TrimSpace(string(errBody)), 300))
			if resp.StatusCode == 404 || resp.StatusCode == 405 {
				continue
			}
			if resp.StatusCode == 401 || resp.StatusCode == 403 {
				if err := wbRefreshAccount(acc); err == nil {
					break
				}
			}
			break
		}
		if lastErr == nil {
			return nil, nil, fmt.Errorf("未知错误")
		}
		if i < attempts {
			time.Sleep(kit.WithRetryJitter(time.Duration(500+300*i) * time.Millisecond))
		}
	}
	return nil, nil, lastErr
}

// wbChatPaths global 先 /console 再 /v2 fallback; cn 只用 /v2。
func wbChatPaths(v *wbVariant) []string {
	if v.Region == "global" {
		return []string{"/console/chat/completions", "/v2/chat/completions"}
	}
	return []string{"/v2/chat/completions"}
}

func maskSecret(s string) string {
	if len(s) <= 8 {
		return "***"
	}
	return s[:4] + "***" + s[len(s)-2:]
}
