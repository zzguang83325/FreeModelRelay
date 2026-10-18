package app

import (
	"FreeModelRelay/internal/kit"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// ============================================================================
// opencode 官方模型目录 (models.opencode.ai) — 权威免费集合
// 移植自 opencode-free-gate/catalog.go, 判定规则与 opencode CLI 一致:
//   provider=opencode 下 cost.input == 0 且 status 不是 deprecated/alpha
// 目录可用时完全以它为准 (含排除官方已下架/转入付费的模型);
// 目录不可用时回退到 seed 白名单 / -free 后缀启发式。
// 同时记录 provider.npm=@ai-sdk/anthropic 的模型 (原生 Anthropic 协议,
// 需发 /v1/messages, 本代理统一走 /chat/completions, 此类模型不参与 zen 路由)。
// ============================================================================

const (
	catalogURL             = "https://models.opencode.ai/api.json"
	catalogTimeout         = 30 * time.Second
	catalogRefreshInterval = 30 * time.Minute
	catalogMaxBody         = 16 << 20
)

// catalogEntry 目录中单个免费模型的元数据
type catalogEntry struct {
	Context   int  `json:"context,omitempty"`   // 官方上下文上限
	Output    int  `json:"output,omitempty"`    // 官方最大输出
	Anthropic bool `json:"anthropic,omitempty"` // 原生 Anthropic 协议模型
	// Image / Video 来自官方目录的 modalities.input。两者都是"官方声明",
	// 不是探测结果 —— 目录里没写就保持 false 且 metaKnown 为 false,
	// 后台必须区分"不支持"与"上游没说"(见 catalogMeta)。
	Image bool `json:"image,omitempty"`
	Video bool `json:"video,omitempty"`
	// MetaKnown 表示该条目是否带了模态信息。官方目录早期版本没有
	// modalities 字段, 缓存文件也可能是旧格式 —— 缺字段时不能当成"不支持"。
	MetaKnown bool `json:"metaKnown,omitempty"`
}

type catalogCacheFile struct {
	Models  map[string]catalogEntry `json:"models"`
	SavedAt time.Time               `json:"saved_at"`
}

var (
	catalogMu    sync.RWMutex
	catalogData  map[string]catalogEntry // id -> 元数据 (仅免费模型)
	catalogAt    time.Time
	catalogReady bool // 是否已成功加载过目录(在线或缓存)
)

type catalogPayload struct {
	Opencode struct {
		Models map[string]struct {
			Cost *struct {
				Input float64 `json:"input"`
			} `json:"cost"`
			Status   string `json:"status"`
			Provider *struct {
				NPM string `json:"npm"`
			} `json:"provider"`
			Limit *struct {
				Context int `json:"context"`
				Output  int `json:"output"`
			} `json:"limit"`
			// modalities.input 形如 ["text","image","video","pdf"]
			Modalities *struct {
				Input []string `json:"input"`
			} `json:"modalities"`
		} `json:"models"`
	} `json:"opencode"`
}

// catalogHasModality 判断官方 modalities.input 是否包含指定模态(大小写不敏感)。
func catalogHasModality(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(strings.TrimSpace(s), want) {
			return true
		}
	}
	return false
}

// parseCatalogModels 解析官方目录 JSON, 返回免费模型元数据表。
// 与 opencode-free-gate 的 parseCatalogModels 同规则。
func parseCatalogModels(body []byte) (map[string]catalogEntry, error) {
	var payload catalogPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("解析目录失败: %w", err)
	}
	free := make(map[string]catalogEntry, 8)
	for id, m := range payload.Opencode.Models {
		if id == "" || m.Cost == nil {
			continue
		}
		if m.Cost.Input != 0 {
			continue
		}
		if m.Status == "deprecated" || m.Status == "alpha" {
			continue
		}
		e := catalogEntry{}
		if m.Limit != nil {
			e.Context = m.Limit.Context
			e.Output = m.Limit.Output
		}
		e.Anthropic = m.Provider != nil && m.Provider.NPM == "@ai-sdk/anthropic"
		if m.Modalities != nil {
			e.MetaKnown = true
			e.Image = catalogHasModality(m.Modalities.Input, "image")
			e.Video = catalogHasModality(m.Modalities.Input, "video")
		}
		free[id] = e
	}
	if len(free) == 0 {
		return nil, fmt.Errorf("目录中没有 cost=0 的可用模型")
	}
	return free, nil
}

func catalogCachePath() string {
	return kit.ResolveDataPath("catalog.free.json")
}

func saveCatalogCache(models map[string]catalogEntry) {
	data, err := json.MarshalIndent(catalogCacheFile{Models: models, SavedAt: time.Now()}, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(catalogCachePath(), data, 0644); err != nil {
		log.Printf("  catalog cache save failed: %v", err)
	}
}

func loadCatalogCache() bool {
	data, err := os.ReadFile(catalogCachePath())
	if err != nil {
		return false
	}
	var f catalogCacheFile
	if json.Unmarshal(data, &f) != nil || len(f.Models) == 0 {
		return false
	}
	setCatalogModels(f.Models)
	log.Printf("  catalog: restored %d free models from cache (saved %s)",
		len(f.Models), f.SavedAt.Format("2006-01-02 15:04:05"))
	return true
}

func setCatalogModels(models map[string]catalogEntry) {
	catalogMu.Lock()
	catalogData = models
	catalogAt = time.Now()
	catalogReady = true
	catalogMu.Unlock()
}

// catalogLoaded 目录是否可用(在线或缓存恢复成功过)
func catalogLoaded() bool {
	catalogMu.RLock()
	defer catalogMu.RUnlock()
	return catalogReady
}

// catalogIsFree 目录判定: 该 ID 是否在免费集合且非 Anthropic 协议。
// 仅在 catalogLoaded() 为 true 时有意义。
func catalogIsFree(id string) bool {
	catalogMu.RLock()
	defer catalogMu.RUnlock()
	e, ok := catalogData[id]
	return ok && !e.Anthropic
}

// catalogMeta 返回目录条目的完整元数据(含模态)。
// ok=false 表示目录里没有这个模型; entry.MetaKnown=false 表示目录没给模态字段,
// 调用方必须把 image/video 当成"未知"而不是"不支持"。
func catalogMeta(id string) (catalogEntry, bool) {
	catalogMu.RLock()
	defer catalogMu.RUnlock()
	e, ok := catalogData[id]
	return e, ok
}

// catalogSnapshot 返回免费集合快照
func catalogSnapshot() map[string]catalogEntry {
	catalogMu.RLock()
	defer catalogMu.RUnlock()
	out := make(map[string]catalogEntry, len(catalogData))
	for k, v := range catalogData {
		out[k] = v
	}
	return out
}

func refreshCatalog() error {
	ctx, cancel := context.WithTimeout(context.Background(), catalogTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, catalogURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", kit.ZenUserAgent())
	client := &http.Client{Timeout: catalogTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("catalog API returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, catalogMaxBody))
	if err != nil {
		return err
	}
	models, err := parseCatalogModels(body)
	if err != nil {
		return err
	}
	setCatalogModels(models)
	saveCatalogCache(models)
	log.Printf("  catalog: loaded official catalog, %d free models (%s)",
		len(models), strings.Join(catalogIDs(models), ", "))
	return nil
}

func catalogIDs(m map[string]catalogEntry) []string {
	ids := make([]string, 0, len(m))
	for k := range m {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	return ids
}

// mergeCatalogModelsLocked 把官方目录的免费模型合并进 zen 主表(调用方需持有 zenModelsMu 写锁):
//   - 补充目录中有而主表缺失的免费模型(带官方上下文/输出元数据, source=catalog)
//   - 用目录元数据修正已有模型的 Context/Output(替代同步时的 200K/32K 估算)
//   - Anthropic 协议模型跳过: 本代理统一走 /chat/completions,
//     这些模型需要 /v1/messages, 混入会导致路由后必然失败
func mergeCatalogModelsLocked() int {
	snap := catalogSnapshot()
	if len(snap) == 0 {
		return 0
	}
	added := 0
	for id, e := range snap {
		if e.Anthropic {
			continue
		}
		if _, conflict := zenAliases[id]; conflict {
			continue
		}
		if m, ok := zenModels[id]; ok {
			if e.Context > 0 {
				m.Context = e.Context
			}
			if e.Output > 0 {
				m.Output = e.Output
			}
			continue
		}
		ctx, out := e.Context, e.Output
		if ctx <= 0 {
			ctx = 200000
		}
		if out <= 0 {
			out = 32768
		}
		zenModels[id] = &ZenModel{ID: id, Context: ctx, Output: out, Source: "catalog"}
		added++
	}
	return added
}

// startCatalogRefresher 启动即拉取一次目录, 之后周期刷新; 失败沿用上次结果
func startCatalogRefresher() {
	go func() {
		if err := refreshCatalog(); err != nil {
			if !loadCatalogCache() {
				log.Printf("  catalog: initial fetch failed (%v), using suffix heuristic until synced", err)
			}
		} else {
			// 目录首次加载成功后立即合并一次, 不必等模型周期同步
			if _, err := syncZenModels(); err != nil {
				log.Printf("  catalog: post-merge sync failed: %v", err)
			}
		}
		ticker := time.NewTicker(catalogRefreshInterval)
		defer ticker.Stop()
		for range ticker.C {
			if err := refreshCatalog(); err != nil {
				log.Printf("  catalog: refresh failed, keep last result: %v", err)
			}
		}
	}()
}

func init() {
	startCatalogRefresher()
}
