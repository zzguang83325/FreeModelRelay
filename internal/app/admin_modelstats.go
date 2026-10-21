package app

import (
	"net/http"
	"strings"
)

// modelSourceStat 单个模型来源的模型数量统计。
type modelSourceStat struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Total int    `json:"total"` // 该来源目录里的模型总数
	Free  int    `json:"free"`  // 其中判定为免费的个数
}

// modelStatsSnapshot 统计各模型来源的模型总数与免费模型数。
// 口径与「全部模型」页保持一致: 上游目录给出的条目全部计入 total,
// 其中判定为免费的计入 free。
//   - cline:        官方免费清单, 整池免费
//   - opencode-zen: zen 模型表(免费 + 付费), 只有免费的会被路由
//   - workbuddy:    credits 倍率为 0 才是免费(缺 credits 字段 = 未标价, 不算免费)
//   - qoder:        price_factor 为 0 才是免费
//   - trae:         必须真的带 credit_multiplier 键(priced)且倍率为 0 才是免费
func modelStatsSnapshot() []modelSourceStat {
	out := []modelSourceStat{}

	// cline: 官方免费清单
	clineModels := getFreeModels()
	clineFree := 0
	for _, m := range clineModels {
		if strings.EqualFold(m.Cost, "free") {
			clineFree++
		}
	}
	out = append(out, modelSourceStat{
		Key: "cline-free", Label: "Cline",
		Total: len(clineModels), Free: clineFree,
	})

	// opencode zen: 付费模型会被路由拒绝, 但仍计入总数便于看免费占比
	initZenModels()
	zenTotal, zenFree := 0, 0
	zenModelsMu.RLock()
	for _, m := range zenModels {
		zenTotal++
		if isZenFreeModel(m) {
			zenFree++
		}
	}
	zenModelsMu.RUnlock()
	out = append(out, modelSourceStat{
		Key: "opencode-zen", Label: "OpenCode Zen",
		Total: zenTotal, Free: zenFree,
	})

	// WorkBuddy 两个变体
	for _, v := range []*wbVariant{wbVariantCN, wbVariantINTL} {
		total, free := 0, 0
		for _, m := range wbModelList() {
			if m["owned_by"] != v.Provider {
				continue
			}
			total++
			if m["cost"] == "free" {
				free++
			}
		}
		out = append(out, modelSourceStat{
			Key: v.Provider, Label: v.DisplayName,
			Total: total, Free: free,
		})
	}

	// Qoder: price_factor 为 0 才是免费
	qTotal, qFree := 0, 0
	for _, m := range qoderModelList() {
		qTotal++
		if f, ok := m["priceFactor"].(float64); ok && f == 0 {
			qFree++
		}
	}
	out = append(out, modelSourceStat{
		Key: "qoder", Label: "Qoder",
		Total: qTotal, Free: qFree,
	})

	// Trae: priced=false(上游没给 credit_multiplier)不能当成免费
	tTotal, tFree := 0, 0
	for _, m := range traeModelList() {
		tTotal++
		if priced, ok := m["priced"].(bool); ok && priced {
			if f, ok := m["priceFactor"].(float64); ok && f == 0 {
				tFree++
			}
		}
	}
	out = append(out, modelSourceStat{
		Key: "trae", Label: "Trae",
		Total: tTotal, Free: tFree,
	})

	// 科大讯飞Loomy: 点数制(倍率>0), 只有标记 free=true 的限时免费模型计入免费;
	// key 与 /providers/credits 的 loomy 一致, 仪表盘卡片会附带剩余点数。
	lTotal, lFree := 0, 0
	for _, m := range loomyModelList() {
		lTotal++
		if f, ok := m["free"].(bool); ok && f {
			lFree++
		}
	}
	out = append(out, modelSourceStat{
		Key: "loomy", Label: "科大讯飞Loomy",
		Total: lTotal, Free: lFree,
	})

	return out
}

// GET /admin/api/model-stats  各模型来源的模型总数与免费模型数
func handleAdminModelStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Data: modelStatsSnapshot()})
}
