package app

import (
	"FreeModelRelay/internal/cline"
	"FreeModelRelay/internal/kit"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	pool       *AccountPool
	poolMu     sync.Mutex
	poolSaveMu sync.Mutex
	poolPath   string
)

func init() {
	poolPath = kit.ResolveDataPath(".cline-accounts.json")
}

// kit.ResolveDataPath 数据文件路径解析：优先可执行文件目录，其次当前工作目录。
// go run 运行时编译产物在临时目录，此时应回退到工作目录（项目根）查找数据文件。

func loadPool() *AccountPool {
	poolMu.Lock()
	defer poolMu.Unlock()

	if pool != nil {
		return pool
	}

	data, err := os.ReadFile(poolPath)
	if err != nil {
		pool = &AccountPool{Accounts: []*Account{}, Keys: []string{}}
		return pool
	}

	var p AccountPool
	if err := json.Unmarshal(data, &p); err != nil {
		pool = &AccountPool{Accounts: []*Account{}, Keys: []string{}}
		return pool
	}

	if p.Accounts == nil {
		p.Accounts = []*Account{}
	}
	if p.Keys == nil {
		p.Keys = []string{}
	}
	pool = &p
	if pool.DefaultModel != "" {
		setDefaultModelValue(pool.DefaultModel)
	}
	return pool
}

// setDefaultModel 持久化默认模型：更新内存全局并写入账号池文件。
// 校验用 isKnownModel —— cline 账号池 / zen 免费 / 第三方 provider 三类模型都能
// 设为默认。原先只查 modelsCache, 导致把 workbuddy/hy3 这类 provider 模型设为
// 默认时被静默忽略(管理页报成功、实际仍是旧默认)。
// 入参可能是 /v1/models 的展示 id(带计费段): 只剥计费装饰, 保留 <provider>/ 分组
// 前缀 —— 同名模型(如 loomy 与 trae 都有 DeepSeek-V4.1-Flash)必须靠前缀区分
// 归属, 剥掉前缀存裸名会让默认请求按 wb→qoder→trae→loomy 顺序打到错误上游。
func setDefaultModel(modelID string) {
	modelID = providerStripDecorate(strings.TrimSpace(modelID))
	if modelID == "" || !isKnownModel(modelID) {
		return
	}
	setDefaultModelValue(modelID)
	p := loadPool()
	poolMu.Lock()
	p.DefaultModel = modelID
	poolMu.Unlock()
	savePool()
}

func savePool() {
	poolMu.Lock()
	defer poolMu.Unlock()
	savePoolLocked()
}

// savePoolLocked 持久化账号池；调用方必须已经持有 poolMu。
func savePoolLocked() {
	poolSaveMu.Lock()
	defer poolSaveMu.Unlock()

	data, _ := json.MarshalIndent(pool, "", "  ")
	if err := os.WriteFile(poolPath, data, 0600); err != nil {
		log.Printf("Failed to save accounts: %v", err)
	}
}

func addAccount(acc *Account) {
	p := loadPool()
	poolMu.Lock()
	p.Accounts = append(p.Accounts, acc)
	poolMu.Unlock()
	savePool()
}

func removeAccount(accountID string) bool {
	p := loadPool()
	poolMu.Lock()

	for i, a := range p.Accounts {
		if a.AccountID == accountID {
			p.Accounts = append(p.Accounts[:i], p.Accounts[i+1:]...)
			savePoolLocked()
			poolMu.Unlock()
			return true
		}
	}
	poolMu.Unlock()
	return false
}

func getAccountByID(accountID string) *Account {
	p := loadPool()
	poolMu.Lock()
	defer poolMu.Unlock()

	for _, a := range p.Accounts {
		if a.AccountID == accountID {
			return a
		}
	}
	return nil
}

func refreshAccountToken(acc *Account) error {
	resp, err := cline.RefreshClineToken(acc.RefreshToken)
	if err != nil {
		poolMu.Lock()
		acc.Status = "expired"
		savePoolLocked()
		poolMu.Unlock()
		return fmt.Errorf("token refresh failed: %w", err)
	}

	poolMu.Lock()
	acc.AccessToken = "workos:" + resp.Data.AccessToken
	if resp.Data.RefreshToken != "" {
		acc.RefreshToken = resp.Data.RefreshToken
	}
	acc.ExpiresAt = cline.ParseExpiry(resp.Data.ExpiresAt) - 60000
	acc.Status = "active"
	savePoolLocked()
	poolMu.Unlock()
	return nil
}

func pickAccount() *Account {
	p := loadPool()
	poolMu.Lock()

	active := make([]*Account, 0)
	for _, a := range p.Accounts {
		// 自动解除已到期的冷却
		if a.Status == "cooldown" && !a.CooldownUntil.IsZero() && time.Now().After(a.CooldownUntil) {
			a.Status = "active"
			a.CooldownUntil = time.Time{}
			a.LastReason = ""
		}
		if a.Status == "active" {
			active = append(active, a)
		}
	}

	if len(active) == 0 {
		poolMu.Unlock()
		return nil
	}

	cfg := getProxyConfig()

	var acc *Account
	switch cfg.Strategy {
	case "fill":
		// Always pick the first available (fill)
		acc = active[0]
	case "random":
		// Random selection — crypto/rand 避免 time 种子可预测
		acc = active[kit.RandIntn(len(active))]
	default: // round_robin
		if p.CurrentIdx >= len(active) {
			p.CurrentIdx = 0
		}
		acc = active[p.CurrentIdx]
		p.CurrentIdx = (p.CurrentIdx + 1) % len(active)
	}

	savePoolLocked()
	poolMu.Unlock()
	return acc
}

func ensureAccountToken(acc *Account) (string, error) {
	// 读 AccessToken/ExpiresAt 必须持锁, 否则与 refreshAccountToken 的写并发构成数据竞争
	poolMu.Lock()
	tok := acc.AccessToken
	valid := tok != "" && time.Now().UnixMilli() < acc.ExpiresAt
	poolMu.Unlock()
	if valid {
		return tok, nil
	}

	if err := refreshAccountToken(acc); err != nil {
		return "", err
	}

	poolMu.Lock()
	defer poolMu.Unlock()
	return acc.AccessToken, nil
}

func ListAccounts() []*Account {
	p := loadPool()
	poolMu.Lock()

	// 自动解除已到期的冷却，确保返回的列表是最新状态
	usageDate := time.Now().Format("2006-01-02")
	for _, a := range p.Accounts {
		if a.Status == "cooldown" && !a.CooldownUntil.IsZero() && time.Now().After(a.CooldownUntil) {
			a.Status = "active"
			a.CooldownUntil = time.Time{}
			a.LastReason = ""
		}
		if a.UsageDate != usageDate {
			a.UsageDate = usageDate
			a.UsageCountToday = 0
		}
		if a.TokensDate != usageDate {
			a.TokensDate = usageDate
			a.TokensToday = 0
		}
	}
	result := make([]*Account, len(p.Accounts))
	for i, a := range p.Accounts {
		// Don't expose tokens
		result[i] = &Account{
			AccountID:       a.AccountID,
			Email:           a.Email,
			Status:          a.Status,
			LastUsed:        a.LastUsed,
			UsageCount:      a.UsageCount,
			UsageCountToday: a.UsageCountToday,
			UsageDate:       a.UsageDate,
			TokensTotal:     a.TokensTotal,
			TokensToday:     a.TokensToday,
			TokensDate:      a.TokensDate,
			CreatedAt:       a.CreatedAt,
			CooldownUntil:   a.CooldownUntil,
			LastReason:      a.LastReason,
		}
	}
	savePoolLocked()
	poolMu.Unlock()
	return result
}

// markAccountCooldown 将账号置为冷却状态，并记录预计恢复时间。
// duration 为冷却时长；duration<=0 时使用默认冷却。
func markAccountCooldown(acc *Account, reason string, duration time.Duration) {
	if acc == nil {
		return
	}
	if duration <= 0 {
		duration = 18 * time.Hour // 默认 18 小时（Cline 免费额度每日重置）
	}
	poolMu.Lock()
	acc.Status = "cooldown"
	acc.CooldownUntil = time.Now().Add(duration)
	acc.LastReason = reason
	savePoolLocked()
	poolMu.Unlock()
}

// bumpUsage 递增本地成功调用计数（含今日计数），自动处理跨日重置。
func bumpUsage(acc *Account) {
	if acc == nil {
		return
	}

	poolMu.Lock()
	now := time.Now()
	today := now.Format("2006-01-02")
	if acc.UsageDate != today {
		acc.UsageDate = today
		acc.UsageCountToday = 0
	}
	acc.UsageCountToday++
	acc.UsageCount++
	acc.LastUsed = now
	savePoolLocked()
	poolMu.Unlock()
}

// resetTodayUsage 仅重置本地今日调用计数，不影响累计调用次数。
func resetTodayUsage(acc *Account) {
	if acc == nil {
		return
	}

	poolMu.Lock()
	acc.UsageDate = time.Now().Format("2006-01-02")
	acc.UsageCountToday = 0
	acc.TokensDate = time.Now().Format("2006-01-02")
	acc.TokensToday = 0
	savePoolLocked()
	poolMu.Unlock()
}

// recordAccountTokens 记录账号本次请求消耗的 token（prompt+completion），
// 自动处理跨日重置，累计值不重置。tokens<=0 时忽略。
func recordAccountTokens(acc *Account, tokens int64) {
	if acc == nil || tokens <= 0 {
		return
	}

	poolMu.Lock()
	today := time.Now().Format("2006-01-02")
	if acc.TokensDate != today {
		acc.TokensDate = today
		acc.TokensToday = 0
	}
	acc.TokensToday += tokens
	acc.TokensTotal += tokens
	savePoolLocked()
	poolMu.Unlock()
}

// describePoolStatus 汇总当前账号池状态，用于错误诊断。
func describePoolStatus() string {
	p := loadPool()
	poolMu.Lock()
	defer poolMu.Unlock()

	total := len(p.Accounts)
	if total == 0 {
		return "pool is empty, add accounts from the admin panel (/admin/)"
	}

	active, cooldown, expired := 0, 0, 0
	var nextRecover *time.Time
	for _, a := range p.Accounts {
		switch a.Status {
		case "active":
			active++
		case "cooldown":
			cooldown++
			if !a.CooldownUntil.IsZero() {
				if nextRecover == nil || a.CooldownUntil.Before(*nextRecover) {
					t := a.CooldownUntil
					nextRecover = &t
				}
			}
		case "expired":
			expired++
		}
	}

	s := fmt.Sprintf("total=%d active=%d cooldown=%d expired=%d", total, active, cooldown, expired)
	if cooldown > 0 && nextRecover != nil {
		s += fmt.Sprintf(", earliest recover at %s", nextRecover.Format("2006-01-02 15:04:05"))
	}
	return s
}

// 说明：Cline 账号的 OAuth 设备授权流程只在管理后台提供
// （admin.go 的 handleOAuthStart / handleOAuthPoll），
// 因此这里不再保留 CLI 版本，避免两处流程各自演化。
