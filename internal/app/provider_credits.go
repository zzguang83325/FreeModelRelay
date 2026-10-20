package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"FreeModelRelay/internal/kit"
)

// ============================================================================
// 积分余额 + 每日签到(WorkBuddy CN/INTL + Qoder)
//
// 三家上游端点均已实测:
//   WorkBuddy CN   https://copilot.tencent.com/billing/meter/{get-user-resource-summary,
//                                                           checkin-activity-status, daily-checkin}
//   WorkBuddy INTL https://www.workbuddy.ai/billing/meter/{同上}
//   Qoder          https://openapi.qoder.com.cn/api/v2/quota/usage
//                  https://openapi.qoder.com.cn/sash/api/v1/me/campaigns[/<campaignId>/claim]
//
// 无独有偶: 积分是"花钱"的凭据 —— WorkBuddy 免费模型 credits 倍率为 0 不扣费,
// 其余模型按倍率扣积分; 签到每天补 100 积分。所以后台必须同时给出余额与签到入口,
// 否则用户只能看见"模型可用"却不知道还能用多久。
// ============================================================================

// WB billing/meter 路径不能带 /v2 前缀 —— 实测 /v2/billing/meter/... 返回
// {"error_msg":"404 Route Not Found"}。global 与 cn 同构, 都走 ChatBase。
const (
	wbResourceSummaryPath = "/billing/meter/get-user-resource-summary"
	wbCheckinStatusPath   = "/billing/meter/checkin-activity-status"
	wbDailyCheckinPath    = "/billing/meter/daily-checkin"
)

// 上游对"重复签到"返回 code!=0 + 中文提示, 必须按文案判定幂等成功,
// 否则用户点两次就被报成失败。
var wbAlreadyCheckinMarkers = []string{"已签到", "already", "已领取"}

// wbCredits 积分余额(所有套餐聚合)。
type wbCredits struct {
	Remain       float64 `json:"remain"`
	Total        float64 `json:"total"`
	Used         float64 `json:"used"`
	Unit         string  `json:"unit"`
	PlanName     string  `json:"planName"`
	IsPaid       bool    `json:"isPaid"`
	PackageCount int     `json:"packageCount"`
}

// wbCheckinState 签到活动状态。
type wbCheckinState struct {
	Active          bool     `json:"active"`
	TodayCheckedIn  bool     `json:"todayCheckedIn"`
	StreakDays      int      `json:"streakDays"`
	DailyCredit     float64  `json:"dailyCredit"`
	TotalCredits    float64  `json:"totalCredits"`
	WeekCheckinDays int      `json:"weekCheckinDays"`
	WeekProgress    []bool   `json:"weekProgress"`
	ThemeName       string   `json:"themeName"`
	ActivityName    string   `json:"activityName"`
	ClaimButtonText string   `json:"claimButtonText"`
	CheckinDates    []string `json:"checkinDates"`
}

// checkinResult 签到动作的统一返回(三家 provider 共用)。
type checkinResult struct {
	Status  string  `json:"status"` // success / already / skipped
	Message string  `json:"message"`
	Reward  float64 `json:"reward,omitempty"`
}

// wbParseAmount 兼容上游把额度写成字符串("1406.78")或数字(1406.78)两种形态。
func wbParseAmount(raw json.RawMessage) float64 {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if s == "" || s == "null" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}

func wbIsAlreadyCheckin(msg string) bool {
	lower := strings.ToLower(msg)
	for _, m := range wbAlreadyCheckinMarkers {
		if strings.Contains(msg, m) || strings.Contains(lower, strings.ToLower(m)) {
			return true
		}
	}
	return false
}

// wbPostJSON 向 WB 上游发一个 JSON POST。非 2xx 不算传输错误(daily-checkin 用
// HTTP 400 + code!=0 表达"今天已签到"), 所以把状态码一并交回调用方判断。
// 若上游回 401 且账号持有 refreshToken, 先刷新再重试一次(dsh-multi-router 同口径),
// 避免本机桌面端令牌过期后签到/额度静默失败。
func wbPostJSON(v *wbVariant, a *wbAccount, path string, payload any, timeout time.Duration) ([]byte, int, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	do := func() ([]byte, int, error) {
		req, err := http.NewRequest(http.MethodPost, v.ChatBase+path, bytes.NewReader(raw))
		if err != nil {
			return nil, 0, err
		}
		wbCommonHeaders(req, a, v)
		client := &http.Client{Timeout: timeout}
		resp, err := client.Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return body, resp.StatusCode, nil
	}
	body, status, err := do()
	if err != nil {
		return nil, 0, err
	}
	if status == 401 && a != nil && a.RefreshToken != "" {
		if rerr := wbRefreshAccessToken(v, a); rerr == nil {
			if b2, s2, e2 := do(); e2 == nil {
				return b2, s2, nil
			}
		}
	}
	return body, status, nil
}

// wbRefreshAccessToken 用 refreshToken 换新 accessToken(CN/INTL 共用 ChatBase 同形路径)。
// 仅作本地会话兜底: 本机桌面端 normally 自己维护令牌新鲜度, 这里只在 401 时补一刀。
func wbRefreshAccessToken(v *wbVariant, a *wbAccount) error {
	if a.RefreshToken == "" {
		return fmt.Errorf("无 refreshToken, 无法刷新")
	}
	url := strings.TrimRight(v.ChatBase, "/") + "/v2/plugin/auth/token/refresh"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("X-Refresh-Token", a.RefreshToken)
	req.Header.Set("X-Auth-Refresh-Source", "plugin")
	req.Header.Set("Origin", v.Origin)
	req.Header.Set("User-Agent", wbUserAgent(v))
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var parsed struct {
		Code int `json:"code"`
		Data struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("刷新响应解析失败: %w", err)
	}
	if parsed.Code != 0 || parsed.Data.AccessToken == "" {
		return fmt.Errorf("刷新失败 code=%d", parsed.Code)
	}
	// 校验新令牌身份与本地一致, 防止串号。
	if claims := wbJWTClaims(parsed.Data.AccessToken); claims != nil {
		if sub, _ := claims["sub"].(string); sub != "" && a.UID != "" && sub != a.UID {
			return fmt.Errorf("刷新返回的账号身份不一致")
		}
	}
	a.AccessToken = parsed.Data.AccessToken
	if parsed.Data.RefreshToken != "" {
		a.RefreshToken = parsed.Data.RefreshToken
	}
	if claims := wbJWTClaims(a.AccessToken); claims != nil {
		if exp, ok := claims["exp"].(float64); ok && exp > 0 {
			a.ExpiresAt = int64(exp * 1000)
		}
	}
	return nil
}

// wbFetchCredits 查询积分余额。上游 code!=0 或 HTTP 非 200 都返回错误, 由调用方
// 决定是展示错误还是继续(签到状态是独立接口, 不因额度查询失败而整体失败)。
func wbFetchCredits(v *wbVariant, a *wbAccount) (*wbCredits, error) {
	body, status, err := wbPostJSON(v, a, wbResourceSummaryPath, map[string]any{}, 20*time.Second)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("额度接口 HTTP %d %s", status, kit.Truncate(strings.TrimSpace(string(body)), 200))
	}
	var parsed struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Packages []struct {
				CycleTotalCapacity  json.RawMessage `json:"CycleTotalCapacity"`
				CycleRemainCapacity json.RawMessage `json:"CycleRemainCapacity"`
				CycleUsedCapacity   json.RawMessage `json:"CycleUsedCapacity"`
				CapacityUnit        string          `json:"CapacityUnit"`
			} `json:"Packages"`
			SubscriptionPackageName string `json:"SubscriptionPackageName"`
			IsPaidUser              bool   `json:"IsPaidUser"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("额度响应解析失败: %w", err)
	}
	if parsed.Code != 0 {
		return nil, fmt.Errorf("额度接口 code=%d %s", parsed.Code, parsed.Msg)
	}
	out := &wbCredits{
		Unit:         "credits",
		PlanName:     parsed.Data.SubscriptionPackageName,
		IsPaid:       parsed.Data.IsPaidUser,
		PackageCount: len(parsed.Data.Packages),
	}
	for _, p := range parsed.Data.Packages {
		out.Total += wbParseAmount(p.CycleTotalCapacity)
		out.Remain += wbParseAmount(p.CycleRemainCapacity)
		out.Used += wbParseAmount(p.CycleUsedCapacity)
		if p.CapacityUnit != "" {
			out.Unit = p.CapacityUnit
		}
	}
	// 上游偶发只给 remain 不给 total(或反之), 用同源字段兜底保证百分比不越界。
	if out.Total < out.Remain {
		out.Total = out.Remain
	}
	return out, nil
}

// wbFetchCheckinState 查询签到活动状态(是否开启、今天签没签、连续天数)。
func wbFetchCheckinState(v *wbVariant, a *wbAccount) (*wbCheckinState, error) {
	body, status, err := wbPostJSON(v, a, wbCheckinStatusPath, map[string]any{}, 20*time.Second)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("签到状态接口 HTTP %d %s", status, kit.Truncate(strings.TrimSpace(string(body)), 200))
	}
	var parsed struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Active          bool     `json:"active"`
			TodayCheckedIn  bool     `json:"today_checked_in"`
			StreakDays      int      `json:"streak_days"`
			DailyCredit     float64  `json:"daily_credit"`
			TotalCredits    float64  `json:"total_credits"`
			WeekCheckinDays int      `json:"week_checkin_days"`
			WeekProgress    []bool   `json:"week_progress"`
			ThemeName       string   `json:"theme_name"`
			ActivityName    string   `json:"activity_name"`
			ClaimButtonText string   `json:"claim_button_text"`
			CheckinDates    []string `json:"checkin_dates"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("签到状态响应解析失败: %w", err)
	}
	if parsed.Code != 0 {
		return nil, fmt.Errorf("签到状态接口 code=%d %s", parsed.Code, parsed.Msg)
	}
	d := parsed.Data
	return &wbCheckinState{
		Active: d.Active, TodayCheckedIn: d.TodayCheckedIn, StreakDays: d.StreakDays,
		DailyCredit: d.DailyCredit, TotalCredits: d.TotalCredits,
		WeekCheckinDays: d.WeekCheckinDays, WeekProgress: d.WeekProgress,
		ThemeName: d.ThemeName, ActivityName: d.ActivityName,
		ClaimButtonText: d.ClaimButtonText, CheckinDates: d.CheckinDates,
	}, nil
}

// wbRunCheckin 领取当日签到积分。已签到按幂等成功处理(上游用 code!=0 表达)。
func wbRunCheckin(v *wbVariant, a *wbAccount) (checkinResult, error) {
	body, status, err := wbPostJSON(v, a, wbDailyCheckinPath, map[string]any{}, 25*time.Second)
	if err != nil {
		return checkinResult{}, err
	}
	var parsed struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Credit float64 `json:"credit"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return checkinResult{}, fmt.Errorf("签到响应解析失败: %w (HTTP %d %s)",
			err, status, kit.Truncate(strings.TrimSpace(string(body)), 200))
	}
	if parsed.Code == 0 {
		msg := "签到成功"
		if parsed.Data.Credit > 0 {
			msg = fmt.Sprintf("签到成功 +%.0f 积分", parsed.Data.Credit)
		} else if parsed.Msg != "" && parsed.Msg != "OK" {
			msg = "签到成功：" + parsed.Msg
		}
		return checkinResult{Status: "success", Message: msg, Reward: parsed.Data.Credit}, nil
	}
	if wbIsAlreadyCheckin(parsed.Msg) {
		return checkinResult{Status: "already", Message: "今日已签到"}, nil
	}
	// 活动未开 / 过期: 不是错误, 是不适用(INTL 常态 active=false)。
	if strings.Contains(parsed.Msg, "未开启") || strings.Contains(parsed.Msg, "已过期") {
		return checkinResult{Status: "skipped", Message: "签到活动未开启或已过期"}, nil
	}
	return checkinResult{}, fmt.Errorf("签到失败(code=%d): %s", parsed.Code, parsed.Msg)
}

// wbCreditsPayload 组装 /admin 用的 WB 积分载荷。额度与签到状态各自独立报错 ——
// INTL 没有签到活动但额度正常, 不能因为一个失败就整块空白。
func wbCreditsPayload(v *wbVariant) map[string]any {
	out := map[string]any{"id": v.ID, "displayName": v.DisplayName, "region": v.Region}
	acc := wbPickAccount(v.ID)
	if acc == nil {
		out["error"] = "无可用账号, 请先在 /admin 导入本机已登录账号"
		return out
	}
	out["account"] = maskSecret(acc.Email)
	if c, err := wbFetchCredits(v, acc); err != nil {
		out["creditsError"] = err.Error()
	} else {
		out["credits"] = c
	}
	if st, err := wbFetchCheckinState(v, acc); err != nil {
		out["checkinError"] = err.Error()
	} else {
		out["checkin"] = st
	}
	return out
}

// ============================================================================
// Qoder
// ============================================================================

// qoderQuotaBucket 单个额度桶(userQuota / addOnQuota)。
type qoderQuotaBucket struct {
	Total      float64 `json:"total"`
	Used       float64 `json:"used"`
	Remaining  float64 `json:"remaining"`
	Percentage float64 `json:"percentage"`
	Unit       string  `json:"unit"`
	DetailURL  string  `json:"detailUrl,omitempty"`
}

type qoderQuota struct {
	UserType             string           `json:"userType"`
	UsageType            string           `json:"usageType"`
	TotalUsagePercentage float64          `json:"totalUsagePercentage"`
	IsQuotaExceeded      bool             `json:"isQuotaExceeded"`
	ExpiresAt            int64            `json:"expiresAt"`
	UpgradeURL           string           `json:"upgradeUrl"`
	UserQuota            qoderQuotaBucket `json:"userQuota"`
	AddOnQuota           qoderQuotaBucket `json:"addOnQuota"`
}

// qoderOpenAPIReq 走 openapi.qoder.com.cn 的原生请求(PAT 换成 jobToken 后使用)。
// 这些端点不吃 Cosy 签名, 但要求 Cosy-MachineId/Token 双写同一 machineId。
// clientType 区分调用场景: 普通业务(chat/quota/userinfo)用 qoderClientType("5",
// IDE 标识); 签到(campaigns)必须用 qoderCheckinClientType("10", desktop 标识),
// 否则上游按标识过滤返回空活动列表(dsh-multi-router 已实测确认)。
func qoderOpenAPIReq(a *qoderAccount, clientType, method, path string, payload []byte) (*http.Response, error) {
	if err := qoderEnsureToken(a); err != nil {
		return nil, err
	}
	var rdr io.Reader
	if payload != nil {
		rdr = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, qoderOpenAPIURL+path, rdr)
	if err != nil {
		return nil, err
	}
	h := req.Header
	h.Set("accept", "application/json")
	h.Set("accept-encoding", "identity")
	h.Set("authorization", "Bearer "+a.JobToken)
	h.Set("user-agent", "qoder/"+qoderIdeVersion)
	h.Set("cosy-version", "1.0.1")
	h.Set("cosy-clienttype", clientType)
	if a.MachineID != "" {
		h.Set("Cosy-MachineId", a.MachineID)
		h.Set("Cosy-MachineToken", a.MachineID)
	}
	if payload != nil {
		h.Set("content-type", "application/json")
	}
	client := &http.Client{Timeout: 25 * time.Second}
	return client.Do(req)
}

// qoderFetchQuota 查询 Qoder 积分额度。
func qoderFetchQuota(a *qoderAccount) (*qoderQuota, error) {
	resp, err := qoderOpenAPIReq(a, qoderClientType, http.MethodGet, "/api/v2/quota/usage", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("quota 接口 HTTP %d %s", resp.StatusCode, kit.Truncate(strings.TrimSpace(string(body)), 200))
	}
	var out qoderQuota
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("quota 响应解析失败: %w", err)
	}
	return &out, nil
}

// qoderCampaignList 取出 campaigns 数组。上游把数组放在顶层 campaigns、
// data.campaigns 或 data 三处之一(与官方客户端 checkin 实现同口径)。
func qoderCampaignList(body []byte) []map[string]any {
	var root map[string]any
	if json.Unmarshal(body, &root) != nil {
		return nil
	}
	pick := func(v any) []map[string]any {
		arr, ok := v.([]any)
		if !ok {
			return nil
		}
		out := make([]map[string]any, 0, len(arr))
		for _, item := range arr {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	if list := pick(root["campaigns"]); list != nil {
		return list
	}
	if data, ok := root["data"].(map[string]any); ok {
		if list := pick(data["campaigns"]); list != nil {
			return list
		}
	}
	return pick(root["data"])
}

// jsonField 兼容 camelCase 与 snake_case 两种上游字段名。
func jsonField(m map[string]any, names ...string) string {
	for _, n := range names {
		if s, ok := m[n].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// jsonEpochSec 兼容秒/毫秒与字符串三种形态, 取出 Unix 秒(缺失/非法返回 0)。
// 上游 campaign 的 startAt/endAt 为秒级时间戳(dsh-multi-router 实测 *1e3 转毫秒比较)。
func jsonEpochSec(m map[string]any, names ...string) int64 {
	for _, n := range names {
		v, ok := m[n]
		if !ok {
			continue
		}
		switch t := v.(type) {
		case float64:
			if t > 0 {
				return int64(t)
			}
		case int64:
			if t > 0 {
				return t
			}
		case json.Number:
			if i, err := t.Int64(); err == nil && i > 0 {
				return i
			}
		case string:
			if s := strings.TrimSpace(t); s != "" {
				if f, err := strconv.ParseFloat(s, 64); err == nil && f > 0 {
					return int64(f)
				}
			}
		}
	}
	return 0
}

// qoderCampaignActive 判断活动是否在当前时间窗内(缺失边界视为不限制)。
func qoderCampaignActive(c map[string]any) bool {
	nowMs := time.Now().UnixMilli()
	start := jsonEpochSec(c, "startAt", "start_at")
	end := jsonEpochSec(c, "endAt", "end_at")
	if start > 0 && start*1000 > nowMs {
		return false // 还没开始
	}
	if end > 0 && end*1000 <= nowMs {
		return false // 已结束
	}
	return true
}

// qoderCheckinCampaigns 返回 (可领取列表, 全部参活列表)。
func qoderCheckinCampaigns(a *qoderAccount) (claimable []map[string]any, all []map[string]any, err error) {
	resp, err := qoderOpenAPIReq(a, qoderCheckinClientType, http.MethodGet, "/sash/api/v1/me/campaigns", nil)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 404 {
		return nil, nil, fmt.Errorf("签到活动未开放")
	}
	if resp.StatusCode != 200 {
		return nil, nil, fmt.Errorf("campaigns 接口 HTTP %d %s", resp.StatusCode, kit.Truncate(strings.TrimSpace(string(body)), 200))
	}
	for _, c := range qoderCampaignList(body) {
		if jsonField(c, "actionType", "action_type") != "CLAIM_BENEFIT" {
			continue
		}
		if jsonField(c, "campaignId", "campaign_id") == "" {
			continue
		}
		all = append(all, c)
		// 只把"当前在时间窗内且可领取"的算作 claimable, 避免去领未开放/已结束的活动。
		if jsonField(c, "claimStatus", "claim_status") == "CLAIMABLE" && qoderCampaignActive(c) {
			claimable = append(claimable, c)
		}
	}
	return claimable, all, nil
}

// qoderRunCheckin 领取 Qoder 签到积分: 逐个 POST campaigns/<id>/claim。
// 同一 session 内只会真正领取一次(其余条目返回已领取), 与官方客户端一致。
func qoderRunCheckin(a *qoderAccount) (checkinResult, error) {
	claimable, all, err := qoderCheckinCampaigns(a)
	if err != nil {
		return checkinResult{}, err
	}
	if len(all) == 0 {
		return checkinResult{Status: "skipped", Message: "签到活动未开放"}, nil
	}
	if len(claimable) == 0 {
		for _, c := range all {
			if jsonField(c, "claimStatus", "claim_status") == "CLAIMED" {
				return checkinResult{Status: "already", Message: "今日已签到"}, nil
			}
		}
		return checkinResult{Status: "skipped", Message: "暂无可领取的签到奖励"}, nil
	}
	confirmed, reward := 0, 0.0
	for _, c := range claimable {
		id := jsonField(c, "campaignId", "campaign_id")
		if b, ok := c["benefit"].(map[string]any); ok {
			if kind, _ := b["kind"].(string); kind == "CREDITS" {
				if amount, ok := b["amount"].(float64); ok && amount >= 0 {
					reward += amount
				}
			}
		}
		resp, err := qoderOpenAPIReq(a, qoderCheckinClientType, http.MethodPost,
			"/sash/api/v1/me/campaigns/"+urlPathEscape(id)+"/claim", []byte("{}"))
		if err != nil {
			return checkinResult{}, err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return checkinResult{}, fmt.Errorf("领取失败 HTTP %d %s", resp.StatusCode, kit.Truncate(strings.TrimSpace(string(body)), 200))
		}
		var payload map[string]any
		json.Unmarshal(body, &payload)
		inner := payload
		if d, ok := payload["data"].(map[string]any); ok {
			inner = d
		}
		if jsonField(inner, "status", "claimStatus", "claim_status") == "CLAIMED" {
			confirmed++
		}
	}
	if confirmed == 0 {
		return checkinResult{}, fmt.Errorf("上游未确认领取结果")
	}
	msg := "签到成功"
	if reward > 0 {
		msg = fmt.Sprintf("签到成功 +%.0f 积分", reward)
	}
	return checkinResult{Status: "success", Message: msg, Reward: reward}, nil
}

// urlPathEscape 仅转义 campaignId 里可能出现的路径分隔/保留字符。
func urlPathEscape(s string) string {
	r := strings.NewReplacer("/", "%2F", "?", "%3F", "#", "%23", " ", "%20")
	return r.Replace(s)
}

// qoderCreditsPayload 组装 /admin 用的 Qoder 积分载荷。
func qoderCreditsPayload() map[string]any {
	out := map[string]any{"id": "qoder", "displayName": "Qoder", "region": "china"}
	acc := qoderPickAccount()
	if acc == nil {
		out["error"] = "无可用 Qoder 账号, 请先添加个人访问令牌"
		return out
	}
	out["account"] = maskSecret(acc.Email)
	if q, err := qoderFetchQuota(acc); err != nil {
		out["creditsError"] = err.Error()
	} else {
		out["quota"] = q
		// 统一成与 WB 同形的余额块, 前端可复用同一张卡片。
		bucket := q.UserQuota
		if bucket.Total == 0 && q.AddOnQuota.Total > 0 {
			bucket = q.AddOnQuota
		}
		out["credits"] = &wbCredits{
			Remain:   bucket.Remaining,
			Total:    bucket.Total,
			Used:     bucket.Used,
			Unit:     bucket.Unit,
			PlanName: q.UserType,
			IsPaid:   !strings.Contains(strings.ToLower(q.UserType), "free"),
		}
		out["quotaExceeded"] = q.IsQuotaExceeded
	}
	if _, all, err := qoderCheckinCampaigns(acc); err != nil {
		out["checkinError"] = err.Error()
	} else {
		checked := false
		for _, c := range all {
			if jsonField(c, "claimStatus", "claim_status") == "CLAIMED" {
				checked = true
				break
			}
		}
		out["checkin"] = &wbCheckinState{
			Active:         len(all) > 0,
			TodayCheckedIn: checked,
		}
	}
	return out
}

// qoderRunCheckinForAdmin 给 /admin 用: 无账号时给明确错误而不是空结果。
func qoderRunCheckinForAdmin() (checkinResult, error) {
	acc := qoderPickAccount()
	if acc == nil {
		return checkinResult{}, fmt.Errorf("无可用 Qoder 账号")
	}
	return qoderRunCheckin(acc)
}

// wbRunCheckinForAdmin 同理。
func wbRunCheckinForAdmin(v *wbVariant) (checkinResult, error) {
	acc := wbPickAccount(v.ID)
	if acc == nil {
		return checkinResult{}, fmt.Errorf("%s 无可用账号", v.DisplayName)
	}
	return wbRunCheckin(v, acc)
}
