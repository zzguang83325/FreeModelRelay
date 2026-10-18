package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"FreeModelRelay/internal/kit"
)

// ============================================================================
// 模态探测
//
// 五个上游里只有 zen(官方 modalities.input)和 WorkBuddy(disabledMultimodal)
// 会在目录里声明模态; Qoder 与 cline 的目录完全没有这个字段。目录给不出的,
// 后台就显示"?" —— 但这不代表不支持, 用户需要能自己验证。
//
// 探测走的是本代理自己的 /v1/chat/completions, 即完整链路(路由 → 上游 →
// SSE/JSON 回写), 所以结果反映的是"通过这个网关能不能真的发图片", 而不是
// 上游裸接口的行为。每次探测消耗一次真实调用额度, 因此只在用户点按钮时触发,
// 结果缓存 7 天。
// ============================================================================

const (
	modalityProbeTTL     = 7 * 24 * time.Hour
	modalityProbeTimeout = 90 * time.Second
	modalityNoteMaxRunes = 160
	// 1x1 红色 PNG, 68 字节。用纯色图是为了让任何视觉模型都能一眼答出颜色。
	modalityProbeImage = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
)

// modalityEntry 单个模型的探测结果。Image/Video 为 nil 表示未探出结论。
type modalityEntry struct {
	Image     *bool  `json:"image"`
	Video     *bool  `json:"video"`
	Note      string `json:"note,omitempty"`
	CheckedAt int64  `json:"checkedAt"`
}

type modalityCacheFile struct {
	Probes map[string]modalityEntry `json:"probes"`
}

var (
	modalityMu       sync.Mutex
	modalityCache    map[string]modalityEntry
	modalityInFlight = map[string]bool{}
)

func modalityCachePath() string { return kit.ResolveDataPath(".modality-probe.json") }

func modalityCacheLoad() map[string]modalityEntry {
	modalityMu.Lock()
	defer modalityMu.Unlock()
	if modalityCache != nil {
		return modalityCache
	}
	cache := map[string]modalityEntry{}
	if data, err := os.ReadFile(modalityCachePath()); err == nil {
		var f modalityCacheFile
		if json.Unmarshal(data, &f) == nil && f.Probes != nil {
			cache = f.Probes
		}
	}
	modalityCache = cache
	return modalityCache
}

func modalityCacheSaveLocked() {
	data, err := json.MarshalIndent(modalityCacheFile{Probes: modalityCache}, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(modalityCachePath(), data, 0600); err != nil {
		log.Printf("  modality probe cache save failed: %v", err)
	}
}

// modalityProbed 取某个 key 的探测结果。freshOnly=true 时超过 TTL 视为无结果。
func modalityProbed(key string, freshOnly bool) (modalityEntry, bool) {
	cache := modalityCacheLoad()
	modalityMu.Lock()
	defer modalityMu.Unlock()
	e, ok := cache[key]
	if !ok {
		return modalityEntry{}, false
	}
	if freshOnly && time.Since(time.UnixMilli(e.CheckedAt)) > modalityProbeTTL {
		return modalityEntry{}, false
	}
	return e, true
}

func modalityStore(key string, e modalityEntry) {
	cache := modalityCacheLoad()
	modalityMu.Lock()
	defer modalityMu.Unlock()
	cache[key] = e
	modalityCacheSaveLocked()
}

// applyModalityProbe 用探测结果覆盖目录给出的值(探测优先, 但只覆盖非 nil 的一侧)。
// 三个模型接口都调它, 保证"探测完刷新列表就能看到结论"。
func applyModalityProbe(key string, image, video any) (any, any) {
	e, ok := modalityProbed(key, true)
	if !ok {
		return image, video
	}
	if e.Image != nil {
		image = *e.Image
	}
	if e.Video != nil {
		video = *e.Video
	}
	return image, video
}

// modalityKey 探测缓存的稳定键。同一裸 id 可能同时存在于 WorkBuddy CN 与
// INTL(hy3 / glm-5.3), 所以必须带来源前缀, 不能只用模型名。
func modalityKey(source, baseID string) string {
	if source == "" {
		return baseID
	}
	return source + ":" + baseID
}

// selfBaseURL 探测要回调本进程, 监听在 0.0.0.0 时不能拿它当目标地址。
func selfBaseURL() string {
	host, port, err := net.SplitHostPort(proxyListenAddress)
	if err != nil {
		return "http://127.0.0.1:9999"
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func firstPoolKey() string {
	pool := loadPool()
	if len(pool.Keys) > 0 {
		return pool.Keys[0]
	}
	return ""
}

// modalityRejectedByUpstream 判断错误响应是不是"上游明确拒绝图片输入"。
// 只有命中这些关键词才敢判定"不支持"; 限流/欠费/参数错一律算"未知"。
func modalityRejectedByUpstream(body string) bool {
	s := strings.ToLower(body)
	for _, kw := range []string{
		"image", "vision", "multimodal", "multi-modal", "media",
		"unsupported content", "content type", "invalid content",
		"does not support", "not support", "不支持",
	} {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}

// 上游错误体里常套一层 JSON 信封(provider 侧还有 "Error in upstream response"
// 再包一层), 直接截断会切出半截 JSON。这里把最内层 message 抠出来再截断。
var modalityInnerMsgRE = regexp.MustCompile(`"message":"([^"]{1,300})"`)

func modalityShortNote(s string) string {
	s = strings.ReplaceAll(s, `\"`, `"`)
	if m := modalityInnerMsgRE.FindAllStringSubmatch(s, -1); len(m) > 0 {
		s = m[len(m)-1][1]
	}
	s = strings.NewReplacer(`\u003c`, "<", `\u003e`, ">", `\u0026`, "&", `\n`, " ").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > modalityNoteMaxRunes {
		s = string(r[:modalityNoteMaxRunes]) + "…"
	}
	return s
}

// probeModelModality 实调一次带图片的请求。model 用对外展示 id 或路由 id 均可,
// 由代理自己的路由逻辑解析。
func probeModelModality(model string) modalityEntry {
	payload := map[string]any{
		"model": model,
		"messages": []any{map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "text", "text": "What color is this image? Answer with one word."},
				map[string]any{"type": "image_url", "image_url": map[string]any{
					"url": "data:image/png;base64," + modalityProbeImage,
				}},
			},
		}},
		"max_tokens": 8,
		"stream":     false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return modalityEntry{CheckedAt: time.Now().UnixMilli(), Note: "构造请求失败: " + err.Error()}
	}

	req, err := http.NewRequest("POST", selfBaseURL()+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return modalityEntry{CheckedAt: time.Now().UnixMilli(), Note: "构造请求失败: " + err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	if key := firstPoolKey(); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	client := &http.Client{Timeout: modalityProbeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return modalityEntry{CheckedAt: time.Now().UnixMilli(), Note: modalityShortNote("请求失败: " + err.Error())}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	text := string(raw)

	yes, no := true, false
	if resp.StatusCode == http.StatusOK {
		return modalityEntry{Image: &yes, CheckedAt: time.Now().UnixMilli(), Note: "实调成功"}
	}
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity ||
		resp.StatusCode == http.StatusUnsupportedMediaType || resp.StatusCode == http.StatusNotAcceptable {
		if modalityRejectedByUpstream(text) {
			return modalityEntry{
				Image:     &no,
				CheckedAt: time.Now().UnixMilli(),
				Note:      modalityShortNote(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, text)),
			}
		}
	}
	// 其它情况(404 模型不存在 / 402 欠费 / 429 限流 / 500)都不能说明模态能力。
	return modalityEntry{
		CheckedAt: time.Now().UnixMilli(),
		Note:      modalityShortNote(fmt.Sprintf("无法判定 HTTP %d: %s", resp.StatusCode, text)),
	}
}

// POST /admin/api/models/probe-modality
// body: {"model":"...", "key":"workbuddy:hy3", "force":false}
func handleAdminModalityProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	var req struct {
		Model string `json:"model"`
		Key   string `json:"key"`
		Force bool   `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPI(w, http.StatusBadRequest, apiResponse{Error: "invalid JSON: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.Model) == "" {
		writeAPI(w, http.StatusBadRequest, apiResponse{Error: "model required"})
		return
	}
	key := req.Key
	if key == "" {
		key = req.Model
	}

	if !req.Force {
		if e, ok := modalityProbed(key, true); ok {
			writeAPI(w, http.StatusOK, apiResponse{Success: true, Data: map[string]any{
				"key": key, "image": e.Image, "video": e.Video, "note": e.Note,
				"checkedAt": e.CheckedAt, "cached": true,
			}})
			return
		}
	}

	// 同一模型并发点两次探测会白烧两次额度, 直接拒绝第二个。
	modalityMu.Lock()
	if modalityInFlight[key] {
		modalityMu.Unlock()
		writeAPI(w, http.StatusConflict, apiResponse{Error: "该模型正在探测中, 请稍候"})
		return
	}
	modalityInFlight[key] = true
	modalityMu.Unlock()
	defer func() {
		modalityMu.Lock()
		delete(modalityInFlight, key)
		modalityMu.Unlock()
	}()

	entry := probeModelModality(req.Model)
	modalityStore(key, entry)
	log.Printf("modality probe: %s (key=%s) image=%v note=%s", req.Model, key, entry.Image, entry.Note)
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Data: map[string]any{
		"key": key, "image": entry.Image, "video": entry.Video, "note": entry.Note,
		"checkedAt": entry.CheckedAt, "cached": false,
	}})
}
