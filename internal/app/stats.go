package app

import (
	"FreeModelRelay/internal/kit"
	"encoding/json"
	"log"
	"os"
	"sync"
	"time"
)

// ============================================================================
// 请求级统计与日志入库
// 落盘: zen-stats.jsonl 追加式(每请求一行)
// 内存: 今日/累计聚合,管理后台展示
// ============================================================================

type zenStatsRecord struct {
	TS               int64  `json:"ts"`
	Upstream         string `json:"upstream"` // zen / cline
	Model            string `json:"model"`    // 上游实际模型 ID
	Stream           bool   `json:"stream"`
	Compacted        bool   `json:"compacted"`
	OK               bool   `json:"ok"`
	Status           int    `json:"status"`
	LatencyMs        int64  `json:"latencyMs"`
	PromptTokens     int    `json:"promptTokens"`     // 入站估算
	CompletionTokens int    `json:"completionTokens"` // 上游 usage 或估算
	CompactionTokens int    `json:"compactionTokens"` // 摘要生成消耗
	RTKSaved         int    `json:"rtkSaved"`         // RTK 工具输出压缩节省的估算 token
	RateLimited      int    `json:"rateLimited"`      // 本次请求触发限流的次数
}

type zenStatsAgg struct {
	Date         string                    `json:"date"`
	Requests     int64                     `json:"requests"`
	PromptTok    int64                     `json:"promptTokens"`
	CompleteTok  int64                     `json:"completionTokens"`
	Compaction   int64                     `json:"compactionTokens"`
	RTKSaved     int64                     `json:"rtkSaved"`
	RateLimited  int64                     `json:"rateLimited"` // 限流命中次数
	ByModel      map[string]*zenStatsModel `json:"byModel"`
	ByClineModel map[string]*zenStatsModel `json:"byClineModel,omitempty"` // cline 上游按模型用量(模型清单页)
}

type zenStatsModel struct {
	Requests    int64 `json:"requests"`
	PromptTok   int64 `json:"promptTokens"`
	CompleteTok int64 `json:"completionTokens"`
	RTKSaved    int64 `json:"rtkSaved"` // RTK 压缩节省的估算 token
}

var (
	statsFile     *os.File
	statsFileMu   sync.Mutex
	statsToday    *zenStatsAgg
	statsTotal    *zenStatsAgg
	statsAggMu    sync.Mutex
	statsFileInit sync.Once
)

type zenStatsTracker struct {
	rec      zenStatsRecord
	started  time.Time
	finished bool
}

func newZenStatsTracker(rec zenStatsRecord) *zenStatsTracker {
	return &zenStatsTracker{rec: rec, started: time.Now()}
}

func (t *zenStatsTracker) finish(ok bool, status int) {
	if t.finished {
		return
	}
	t.finished = true
	t.rec.OK = ok
	t.rec.Status = status
	t.rec.LatencyMs = time.Since(t.started).Milliseconds()
	recordZenStats(t.rec)
}

func initStats() {
	statsFileInit.Do(func() {
		f, err := os.OpenFile(kit.ResolveDataPath("zen-stats.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			log.Printf("zen stats file open failed: %v", err)
			return
		}
		statsFile = f
		statsToday = newZenStatsAgg()
		statsTotal = newZenStatsAgg()
		agg := loadStatsFromFile()
		if agg != nil {
			statsTotal = agg
		}
		go rollStatsDate()
	})
}

func newZenStatsAgg() *zenStatsAgg {
	return &zenStatsAgg{
		Date:         time.Now().Format("2006-01-02"),
		ByModel:      map[string]*zenStatsModel{},
		ByClineModel: map[string]*zenStatsModel{},
	}
}

// loadStatsFromFile 从 JSONL 重建累计统计(仅今日的计入今日)
func loadStatsFromFile() *zenStatsAgg {
	agg := newZenStatsAgg()
	data, err := os.ReadFile(kit.ResolveDataPath("zen-stats.jsonl"))
	if err != nil {
		return nil
	}
	today := time.Now().Format("2006-01-02")
	lines := splitLines(string(data))
	for _, line := range lines {
		var rec zenStatsRecord
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		aggregateRecord(agg, &rec)
		if time.UnixMilli(rec.TS).Format("2006-01-02") == today {
			aggregateRecord(statsToday, &rec)
		}
	}
	return agg
}

func aggregateRecord(agg *zenStatsAgg, rec *zenStatsRecord) {
	if agg == nil || rec == nil {
		return
	}
	// cline 上游请求只进独立按模型桶(ByClineModel), 不进顶层汇总与 ByModel,
	// 保持 opencode 统计页(zen 口径)不变; 模型清单页读 cline 桶
	if rec.Upstream == "cline" {
		m := agg.ByClineModel[rec.Model]
		if m == nil {
			m = &zenStatsModel{}
			agg.ByClineModel[rec.Model] = m
		}
		m.Requests++
		m.PromptTok += int64(rec.PromptTokens)
		m.CompleteTok += int64(rec.CompletionTokens)
		return
	}
	agg.Requests++
	agg.PromptTok += int64(rec.PromptTokens)
	agg.CompleteTok += int64(rec.CompletionTokens)
	agg.Compaction += int64(rec.CompactionTokens)
	agg.RTKSaved += int64(rec.RTKSaved)
	agg.RateLimited += int64(rec.RateLimited)
	m := agg.ByModel[rec.Model]
	if m == nil {
		m = &zenStatsModel{}
		agg.ByModel[rec.Model] = m
	}
	m.Requests++
	m.PromptTok += int64(rec.PromptTokens)
	m.CompleteTok += int64(rec.CompletionTokens)
	m.RTKSaved += int64(rec.RTKSaved)
}

// modelUsageSnapshot 返回全部模型的用量快照, key=上游模型 ID。
// 合并 zen 桶(ByModel)与 cline 桶(ByClineModel), 两个模型列表页各自按 ID 取用。
// 每个模型含 today*/total* 两组计数(请求数/输入/输出/RTK 节省)。
func modelUsageSnapshot() map[string]map[string]int64 {
	statsAggMu.Lock()
	defer statsAggMu.Unlock()
	out := make(map[string]map[string]int64, len(statsTotal.ByModel))
	collect := func(mm map[string]*zenStatsModel, prefix string) {
		for id, m := range mm {
			e, ok := out[id]
			if !ok {
				e = map[string]int64{}
				out[id] = e
			}
			e[prefix+"Requests"] += m.Requests
			e[prefix+"PromptTokens"] += m.PromptTok
			e[prefix+"CompletionTokens"] += m.CompleteTok
			e[prefix+"RTKSaved"] += m.RTKSaved
		}
	}
	collect(statsToday.ByModel, "today")
	collect(statsToday.ByClineModel, "today")
	collect(statsTotal.ByModel, "total")
	collect(statsTotal.ByClineModel, "total")
	return out
}

// recordClineModelStats 记录 cline 上游请求的按模型用量: 写入 zen-stats.jsonl
// (upstream=cline, 便于事后分析) 并聚合到 ByClineModel, 供模型清单页用量列展示。
// model 必须是上游实际收到的原始模型名(不做 normalize, 避免误归属到默认模型)。
func recordClineModelStats(model string, promptTok, completeTok int) {
	if model == "" {
		return
	}
	recordZenStats(zenStatsRecord{
		TS:               time.Now().UnixMilli(),
		Upstream:         "cline",
		Model:            model,
		OK:               true,
		PromptTokens:     promptTok,
		CompletionTokens: completeTok,
	})
}

func splitLines(s string) []string {
	out := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func recordZenStats(rec zenStatsRecord) {
	initStats()
	statsFileMu.Lock()
	if statsFile != nil {
		b, err := json.Marshal(rec)
		if err == nil {
			statsFile.Write(append(b, '\n'))
		}
	}
	statsFileMu.Unlock()

	statsAggMu.Lock()
	aggregateRecord(statsToday, &rec)
	aggregateRecord(statsTotal, &rec)
	statsAggMu.Unlock()
}

func rollStatsDate() {
	ticker := time.NewTicker(time.Minute)
	for range ticker.C {
		today := time.Now().Format("2006-01-02")
		statsAggMu.Lock()
		if statsToday.Date != today {
			statsToday = newZenStatsAgg()
		}
		statsAggMu.Unlock()
	}
}

func zenStatsSnapshot() map[string]any {
	initStats()
	statsAggMu.Lock()
	defer statsAggMu.Unlock()
	return map[string]any{
		"today": statsToday,
		"total": statsTotal,
	}
}
