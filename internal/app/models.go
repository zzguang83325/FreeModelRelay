package app

import (
	"FreeModelRelay/internal/cline"
	"FreeModelRelay/internal/kit"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type ModelStatus string

const (
	ModelActive  ModelStatus = "active"
	ModelRemoved ModelStatus = "removed"
	ModelUnknown ModelStatus = "unknown"
	ModelError   ModelStatus = "error"
)

type ModelInfo struct {
	ID             string           `json:"id"`
	Name           string           `json:"name,omitempty"`
	Source         string           `json:"source"`
	Provider       string           `json:"provider"`
	Cost           string           `json:"cost"`
	Status         ModelStatus      `json:"status"`
	RequiresStream bool             `json:"requiresStream,omitempty"`
	SyncedAt       time.Time        `json:"syncedAt,omitempty"`
	Usage          map[string]int64 `json:"usage,omitempty"` // 按模型用量统计(仅 admin 接口填充)
	// cline 官方模型清单不含模态字段, 恒为 null(未知)。保留字段是为了让
	// 后台三个模型接口的键名一致, 前端统一按三态处理。
	Image *bool `json:"image"`
	Video *bool `json:"video"`
}

var (
	modelsMu       sync.Mutex
	modelsCache    map[string]*ModelInfo
	modelsSyncing  bool
	modelsLastSync time.Time
)

// defaultModelMu 保护 defaultModel 全局变量。
// defaultModel 在 pool.go(loadPool/setDefaultModel) 与本项目多处被跨 goroutine 读写,
// 统一经 get/setDefaultModelValue 访问, 避免与 modelsMu 锁不一致导致数据竞争。
var defaultModelMu sync.Mutex

func getDefaultModelValue() string {
	defaultModelMu.Lock()
	defer defaultModelMu.Unlock()
	return defaultModel
}

func setDefaultModelValue(m string) {
	defaultModelMu.Lock()
	defaultModel = m
	defaultModelMu.Unlock()
}

const (
	modelsRefreshInterval = 60 * time.Second
	modelsSyncTimeout     = 25 * time.Second
)

const recommendedModelsURL = cline.ClineAPIBase + "/ai/cline/recommended-models"

func initModelsCache() {
	modelsMu.Lock()
	seeded := modelsCache != nil
	if modelsCache == nil {
		modelsCache = make(map[string]*ModelInfo)
	}
	modelsMu.Unlock()
	// 首次初始化时用本地 dump 预填免费列表, 不阻塞、失败静默(随后网络同步会纠正)
	if !seeded {
		seedModelsFromDump()
	}
}

func getFreeModels() []*ModelInfo {
	initModelsCache()
	modelsMu.Lock()
	defer modelsMu.Unlock()
	out := make([]*ModelInfo, 0, len(modelsCache))
	for _, m := range modelsCache {
		cp := *m
		out = append(out, &cp)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			if out[j-1].ID < out[j].ID {
				break
			}
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

type recommendedPayload struct {
	Free []struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
	} `json:"free"`
	Recommended []struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
	} `json:"recommended"`
	ClinePass []struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
	} `json:"clinePass"`
}

// remoteFreeModel 官方接口中一个免费模型条目。
type remoteFreeModel struct {
	id   string
	name string
}

// hasFreeTag 判断模型是否带 FREE 标签(官方有时在非 free 数组里也放免费模型)。
func hasFreeTag(tags []string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, "FREE") {
			return true
		}
	}
	return false
}

// collectFreeModels 从官方响应中提取免费模型(free 数组 + 任意区带 FREE 标签者)。
func collectFreeModels(payload *recommendedPayload) []remoteFreeModel {
	var list []remoteFreeModel
	seen := make(map[string]bool)
	add := func(id, name string, tags []string, inFree bool) {
		if id == "" || seen[id] {
			return
		}
		if !inFree && !hasFreeTag(tags) {
			return
		}
		seen[id] = true
		list = append(list, remoteFreeModel{id, name})
	}
	for _, m := range payload.Free {
		add(m.ID, m.Name, m.Tags, true)
	}
	for _, m := range payload.Recommended {
		add(m.ID, m.Name, m.Tags, false)
	}
	for _, m := range payload.ClinePass {
		add(m.ID, m.Name, m.Tags, false)
	}
	return list
}

// applyFreeModels 用官方免费列表更新模型表: 新增/刷新条目, 并剔除已退出
// 官方免费列表的模型(免费促销限时轮换)。剔除后视为未知模型, 请求自动回退
// 默认免费模型 —— 若继续请求已计费模型会导致账号欠费。
// (cline2api 实测下架模型常仍可免费使用, 但为避免再次产生计费, 这里从严处理。)
func applyFreeModels(list []remoteFreeModel, now time.Time) (added int, removed []string) {
	current := make(map[string]bool, len(list))
	for _, m := range list {
		current[m.id] = true
	}

	modelsMu.Lock()
	defer modelsMu.Unlock()
	if modelsCache == nil {
		modelsCache = make(map[string]*ModelInfo)
	}

	for _, m := range list {
		provider := m.id
		if i := indexByte(m.id, '/'); i >= 0 {
			provider = m.id[:i]
		}
		if cached, ok := modelsCache[m.id]; ok {
			cached.Source = "free"
			cached.Cost = "free"
			cached.Provider = provider
			cached.Status = ModelActive
			cached.SyncedAt = now
			if cached.Name == "" {
				cached.Name = m.name
			}
			continue
		}
		modelsCache[m.id] = &ModelInfo{
			ID:             m.id,
			Name:           m.name,
			Source:         "free",
			Provider:       provider,
			Cost:           "free",
			Status:         ModelActive,
			RequiresStream: indexByte(m.id, ':') < 0,
			SyncedAt:       now,
		}
		added++
	}

	for id := range modelsCache {
		if !current[id] {
			removed = append(removed, id)
			delete(modelsCache, id)
		}
	}
	return added, removed
}

// syncRecommendedModels 拉取 Cline 官方推荐模型接口并重建免费模型表。
// 该接口为公开端点(实测无需认证), 不再绑定账号, 避免每次同步轮换账号/刷新 token。
// 仅收录免费模型; clinePass(订阅)/recommended(付费)一律不入表。
func syncRecommendedModels() (int, error) {
	req, err := http.NewRequest("GET", recommendedModelsURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Task-ID", fmt.Sprintf("sess_sync_%d", time.Now().UnixMilli()))
	cfg := getProxyConfig()
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: modelsSyncTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var payload recommendedPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, err
	}
	list := collectFreeModels(&payload)
	if len(list) == 0 {
		return 0, fmt.Errorf("official feed returned no free models")
	}

	// 留存官方原始列表: 便于核对免费模型轮换, 也作为下次启动的本地种子
	if p := kit.ResolveDataPath("models.raw.json"); p != "" {
		_ = os.WriteFile(p, body, 0644)
	}

	added, removed := applyFreeModels(list, time.Now())
	if len(removed) > 0 {
		log.Printf("  model sync: removed delisted models: %s", strings.Join(removed, ", "))
	}
	return added, nil
}

// seedModelsFromDump 用上次同步留下的 data/models.raw.json 填充模型表,
// 让重启后的首个请求(网络同步完成前)也能拿到正确的免费列表。
func seedModelsFromDump() {
	p := kit.ResolveDataPath("models.raw.json")
	if p == "" {
		return
	}
	body, err := os.ReadFile(p)
	if err != nil || len(body) == 0 {
		return
	}
	var payload recommendedPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return
	}
	list := collectFreeModels(&payload)
	if len(list) == 0 {
		return
	}
	if _, removed := applyFreeModels(list, time.Time{}); len(removed) > 0 {
		log.Printf("  model seed: dropped delisted models: %s", strings.Join(removed, ", "))
	}
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func syncModelsOnce() {
	initModelsCache()
	modelsMu.Lock()
	if modelsSyncing {
		modelsMu.Unlock()
		return
	}
	modelsSyncing = true
	modelsMu.Unlock()
	defer func() {
		modelsMu.Lock()
		modelsSyncing = false
		modelsMu.Unlock()
	}()

	added, err := syncRecommendedModels()
	if err != nil {
		log.Printf("  model sync: failed (%v), using cached list", err)
		return
	}
	if added > 0 {
		log.Printf("  model sync: %d new free models from official feed", added)
	} else {
		// 每 60 秒跑一次的例行同步, 属于可预期信息, 降为 debug 级,
		// 日常运行不刷屏(需要排查时设 FMR_DEBUG=1 打开)。
		debugLogf("  model sync: %d free models up to date", len(getFreeModels()))
	}
}

func getDefaultModel() string {
	initModelsCache()
	// 管理页保存的默认模型可能是 /v1/models 里的展示 id(带分组前缀与计费段),
	// 先归一成真实模型 id 再判定, 否则会被当成未知模型而静默回退到别的上游。
	dm := providerNormalizeDisplayID(getDefaultModelValue())
	modelsMu.Lock()
	defer modelsMu.Unlock()

	// 已设置的默认模型只要仍在模型表中就生效, 保证与管理页"设为默认"一致
	if _, ok := modelsCache[dm]; ok {
		return dm
	}
	// zen 免费模型不在 modelsCache, 设为默认时直接生效
	if _, isZen := resolveZenFreeModel(dm); isZen {
		return dm
	}
	// 第三方 provider (workbuddy / workbuddy-ai / qoder) 同样不在 modelsCache。
	// 少了这一条, 把 workbuddy/hy3 设为默认后会静默回退到某个 cline 模型,
	// 管理页显示成功、实际请求却打到另一个上游。
	if dm != "" && providerRoute(dm) != "" {
		return dm
	}
	// 兜底: 取 ID 最小的可用模型, 避免 map 随机遍历导致每次结果不同
	best := ""
	for id, m := range modelsCache {
		if m.Status != ModelActive {
			continue
		}
		if best == "" || id < best {
			best = id
		}
	}
	if best != "" {
		return best
	}
	return dm
}

func normalizeRequestModel(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return getDefaultModel()
	}
	// 客户端可能把 /v1/models 里的展示 id(带分组前缀与 " · free" 计费段)原样发回来,
	// 归一成真实模型 id 后才能命中缓存, 否则会被误判为未知模型而回退默认模型。
	id = providerNormalizeDisplayID(id)
	if id == "" {
		return getDefaultModel()
	}
	initModelsCache()
	modelsMu.Lock()
	_, ok := modelsCache[id]
	modelsMu.Unlock()
	if ok {
		return id
	}
	log.Printf("  model %q not in free list, fallback to %q", id, getDefaultModel())
	return getDefaultModel()
}

// isKnownModel 模型是否可识别: 账号池模型表 或 zen 免费模型 或第三方 provider
func isKnownModel(id string) bool {
	id = providerNormalizeDisplayID(id)
	initModelsCache()
	modelsMu.Lock()
	_, ok := modelsCache[id]
	modelsMu.Unlock()
	if ok {
		return true
	}
	if _, isZen := resolveZenFreeModel(id); isZen {
		return true
	}
	// workbuddy / workbuddy-ai / qoder 免费模型
	return providerRoute(id) != ""
}

// routeModelAny routeModel 的补全版: 先判第三方 provider, 再落到 cline/zen 判定。
// routeModel 只认识 zen/cline/reject, 所以把 workbuddy/hy3 这类模型交给它会得到
// "cline" —— 空 model 的请求会带着 provider 默认模型进 cline 账号池, 由 cline 用
// 自己的额度去请求一个不存在的 cline 模型, 最终回 404(实测)。
func routeModelAny(id string) string {
	if p := providerRoute(id); p != "" {
		return p
	}
	return routeModel(providerNormalizeDisplayID(id))
}

// resolveRouteModel 解析请求的生效模型与路由目标
// ("zen"/"reject"/"cline"/"workbuddy"/"workbuddy-ai"/"qoder")。
// 空模型或未知模型: 解析为默认模型后再路由, 保证 zen 免费模型设为默认时同样生效。
// 返回 (生效模型, 路由)
func resolveRouteModel(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		eff := getDefaultModel()
		return eff, routeModelAny(eff)
	}
	// 第三方 provider 模型优先(它们不在 cline 账号池模型表内)。
	// providerRoute 内部兼容展示装饰(" · x倍率" / " · free"), 客户端从
	// /v1/models 原样选来的 id 才能落到正确上游; 装饰只留在展示层。
	if p := providerRoute(raw); p != "" {
		return raw, p
	}
	// cline / zen 的展示 id 归一成真实模型 id 再判路由:
	// zen 的 "opencode-zen/<id> · free" 与 cline 的 "<id> · free" 都要还原。
	base := providerNormalizeDisplayID(raw)
	route := routeModel(base)
	if route == "cline" && !isKnownModel(base) {
		// 未知模型 → 默认模型兜底
		log.Printf("  model %q not recognized, fallback to default %q", raw, getDefaultModel())
		eff := getDefaultModel()
		return eff, routeModelAny(eff)
	}
	return base, route
}

func apiModelList() []map[string]any {
	out := make([]map[string]any, 0, len(modelsCache))
	for _, m := range getFreeModels() {
		// 整池免费, 但仍补上 " · free" 计费段: /v1/models 里每个 id 末尾要么是
		// "x<倍率>" 要么是 "free", 客户端一眼能分辨(与第三方 provider 统一)。
		id := m.ID
		if m.Cost == "" || strings.EqualFold(m.Cost, "free") {
			id = providerFreeID(m.ID)
		}
		out = append(out, map[string]any{
			"id":             id,
			"object":         "model",
			"created":        time.Now().UnixMilli(),
			"owned_by":       m.Provider,
			"source":         m.Source,
			"status":         m.Status,
			"cost":           m.Cost,
			"requiresStream": m.RequiresStream,
			"syncedAt":       m.SyncedAt,
		})
	}
	return out
}

func ensureModelsFresh() {
	initModelsCache()
	modelsMu.Lock()
	needSync := modelsLastSync.IsZero() || time.Since(modelsLastSync) > modelsRefreshInterval
	syncing := modelsSyncing
	modelsMu.Unlock()
	if needSync && !syncing {
		go syncModelsOnce()
	}
}

func startModelsRefresher() {
	go func() {
		syncModelsOnce()
		ticker := time.NewTicker(modelsRefreshInterval)
		for range ticker.C {
			syncModelsOnce()
		}
	}()
}
