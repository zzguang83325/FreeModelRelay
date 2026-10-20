package app

import (
	"log"
	"sync"
	"time"
)

// ============================================================================
// 启动期自动积分同步 + 自动签到
//
// 背景: 四个 provider 的积分都靠"每日签到"补给(WorkBuddy 每天 +100 credits,
// Qoder/Trae 每天一个 campaign), 用户不打开后台就不会知道今天还没领, 额度
// 被用光才发现。所以程序一启动就把每个来源的签到状态和余额拉一遍, 凡是
// "有活动且今日未签到"的立即自动领取, 并把结果记下来供后台展示。
//
// 判定口径与后台「🎁 一键签到」完全一致(active && !todayCheckedIn), 避免
// 出现"自动签过了但页面还显示未签到"的两种口径打架。
// ============================================================================

// autoCheckinOutcome 单个来源的启动期自动签到结果。
type autoCheckinOutcome struct {
	Provider string     `json:"provider"`
	Label    string     `json:"label"`
	Status   string     `json:"status"` // success / already / skipped / error
	Message  string     `json:"message"`
	Reward   float64    `json:"reward,omitempty"`
	Credits  *wbCredits `json:"credits,omitempty"`
	At       int64      `json:"at"`
}

var (
	autoCheckinMu      sync.Mutex
	autoCheckinResults = map[string]*autoCheckinOutcome{}
	autoCheckinRunAt   int64
)

// autoCheckinResultsSnapshot 返回最近一次启动期自动签到的结果副本。
func autoCheckinResultsSnapshot() map[string]any {
	autoCheckinMu.Lock()
	defer autoCheckinMu.Unlock()
	out := map[string]any{"lastRunAt": autoCheckinRunAt, "providers": map[string]any{}}
	for k, v := range autoCheckinResults {
		out["providers"].(map[string]any)[k] = v
	}
	return out
}

// setAutoCheckinOutcome 记录单个来源的结果。
func setAutoCheckinOutcome(o *autoCheckinOutcome) {
	o.At = time.Now().UnixMilli()
	autoCheckinMu.Lock()
	autoCheckinResults[o.Provider] = o
	autoCheckinRunAt = o.At
	autoCheckinMu.Unlock()
	switch o.Status {
	case "success":
		log.Printf("  provider: auto checkin %s → %s (+%.0f 积分)", o.Label, o.Message, o.Reward)
	case "error":
		log.Printf("  provider: auto checkin %s failed: %s", o.Label, o.Message)
	default:
		log.Printf("  provider: auto checkin %s [%s]: %s", o.Label, o.Status, o.Message)
	}
}

// ============ 各 provider 的签到判定与执行 ============

// autoCheckinWB 处理一个 WorkBuddy 变体(CN/INTL)。
func autoCheckinWB(v *wbVariant) *autoCheckinOutcome {
	out := &autoCheckinOutcome{Provider: v.ID, Label: v.DisplayName}
	acc := wbPickAccount(v.ID)
	if acc == nil {
		out.Status, out.Message = "skipped", "无可用账号"
		return out
	}
	st, err := wbFetchCheckinState(v, acc)
	if err != nil {
		out.Status, out.Message = "error", "签到状态查询失败: "+err.Error()
		return out
	}
	switch {
	case !st.Active:
		out.Status, out.Message = "skipped", "该区暂无签到活动"
	case st.TodayCheckedIn:
		out.Status, out.Message = "already", "今日已签到"
	default:
		res, err := wbRunCheckin(v, acc)
		if err != nil {
			out.Status, out.Message = "error", err.Error()
		} else {
			out.Status, out.Message, out.Reward = res.Status, res.Message, res.Reward
		}
	}
	if c, err := wbFetchCredits(v, acc); err == nil {
		out.Credits = c
	}
	return out
}

// autoCheckinQoder 处理 Qoder。
func autoCheckinQoder() *autoCheckinOutcome {
	out := &autoCheckinOutcome{Provider: "qoder", Label: "Qoder"}
	acc := qoderPickAccount()
	if acc == nil {
		out.Status, out.Message = "skipped", "无可用账号"
		return out
	}
	claimable, all, err := qoderCheckinCampaigns(acc)
	if err != nil {
		out.Status, out.Message = "error", "签到状态查询失败: "+err.Error()
		return out
	}
	checked := false
	for _, c := range all {
		if jsonField(c, "claimStatus", "claim_status") == "CLAIMED" {
			checked = true
			break
		}
	}
	switch {
	case len(claimable) == 0 && checked:
		out.Status, out.Message = "already", "今日已签到"
	case len(claimable) == 0:
		out.Status, out.Message = "skipped", "暂无可领取的签到奖励"
	default:
		res, err := qoderRunCheckin(acc)
		if err != nil {
			out.Status, out.Message = "error", err.Error()
		} else {
			out.Status, out.Message, out.Reward = res.Status, res.Message, res.Reward
		}
	}
	if q, err := qoderFetchQuota(acc); err == nil {
		bucket := q.UserQuota
		if bucket.Total == 0 && q.AddOnQuota.Total > 0 {
			bucket = q.AddOnQuota
		}
		out.Credits = &wbCredits{
			Remain: bucket.Remaining, Total: bucket.Total, Used: bucket.Used,
			Unit: bucket.Unit, PlanName: q.UserType,
		}
	}
	return out
}

// autoCheckinTrae 处理 Trae(仅 CN 有签到接口)。
func autoCheckinTrae() *autoCheckinOutcome {
	out := &autoCheckinOutcome{Provider: "trae", Label: "Trae"}
	acc := traePickAccount()
	if acc == nil {
		out.Status, out.Message = "skipped", "无可用账号"
		return out
	}
	st, err := traeCheckinStatus(acc)
	if err != nil {
		out.Status, out.Message = "skipped", err.Error()
		return out
	}
	enabled, _ := st["enabled"].(bool)
	checked, _ := st["checkedIn"].(bool)
	switch {
	case !enabled:
		out.Status, out.Message = "skipped", "暂无签到活动"
	case checked:
		out.Status, out.Message = "already", "今日已签到"
	default:
		res, err := traeRunCheckin(acc)
		if err != nil {
			out.Status, out.Message = "error", err.Error()
		} else {
			out.Status, out.Message, out.Reward = res.Status, res.Message, res.Reward
		}
	}
	if usage, err := traeFetchUsage(acc); err == nil {
		out.Credits = traeCreditsOf(usage)
	}
	return out
}

// autoCheckinAll 依次处理四个来源: 拉签到状态 → 需要就自动签 → 顺手拉余额。
func autoCheckinAll() {
	for _, o := range []*autoCheckinOutcome{
		autoCheckinWB(wbVariantCN),
		autoCheckinWB(wbVariantINTL),
		autoCheckinQoder(),
		autoCheckinTrae(),
	} {
		setAutoCheckinOutcome(o)
	}
}

// startAutoCheckin 启动后延时跑一次自动签到。延迟是为了等本机凭据导入
// (WorkBuddy/Trae 都是从桌面端读文件) 与模型目录同步先落地, 否则启动初期
// 会因为"还没账号"而全部跳过。
func startAutoCheckin() {
	go func() {
		time.Sleep(8 * time.Second)
		log.Printf("  provider: auto checkin starting ...")
		autoCheckinAll()
	}()
}
