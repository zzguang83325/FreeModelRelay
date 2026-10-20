package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"FreeModelRelay/internal/kit"
)

// ============================================================================
// WorkBuddy 模型可用性探测
//
// 上游 /v2/enterprises/personal/models 列出 33 个模型, 其中 9 个(glm-5.1 /
// glm-5.0 / glm-4.7 / glm-4.6 / glm-4.6v / hy4-preview-x / minimax-m2.5 /
// kimi-k2-thinking)一旦调用就返回:
//   {"code":11102,"msg":"model [glm-4.6] service info not found",
//    "displayMsg":{"zh":"当前模型不可用，请切换模型"}}
// 目录里既没有 configured 也没有 disabled 之类的字段能区分它们, 所以"放开全部
// 可用模型"必须靠实调一次判定, 并把结果缓存下来(否则每次同步 = 33 次上游调用)。
// ============================================================================

const (
	// 探测结果缓存有效期。模型接入状态是运营动作, 变化慢; 太短会频繁打上游,
	// 太长则新接入的模型长期显示为不可用。
	wbProbeTTLMillis = 6 * 60 * 60 * 1000
	// 探测并发数。33 个模型若串行, 按 5s 超时最坏要 165s。
	wbProbeWorkers = 6
)

// wbProbeEntry 单条探测记录。
type wbProbeEntry struct {
	Available bool   `json:"available"`
	Note      string `json:"note,omitempty"`
	CheckedAt int64  `json:"checkedAt"`
}

var wbProbeMu sync.Mutex

func wbProbeCachePath() string { return kit.ResolveDataPath(".workbuddy-probe.json") }

func wbProbeCacheLoad() map[string]wbProbeEntry {
	out := map[string]wbProbeEntry{}
	data, err := os.ReadFile(wbProbeCachePath())
	if err != nil {
		return out
	}
	json.Unmarshal(data, &out)
	return out
}

func wbProbeCacheSave(cache map[string]wbProbeEntry) {
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(wbProbeCachePath(), data, 0600)
}

// wbProbeOne 实调一次判定模型是否真的接入。max_tokens=1 + 极短 prompt,
// 成功路径的积分消耗可忽略(未接入的模型在计费前就被上游拒绝, 不消耗积分)。
func wbProbeOne(v *wbVariant, a *wbAccount, modelID string) wbProbeEntry {
	now := time.Now().UnixMilli()
	payload := map[string]any{
		"model":          modelID,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
		"messages": []any{
			map[string]any{"role": "system", "content": "You are a helpful assistant."},
			map[string]any{"role": "user", "content": "1"},
		},
		"max_tokens": 1,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return wbProbeEntry{Available: true, Note: "marshal: " + err.Error(), CheckedAt: now}
	}
	// 探测失败不应把模型误标为不可用(否则上游抖动会把好模型永久下线),
	// 所以任何非 11102 的结果一律视为可用, 只记录 note。
	entry := wbProbeEntry{Available: true, CheckedAt: now}
	for _, path := range wbChatPaths(v) {
		req, err := http.NewRequest(http.MethodPost, v.ChatBase+path, bytes.NewReader(raw))
		if err != nil {
			entry.Note = err.Error()
			return entry
		}
		wbChatHeaders(req, a, v, "")
		client := kit.NewHTTPClient(30 * time.Second)
		resp, err := client.Do(req)
		if err != nil {
			entry.Note = err.Error()
			return entry
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		text := string(body)
		if resp.StatusCode == 200 && !strings.Contains(text, `"error"`) {
			return entry
		}
		if wbIsModelUnavailable(text) {
			entry.Available = false
			entry.Note = wbUnavailableReason(text)
			return entry
		}
		if resp.StatusCode == 404 || resp.StatusCode == 405 {
			continue
		}
		// 其他错误(限流/配额/风控): 不改变可用性判断, 只留痕。
		entry.Note = fmt.Sprintf("HTTP %d %s", resp.StatusCode, kit.Truncate(strings.TrimSpace(text), 160))
		return entry
	}
	return entry
}

// wbIsModelUnavailable 判定响应是否为"模型未接入"(11102)。
// 只认这个 code —— 配额/限流错误不代表模型不存在。
func wbIsModelUnavailable(body string) bool {
	return strings.Contains(body, "11102") ||
		strings.Contains(body, "service info not found") ||
		strings.Contains(body, "当前模型不可用")
}

func wbUnavailableReason(body string) string {
	var parsed struct {
		Msg string `json:"msg"`
	}
	if json.Unmarshal([]byte(body), &parsed) == nil && parsed.Msg != "" {
		return parsed.Msg
	}
	return kit.Truncate(strings.TrimSpace(body), 160)
}

// wbProbeModelsAsync 后台并发探测并落缓存。all=false 时只更新探测结果,
// 不重建模型表(避免与并发的手动同步互相覆盖)。
func wbProbeModelsAsync(v *wbVariant, a *wbAccount, models []wbModelInfo, logEach bool) {
	sem := make(chan struct{}, wbProbeWorkers)
	var wg sync.WaitGroup
	results := make([]wbProbeEntry, len(models))

	for i := range models {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = wbProbeOne(v, a, models[i].ID)
			if logEach {
				log.Printf("  workbuddy probe %s/%s: available=%v %s",
					v.ID, models[i].ID, results[i].Available, results[i].Note)
			}
		}(i)
	}
	wg.Wait()

	wbProbeMu.Lock()
	cache := wbProbeCacheLoad()
	for i := range models {
		cache[wbModelKey(v.ID, models[i].ID)] = results[i]
	}
	wbProbeCacheSave(cache)
	wbProbeMu.Unlock()

	// 把探测结果回填进内存模型表(只改同一变体且仍在表里的条目)。
	nUnavail := 0
	wbModelsMu.Lock()
	for i := range models {
		key := wbModelKey(v.ID, models[i].ID)
		m, ok := wbModels[key]
		if !ok {
			continue
		}
		m.Available = results[i].Available
		m.ProbeNote = results[i].Note
		m.ProbedAt = results[i].CheckedAt
		wbModels[key] = m
		if !results[i].Available {
			nUnavail++
		}
	}
	wbModelsMu.Unlock()
	if nUnavail > 0 {
		log.Printf("  workbuddy %s: probe done, %d/%d models not wired up on upstream",
			v.ID, nUnavail, len(models))
	}
}

// wbProbeAll 强制重新探测一个变体的全部模型(供 /admin 手动触发)。
func wbProbeAll(v *wbVariant, force bool) (probed int, unavailable int, err error) {
	acc := wbPickAccount(v.ID)
	if acc == nil {
		return 0, 0, fmt.Errorf("%s 无可用账号", v.DisplayName)
	}
	wbModelsMu.RLock()
	var models []wbModelInfo
	for _, m := range wbModels {
		if m.Variant == v.ID {
			models = append(models, m)
		}
	}
	wbModelsMu.RUnlock()
	if len(models) == 0 {
		return 0, 0, fmt.Errorf("%s 模型表为空, 请先同步模型", v.DisplayName)
	}
	if !force {
		cache := wbProbeCacheLoad()
		filtered := models[:0]
		now := time.Now().UnixMilli()
		for _, m := range models {
			if e, ok := cache[wbModelKey(v.ID, m.ID)]; ok && now-e.CheckedAt < wbProbeTTLMillis {
				continue
			}
			filtered = append(filtered, m)
		}
		models = filtered
	}
	if len(models) == 0 {
		return 0, 0, nil
	}
	wbProbeModelsAsync(v, acc, models, false)
	wbModelsMu.RLock()
	for _, m := range wbModels {
		if m.Variant != v.ID {
			continue
		}
		probed++
		if !m.Available {
			unavailable++
		}
	}
	wbModelsMu.RUnlock()
	return probed, unavailable, nil
}
