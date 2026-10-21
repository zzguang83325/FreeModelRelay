package app

import (
	"sync"
	"sync/atomic"
	"time"
)

// ============ 全局指标收集器（纯内存，无持久化） ============

var globalMetrics metricsCollector

func init() {
	globalMetrics.init()
}

type metricsCollector struct {
	startTime time.Time

	// 请求计数
	totalRequests atomic.Int64
	totalErrors   atomic.Int64

	// Token 累计
	totalPromptTokens     atomic.Int64
	totalCompletionTokens atomic.Int64

	// 按 provider 的 token 统计
	providerMu    sync.RWMutex
	providerStats map[string]*providerMetric // key: route (zen/cline/workbuddy/qoder/trae/loomy)

	// 按模型的请求次数统计
	modelMu    sync.RWMutex
	modelStats map[string]*modelMetric // key: model id

	// 最近 60 分钟流量（每分钟一个桶，环形缓冲）
	trafficMu      sync.Mutex
	trafficBuckets [60]int64
	lastMinute     int // 上次写入的分钟索引（0-59），用于惰性清零
}

type providerMetric struct {
	PromptTokens     int64 `json:"promptTokens"`
	CompletionTokens int64 `json:"completionTokens"`
	Requests         int64 `json:"requests"`
	Errors           int64 `json:"errors"`
}

type modelMetric struct {
	Requests         int64 `json:"requests"`
	PromptTokens     int64 `json:"promptTokens"`
	CompletionTokens int64 `json:"completionTokens"`
}

func (m *metricsCollector) init() {
	m.startTime = time.Now()
	m.providerStats = make(map[string]*providerMetric)
	m.modelStats = make(map[string]*modelMetric)
	m.lastMinute = -1
}

// RecordRequest 记录一次 LLM 请求完成。在 proxy.go 的请求处理结束后调用。
func RecordRequest(route, model string, status int, promptTokens, completionTokens int64) {
	g := &globalMetrics

	g.totalRequests.Add(1)
	if status >= 400 {
		g.totalErrors.Add(1)
	}
	g.totalPromptTokens.Add(promptTokens)
	g.totalCompletionTokens.Add(completionTokens)

	// 按 provider 统计
	g.providerMu.Lock()
	ps, ok := g.providerStats[route]
	if !ok {
		ps = &providerMetric{}
		g.providerStats[route] = ps
	}
	ps.Requests++
	ps.PromptTokens += promptTokens
	ps.CompletionTokens += completionTokens
	if status >= 400 {
		ps.Errors++
	}
	g.providerMu.Unlock()

	// 按模型统计
	if model != "" {
		g.modelMu.Lock()
		ms, ok := g.modelStats[model]
		if !ok {
			ms = &modelMetric{}
			g.modelStats[model] = ms
		}
		ms.Requests++
		ms.PromptTokens += promptTokens
		ms.CompletionTokens += completionTokens
		g.modelMu.Unlock()
	}

	// 流量桶
	g.recordTraffic()
}

// recordTraffic 将当前请求计入对应分钟的桶中
func (m *metricsCollector) recordTraffic() {
	now := time.Now()
	currentMinute := now.Hour()*60 + now.Minute() // 一天内的分钟数
	idx := currentMinute % 60                     // 环形缓冲索引

	m.trafficMu.Lock()
	defer m.trafficMu.Unlock()

	// 如果跨过了分钟边界，需要把旧数据清零
	if m.lastMinute != idx {
		// 计算跨过了多少个桶
		var gap int
		if m.lastMinute < 0 {
			gap = 60 // 首次启动，全部清零
		} else if idx > m.lastMinute {
			gap = idx - m.lastMinute
		} else {
			gap = idx + 60 - m.lastMinute
		}
		// 清零经过的桶
		for i := 1; i <= gap && i <= 60; i++ {
			clearIdx := (m.lastMinute + i) % 60
			m.trafficBuckets[clearIdx] = 0
		}
		m.lastMinute = idx
	}
	m.trafficBuckets[idx]++
}

// dashboardSummary 返回仪表盘概览数据
func dashboardSummary() map[string]any {
	g := &globalMetrics

	g.providerMu.RLock()
	providerCopy := make(map[string]providerMetric, len(g.providerStats))
	for k, v := range g.providerStats {
		providerCopy[k] = *v
	}
	g.providerMu.RUnlock()

	g.modelMu.RLock()
	var topModels []modelStatEntry
	for k, v := range g.modelStats {
		topModels = append(topModels, modelStatEntry{
			Model: k, Requests: v.Requests,
			PromptTokens: v.PromptTokens, CompletionTokens: v.CompletionTokens,
		})
	}
	g.modelMu.RUnlock()

	// 按请求数降序排列，取前 10
	sortModelsByRequests(topModels)
	if len(topModels) > 10 {
		topModels = topModels[:10]
	}

	// 流量时间线：从当前分钟往前推 60 个桶
	traffic := buildTrafficTimeline()

	totalReqs := g.totalRequests.Load()
	totalErrs := g.totalErrors.Load()
	var errorRate float64
	if totalReqs > 0 {
		errorRate = float64(totalErrs) / float64(totalReqs) * 100
	}

	return map[string]any{
		"uptime":                time.Since(g.startTime).Truncate(time.Second).String(),
		"totalRequests":         totalReqs,
		"totalErrors":           totalErrs,
		"errorRate":             roundTo(errorRate, 2),
		"totalPromptTokens":     g.totalPromptTokens.Load(),
		"totalCompletionTokens": g.totalCompletionTokens.Load(),
		"totalTokens":           g.totalPromptTokens.Load() + g.totalCompletionTokens.Load(),
		"providers":             providerCopy,
		"topModels":             topModels,
		"traffic":               traffic,
	}
}

// buildTrafficTimeline 构建最近 60 分钟的流量时间线
func buildTrafficTimeline() []map[string]any {
	g := &globalMetrics
	now := time.Now()
	currentMinute := now.Hour()*60 + now.Minute()
	currentIdx := currentMinute % 60

	g.trafficMu.Lock()
	defer g.trafficMu.Unlock()

	result := make([]map[string]any, 0, 60)
	// 从最旧的桶开始，到当前桶结束
	for i := 59; i >= 0; i-- {
		idx := (currentIdx - i + 60) % 60
		minutesAgo := i
		t := now.Add(-time.Duration(minutesAgo) * time.Minute)
		label := t.Format("15:04")
		count := g.trafficBuckets[idx]
		// 如果这个桶是超过 60 分钟前的旧数据（即 lastMinute 之后没更新过），显示为 0
		result = append(result, map[string]any{
			"label": label,
			"count": count,
		})
	}
	return result
}

type modelStatEntry struct {
	Model            string `json:"model"`
	Requests         int64  `json:"requests"`
	PromptTokens     int64  `json:"promptTokens"`
	CompletionTokens int64  `json:"completionTokens"`
}

func sortModelsByRequests(models []modelStatEntry) {
	for i := 1; i < len(models); i++ {
		key := models[i]
		j := i - 1
		for j >= 0 && models[j].Requests < key.Requests {
			models[j+1] = models[j]
			j--
		}
		models[j+1] = key
	}
}

func roundTo(val float64, places int) float64 {
	mul := 1.0
	for i := 0; i < places; i++ {
		mul *= 10
	}
	return float64(int(val*mul+0.5)) / mul
}
