package app

import (
	"encoding/json"
	"net/http"
	"os"
	"sync"

	"FreeModelRelay/internal/kit"
)

// 出口代理配置: 手动代理地址 + 是否跟随系统代理。
type egressProxyConfigData struct {
	Manual       string `json:"manual"`
	FollowSystem bool   `json:"followSystem"`
}

var (
	egressProxyMu   sync.Mutex
	egressProxyData = egressProxyConfigData{FollowSystem: true}
)

const egressProxyFile = ".egress-proxy.json"

// loadEgressProxyConfig 从 data/ 目录读取出口代理配置并应用到 kit。
// 读取失败时保留默认值(跟随系统代理)。
func loadEgressProxyConfig() {
	path := kit.ResolveDataPath(egressProxyFile)
	data, err := os.ReadFile(path)
	if err != nil {
		// 无配置文件: 用默认值(跟随系统代理)
		egressProxyMu.Lock()
		egressProxyData = egressProxyConfigData{FollowSystem: true}
		egressProxyMu.Unlock()
		kit.SetEgressProxy("", true)
		return
	}
	var cfg egressProxyConfigData
	if err := json.Unmarshal(data, &cfg); err != nil {
		cfg = egressProxyConfigData{FollowSystem: true}
	}
	egressProxyMu.Lock()
	egressProxyData = cfg
	egressProxyMu.Unlock()
	if err := kit.SetEgressProxy(cfg.Manual, cfg.FollowSystem); err != nil {
		// 配置非法时退回跟随系统代理, 避免启动失败
		kit.SetEgressProxy("", true)
	}
}

// saveEgressProxyConfig 保存并应用出口代理配置。
func saveEgressProxyConfig(manual string, followSystem bool) error {
	if _, err := kit.ParseProxyURL(manual); err != nil {
		return err
	}
	// 先校验通过再落盘, 避免写入非法配置
	if err := kit.SetEgressProxy(manual, followSystem); err != nil {
		return err
	}
	egressProxyMu.Lock()
	egressProxyData = egressProxyConfigData{Manual: manual, FollowSystem: followSystem}
	egressProxyMu.Unlock()

	data, err := json.MarshalIndent(egressProxyData, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(kit.ResolveDataPath(egressProxyFile), data, 0644)
}

// getEgressProxyConfig 返回当前配置与生效状态。
func getEgressProxyConfig() map[string]any {
	egressProxyMu.Lock()
	cfg := egressProxyData
	egressProxyMu.Unlock()

	proxyURL, source, systemURL, systemEnabled := kit.EgressProxyStatus()
	if proxyURL != "" {
		proxyURL = maskProxyURL(proxyURL)
	}
	if systemURL != "" {
		systemURL = maskProxyURL(systemURL)
	}
	return map[string]any{
		"manual":       cfg.Manual,
		"followSystem": cfg.FollowSystem,
		"effective":    proxyURL,
		"source":       source,
		"systemProxy":  systemURL,
		"systemOn":     systemEnabled,
	}
}

// handleEgressProxyConfig 返回出口代理配置。
func handleEgressProxyConfig(w http.ResponseWriter, r *http.Request) {
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Data: getEgressProxyConfig()})
}

// handleEgressProxyConfigUpdate 保存出口代理配置。
func handleEgressProxyConfigUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "只支持 POST"})
		return
	}
	var patch struct {
		Manual       *string `json:"manual"`
		FollowSystem *bool   `json:"followSystem"`
	}
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeAPI(w, http.StatusBadRequest, apiResponse{Error: "参数解析失败: " + err.Error()})
		return
	}

	egressProxyMu.Lock()
	next := egressProxyData
	egressProxyMu.Unlock()
	if patch.Manual != nil {
		next.Manual = *patch.Manual
	}
	if patch.FollowSystem != nil {
		next.FollowSystem = *patch.FollowSystem
	}

	if err := saveEgressProxyConfig(next.Manual, next.FollowSystem); err != nil {
		writeAPI(w, http.StatusBadRequest, apiResponse{Error: err.Error()})
		return
	}
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Data: getEgressProxyConfig()})
}
