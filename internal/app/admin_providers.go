package app

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ============================================================================
// 第三方 provider 后台管理 API (/admin/api/providers/...)
//
// 四个 provider:
//   workbuddy     WorkBuddy (CN)    —— 复用本机 WorkBuddy 桌面端登录
//   workbuddy-ai  WorkBuddy AI (INTL) —— 复用本机 WorkBuddy AI 桌面端登录
//   qoder         Qoder (China)     —— 手动填入个人访问令牌 (pat-…)
//   trae          Trae (CN)         —— 从本机 storage.json 解密导入(同 WorkBuddy)
// ============================================================================

// GET /admin/api/providers/status
func handleProvidersStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Data: map[string]any{
		"workbuddy":    wbStatusPayload(wbVariantCN),
		"workbuddy-ai": wbStatusPayload(wbVariantINTL),
		"qoder":        qoderStatusPayload(),
		"trae":         traeStatusPayload(),
	}})
}

func wbStatusPayload(v *wbVariant) map[string]any {
	cfg := getWBConfig()
	accounts := wbActiveAccounts(v.ID)
	list := make([]map[string]any, 0, len(accounts))
	for _, a := range accounts {
		expired := a.ExpiresAt > 0 && time.Now().UnixMilli() >= a.ExpiresAt
		list = append(list, map[string]any{
			"variant":       a.Variant,
			"uid":           a.UID,
			"uin":           a.Uin,
			"email":         a.Email,
			"nickname":      a.Nickname,
			"domain":        a.Domain,
			"expiresAt":     a.ExpiresAt,
			"expiresAtText": msToText(a.ExpiresAt),
			"expired":       expired,
			"hasToken":      a.AccessToken != "",
			"lastError":     a.LastError,
		})
	}
	enabled := cfg.CN
	if v.Region == "global" {
		enabled = cfg.INTL
	}
	models := []map[string]any{}
	for _, m := range wbModelList() {
		if m["owned_by"] == v.Provider {
			models = append(models, m)
		}
	}
	return map[string]any{
		"id":          v.ID,
		"displayName": v.DisplayName,
		"region":      v.Region,
		"chatBase":    v.ChatBase,
		"enabled":     enabled,
		"maxRetries":  cfg.MaxRetries,
		"importable":  true,
		"desktopFile": v.DesktopFilename,
		"fileFound":   fileExists(filepath.Join(wbAuthDirOf(v), v.DesktopFilename)),
		"electron":    wbFindElectron(v),
		"authDir":     wbAuthDirOf(v),
		"accounts":    list,
		"models":      models,
		"modelCount":  len(models),
	}
}

func wbAuthDirOf(v *wbVariant) string {
	for _, dir := range wbAuthDirs() {
		if fileExists(filepath.Join(dir, v.DesktopFilename)) {
			return dir
		}
	}
	dirs := wbAuthDirs()
	if len(dirs) > 0 {
		return dirs[0]
	}
	return ""
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func msToText(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).Format("2006-01-02 15:04:05")
}

func qoderStatusPayload() map[string]any {
	cfg := getQoderConfig()
	accounts := qoderLoadAccounts()
	list := make([]map[string]any, 0, len(accounts))
	for _, a := range accounts {
		list = append(list, map[string]any{
			"id":          a.ID,
			"region":      a.Region,
			"userId":      a.UserID,
			"name":        a.Name,
			"email":       a.Email,
			"patTail":     patTail(a.PAT),
			"hasToken":    a.JobToken != "",
			"tokenExpiry": msToText(a.TokenExpiresAt),
			"machineId":   a.MachineID,
			"lastError":   a.LastError,
		})
	}
	models := qoderModelList()
	return map[string]any{
		"id":          "qoder",
		"displayName": "Qoder",
		"region":      "china",
		"openApiUrl":  qoderOpenAPIURL,
		"chatBase":    qoderBaseURL,
		"enabled":     cfg.Enabled,
		"freeOnly":    cfg.FreeOnly,
		"maxRetries":  cfg.MaxRetries,
		"importable":  false,
		"accounts":    list,
		"models":      models,
		"modelCount":  len(models),
	}
}

func patTail(pat string) string {
	if len(pat) <= 10 {
		return "***"
	}
	return "…" + pat[len(pat)-6:]
}

// POST /admin/api/providers/workbuddy/config  body: {enabled, cn, intl, maxRetries}
func handleProviderConfigUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	provider := strings.TrimPrefix(r.URL.Path, "/admin/api/providers/")
	provider = strings.TrimSuffix(provider, "/config")
	provider = strings.TrimSuffix(provider, "/config/update")

	switch provider {
	case "workbuddy", "workbuddy-ai":
		cur := getWBConfig()
		var patch struct {
			Enabled    *bool `json:"enabled"`
			MaxRetries *int  `json:"maxRetries"`
			CN         *bool `json:"cn"`
			INTL       *bool `json:"intl"`
		}
		if err := readJSONBody(r, &patch); err != nil {
			writeAPI(w, http.StatusBadRequest, apiResponse{Error: "invalid JSON: " + err.Error()})
			return
		}
		if patch.Enabled != nil {
			// 单变体开关: 只影响该变体
			if provider == "workbuddy" {
				cur.CN = *patch.Enabled
			} else {
				cur.INTL = *patch.Enabled
			}
		}
		if patch.CN != nil {
			cur.CN = *patch.CN
		}
		if patch.INTL != nil {
			cur.INTL = *patch.INTL
		}
		if patch.MaxRetries != nil && *patch.MaxRetries >= 0 {
			cur.MaxRetries = *patch.MaxRetries
		}
		cur.Enabled = cur.CN || cur.INTL
		setWBConfig(cur)
		writeAPI(w, http.StatusOK, apiResponse{Success: true, Message: "配置已保存", Data: getWBConfig()})
	case "qoder":
		cur := getQoderConfig()
		var patch struct {
			Enabled    *bool `json:"enabled"`
			FreeOnly   *bool `json:"freeOnly"`
			MaxRetries *int  `json:"maxRetries"`
		}
		if err := readJSONBody(r, &patch); err != nil {
			writeAPI(w, http.StatusBadRequest, apiResponse{Error: "invalid JSON: " + err.Error()})
			return
		}
		if patch.Enabled != nil {
			cur.Enabled = *patch.Enabled
		}
		if patch.FreeOnly != nil {
			cur.FreeOnly = *patch.FreeOnly
		}
		if patch.MaxRetries != nil && *patch.MaxRetries >= 0 {
			cur.MaxRetries = *patch.MaxRetries
		}
		setQoderConfig(cur)
		writeAPI(w, http.StatusOK, apiResponse{Success: true, Message: "配置已保存", Data: getQoderConfig()})
	case "trae":
		cur := getTraeConfig()
		var patch struct {
			Enabled    *bool `json:"enabled"`
			FreeOnly   *bool `json:"freeOnly"`
			MaxRetries *int  `json:"maxRetries"`
		}
		if err := readJSONBody(r, &patch); err != nil {
			writeAPI(w, http.StatusBadRequest, apiResponse{Error: "invalid JSON: " + err.Error()})
			return
		}
		if patch.Enabled != nil {
			cur.Enabled = *patch.Enabled
		}
		if patch.FreeOnly != nil {
			cur.FreeOnly = *patch.FreeOnly
		}
		if patch.MaxRetries != nil && *patch.MaxRetries >= 0 {
			cur.MaxRetries = *patch.MaxRetries
		}
		setTraeConfig(cur)
		writeAPI(w, http.StatusOK, apiResponse{Success: true, Message: "配置已保存", Data: getTraeConfig()})
	default:
		writeAPI(w, http.StatusNotFound, apiResponse{Error: "unknown provider"})
	}
}

// wbSetAccountsIfEmpty 仅在账号池为空时写入导入结果并落盘, 返回是否写入。
// 锁的获取/释放都在这里完成(defer), 调用方不得再持有 wbAccMu。
func wbSetAccountsIfEmpty(accounts []*wbAccount) bool {
	wbAccMu.Lock()
	defer wbAccMu.Unlock()
	if len(wbAccounts) > 0 {
		return false
	}
	wbAccounts = accounts
	wbSaveAccountsLocked()
	return true
}

// wbMergeImportedAccounts 把本机导入的账号按 variant 覆盖进账号池并落盘。
// 锁的获取/释放都在这里完成(defer), 调用方不得再持有 wbAccMu。
func wbMergeImportedAccounts(accounts []*wbAccount) {
	wbAccMu.Lock()
	defer wbAccMu.Unlock()
	byVariant := map[string]*wbAccount{}
	for _, a := range wbAccounts {
		byVariant[a.Variant] = a
	}
	for _, a := range accounts {
		byVariant[a.Variant] = a
	}
	merged := make([]*wbAccount, 0, len(byVariant))
	for _, a := range byVariant {
		merged = append(merged, a)
	}
	wbAccounts = merged
	wbSaveAccountsLocked()
}

// POST /admin/api/providers/workbuddy/import  从本机桌面端重新导入账号
func handleProviderImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	accounts, notes := wbReadLocalAccounts()
	if len(accounts) == 0 {
		writeAPI(w, http.StatusBadGateway, apiResponse{
			Error:   "未从本机读取到 WorkBuddy 账号",
			Message: strings.Join(notes, "; "),
		})
		return
	}
	// 先把已保存账号读进内存(带锁包装内部自己加锁, 不可与下面临界区嵌套调用)
	wbLoadAccounts()
	wbMergeImportedAccounts(accounts)

	msg := fmt.Sprintf("已导入 %d 个账号", len(accounts))
	if len(notes) > 0 {
		msg += "；" + strings.Join(notes, "; ")
	}
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Message: msg})
}

// POST /admin/api/providers/workbuddy/refresh  body: {variant}
func handleProviderRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	var req struct {
		Variant string `json:"variant"`
	}
	readJSONBody(r, &req)
	targets := wbActiveAccounts(req.Variant)
	if len(targets) == 0 {
		writeAPI(w, http.StatusBadGateway, apiResponse{Error: "没有可刷新的 WorkBuddy 账号"})
		return
	}
	ok, fail := 0, 0
	var errs []string
	for _, a := range targets {
		if err := wbRefreshAccount(a); err != nil {
			fail++
			errs = append(errs, maskSecret(a.Email)+": "+err.Error())
			continue
		}
		ok++
	}
	resp := apiResponse{Success: fail == 0, Message: fmt.Sprintf("刷新成功 %d 个, 失败 %d 个", ok, fail)}
	if len(errs) > 0 {
		resp.Error = strings.Join(errs, "; ")
	}
	writeAPI(w, http.StatusOK, resp)
}

// POST /admin/api/providers/qoder/accounts/add  body: {pat, region}
func handleQoderAccountAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	var req struct {
		PAT    string `json:"pat"`
		Region string `json:"region"`
	}
	if err := readJSONBody(r, &req); err != nil {
		writeAPI(w, http.StatusBadRequest, apiResponse{Error: "invalid JSON: " + err.Error()})
		return
	}
	acc, err := qoderAddAccount(req.PAT, req.Region)
	if err != nil {
		writeAPI(w, http.StatusBadGateway, apiResponse{
			Error:   "令牌验证失败: " + err.Error(),
			Message: "请确认这是 Qoder 中国区个人访问令牌 (pt-…)",
		})
		return
	}
	msg := fmt.Sprintf("已添加 Qoder 账号 %s", acc.Email)
	if n, err := qoderSyncModels(); err == nil {
		msg += fmt.Sprintf("，已同步 %d 个模型", n)
	}
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Message: msg})
}

// POST /admin/api/providers/qoder/accounts/delete  body: {id}
func handleQoderAccountDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	readJSONBody(r, &req)
	if !qoderDeleteAccount(req.ID) {
		writeAPI(w, http.StatusNotFound, apiResponse{Error: "账号不存在"})
		return
	}
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Message: "已删除账号"})
}

// POST /admin/api/providers/trae/import  从本机 Trae 桌面端/CLI 导入账号
//
// Trae 把登录态写进 User/globalStorage/storage.json, 加密是纯静态的(四个
// 硬编码盐值, 不掺机器指纹), 所以能像 WorkBuddy 那样直接读 —— 解密与路径
// 扫描见 trae_local.go。导入时会一并取出设备密钥对, 否则新版刷新接口签不
// 出 DeviceProof。
func handleTraeImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	accounts, notes, err := traeImportLocalAccounts()
	if err != nil {
		writeAPI(w, http.StatusBadGateway, apiResponse{
			Error:   "导入失败: " + err.Error(),
			Message: strings.Join(notes, "; "),
		})
		return
	}
	msg := fmt.Sprintf("已导入 %d 个 Trae 账号", len(accounts))
	if len(notes) > 0 {
		msg += "；" + strings.Join(notes, "; ")
	}
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Message: msg})
}

// POST /admin/api/providers/trae/accounts/add  body: 凭据 JSON
// 兜底入口: 本机导入走 /providers/trae/import, 这里给从别的机器搬账号用。
func handleTraeAccountAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	var cred traeCredentialInput
	if err := readJSONBody(r, &cred); err != nil {
		writeAPI(w, http.StatusBadRequest, apiResponse{Error: "invalid JSON: " + err.Error()})
		return
	}
	acc, err := traeAddAccount(cred)
	if err != nil {
		writeAPI(w, http.StatusBadGateway, apiResponse{
			Error:   "凭据无效: " + err.Error(),
			Message: "手动粘贴的凭据至少要有 accessToken 与 deviceId; 本机已登录请直接点「从本机导入账号」",
		})
		return
	}
	msg := fmt.Sprintf("已添加 Trae 账号 %s", acc.UserID)
	if n, err := traeSyncModelsSafe(); err == nil {
		msg += fmt.Sprintf("，已同步 %d 个模型", n)
	} else {
		msg += "（模型同步失败: " + err.Error() + "）"
	}
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Message: msg})
}

// POST /admin/api/providers/trae/accounts/delete  body: {id}
func handleTraeAccountDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	readJSONBody(r, &req)
	if !traeDeleteAccount(req.ID) {
		writeAPI(w, http.StatusNotFound, apiResponse{Error: "账号不存在"})
		return
	}
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Message: "已删除账号"})
}

// POST /admin/api/providers/{id}/models/refresh
func handleProviderModelsRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	provider := strings.TrimPrefix(r.URL.Path, "/admin/api/providers/")
	provider = strings.TrimSuffix(provider, "/models/refresh")

	switch provider {
	case "workbuddy", "workbuddy-ai":
		// 先确保凭据可用(必要时应先从本机导入)
		if len(wbActiveAccounts("")) == 0 {
			accounts, notes := wbReadLocalAccounts()
			if len(accounts) > 0 {
				wbSetAccountsIfEmpty(accounts)
			} else {
				writeAPI(w, http.StatusBadGateway, apiResponse{
					Error:   "未找到本机 WorkBuddy 登录凭据",
					Message: strings.Join(notes, "; "),
				})
				return
			}
		}
		n, err := wbSyncModels()
		if err != nil {
			writeAPI(w, http.StatusBadGateway, apiResponse{Success: n > 0, Error: err.Error(), Message: fmt.Sprintf("已同步 %d 个模型", n)})
			return
		}
		writeAPI(w, http.StatusOK, apiResponse{Success: true, Message: fmt.Sprintf("已同步 %d 个模型（正在后台探测可用性）", n)})
	case "qoder":
		n, err := qoderSyncModels()
		if err != nil {
			writeAPI(w, http.StatusBadGateway, apiResponse{Error: err.Error()})
			return
		}
		writeAPI(w, http.StatusOK, apiResponse{Success: true, Message: fmt.Sprintf("已同步 %d 个模型", n)})
	case "trae":
		n, err := traeSyncModelsSafe()
		if err != nil {
			writeAPI(w, http.StatusBadGateway, apiResponse{Error: err.Error()})
			return
		}
		writeAPI(w, http.StatusOK, apiResponse{Success: true, Message: fmt.Sprintf("已同步 %d 个模型", n)})
	default:
		writeAPI(w, http.StatusNotFound, apiResponse{Error: "unknown provider"})
	}
}

// GET /admin/api/providers/models  汇总三个 provider 的模型(后台展示用)
func handleProviderModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Data: map[string]any{
		"workbuddy": func() []map[string]any {
			var out []map[string]any
			for _, m := range wbModelList() {
				if m["owned_by"] == wbVariantCN.Provider {
					out = append(out, m)
				}
			}
			return out
		}(),
		"workbuddy-ai": func() []map[string]any {
			var out []map[string]any
			for _, m := range wbModelList() {
				if m["owned_by"] == wbVariantINTL.Provider {
					out = append(out, m)
				}
			}
			return out
		}(),
		"qoder": qoderModelList(),
		"trae":  traeModelList(),
	}})
}

// GET /admin/api/providers/credits  三个 provider 的积分余额 + 签到状态
func handleProviderCredits(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Data: map[string]any{
		"workbuddy":    wbCreditsPayload(wbVariantCN),
		"workbuddy-ai": wbCreditsPayload(wbVariantINTL),
		"qoder":        qoderCreditsPayload(),
		"trae":         traeCreditsPayload(),
		"autoCheckin":  autoCheckinResultsSnapshot(),
	}})
}

// POST /admin/api/providers/{id}/checkin  领取当日签到积分
func handleProviderCheckin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	provider := strings.TrimPrefix(r.URL.Path, "/admin/api/providers/")
	provider = strings.TrimSuffix(provider, "/checkin")

	var res checkinResult
	var err error
	switch provider {
	case "workbuddy":
		res, err = wbRunCheckinForAdmin(wbVariantCN)
	case "workbuddy-ai":
		res, err = wbRunCheckinForAdmin(wbVariantINTL)
	case "qoder":
		res, err = qoderRunCheckinForAdmin()
	case "trae":
		res, err = traeRunCheckinForAdmin()
	default:
		writeAPI(w, http.StatusNotFound, apiResponse{Error: "unknown provider"})
		return
	}
	if err != nil {
		writeAPI(w, http.StatusBadGateway, apiResponse{Error: "签到失败: " + err.Error()})
		return
	}
	// 已签到 / 活动未开不是错误, 但 success 置 false 让前端用中性样式提示。
	writeAPI(w, http.StatusOK, apiResponse{
		Success: res.Status == "success",
		Message: res.Message,
		Data:    res,
	})
}

// POST /admin/api/providers/{id}/probe  重新探测 WB 模型可用性
func handleProviderProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "method not allowed"})
		return
	}
	provider := strings.TrimPrefix(r.URL.Path, "/admin/api/providers/")
	provider = strings.TrimSuffix(provider, "/probe")
	v := wbVariantByID(provider)
	if v == nil {
		writeAPI(w, http.StatusNotFound, apiResponse{Error: "该 provider 不支持可用性探测"})
		return
	}
	probed, unavailable, err := wbProbeAll(v, true)
	if err != nil {
		writeAPI(w, http.StatusBadGateway, apiResponse{Error: err.Error()})
		return
	}
	writeAPI(w, http.StatusOK, apiResponse{
		Success: true,
		Message: fmt.Sprintf("已探测 %d 个模型：%d 个可用，%d 个上游未接入", probed, probed-unavailable, unavailable),
	})
}

// registerProviderRoutes 挂载第三方 provider 后台路由(由 registerAdminRoutes 调用)。
func registerProviderRoutes(adminMux *http.ServeMux) {
	adminMux.HandleFunc("/admin/api/providers/status", corsHandler(handleProvidersStatus))
	adminMux.HandleFunc("/admin/api/providers/models", corsHandler(handleProviderModels))
	adminMux.HandleFunc("/admin/api/providers/credits", corsHandler(handleProviderCredits))
	adminMux.HandleFunc("/admin/api/providers/workbuddy/config", corsHandler(handleProviderConfigUpdate))
	adminMux.HandleFunc("/admin/api/providers/workbuddy-ai/config", corsHandler(handleProviderConfigUpdate))
	adminMux.HandleFunc("/admin/api/providers/qoder/config", corsHandler(handleProviderConfigUpdate))
	adminMux.HandleFunc("/admin/api/providers/trae/config", corsHandler(handleProviderConfigUpdate))
	adminMux.HandleFunc("/admin/api/providers/trae/import", corsHandler(handleTraeImport))
	adminMux.HandleFunc("/admin/api/providers/trae/accounts/add", corsHandler(handleTraeAccountAdd))
	adminMux.HandleFunc("/admin/api/providers/trae/accounts/delete", corsHandler(handleTraeAccountDelete))
	adminMux.HandleFunc("/admin/api/providers/trae/models/refresh", corsHandler(handleProviderModelsRefresh))
	adminMux.HandleFunc("/admin/api/providers/trae/checkin", corsHandler(handleProviderCheckin))
	adminMux.HandleFunc("/admin/api/providers/workbuddy/import", corsHandler(handleProviderImport))
	adminMux.HandleFunc("/admin/api/providers/workbuddy/refresh", corsHandler(handleProviderRefresh))
	adminMux.HandleFunc("/admin/api/providers/workbuddy/models/refresh", corsHandler(handleProviderModelsRefresh))
	adminMux.HandleFunc("/admin/api/providers/workbuddy-ai/models/refresh", corsHandler(handleProviderModelsRefresh))
	adminMux.HandleFunc("/admin/api/providers/qoder/models/refresh", corsHandler(handleProviderModelsRefresh))
	adminMux.HandleFunc("/admin/api/providers/qoder/accounts/add", corsHandler(handleQoderAccountAdd))
	adminMux.HandleFunc("/admin/api/providers/qoder/accounts/delete", corsHandler(handleQoderAccountDelete))
	adminMux.HandleFunc("/admin/api/providers/workbuddy/checkin", corsHandler(handleProviderCheckin))
	adminMux.HandleFunc("/admin/api/providers/workbuddy-ai/checkin", corsHandler(handleProviderCheckin))
	adminMux.HandleFunc("/admin/api/providers/qoder/checkin", corsHandler(handleProviderCheckin))
	adminMux.HandleFunc("/admin/api/providers/workbuddy/probe", corsHandler(handleProviderProbe))
	adminMux.HandleFunc("/admin/api/providers/workbuddy-ai/probe", corsHandler(handleProviderProbe))
}

// startProviderBoot 启动时的第三方 provider 初始化: 导入本机 WorkBuddy 凭据并同步免费模型目录。
func startProviderBoot() {
	if len(wbActiveAccounts("")) == 0 {
		accounts, notes := wbReadLocalAccounts()
		for _, n := range notes {
			log.Printf("  provider: %s", n)
		}
		if len(accounts) > 0 {
			wbSetAccountsIfEmpty(accounts)
			for _, a := range accounts {
				log.Printf("  provider: imported workbuddy account %s (%s)", a.Variant, maskSecret(a.Email))
			}
		}
	}
	go func() {
		time.Sleep(3 * time.Second)
		if n, err := wbSyncModels(); err != nil {
			log.Printf("  provider: workbuddy models sync failed: %v", err)
		} else {
			log.Printf("  provider: workbuddy %d models ready (availability probing in background)", n)
		}
		if len(qoderLoadAccounts()) > 0 {
			if n, err := qoderSyncModels(); err != nil {
				log.Printf("  provider: qoder models sync failed: %v", err)
			} else {
				log.Printf("  provider: qoder %d models ready", n)
			}
		}
		// 各来源目录同步基本就绪, 输出一次多来源模型概览
		time.Sleep(2 * time.Second)
		logModelSourceSummary()
	}()
	traeBoot()
	startAutoCheckin()
}
