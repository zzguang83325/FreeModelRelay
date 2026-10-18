package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// ============ Zen 免费模型管理 API ============

// GET /admin/api/zen/config
func handleZenConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	cfg := getZenConfig()
	data := map[string]any{
		"enabled":         cfg.Enabled,
		"key":             cfg.Key,
		"baseURL":         cfg.BaseURL,
		"proxies":         cfg.Proxies,
		"proxyStrategy":   cfg.ProxyStrategy,
		"maxConcurrency":  cfg.MaxConcurrency,
		"retries":         cfg.Retries,
		"failover":        cfg.Failover,
		"failoverCount":   cfg.FailoverCount,
		"failoverMinutes": cfg.FailoverMinutes,
		"compaction":      cfg.Compaction,
		"rtk":             cfg.RTK,
		"runtime": map[string]any{
			"failoverActive": zenFailedNow(),
			"proxyCooldowns": zenProxyCooldownStatus(),
		},
	}
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Data: data})
}

// POST /admin/api/zen/config/update
func handleZenConfigUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAPI(w, http.StatusBadRequest, apiResponse{Error: err.Error()})
		return
	}
	defer r.Body.Close()

	cur := getZenConfig()
	var patch struct {
		Enabled         *bool    `json:"enabled"`
		Key             *string  `json:"key"`
		BaseURL         *string  `json:"baseURL"`
		Proxies         []string `json:"proxies"`
		ProxyStrategy   *string  `json:"proxyStrategy"`
		MaxConcurrency  *int     `json:"maxConcurrency"`
		Retries         *int     `json:"retries"`
		Failover        *bool    `json:"failover"`
		FailoverCount   *int     `json:"failoverCount"`
		FailoverMinutes *int     `json:"failoverMinutes"`
		Compaction      *struct {
			Auto         *bool   `json:"auto"`
			Buffer       *int    `json:"buffer"`
			KeepTokens   *int    `json:"keepTokens"`
			SummaryModel *string `json:"summaryModel"`
			MaxSummary   *int    `json:"maxSummary"`
		} `json:"compaction"`
		Rtk *struct {
			Level       *int  `json:"level"`
			MinChars    *int  `json:"minChars"`
			KeepHead    *int  `json:"keepHead"`
			KeepTail    *int  `json:"keepTail"`
			GrepDedup   *bool `json:"grepDedup"`
			FilterNoise *bool `json:"filterNoise"`
		} `json:"rtk"`
	}
	if err := json.Unmarshal(body, &patch); err != nil {
		writeAPI(w, http.StatusBadRequest, apiResponse{Error: "invalid JSON: " + err.Error()})
		return
	}
	next := &zenConfigData{
		Enabled:         cur.Enabled,
		Key:             cur.Key,
		BaseURL:         cur.BaseURL,
		Proxies:         cur.Proxies,
		ProxyStrategy:   cur.ProxyStrategy,
		MaxConcurrency:  cur.MaxConcurrency,
		Retries:         cur.Retries,
		Failover:        cur.Failover,
		FailoverCount:   cur.FailoverCount,
		FailoverMinutes: cur.FailoverMinutes,
		Compaction:      cur.Compaction,
		RTK:             cur.RTK,
	}
	if patch.Enabled != nil {
		next.Enabled = *patch.Enabled
	}
	if patch.Key != nil && *patch.Key != "" {
		next.Key = *patch.Key
	}
	if patch.BaseURL != nil && *patch.BaseURL != "" {
		next.BaseURL = strings.TrimRight(*patch.BaseURL, "/")
	}
	if patch.Proxies != nil {
		if err := validateProxyList(patch.Proxies); err != nil {
			writeAPI(w, http.StatusBadRequest, apiResponse{Error: err.Error()})
			return
		}
		next.Proxies = patch.Proxies
	}
	if patch.ProxyStrategy != nil && *patch.ProxyStrategy != "" {
		next.ProxyStrategy = *patch.ProxyStrategy
	}
	if patch.MaxConcurrency != nil && *patch.MaxConcurrency > 0 {
		next.MaxConcurrency = *patch.MaxConcurrency
	}
	if patch.Retries != nil && *patch.Retries >= 0 {
		next.Retries = *patch.Retries
	}
	if patch.Failover != nil {
		next.Failover = *patch.Failover
	}
	if patch.FailoverCount != nil && *patch.FailoverCount > 0 {
		next.FailoverCount = *patch.FailoverCount
	}
	if patch.FailoverMinutes != nil && *patch.FailoverMinutes > 0 {
		next.FailoverMinutes = *patch.FailoverMinutes
	}
	if patch.Compaction != nil {
		base := cur.Compaction
		if patch.Compaction.Buffer != nil {
			base.Buffer = *patch.Compaction.Buffer
		}
		if patch.Compaction.KeepTokens != nil {
			base.KeepTokens = *patch.Compaction.KeepTokens
		}
		if patch.Compaction.SummaryModel != nil {
			base.SummaryModel = *patch.Compaction.SummaryModel
		}
		if patch.Compaction.MaxSummary != nil {
			base.MaxSummary = *patch.Compaction.MaxSummary
		}
		if patch.Compaction.Auto != nil {
			base.Auto = *patch.Compaction.Auto
		}
		next.Compaction = base
	}
	if patch.Rtk != nil {
		base := cur.RTK
		if patch.Rtk.Level != nil {
			base.Level = *patch.Rtk.Level
		}
		if patch.Rtk.MinChars != nil {
			base.MinChars = *patch.Rtk.MinChars
		}
		if patch.Rtk.KeepHead != nil {
			base.KeepHead = *patch.Rtk.KeepHead
		}
		if patch.Rtk.KeepTail != nil {
			base.KeepTail = *patch.Rtk.KeepTail
		}
		if patch.Rtk.GrepDedup != nil {
			base.GrepDedup = *patch.Rtk.GrepDedup
		}
		if patch.Rtk.FilterNoise != nil {
			base.FilterNoise = *patch.Rtk.FilterNoise
		}
		next.RTK = base
	}
	setZenConfig(next)
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Data: getZenConfig()})
}

// GET /admin/api/opencode/models — 只返回免费模型
func handleZenModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	initZenModels()
	usage := modelUsageSnapshot()
	zenModelsMu.RLock()
	models := make([]map[string]any, 0, len(zenModels))
	for _, m := range zenModels {
		if !isZenFreeModel(m) {
			continue
		}
		entry, known := catalogMeta(m.ID)
		// 目录没给 modalities 时留 null —— 后台显示"?"而不是"不支持"。
		var image, video any
		if known && entry.MetaKnown {
			image = entry.Image
			video = entry.Video
		}
		image, video = applyModalityProbe(modalityKey("zen", m.ID), image, video)
		models = append(models, map[string]any{
			"id":      m.ID,
			"aliases": m.Aliases,
			"context": m.Context,
			"output":  m.Output,
			"source":  m.Source,
			"image":   image,
			"video":   video,
			"usage":   usage[m.ID],
		})
	}
	zenModelsMu.RUnlock()
	// map 遍历顺序随机, 按 ID 排序保证管理页列表顺序稳定
	sort.Slice(models, func(i, j int) bool {
		return models[i]["id"].(string) < models[j]["id"].(string)
	})
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Data: map[string]any{"models": models, "count": len(models)}})
}

// POST /admin/api/zen/models/refresh
func handleZenModelsRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	catErr := refreshCatalog()
	added, err := syncZenModels()
	if err != nil && catErr != nil {
		writeAPI(w, http.StatusBadGateway, apiResponse{Error: "sync failed: " + err.Error() + "; catalog: " + catErr.Error()})
		return
	}
	msg := fmt.Sprintf("synced, %d new models", added)
	if catErr != nil {
		msg += fmt.Sprintf("; catalog refresh failed (%v), using cached", catErr)
	} else {
		msg += fmt.Sprintf("; catalog %d free models", len(catalogSnapshot()))
	}
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Message: msg})
}

// GET /admin/api/zen/stats
func handleZenStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Data: zenStatsSnapshot()})
}
