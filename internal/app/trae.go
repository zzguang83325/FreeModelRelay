package app

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"FreeModelRelay/internal/kit"
)

// ============================================================================
// Trae 上游 (CN = www.trae.cn / api.trae.cn; 本机已装 Trae CN)
//
// 凭据来源: **直接从本机读**。Trae 把登录态写进 Electron 的
// User/globalStorage/storage.json, 加密是纯静态的(硬编码盐值, 不掺机器指纹),
// 所以后台「从本机导入」就能像 WorkBuddy 一样自动拿到账号, 不需要用户去
// F12 里抄 token —— 解密与路径扫描在 trae_local.go。手动粘贴凭据 JSON 保留
// 为兜底(比如从别的机器搬账号)。
//
// 端点(对照 dsh REGION_GATEWAYS / TRAE_SOLO_*_PATH):
//   聊天:  POST <chat>/api/agent/v3/llm_utils_chat            (SSE)
//   目录:  POST <models>/api/ide/v1/get_detail_param           (多 directory function 合并)
//   签到:  GET/POST <pay>/trae/api/v2/ug/checkin_credits/{status,claim}
//   用量:  POST <pay>/trae/api/v2/pay/web_user_ent_usage
//   刷新:  POST <host>/trae/api/v3/oauth/ExchangeToken         (需 DeviceProof)
//
// 请求头(对照 dsh buildTraeHeaders / authedHeaders):
//   聊天用 Cloud-IDE-JWT + X-Ide-Token + x-plugin-channel:icube-ai + 设备/版本头;
//   签到/用量用 Cloud-IDE-JWT + x-device-id(仅 CN)。
// ============================================================================

const (
	traeAppID             = "6eefa01c-1036-4c7e-9ca5-d891f63bfcd8"
	traeChatPathCN        = "/api/agent/v3/llm_utils_chat"
	traeModelsPathCN      = "/api/ide/v1/get_detail_param"
	traeCheckinStatusPath = "/trae/api/v2/ug/checkin_credits/status"
	traeCheckinClaimPath  = "/trae/api/v2/ug/checkin_credits/claim"
	traeUsagePath         = "/trae/api/v2/pay/web_user_ent_usage"
)

// traeRegionGateways 各区域的网关(逐字对照 dsh region.ts 的 REGION_GATEWAYS,
// 别手改 —— 域名拼错会拿到 CDN 的 HTML 反爬页而不是 JSON)。
//
// 两个模型目录端点分属不同网关, 不能混:
//   - /api/remote/v1/models(官方花名册骨架)在 **remote**(solo.trae.cn)下,
//     且必须带 ?functions= 参数(不带只回一个分组, 2026-10-19 实测只回 solo_coder)。
//   - /api/ide/v1/get_detail_param(wire 目录)挂在 **chat** 网关下: 拿
//     solo.trae.cn 问它会拿到一段 <!doctype html> 而不是模型列表。
var traeRegionGateways = map[string]struct {
	remote string
	chat   string
	pay    string
	origin string
}{
	"cn": {
		remote: "https://solo.trae.cn",
		chat:   "https://trae-api-cn.mchost.guru",
		pay:    "https://api.trae.cn",
		origin: "https://www.trae.cn",
	},
	"global": {
		remote: "https://coresg-normal.trae.ai",
		chat:   "https://coresg-normal.trae.ai",
		pay:    "https://growsg-normal.trae.ai",
		origin: "https://www.trae.ai",
	},
}

// traeDirectoryFunctions 拉取模型目录时逐个询问的函数(与 dsh
// TRAE_DIRECTORY_FUNCTIONS.cn 同序)。顺序即优先级: 同一模型出现在多个函数下时
// 取先问到的那个。
//
// 这里的名字不能想当然: 第二个是 solo_work_lite(不是 solo_agent —— 后者能答
// 目录但拒绝聊天, 让它抢先会把一半模型标成不可调用), 也没有 builder_v3。
// 漏问会少模型, 多问会挤占优先级。
var traeDirectoryFunctions = []string{
	"solo_work_remote", "solo_work_lite", "solo_agent_remote", "chat_v3", "solo_coder",
}

// traeSoloFunction 没有 wire 行可用时的默认聊天函数(dsh TRAE_SOLO_FUNCTION)。
const traeSoloFunction = "solo_work_lite"

// traeRemoteDirectoryFunctions remote 花名册一次请求里要并的 function 分组
// (拼进 ?functions= 查询参数, 与 dsh TRAE_REMOTE_DIRECTORY_FUNCTIONS 同集)。
// 与 traeDirectoryFunctions 不同: 这里的名字只是"目录分组", 多问无害 ——
// 网关不认识的分组直接回空而不是报错。CN 之前只读到 solo_coder 一个分组
// (12 个模型), 就是漏了 functions 参数导致上游只回默认分组。
var traeRemoteDirectoryFunctions = []string{
	"solo_agent", "solo_agent_remote", "solo_work_remote", "solo_work_lite",
	"chat_v3", "solo_coder", "builder_v3",
}

type traeProviderConfig struct {
	Enabled    bool     `json:"enabled"`
	Proxies    []string `json:"proxies"`
	FreeOnly   bool     `json:"freeOnly"`
	MaxRetries int      `json:"maxRetries"`
}

func defaultTraeConfig() *traeProviderConfig {
	return &traeProviderConfig{Enabled: true, MaxRetries: 2}
}

var (
	traeCfg      = loadTraeConfig()
	traeCfgMu    sync.Mutex
	traeAccounts []*traeAccount
	traeAccMu    sync.RWMutex
	traeIdx      int
	traeModels   = map[string]traeModelInfo{}
	traeModelsMu sync.RWMutex
)

type traeAccount struct {
	ID       string `json:"id"`
	Region   string `json:"region"`  // cn / global
	Source   string `json:"source"`  // desktop(从本机 storage.json 导入) / cli / manual
	Edition  string `json:"edition"` // cn / sg / solo / solo-sg —— 决定刷新用的 ClientID
	Host     string `json:"host"`    // 上游给的网关, 刷新必须挂在它下面
	UserID   string `json:"userId"`
	UserName string `json:"userName"` // 账号名(可选, 仅展示)
	// UserRegion 上游声明的区(SG/CN)。与 Host 一起决定路由桶。
	UserRegion  string `json:"userRegion"`
	AccessToken string `json:"accessToken"`
	// RefreshToken 换令牌用。本机导入时一定带; 手动粘贴可能没有。
	RefreshToken string `json:"refreshToken"`
	DeviceID     string `json:"deviceId"`
	MachineID    string `json:"machineId"`
	AppVersion   string `json:"appVersion"`
	BuildVersion string `json:"buildVersion"`
	Platform     string `json:"platform"`
	// PrivateKeyPEM / PublicKeyPEM 本机的 ECDSA P-256 设备密钥对。
	// 新版刷新接口 /trae/api/v3/oauth/ExchangeToken 强制要用它签 DeviceProof:
	// 不签回 20405, 用别的 key 签回 20403。没有它刷新只能走旧接口兜底。
	PrivateKeyPEM   string `json:"privateKeyPEM"`
	PublicKeyPEM    string `json:"publicKeyPEM"`
	ExpiresAt       int64  `json:"expiresAt"`        // ms epoch
	RefreshExpireAt int64  `json:"refreshExpiresAt"` // ms epoch
	SavedAt         int64  `json:"savedAt"`
	LastError       string `json:"-"`
}

type traeModelInfo struct {
	// Key 是对外暴露的模型 id = remote 花名册的 name(即 Trae IDE 里显示的那
	// 个 id, 如 glm-5.1 / DeepSeek-V4-Flash)。与 Trae 客户端列表对齐就靠它。
	Key         string
	DisplayName string
	Context     int
	Output      int
	Multiplier  float64 // creditMultiplier
	// Priced 表示上游目录里这条真的带了 credit_multiplier 键。
	// 没有这个标记就无法区分"免费(0)"与"上游没给" —— 把没给的当成免费,
	// 客户端会以为能白嫖, 与 WorkBuddy hunyuan 那个坑同源(providerDecorateID
	// 注释里有完整说明), 所以必须单独记一位。
	Priced     bool
	Reasoning  bool
	Multimodal bool // remote 目录的 multimodal 位(上游自己的"能读图"答复)
	IsDefault  bool
	// MaxContext 是上游给的 Max 档窗口(context_window_tokens.max)。
	//
	// 只在 max_mode=true 时才算数 —— 上游几乎每个模型都带一个 max 数字, 但那
	// 是"开通 Max 档后会有多大", 不等于当前可用(dsh model-metadata.ts 同
	// 规则)。与 Context(dev 档)分开存, 只是为了跨分组去重时挑出能力更强
	// 的那一行(见 traePreferRow), 对外公布的窗口仍用 Context。
	MaxContext int
	// WireID 是 llm_utils_chat 真正接受的 config_name(来自 get_detail_param)。
	// 大多数模型 Key 就是 config_name, 此时留空; 少数两个不一致(如
	// DeepSeek-V4-Flash 的 wire id 是 DeepSeek-V4-Flash-Official), 聊天时必须
	// 用 wire id —— 拿展示 id 去调, 上游按"这个 function 不认识的名字"拒掉
	// (4001/3003), dsh 里对应 wireConfigName 字段。空值表示直接用 Key。
	WireID string
	// Function 是 remote 花名册里这条所在的分组名(如 solo_agent /
	// builder_v3 / chat_v3)。
	//
	// 它**不直接用于聊天**: 花名册的分组名里有 solo_agent 和 builder_v3,
	// 这两个能答目录却拒绝聊天(dsh 实测: 用 solo_agent 发任何模型都回
	// 4011)。它在这里的用处是记住该问 get_detail_param 的哪些分组, 以及
	// 在没有 wire 行时充当最后兜底(且必须过 traeIsChatFunction 白名单)。
	Function string
	// WireFunction 是 get_detail_param 里把这个 wire config_name 列出来的
	// function —— 也就是 llm_utils_chat 真正认的那个。
	//
	// 取值只来自 traeDirectoryFunctions(能聊天的那几个, 有序=优先级), 所以
	// 它天然不会是 solo_agent / builder_v3。聊天时优先用它。
	WireFunction string
}

// ============ 配置 ============

func traeConfigPath() string       { return kit.ResolveDataPath(".trae-config.json") }
func traeAccountsFilePath() string { return kit.ResolveDataPath(".trae-accounts.json") }

func loadTraeConfig() *traeProviderConfig {
	cfg := defaultTraeConfig()
	data, err := os.ReadFile(traeConfigPath())
	if err != nil {
		return cfg
	}
	if json.Unmarshal(data, cfg) != nil {
		return cfg
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 2
	}
	return cfg
}

func saveTraeConfig() {
	data, err := json.MarshalIndent(traeCfg, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(traeConfigPath(), data, 0600)
}

func getTraeConfig() *traeProviderConfig {
	traeCfgMu.Lock()
	defer traeCfgMu.Unlock()
	return &traeProviderConfig{
		Enabled: traeCfg.Enabled, Proxies: append([]string{}, traeCfg.Proxies...),
		FreeOnly: traeCfg.FreeOnly, MaxRetries: traeCfg.MaxRetries,
	}
}

func setTraeConfig(c *traeProviderConfig) {
	traeCfgMu.Lock()
	traeCfg = c
	traeCfgMu.Unlock()
	saveTraeConfig()
}

// ============ 账号 ============

func traeLoadAccountsLocked() []*traeAccount {
	if traeAccounts != nil {
		return traeAccounts
	}
	if data, err := os.ReadFile(traeAccountsFilePath()); err == nil {
		var list []*traeAccount
		if json.Unmarshal(data, &list) == nil {
			traeAccounts = list
		}
	}
	if traeAccounts == nil {
		traeAccounts = []*traeAccount{}
	}
	for _, a := range traeAccounts {
		if a.Region == "" {
			a.Region = "cn"
		}
		if a.MachineID == "" {
			a.MachineID = traeLocalDeviceID()
		}
		if a.Platform == "" {
			a.Platform = "win32"
		}
	}
	return traeAccounts
}

func traeLoadAccounts() []*traeAccount {
	traeAccMu.Lock()
	defer traeAccMu.Unlock()
	return traeLoadAccountsLocked()
}

func traeSaveAccountsLocked() {
	data, err := json.MarshalIndent(traeAccounts, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(traeAccountsFilePath(), data, 0600)
}

func traeSaveAccounts() {
	traeAccMu.Lock()
	defer traeAccMu.Unlock()
	traeSaveAccountsLocked()
}

func traePickAccount() *traeAccount {
	list := traeLoadAccounts()
	if len(list) == 0 {
		return nil
	}
	traeAccMu.Lock()
	idx := traeIdx % len(list)
	traeIdx++
	traeAccMu.Unlock()
	return list[idx]
}

func traeFindAccount(id string) *traeAccount {
	for _, a := range traeLoadAccounts() {
		if a.ID == id {
			return a
		}
	}
	return nil
}

// traeLocalDeviceID 稳定的本机设备标识(写入文件持久化, 不随账号切换变化)。
func traeLocalDeviceID() string {
	path := kit.ResolveDataPath(".trae-device.json")
	if data, err := os.ReadFile(path); err == nil {
		var m map[string]string
		if json.Unmarshal(data, &m) == nil && m["deviceId"] != "" {
			return m["deviceId"]
		}
	}
	b := make([]byte, 16)
	rand.Read(b)
	id := fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	_ = os.WriteFile(path, []byte(fmt.Sprintf(`{"deviceId":%q}`, id)), 0600)
	return id
}

// traeAddAccount 新增/更新账号, 立即同步模型目录。
//
// 去重口径是 (edition, userId) 而不是 accessToken —— 令牌每次刷新都会变,
// 按令牌去重会在每次「从本机导入」时复制出一个新账号。userId 才是稳定的。
func traeAddAccount(cred traeCredentialInput) (*traeAccount, error) {
	cred.AccessToken = strings.TrimSpace(cred.AccessToken)
	cred.DeviceID = strings.TrimSpace(cred.DeviceID)
	cred.UserID = strings.TrimSpace(cred.UserID)
	cred.Edition = strings.TrimSpace(cred.Edition)
	if cred.AccessToken == "" {
		return nil, fmt.Errorf("accessToken 不能为空")
	}
	if cred.DeviceID == "" {
		return nil, fmt.Errorf("deviceId 不能为空(本机导入会自动带上)")
	}
	if cred.Region == "" {
		cred.Region = "cn"
	}
	if cred.Edition == "" {
		cred.Edition = "cn"
	}
	// 账号身份: 优先用 userId, 否则从 JWT 解析 sub。
	uid := cred.UserID
	if uid == "" {
		uid = jwtSub(cred.AccessToken)
	}
	list := traeLoadAccounts()
	var acc *traeAccount
	for _, a := range list {
		if uid != "" && a.UserID == uid && a.Edition == cred.Edition {
			acc = a
			break
		}
		if a.AccessToken == cred.AccessToken {
			acc = a
			break
		}
	}
	if acc == nil {
		b := make([]byte, 6)
		rand.Read(b)
		acc = &traeAccount{ID: "trae_" + base64.StdEncoding.EncodeToString(b)[:8]}
		traeAccMu.Lock()
		traeAccounts = append(traeAccounts, acc)
		traeAccMu.Unlock()
	}
	acc.Region = cred.Region
	acc.Source = strings.TrimSpace(cred.Source)
	if acc.Source == "" {
		acc.Source = "manual"
	}
	acc.Edition = cred.Edition
	acc.Host = strings.TrimRight(strings.TrimSpace(cred.Host), "/")
	acc.UserRegion = strings.TrimSpace(cred.UserRegion)
	acc.AccessToken = cred.AccessToken
	acc.RefreshToken = strings.TrimSpace(cred.RefreshToken)
	acc.UserID = uid
	acc.UserName = strings.TrimSpace(cred.AccountName)
	acc.DeviceID = cred.DeviceID
	acc.MachineID = strings.TrimSpace(cred.MachineID)
	acc.AppVersion = strings.TrimSpace(cred.AppVersion)
	acc.BuildVersion = strings.TrimSpace(cred.BuildVersion)
	acc.PrivateKeyPEM = strings.TrimSpace(cred.PrivateKeyPEM)
	acc.PublicKeyPEM = strings.TrimSpace(cred.PublicKeyPEM)
	acc.Platform = strings.TrimSpace(cred.Platform)
	if acc.Platform == "" {
		acc.Platform = "win32"
	}
	if acc.MachineID == "" {
		acc.MachineID = traeLocalDeviceID()
	}
	// 过期时间: 上游显式给了就用它的, 否则从 JWT 的 exp 推断。
	if cred.ExpiresAt > 0 {
		acc.ExpiresAt = cred.ExpiresAt
	} else if t, ok := jwtExp(cred.AccessToken); ok {
		acc.ExpiresAt = t
	}
	if cred.RefreshExpireAt > 0 {
		acc.RefreshExpireAt = cred.RefreshExpireAt
	}
	acc.SavedAt = time.Now().UnixMilli()
	acc.LastError = ""
	traeSaveAccounts()
	if _, err := traeSyncModels(); err != nil {
		acc.LastError = err.Error()
		traeSaveAccounts()
	}
	return acc, nil
}

func traeDeleteAccount(id string) bool {
	traeAccMu.Lock()
	defer traeAccMu.Unlock()
	list := traeLoadAccountsLocked()
	out := list[:0]
	removed := false
	for _, a := range list {
		if a.ID == id {
			removed = true
			continue
		}
		out = append(out, a)
	}
	traeAccounts = out
	traeSaveAccountsLocked()
	return removed
}

// jwtSub / jwtExp 从 JWT 取 sub / exp(秒)。解析失败返回空/0。
func jwtSub(token string) string {
	c := jwtClaims(token)
	if c == nil {
		return ""
	}
	if s, _ := c["sub"].(string); s != "" {
		return s
	}
	return ""
}

func jwtExp(token string) (int64, bool) {
	c := jwtClaims(token)
	if c == nil {
		return 0, false
	}
	switch v := c["exp"].(type) {
	case float64:
		return int64(v * 1000), true
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return int64(f * 1000), true
		}
	}
	return 0, false
}

func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	raw := parts[1]
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		payload, err = base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return nil
		}
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return nil
	}
	return claims
}

// ============ 请求头 ============

// traeChatHeaders 聊天请求头(对照 dsh buildTraeHeaders)。
// traeChatHeaders 聊天/目录请求头(对照 dsh buildTraeHeaders 的 agent-task 口径)。
//
// 这一组头**缺一不可**, 少了会一路"看起来通了但其实是别的错误":
//   - 只发 Authorization + 机器号: 官方模型回
//     2001 no function config found for appId=..., function=solo_coder
//     —— 服务端认不出你是哪个客户端, 查不到该 function 的配置。
//   - 补上 X-Cloudide-Token / x-uid / x-request-id / x-trae-request-id 后
//     这条 2001 消失(实测), 请求才真正走到模型服务。
func traeChatHeaders(a *traeAccount) map[string]string {
	appVer := a.AppVersion
	if appVer == "" {
		appVer = "1.0.0"
	}
	verCode := normalizeTraeVersionCode(a.BuildVersion)
	if verCode == traeVersionCodeFallback && a.AppVersion != "" {
		// 构建号读不到时退回应用版本号(若它本身是纯整数)。
		if c := normalizeTraeVersionCode(a.AppVersion); c != traeVersionCodeFallback {
			verCode = c
		}
	}
	devType := "windows"
	switch a.Platform {
	case "darwin":
		devType = "mac"
	case "linux":
		devType = "linux"
	}
	reqID := newUUID()
	traceID := strings.ReplaceAll(reqID, "-", "")[:32]
	origin := traeRegionGateways[regionOfTrae(a)].origin
	return map[string]string{
		"Authorization":        "Cloud-IDE-JWT " + a.AccessToken,
		"X-Ide-Token":          a.AccessToken,
		"X-Cloudide-Token":     a.AccessToken,
		"x-uid":                a.UserID,
		"x-request-id":         reqID,
		"x-trae-request-id":    reqID,
		"x-plugin-channel":     "icube-ai",
		"User-Agent":           "Trae/" + appVer,
		"x-app-id":             traeAppID,
		"x-machine-id":         a.MachineID,
		"x-device-id":          a.DeviceID,
		"x-device-type":        devType,
		"x-app-version":        appVer,
		"x-ide-version":        appVer,
		"x-app-version-code":   verCode,
		"x-ide-version-code":   verCode,
		"x-custom-trace-id":    traceID,
		"x-flow-traceparent":   fmt.Sprintf("04-%s-%s-01", traceID, traceID[:16]),
		"request-traffic-type": "prod",
		"Content-Type":         "application/json",
		"Accept":               "text/event-stream, application/json",
		"Origin":               origin,
		"Referer":              origin + "/",
	}
}

// traeUsageHeaders 签到/用量请求头(对照 dsh authedHeaders)。
func traeUsageHeaders(a *traeAccount) map[string]string {
	origin := traeRegionGateways[regionOfTrae(a)].origin
	h := map[string]string{
		"Authorization": "Cloud-IDE-JWT " + a.AccessToken,
		"Content-Type":  "application/json",
		"User-Agent":    "Mozilla/5.0",
		"Origin":        origin,
		"Referer":       origin + "/",
	}
	if a.DeviceID != "" {
		h["x-device-id"] = a.DeviceID
	}
	return h
}

func regionOfTrae(a *traeAccount) string {
	if a.Region == "global" {
		return "global"
	}
	return "cn"
}

// traeVersionCodeFallback 上游把 x-app-version-code 绑定成数字校验。
// Trae 存的 iCubeLastVersion 是点分构建串(如 "2.3.92182"), 直接发会被拒:
// 4001 expr_path=app_version_code, cause=parameter type does not match
// binding data。实测(对照 dsh normalizeTraeVersionCode)要么发纯整数, 要么
// 发这个固定值 —— 取前导位数(把 2.3.92182 变成 "2")虽然类型对, 但值不匹配
// 已注册构建, 属于碰运气。
const traeVersionCodeFallback = "20260716"

// normalizeTraeVersionCode 保留纯整数版本串, 其余(点分构建号/空)一律回退。
func normalizeTraeVersionCode(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return traeVersionCodeFallback
	}
	for _, r := range v {
		if r < '0' || r > '9' {
			return traeVersionCodeFallback
		}
	}
	return v
}

// ============ 模型目录 ============

// traeRateOf 从 get_detail_param 的一行里取"用户实际看到的"倍率。
//
// 上游给两个地方: credit_multiplier 是裸数字; display_contact_config 是一串
// 二次 JSON 字符串, 里的 consumption_rate.data.rate 才是打折后的真实值。
// 实测同一条 Remote 模型 credit_multiplier=0.8 而 rate=0.08 —— 差一个数量级。
// 后者只在 CN 网关有, 国际版完全没有, 所以不能只读一个字段:
// 两个都缺才认为"上游没给价"(Priced=false), 只要有一个就认一个。
func traeRateOf(raw map[string]any) (float64, bool) {
	if d, ok := raw["display_contact_config"].(string); ok && d != "" {
		var outer map[string]any
		if json.Unmarshal([]byte(d), &outer) == nil {
			// 上游字段名两种拼法都见过(驼峰/下划线), 都试
			for _, key := range []string{"consumption_rate", "consumptionRate"} {
				inner, _ := outer[key].(map[string]any)
				if inner == nil {
					continue
				}
				if en, ok := inner["enable"].(bool); !ok || !en {
					continue
				}
				data, _ := inner["data"].(map[string]any)
				if data == nil {
					continue
				}
				if r, ok := data["rate"].(float64); ok && r > 0 {
					return r, true
				}
			}
		}
	}
	if m, ok := raw["credit_multiplier"].(float64); ok {
		return m, true
	}
	// 有的行只在嵌套 display_config 里给, 兜一层
	if d, ok := raw["display_config"].(map[string]any); ok {
		if m, ok := d["credit_multiplier"].(float64); ok {
			return m, true
		}
	}
	return 0, false
}

// traeContextOf 从 {dev,max} 里取默认(dev 档)窗口, 缺 dev 时退到 max。
func traeContextOf(v any) int {
	if n := traeWindowOf(v, "dev"); n > 0 {
		return n
	}
	return traeWindowOf(v, "max")
}

// traeWindowOf 取 context_window_tokens 里指定的那一档(dev / max)。
func traeWindowOf(v any, key string) int {
	cw, ok := v.(map[string]any)
	if !ok {
		return 0
	}
	if n, ok := cw[key].(float64); ok && n > 0 {
		return int(n)
	}
	return 0
}

// traeMaxWindowOf 取 Max 档窗口; max_mode 不为 true 时一律返回 0。
//
// 上游几乎每个模型都带一个 context_window_tokens.max 数字, 但那是"开通 Max
// 档后会有多大", 不等于当前可用。不判 max_mode 就把所有模型的窗口都当成
// Max 档, 对外公布的上下文会整体虚高(dsh model-metadata.ts 同规则)。
func traeMaxWindowOf(raw map[string]any) int {
	if raw["max_mode"] != true {
		return 0
	}
	return traeWindowOf(raw["context_window_tokens"], "max")
}

// traeReasoningOf 判断一个 remote 目录行是否支持推理。
func traeReasoningOf(raw map[string]any) bool {
	if cfg, ok := raw["reasoning_effort_config"].(map[string]any); ok {
		if b, ok := cfg["support_thinking"].(bool); ok && b {
			return true
		}
		if b, ok := cfg["enable"].(bool); ok && b {
			return true
		}
		if opts, ok := cfg["options"].([]any); ok && len(opts) > 0 {
			return true
		}
	}
	if b, ok := raw["is_reasoning"].(bool); ok && b {
		return true
	}
	// features 是一串二次 JSON, 里的 reasoning.enable 是另一处声明
	if s, ok := raw["features"].(string); ok && s != "" {
		var feat map[string]any
		if json.Unmarshal([]byte(s), &feat) == nil {
			if r, ok := feat["reasoning"].(map[string]any); ok && r["enable"] == true {
				return true
			}
		}
	}
	return false
}

// traePreferRow 同一个模型 id 在 remote 花名册的多行里挑"能力更强"的那行
// (对照 dsh solo-remote.ts 的 preferStrongerRow)。
//
// remote 目录按 function 分组返回, 同一模型会出现在多个分组里, 而且各分组
// 报的窗口并不一致 —— 上游每次返回的分组顺序还不一样(dsh issue #23: 连打
// 5 次是 5 种顺序)。所以"先到先得"会让对外公布的上下文窗口变成抽奖: 实测
// glm-5.2 有时是 dev=116000 有时是 dev=200000。
//
// 逐层比较: 有 Max 档 > 无 Max 档 → Max 大的赢 → 都没有(或相等)时 dev 窗口
// 宽的赢 → 全相等时保留先到的, 维持上游给出的模型顺序。
func traePreferRow(cand, inc traeModelInfo) traeModelInfo {
	if cand.MaxContext > 0 && inc.MaxContext == 0 {
		return cand
	}
	if cand.MaxContext == 0 && inc.MaxContext > 0 {
		return inc
	}
	if cand.MaxContext != inc.MaxContext {
		if cand.MaxContext > inc.MaxContext {
			return cand
		}
		return inc
	}
	if cand.Context > inc.Context {
		return cand
	}
	return inc
}

// traeFeaturesRateOf 解析 models 接口里的 features 字段(一串二次 JSON)。
// 真实倍率在 consumption_rate.data.rate 里, enable=false 时上游是"不计费",
// 此时视为未标价 —— 与 traeRateOf 对 display_contact_config 的处理同口径。
func traeFeaturesRateOf(v any) (float64, bool) {
	s, ok := v.(string)
	if !ok || s == "" {
		return 0, false
	}
	var feat map[string]any
	if json.Unmarshal([]byte(s), &feat) != nil {
		return 0, false
	}
	rate, priced := traeConsumptionRateOf(feat)
	return rate, priced
}

// traeConsumptionRateOf 从任一形如 {consumption_rate:{enable,data:{rate}}} 的
// 对象里取倍率。驼峰/下划线两种键名都认(上游两种拼法都出现过)。
func traeConsumptionRateOf(obj map[string]any) (float64, bool) {
	if obj == nil {
		return 0, false
	}
	for _, key := range []string{"consumption_rate", "consumptionRate"} {
		inner, _ := obj[key].(map[string]any)
		if inner == nil {
			continue
		}
		if en, ok := inner["enable"].(bool); !ok || !en {
			continue
		}
		data, _ := inner["data"].(map[string]any)
		if data == nil {
			continue
		}
		if r, ok := data["rate"].(float64); ok && r >= 0 {
			return r, true
		}
	}
	return 0, false
}

// traeRemoteModelsPath 官方模型目录(按 function 分组, 自带 function 归属)。
const traeRemoteModelsPath = "/api/remote/v1/models"

// traeFetchRemoteModels 拉官方模型目录(remote 网关 + ?functions= 并集, 分组列表见
// traeRemoteDirectoryFunctions)。
//
// 这是"有哪些模型"的**唯一权威来源**: 只收 remote 目录里的模型, 永不把
// get_detail_param 的行当花名册来源 —— 否则用户自配的自定义模型
// (config_source=3, 其中 4 个 base_url 指回本服务 127.0.0.1:9999) 和
// fast_apply 这类非聊天工具模型会混进来, 选中即无限自调用。
//
// 按 group.function 记下每个模型的归属, 聊天时原样回传上游(llm_utils_chat
// 缺 function 会回 2001 "function is empty")。
func traeFetchRemoteModels(acc *traeAccount, base string) ([]traeModelInfo, error) {
	url := base + traeRemoteModelsPath
	if len(traeRemoteDirectoryFunctions) > 0 {
		url += "?functions=" + strings.Join(traeRemoteDirectoryFunctions, ",")
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range traeChatHeaders(acc) {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "application/json")
	client := kit.NewHTTPClient(30 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	rb, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("models 目录 HTTP %d %s", resp.StatusCode, kit.Truncate(strings.TrimSpace(string(rb)), 200))
	}
	var doc struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			List []struct {
				Function string           `json:"function"`
				Models   []map[string]any `json:"models"`
			} `json:"list"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rb, &doc); err != nil {
		return nil, fmt.Errorf("models 目录解析失败: %w", err)
	}
	if doc.Code != 0 && doc.Code != 200 {
		return nil, fmt.Errorf("models 目录 code=%d %s", doc.Code, doc.Msg)
	}
	out := make([]traeModelInfo, 0, 64)
	for _, group := range doc.Data.List {
		fn := strings.TrimSpace(group.Function)
		if fn == "" {
			continue
		}
		for _, raw := range group.Models {
			id, _ := raw["name"].(string)
			if strings.TrimSpace(id) == "" {
				continue
			}
			mult, priced := traeFeaturesRateOf(raw["features"])
			// reasoning 上游在两处声明, 任一说"是"就算 —— 只认
			// support_thinking 会漏掉那些只在 features.reasoning 或
			// reasoning_effort_config.options 里声明的模型(dsh 的
			// model-metadata.ts 认的就是这两处)。
			reasoning := traeReasoningOf(raw)
			display, _ := raw["display_name"].(string)
			if strings.TrimSpace(display) == "" {
				display = id
			}
			multimodal := raw["multimodal"] == true
			out = append(out, traeModelInfo{
				Key: id, DisplayName: display,
				Context:    traeWindowOf(raw["context_window_tokens"], "dev"),
				MaxContext: traeMaxWindowOf(raw),
				Multiplier: mult, Priced: priced, Reasoning: reasoning, Multimodal: multimodal,
				IsDefault: raw["is_default"] == true,
				Function:  fn,
			})
		}
	}
	return out, nil
}

// traeFetchDetailRow 拉一个 function 的 get_detail_param 结果(官方行)。
// 返回行本身; 解析失败时把错误写进 firstErr 供最终报告。
func traeFetchDetailRow(acc *traeAccount, base, fn string, firstErr *error) []map[string]any {
	body, _ := json.Marshal(map[string]any{
		"function":            fn,
		"config_names":        nil,
		"need_prompt":         false,
		"current_config_info": nil,
		"poly_prompt":         true,
		"mode_type":           nil,
		"agent_type":          nil,
	})
	req, err := http.NewRequest(http.MethodPost, base+traeModelsPathCN, bytes.NewReader(body))
	if err != nil {
		return nil
	}
	for k, v := range traeChatHeaders(acc) {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "application/json")
	client := kit.NewHTTPClient(30 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		if firstErr != nil && *firstErr == nil {
			*firstErr = fmt.Errorf("%s: %v", fn, err)
		}
		return nil
	}
	rb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		if firstErr != nil && *firstErr == nil {
			*firstErr = fmt.Errorf("%s: HTTP %d %s", fn, resp.StatusCode, kit.Truncate(strings.TrimSpace(string(rb)), 200))
		}
		return nil
	}
	var doc struct {
		ConfigInfoList []map[string]any `json:"config_info_list"`
	}
	if json.Unmarshal(rb, &doc) != nil {
		if firstErr != nil && *firstErr == nil {
			*firstErr = fmt.Errorf("%s: 响应解析失败 %s", fn, kit.Truncate(strings.TrimSpace(string(rb)), 200))
		}
		return nil
	}
	return doc.ConfigInfoList
}

// traeIsCustomRow 判断一行是不是用户自配的自定义模型(config_source=3)。
// 这类模型不属于 Trae 提供的模型, 且可能把 base_url 指回本服务造成死循环,
// 一律不当作花名册来源。
func traeIsCustomRow(raw map[string]any) bool {
	if s, ok := raw["is_custom_model"].(bool); ok && s {
		return true
	}
	if d, ok := raw["display_config"].(map[string]any); ok {
		if s, ok := d["is_custom_model"].(bool); ok && s {
			return true
		}
	}
	// config_source: 1=内置 3=用户自定义
	if c, ok := raw["config_source"].(float64); ok && int(c) == 3 {
		return true
	}
	return false
}

func traeSyncModels() (int, error) {
	acc := traePickAccount()
	if acc == nil {
		return 0, fmt.Errorf("无可用 Trae 账号")
	}
	gw := traeRegionGateways[regionOfTrae(acc)]
	next := map[string]traeModelInfo{}
	var firstErr error

	// 1) 官方目录 = 花名册(含每个模型的 function 归属)。remote 端点在
	//    remote 网关下, 喂 chat 网关会拿到 CDN 的 HTML 反爬页。
	if official, err := traeFetchRemoteModels(acc, gw.remote); err != nil {
		firstErr = err
	} else {
		for _, m := range official {
			// 同一模型在多个分组里都有, 且各分组报的窗口不一致、分组顺序
			// 每次还不一样, 所以按"能力更强"合并而不是先到先得(见
			// traePreferRow)。
			if inc, ok := next[m.Key]; ok {
				next[m.Key] = traePreferRow(m, inc)
				continue
			}
			next[m.Key] = m
		}
	}

	// 2) get_detail_param 补 wire 字段: 真正能聊天的 config_name、要回放的
	//    function、打折后的真实倍率。
	//
	//    只补花名册里已有的, 绝不新增 —— 否则用户自配的自定义模型
	//    (config_source=3, 其中几个 base_url 指回本服务造成死循环)和
	//    fast_apply 这类非聊天工具模型又混进来了。
	//
	//    严格按 traeDirectoryFunctions 的**顺序**逐个问: 顺序即优先级, 同一个
	//    config_name 被多个函数列出时取先问到的那个(dsh 的 "first wins")。
	//    之前这里把 remote 花名册的分组名也混进来, 又 sort.Strings 排了一遍
	//    —— 优先级直接失效, 而且把 solo_agent / builder_v3 这两个"只答目录、
	//    拒绝聊天"的名字也当成了可调用来源, 拿它们去聊天会被上游整批拒掉。
	wireByID := map[string]map[string]any{} // 小写 config_name -> 原始行
	wireFn := map[string]string{}           // 小写 config_name -> 列出它的 function
	wireByName := map[string]string{}       // 小写 display_name -> 小写 config_name
	for _, fn := range traeDirectoryFunctions {
		for _, raw := range traeFetchDetailRow(acc, gw.chat, fn, &firstErr) {
			if traeIsCustomRow(raw) {
				continue
			}
			id, _ := raw["config_name"].(string)
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			lk := strings.ToLower(id)
			if _, ok := wireByID[lk]; ok {
				continue // 先到先得: 顺序即优先级
			}
			wireByID[lk] = raw
			wireFn[lk] = fn
			if d, ok := raw["display_config"].(map[string]any); ok {
				if s, ok := d["display_name"].(string); ok && strings.TrimSpace(s) != "" {
					wireByName[strings.ToLower(strings.TrimSpace(s))] = lk
				}
			}
		}
	}

	for key, m := range next {
		lk := strings.ToLower(key)
		// 两级 join(对照 dsh catalog.ts 的 mergeTraeModelSources), 都按小写
		// 比 —— 上游两侧的大小写并不总是一致:
		//   1. wire 的 config_name == 花名册 id(大多数模型, 命中率最高)
		//   2. wire 的 display_name == 花名册的 display_name(展示 id 与 wire
		//      id 不同的那些, 如 Seed-Code)
		// 只做第一级会让展示 id ≠ wire id 的模型既拿不到打折倍率, 也拿不到
		// WireID —— 而聊天时没有 WireID 就等于拿一个上游不认识的名字去调。
		raw, ok := wireByID[lk]
		if !ok && strings.TrimSpace(m.DisplayName) != "" {
			if alias, ok2 := wireByName[strings.ToLower(strings.TrimSpace(m.DisplayName))]; ok2 {
				raw, ok = wireByID[alias]
			}
		}
		if !ok {
			continue
		}
		wid, _ := raw["config_name"].(string)
		wid = strings.TrimSpace(wid)
		// wire id 与展示 id 不同才记 —— 相同就留空, 免得下游到处判空等于判等。
		if wid != "" && !strings.EqualFold(wid, key) {
			m.WireID = wid
		}
		if f := wireFn[strings.ToLower(wid)]; f != "" {
			m.WireFunction = f
		}
		// wire 倍率(打折后)比 Remote 目录里的准, 有就覆盖
		if mult, priced := traeRateOf(raw); priced {
			m.Multiplier, m.Priced = mult, priced
		}
		if m.Context == 0 {
			m.Context = traeContextOf(raw["context_window_tokens"])
		}
		if m.MaxContext == 0 {
			m.MaxContext = traeMaxWindowOf(raw)
		}
		if !m.Reasoning {
			m.Reasoning = traeReasoningOf(raw)
		}
		if m.DisplayName == "" {
			if d, ok := raw["display_config"].(map[string]any); ok {
				if s, ok := d["display_name"].(string); ok {
					m.DisplayName = s
				}
			}
		}
		if d, ok := raw["is_default"].(bool); ok {
			m.IsDefault = d
		}
		next[key] = m
	}

	if len(next) == 0 {
		if firstErr != nil {
			return 0, fmt.Errorf("上游未返回可用模型 (%v)", firstErr)
		}
		return 0, fmt.Errorf("上游未返回可用模型")
	}
	traeModelsMu.Lock()
	traeModels = next
	traeModelsMu.Unlock()
	log.Printf("  trae: %d models synced (account %s)", len(next), maskSecret(acc.UserID))
	return len(next), nil
}

func traeModelList() []map[string]any {
	traeModelsMu.RLock()
	keys := make([]string, 0, len(traeModels))
	for k := range traeModels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	list := make([]traeModelInfo, 0, len(keys))
	for _, k := range keys {
		list = append(list, traeModels[k])
	}
	traeModelsMu.RUnlock()
	dup := providerPublicIDs()
	out := make([]map[string]any, 0, len(list))
	for _, m := range list {
		publicID := m.Key
		if dup[strings.ToLower(m.Key)] {
			publicID = "trae/" + m.Key
		}
		image, video := applyModalityProbe(modalityKey("trae", m.Key), nil, nil)
		out = append(out, map[string]any{
			"id":          publicID,
			"baseId":      m.Key,
			"name":        m.DisplayName,
			"context":     m.Context,
			"output":      m.Output,
			"price":       m.Multiplier,
			"priceFactor": m.Multiplier,
			"priced":      m.Priced,
			"reason":      m.Reasoning,
			"image":       image,
			"video":       video,
		})
	}
	return out
}

func traeModelSnapshot() []traeModelInfo {
	traeModelsMu.RLock()
	defer traeModelsMu.RUnlock()
	out := make([]traeModelInfo, 0, len(traeModels))
	for _, m := range traeModels {
		out = append(out, m)
	}
	return out
}

func traeResolveModel(id string) (traeModelInfo, bool) {
	traeModelsMu.RLock()
	defer traeModelsMu.RUnlock()
	if m, ok := traeModels[id]; ok {
		return m, true
	}
	for _, m := range traeModels {
		if strings.TrimSpace(m.DisplayName) == id {
			return m, true
		}
	}
	return traeModelInfo{}, false
}

func traeResolveDecorated(id string) (traeModelInfo, bool) {
	traeModelsMu.RLock()
	defer traeModelsMu.RUnlock()
	for _, m := range traeModels {
		// 与 providerModelList 同口径: 只有上游给了倍率才能判免费
		free := m.Priced && m.Multiplier == 0
		if providerDecorateID("trae", m.Key, m.DisplayName, m.Multiplier, free) == id {
			return m, true
		}
		if providerDecorateID("", m.Key, m.DisplayName, m.Multiplier, false) == id {
			return m, true
		}
	}
	return traeModelInfo{}, false
}

// ============ chat ============

// traeBuildBody 把 OpenAI 请求体改写成 llm_utils_chat 的信封。
//
// llm_utils_chat 不是 OpenAI 兼容端点 —— 它按 config_name + function 定位
// 模型, 且对可选字段(temperature / max_tokens / tool_choice /
// response_format …)一律校验失败。所以能带过去的只有 messages / tools /
// reasoning_effort, 其余必须丢掉。
//
// 对照 dsh upstream/trae/src/solo.ts 的 prepareSoloBody。
func traeBuildBody(params map[string]any, m traeModelInfo) map[string]any {
	// llm_utils_chat 按 config_name + function 定位模型, 两个都要发。
	//
	// config_name 必须**带**, 而且必须是 get_detail_param 给的 wire id ——
	// dsh 的 prepareSoloBody 就是把 model 和 config_name 都填成 wire id。
	// 这里以前是"禁带 config_name", 依据是"一带就回 4001"。那次实测是把展示
	// id 当 config_name 发: 展示 id 只有刚好等于 wire id 时才成立, 不等时上
	// 游按"这个 function 不认识的名字"拒掉 —— dsh 在 solo.ts 里记了同一个坑
	// ("the probe had called them by display id while the chat endpoint needs
	// the config_name its owning function returned")。所以修法不是删字段, 而
	// 是填上正确的 wire id。
	wire := m.WireID
	if strings.TrimSpace(wire) == "" {
		wire = m.Key
	}
	body := map[string]any{
		"model":       wire,
		"config_name": wire,
		"stream":      true,
	}
	// function 必填: 缺了上游回 2001 "function is empty, cannot resolve
	// model by usage="。客户端显式指定时优先用客户端的(dsh 同规则)。
	fn := traeChatFunction(m)
	if s, ok := params["function"].(string); ok && strings.TrimSpace(s) != "" {
		fn = strings.TrimSpace(s)
	}
	body["function"] = fn

	if v, ok := params["messages"]; ok {
		body["messages"] = traeNormalizeMessages(v)
	} else {
		body["messages"] = []any{}
	}
	if v, ok := params["tools"]; ok && v != nil {
		body["tools"] = traeNormalizeTools(v)
	}
	if v, ok := params["reasoning_effort"].(string); ok && v != "" {
		body["reasoning_effort"] = v
	}
	return body
}

// traeChatFunction 决定这次聊天要回放哪个 directory function。
//
// 优先级: wire 目录给的函数 > remote 分组(仅当它是聊天函数) > 默认。
// remote 花名册的分组名里有 solo_agent 和 builder_v3 —— 它们能答目录却拒绝
// 聊天。直接拿 remote 的分组名去发聊天, 会把这些模型的每一次调用都送进一条
// 上游根本不服务的路由。
func traeChatFunction(m traeModelInfo) string {
	if traeIsChatFunction(m.WireFunction) {
		return m.WireFunction
	}
	if traeIsChatFunction(m.Function) {
		return m.Function
	}
	return traeSoloFunction
}

// traeIsChatFunction 一个 directory function 是不是 llm_utils_chat 认的。
//
// 白名单就是 traeDirectoryFunctions —— 那是"能用来聊天"的那几个, 不是"能答
// 目录"的那几个(后者还多出 solo_agent / builder_v3)。
func traeIsChatFunction(fn string) bool {
	fn = strings.TrimSpace(fn)
	if fn == "" {
		return false
	}
	for _, f := range traeDirectoryFunctions {
		if f == fn {
			return true
		}
	}
	return false
}

// traeNormalizeMessages 改写消息信封。上游只认 system/assistant/user/tool,
// 且 content 必须是数组(传字符串会回
// "json: cannot unmarshal string into Go struct field LLMRawMessage.messages.content of type []*idecopilot.LLMRawMessageContent")。
//
// 不就地修改: 同一份 params 在重试路径里会被复用, 改坏了第二次就发错。
func traeNormalizeMessages(v any) any {
	msgs, ok := v.([]any)
	if !ok {
		return cleanMessages([]any{v})
	}
	src := cleanMessages(msgs)
	out := make([]any, 0, len(src))
	for _, raw := range src {
		msg, ok := raw.(map[string]any)
		if !ok {
			out = append(out, raw)
			continue
		}
		role, _ := msg["role"].(string)
		if role == "tool" {
			// 上游要求 tool 消息必须带 tool_call_id, 缺了整条拒收。
			// 这里丢弃而不是报错: 少一条工具结果远好过让整次调用 400,
			// 模型仍能基于之前的调用回答。
			if id, _ := msg["tool_call_id"].(string); strings.TrimSpace(id) == "" {
				continue
			}
		}
		// 与 cleanMessages 同理: clone 而不是就地改
		clone := make(map[string]any, len(msg)+2)
		for k, val := range msg {
			clone[k] = val
		}
		if role == "assistant" {
			// dsh: assistant 的 tool_calls 要摊平成 function_call。
			if calls, ok := clone["tool_calls"].([]any); ok {
				clone["function_call"] = calls
				delete(clone, "tool_calls")
			}
		}
		// content 字符串 → [{type:text, text}]
		if c, ok := clone["content"].(string); ok {
			clone["content"] = []any{map[string]any{"type": "text", "text": c}}
		}
		out = append(out, clone)
	}
	return out
}

// traeNormalizeTools 把 OpenAI 的 tools 转成上游要的形态:
// function.parameters 必须是 JSON 字符串而不是对象。
func traeNormalizeTools(v any) any {
	tools, ok := v.([]any)
	if !ok {
		return v
	}
	out := make([]any, 0, len(tools))
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			out = append(out, raw)
			continue
		}
		fn, ok := tool["function"].(map[string]any)
		if !ok {
			out = append(out, raw)
			continue
		}
		clone := make(map[string]any, len(fn))
		for k, val := range fn {
			clone[k] = val
		}
		if p, ok := clone["parameters"].(map[string]any); ok {
			if b, err := json.Marshal(p); err == nil {
				clone["parameters"] = string(b)
			}
		}
		outer := make(map[string]any, len(tool))
		for k, val := range tool {
			outer[k] = val
		}
		outer["function"] = clone
		out = append(out, outer)
	}
	return out
}

func traePrepareChat(params map[string]any) (*http.Response, traeModelInfo, error) {
	model, _ := params["model"].(string)
	name := providerStripPrefix(model)
	m, ok := traeResolveModel(name)
	if !ok {
		if providerIsAliasModel(name) {
			return nil, m, providerErr(http.StatusBadRequest, "invalid_request_error",
				"model %q is a placeholder alias excluded from the catalog; pick a concrete model from /v1/models", model)
		}
		return nil, m, providerErr(http.StatusBadRequest, "invalid_request_error",
			"model %q is not an available trae model", model)
	}
	acc := traePickAccount()
	if acc == nil {
		return nil, m, providerErr(http.StatusServiceUnavailable, "auth_error",
			"no trae account available; add a credential in /admin/")
	}
	// 令牌已过期或 5 分钟内到期: 先主动换一次, 免得先撞一次 401 再重试
	// (撞 401 再换虽然也能成功, 但每次都要多打一次上游)。刷新失败不阻断 ——
	// 让 traeCallChat 实测一次, 可能是本机时间/上游判定不一致导致的误判。
	if acc.ExpiresAt > 0 && time.Now().UnixMilli() >= acc.ExpiresAt-5*60*1000 {
		if err := traeRefreshToken(acc); err != nil {
			log.Printf("  trae: proactive token refresh failed: %v", err)
		}
	}
	resp, err := traeCallChat(params, m, acc)
	if err != nil {
		return nil, m, err
	}
	return resp, m, nil
}

func traeCallChat(params map[string]any, m traeModelInfo, a *traeAccount) (*http.Response, error) {
	gw := traeRegionGateways[regionOfTrae(a)]
	url := gw.chat + traeChatPathCN
	raw, err := json.Marshal(traeBuildBody(params, m))
	if err != nil {
		return nil, err
	}
	cfg := getTraeConfig()
	attempts := cfg.MaxRetries
	if attempts <= 0 {
		attempts = 2
	}
	var lastErr error
	for i := 0; i <= attempts; i++ {
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		for k, v := range traeChatHeaders(a) {
			req.Header.Set(k, v)
		}
		req.Header.Set("Accept", "text/event-stream")
		client := kit.NewHTTPClient(10 * time.Minute)
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
		} else if resp.StatusCode == 200 {
			return resp, nil
		} else {
			eb, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d %s", resp.StatusCode, kit.Truncate(strings.TrimSpace(string(eb)), 300))
			if resp.StatusCode == http.StatusUnauthorized {
				// 令牌过期: 换一次令牌, 清掉错误让下一次循环用新令牌重试
				// (不能在这里递归 —— 递归结果会被丢弃, 白跑一次请求还盖掉真实错误)。
				// 刷新失败则直接返回, 重试同一个过期令牌没有意义。
				if rerr := traeRefreshToken(a); rerr != nil {
					return nil, fmt.Errorf("令牌已过期且刷新失败: %v (上游: HTTP 401)", rerr)
				}
				lastErr = nil
			}
		}
		if i < attempts {
			time.Sleep(time.Duration(500+400*i) * time.Millisecond)
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("上游未返回可用响应")
	}
	return nil, lastErr
}

// ============ 令牌刷新 ============
//
// 刷新契约按**版本**分叉(不是按地区), 对照 dsh refresh.ts:
//
//	| 版本      | ClientID        | PlatformCode |
//	|-----------|-----------------|--------------|
//	| cn / sg   | ono9krqynydwx5  | IDE_PC       |
//	| solo / solo-sg | en1oxy7wnw8j9n | SOLO_PC  |
//
// 两套路径:
//   现在:  POST <host>/trae/api/v3/oauth/ExchangeToken   —— 必须带 DeviceProof
//   旧版:  POST <host>/cloudide/api/v3/trae/oauth/ExchangeToken —— 不用签名
//
// DeviceProof 是本机 ECDSA P-256 设备私钥对一段固定消息的签名:
//   ["POST", path, clientId, refreshToken, timestamp, nonce].join("\n")
// 分隔符是**换行不是空格** —— 用空格 join 出来的签名会被当成换了台设备,
// 报 20403 Token device not match, 排查方向会完全跑偏。
// 不签直接请求回 20405 Device proof required。
//
// 两个路径都用同一个 ClientID 分叉, 因为那是 OAuth 客户端而不是接口在校验
// (SOLO 的 refresh token 拿桌面端 ClientID 刷会回 10101)。

const (
	traeExchangePath        = "/trae/api/v3/oauth/ExchangeToken"
	traeLegacyExchangePath  = "/cloudide/api/v3/trae/oauth/ExchangeToken"
	traeDesktopClientID     = "ono9krqynydwx5"
	traeSoloClientID        = "en1oxy7wnw8j9n"
	traeDesktopPlatformCode = "IDE_PC"
	traeSoloPlatformCode    = "SOLO_PC"
)

// traeRefreshContractOf 取该版本的客户端标识与平台码。
func traeRefreshContractOf(edition string) (clientID, platformCode string) {
	if edition == "solo" || edition == "solo-sg" {
		return traeSoloClientID, traeSoloPlatformCode
	}
	return traeDesktopClientID, traeDesktopPlatformCode
}

// traeExchangeHost 刷新要挂在凭据自己声明的 host 下, 不能写死网关。
// 没有 host 时才退回区域默认网关。
func traeExchangeHost(a *traeAccount) string {
	if a.Host != "" {
		return a.Host
	}
	return strings.TrimRight(traeRegionGateways[regionOfTrae(a)].pay, "/")
}

// traeSignDeviceProof 用本机设备私钥签 DeviceProof(返回 DER 的 base64)。
func traeSignDeviceProof(path, clientID, refreshToken, privateKeyPEM string) (signature string, timestamp int64, nonce string, err error) {
	priv, perr := traeParsePrivateKey(privateKeyPEM)
	if perr != nil {
		return "", 0, "", perr
	}
	nb := make([]byte, 16)
	if _, rerr := rand.Read(nb); rerr != nil {
		return "", 0, "", rerr
	}
	nonce = hex.EncodeToString(nb)
	timestamp = time.Now().Unix()
	msg := strings.Join([]string{"POST", path, clientID, refreshToken, strconv.FormatInt(timestamp, 10), nonce}, "\n")
	digest := sha256.Sum256([]byte(msg))
	sig, serr := ecdsa.SignASN1(rand.Reader, priv, digest[:])
	if serr != nil {
		return "", 0, "", serr
	}
	return base64.StdEncoding.EncodeToString(sig), timestamp, nonce, nil
}

// traeParsePrivateKey 解析 PEM 私钥, 兼容 PKCS#8 与 SEC1(EC PRIVATE KEY)。
func traeParsePrivateKey(pemText string) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, fmt.Errorf("私钥 PEM 解析失败")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if ec, ok := k.(*ecdsa.PrivateKey); ok {
			return ec, nil
		}
		return nil, fmt.Errorf("私钥不是 ECDSA")
	}
	if k, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	return nil, fmt.Errorf("私钥格式不支持(需 PKCS#8 或 SEC1)")
}

// traePostExchange 发一次换令牌请求并解析出结果。
func traePostExchange(url string, body map[string]any, label string) (accessToken, refreshToken string, expiresAt, refreshExpiresAt int64, err error) {
	raw, merr := json.Marshal(body)
	if merr != nil {
		err = merr
		return
	}
	req, rerr := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if rerr != nil {
		err = rerr
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	client := kit.NewHTTPClient(30 * time.Second)
	resp, derr := client.Do(req)
	if derr != nil {
		err = derr
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("%s HTTP %d %s", label, resp.StatusCode, kit.Truncate(strings.TrimSpace(string(respBody)), 200))
		return
	}
	var parsed struct {
		Result struct {
			Token           string `json:"Token"`
			RefreshToken    string `json:"RefreshToken"`
			TokenExpireAt   any    `json:"TokenExpireAt"`
			RefreshExpireAt any    `json:"RefreshExpireAt"`
		} `json:"Result"`
	}
	if uerr := json.Unmarshal(respBody, &parsed); uerr != nil {
		err = fmt.Errorf("%s 响应解析失败: %w", label, uerr)
		return
	}
	if strings.TrimSpace(parsed.Result.Token) == "" {
		err = fmt.Errorf("%s 没有返回 Token", label)
		return
	}
	return strings.TrimSpace(parsed.Result.Token),
		strings.TrimSpace(parsed.Result.RefreshToken),
		traeTimeToMs(parsed.Result.TokenExpireAt),
		traeTimeToMs(parsed.Result.RefreshExpireAt),
		nil
}

// traeRefreshToken 用 refreshToken 换新 accessToken。
// 优先走带 DeviceProof 的新接口; 没有设备私钥(老安装/CLI 只有裸 JWT)时
// 降级到旧接口, 两条都失败才报错, 并把两条的具体原因都列出来。
func traeRefreshToken(a *traeAccount) error {
	if a.RefreshToken == "" {
		return fmt.Errorf("无 refreshToken(手动粘贴的凭据通常不带, 请用「从本机导入」)")
	}
	clientID, platformCode := traeRefreshContractOf(a.Edition)
	host := traeExchangeHost(a)
	var tried []string

	// --- 新接口: 需要本机设备密钥对来签证明 ---
	if a.PrivateKeyPEM == "" || a.PublicKeyPEM == "" {
		tried = append(tried, traeExchangePath+": 本机缺少设备密钥对, 无法签 DeviceProof")
	} else {
		sig, ts, nonce, serr := traeSignDeviceProof(traeExchangePath, clientID, a.RefreshToken, a.PrivateKeyPEM)
		if serr != nil {
			tried = append(tried, traeExchangePath+": 签名失败 "+serr.Error())
		} else {
			body := map[string]any{
				"ClientID":     clientID,
				"ClientSecret": "",
				"RefreshToken": a.RefreshToken,
				"DeviceInfo": map[string]any{
					"DeviceID":        a.DeviceID,
					"MachineID":       a.MachineID,
					"PlatformCode":    platformCode,
					"DeviceType":      "PC",
					"DevicePublicKey": a.PublicKeyPEM,
					"OSInfo":          traeOSInfo(a.Platform),
					"OSVersion":       traeOSVersion(a.Platform),
				},
				"DeviceProof": map[string]any{
					"Signature": sig,
					"Timestamp": ts,
					"Nonce":     nonce,
				},
			}
			if a.AppVersion != "" {
				body["DeviceInfo"].(map[string]any)["ClientVersion"] = a.AppVersion
				body["IDEVersion"] = a.AppVersion
			}
			tok, ref, exp, refExp, err := traePostExchange(host+traeExchangePath, body, "刷新")
			if err == nil {
				return traeApplyRefresh(a, tok, ref, exp, refExp)
			}
			tried = append(tried, traeExchangePath+": "+err.Error())
		}
	}

	// --- 旧接口兜底: 不验签, 但 ClientID 分叉是同一个 ---
	tok, ref, exp, refExp, err := traePostExchange(host+traeLegacyExchangePath, map[string]any{
		"ClientID":     clientID,
		"ClientSecret": "-",
		"RefreshToken": a.RefreshToken,
		"UserID":       a.UserID,
	}, "刷新(旧接口)")
	if err == nil {
		return traeApplyRefresh(a, tok, ref, exp, refExp)
	}
	tried = append(tried, traeLegacyExchangePath+": "+err.Error())
	return fmt.Errorf("换令牌失败 —— %s", strings.Join(tried, "; "))
}

// traeApplyRefresh 把换回来的令牌写回账号并检查身份一致。
func traeApplyRefresh(a *traeAccount, accessToken, refreshToken string, expiresAt, refreshExpiresAt int64) error {
	if sub := jwtSub(accessToken); sub != "" && a.UserID != "" && sub != a.UserID {
		return fmt.Errorf("刷新返回的账号身份不一致(要 %s, 回 %s)", a.UserID, sub)
	}
	a.AccessToken = accessToken
	if refreshToken != "" {
		a.RefreshToken = refreshToken
	}
	if expiresAt > 0 {
		a.ExpiresAt = expiresAt
	} else if t, ok := jwtExp(accessToken); ok {
		a.ExpiresAt = t
	}
	if refreshExpiresAt > 0 {
		a.RefreshExpireAt = refreshExpiresAt
	}
	a.LastError = ""
	traeSaveAccounts()
	return nil
}

// traeOSInfo / traeOSVersion 刷新 DeviceInfo 里的系统字段。
func traeOSInfo(platform string) string {
	switch platform {
	case "darwin":
		return "mac"
	case "windows", "win32":
		return "windows"
	case "linux":
		return "linux"
	}
	return platform
}

func traeOSVersion(platform string) string {
	name := "Windows"
	switch platform {
	case "darwin":
		name = "macOS"
	case "linux":
		name = "Linux"
	}
	return name
}

// traeSSEExtract 把 llm_utils_chat 的 SSE 帧转成 OpenAI chat.completion.chunk。
//
// 上游用的是自定义 event 流, 不是 OpenAI 格式:
//
//	event:metadata   data:{"model":"","session_id":...}
//	event:timing_cost data:{"name":"llm_raw_chat_v2",...}
//	event:output     data:{"response":"你好", "reasoning_content":null,
//	                       "tool_calls":null, "phase":null}
//	event:error      data:{"code":2001,"message":"function is empty, ..."}
//	event:done       data:{"finish_reason":"stop"}
//
// 这里逐帧转成 chunk: response/reasoning 是增量文本, tool_calls 透传,
// finish_reason 转成结束帧, error 压成 OpenAI 的 error 信封 —— 否则客户端
// 只看到一段空回答, 真实原因(模型配额、内容审核、function 名错)全被吞掉。
func traeSSEExtract(payload []byte) ([]byte, bool, bool) {
	if string(payload) == "[DONE]" {
		return nil, false, true
	}
	var frame map[string]any
	if err := json.Unmarshal(payload, &frame); err != nil {
		// progress_notice 之类发的是裸字符串("Processing_1792387505"),
		// 解析不了就当心跳丢掉, 不是错误。
		return nil, false, false
	}

	// 错误帧: 只有 code/message 而没有 response 内容时才是。
	// (progress_notice 之类发的是裸字符串, 上面已经按"心跳"丢掉。)
	if _, hasResp := frame["response"]; !hasResp {
		if msg, ok := frame["message"].(string); ok && msg != "" {
			// 提示拼在原文后面再整体截断 —— 先截断会把说明切掉, 而说明才是
			// 这个码唯一有用的部分(1005 的 message 干脆是空的)。
			text := fmt.Sprintf("[trae %v] %s", frame["code"], msg)
			if hint := traeErrorHint(frame["code"]); hint != "" {
				text = strings.TrimSpace(strings.TrimSpace(text) + " — " + hint)
			}
			raw, _ := json.Marshal(map[string]any{
				"error": map[string]any{
					"message": kit.Truncate(text, 600),
					"type":    traeErrorType(frame["code"]),
				},
			})
			return raw, true, false
		}
	}

	// 结束帧
	if fr, ok := frame["finish_reason"].(string); ok && fr != "" {
		raw, _ := json.Marshal(map[string]any{
			"id":      "chatcmpl-trae",
			"object":  "chat.completion.chunk",
			"model":   "trae",
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": fr}},
		})
		return raw, true, false
	}

	delta := map[string]any{}
	// 推理增量 —— 与 content 分帧发送, 一帧只带一种, 所以用 if/else 而不是累加。
	if r, ok := frame["reasoning_content"].(string); ok && r != "" {
		delta["reasoning_content"] = r
	} else if resp, ok := frame["response"].(string); ok && resp != "" {
		delta["content"] = resp
	}
	if tc, ok := frame["tool_calls"].([]any); ok && len(tc) > 0 {
		delta["tool_calls"] = tc
	}
	if len(delta) == 0 {
		return nil, false, false
	}
	raw, _ := json.Marshal(map[string]any{
		"id":      "chatcmpl-trae",
		"object":  "chat.completion.chunk",
		"model":   "trae",
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}},
	})
	return raw, true, false
}

// traeErrorType 把上游错误码映射到本项目统一的 error.type 取值。
//
// 上游混用**两套编号**, 不能只按大小判断:
//   - 三位数是 HTTP 状态码(401 鉴权 / 402 配额 / 404 路由 / 429 限流 / 5xx 故障)
//   - 四位数是 Trae 自己的业务码(实测 2001 = function is empty,
//     4001 = the param is invalid), 两个都是**请求侧**问题
//
// 直接写 `case n >= 500` 会把 4001 一并判成"上游故障", 排查方向会整个跑偏
// (明明是自己请求形态不对, 却去查网关)。
func traeErrorType(code any) string {
	var n int
	switch v := code.(type) {
	case float64:
		n = int(v)
	case int:
		n = v
	case string:
		// 上游在不同帧里 code 有时是数字有时是字符串, 两种都要认。
		p, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return "upstream_error"
		}
		n = p
	default:
		return "upstream_error"
	}
	if n >= 1000 { // Trae 业务码
		switch n {
		case 1005: // 订阅档位不够: 等同硬性扣费失败, 重试没用
			return "api_error"
		case 3003: // ModelLoading: 上游在冷加载这个模型, 瞬时故障
			return "upstream_error"
		case 4008: // 配额耗尽
			return "api_error"
		case 4011: // 对该账号这条通道的限流, 不是额度问题 —— 重试通常有效
			return "upstream_error"
		case 4001, 4023: // 请求形态不对: 这类 solution 换了 function/config_name 才有救
			return "invalid_request_error"
		}
		if n >= 5000 {
			return "upstream_error"
		}
		return "invalid_request_error"
	}
	switch {
	case n == 401 || n == 403:
		return "auth_error"
	case n == 402:
		return "api_error" // 上游配额不足(硬性扣费失败)
	case n == 404:
		return "invalid_request_error"
	case n == 429:
		return "upstream_error"
	case n >= 500:
		return "upstream_error"
	case n >= 400:
		return "invalid_request_error"
	}
	return "upstream_error"
}

// traeErrorCodeHint 上游业务码的中文说明。
//
// 对照 dsh upstream/trae/src/solo-bridge.ts 的 TRAE_ERROR_HINTS —— 那是
// 2026-10-01 与 2026-10-12 两次拿真实账号、22 function × 6 model 矩阵实测
// 出来的, 不是照着字面猜的。最关键的两条:
//   - 4011 是**限流**(上游原话 "your requests have exceeded the rate
//     limit"), 不是"这条路由不服务这个模型"; 后者是 4001。早先把它读反了,
//     结果是限流被当成永久失败、真正的路由错配反而被当成限流去重试。
//   - 1005 是订阅档位门槛, 只在 extra.plan 里体现, message 是空的 —— 光看
//     message 会以为上游什么都没说。
var traeErrorCodeHint = map[int]string{
	1005: "该模型需要付费套餐, 当前账号档位不够",
	2001: "请求缺 function, 上游无法定位模型",
	3003: "上游正在冷加载这个模型(ModelLoading), 稍后重试即可",
	4001: "当前 function 下没有这个 config_name —— 模型归属的函数不对",
	4008: "账号的 SOLO 配额已用完",
	4011: "上游对该账号的这条通道限流了(不是额度问题, 稍后重试通常有效)",
	4023: "这条路由上不认识这个模型名",
}

// traeErrorHint 取一个上游错误码的说明; 没收录的码返回空串。
func traeErrorHint(code any) string {
	var n int
	switch v := code.(type) {
	case float64:
		n = int(v)
	case int:
		n = v
	case string:
		p, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return ""
		}
		n = p
	default:
		return ""
	}
	return traeErrorCodeHint[n]
}

// ============ 签到 ============

// traeCheckinStatus 查询签到活动状态(仅 CN 有接口)。
func traeCheckinStatus(a *traeAccount) (map[string]any, error) {
	if regionOfTrae(a) != "cn" {
		return nil, fmt.Errorf("仅 Trae CN 支持签到")
	}
	gw := traeRegionGateways["cn"]
	req, err := http.NewRequest(http.MethodPost, gw.pay+traeCheckinStatusPath, bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, err
	}
	for k, v := range traeUsageHeaders(a) {
		req.Header.Set(k, v)
	}
	client := kit.NewHTTPClient(20 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var parsed struct {
		CheckedIn    bool    `json:"checked_in"`
		Credits      float64 `json:"credits"`
		Enable       bool    `json:"enable"`
		DidCheckedIn bool    `json:"did_checked_in"`
		ExtraCredits float64 `json:"extra_credits"`
		Code         int     `json:"code"`
		Message      string  `json:"message"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("签到状态解析失败: %w", err)
	}
	return map[string]any{
		"checkedIn":    parsed.CheckedIn,
		"credits":      parsed.Credits,
		"enabled":      parsed.Enable,
		"didCheckedIn": parsed.DidCheckedIn,
		"extraCredits": parsed.ExtraCredits,
		"code":         parsed.Code,
		"message":      parsed.Message,
	}, nil
}

// traeRunCheckin 领取当日签到积分。上游按北京天幂等(重复调用 code=0 但额度不变)。
func traeRunCheckin(a *traeAccount) (checkinResult, error) {
	if regionOfTrae(a) != "cn" {
		return checkinResult{}, fmt.Errorf("仅 Trae CN 支持签到")
	}
	gw := traeRegionGateways["cn"]
	req, err := http.NewRequest(http.MethodPost, gw.pay+traeCheckinClaimPath, bytes.NewReader([]byte("{}")))
	if err != nil {
		return checkinResult{}, err
	}
	for k, v := range traeUsageHeaders(a) {
		req.Header.Set(k, v)
	}
	client := kit.NewHTTPClient(25 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return checkinResult{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var parsed struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Claimed bool   `json:"claimed"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return checkinResult{}, fmt.Errorf("签到响应解析失败: %w (HTTP %d %s)", err, resp.StatusCode, kit.Truncate(strings.TrimSpace(string(body)), 200))
	}
	if parsed.Code == 0 && parsed.Claimed {
		msg := "签到成功"
		if amt := traeCheckinReward(body); amt > 0 {
			msg = fmt.Sprintf("签到成功 +%.0f 积分", amt)
		}
		return checkinResult{Status: "success", Message: msg, Reward: traeCheckinReward(body)}, nil
	}
	if parsed.Code == 9095 || parsed.Claimed == false {
		// 设备已领 / 未领取成功: 按已签或活动未开中性处理
		if parsed.Message != "" {
			return checkinResult{Status: "skipped", Message: "今日已签或设备已使用: " + parsed.Message}, nil
		}
		return checkinResult{Status: "already", Message: "今日已签到"}, nil
	}
	return checkinResult{}, fmt.Errorf("签到失败(code=%d): %s", parsed.Code, parsed.Message)
}

// traeCheckinReward 从签到响应里尽量取出奖励积分(字段形态不确定, 防御解析)。
func traeCheckinReward(body []byte) float64 {
	var obj map[string]any
	if json.Unmarshal(body, &obj) != nil {
		return 0
	}
	for _, key := range []string{"credit", "credits", "reward", "amount", "benefit"} {
		if v, ok := obj[key]; ok {
			switch t := v.(type) {
			case float64:
				return t
			case map[string]any:
				if a, ok := t["amount"].(float64); ok {
					return a
				}
				if a, ok := t["credit"].(float64); ok {
					return a
				}
			}
		}
	}
	return 0
}

// traeUsageSummary 上游 usage_summary: 总额/已用/消耗比。
type traeUsageSummary struct {
	TotalAmount      float64 `json:"total_amount"`
	ConsumedAmount   float64 `json:"consumed_amount"`
	ConsumptionRatio float64 `json:"consumption_ratio"`
}

// traeEntPack 上游 user_entitlement_pack_list 里的一条权益包。
type traeEntPack struct {
	DisplayDesc string `json:"display_desc"`
	GroupName   string `json:"group_name"`
	Status      int    `json:"status"`
	Usage       struct {
		CreditsAmount float64 `json:"credits_amount"`
	} `json:"usage"`
	EntitlementBaseInfo struct {
		Quota struct {
			CreditsLimit float64 `json:"credits_limit"`
		} `json:"quota"`
		ProductExtra struct {
			PackageExtra struct {
				Quota struct {
					CreditsLimit float64 `json:"credits_limit"`
				} `json:"quota"`
			} `json:"package_extra"`
		} `json:"product_extra"`
	} `json:"entitlement_base_info"`
}

// traeCreditsLimitOf 取该权益包的额度上限。上游把 credits_limit 放在
// entitlement_base_info.quota 或 product_extra.package_extra.quota 两处,
// 且只有带额度的付费/赠送包才有 —— 免费包完全没有该字段。
func (p traeEntPack) traeCreditsLimitOf() float64 {
	if p.EntitlementBaseInfo.Quota.CreditsLimit > 0 {
		return p.EntitlementBaseInfo.Quota.CreditsLimit
	}
	return p.EntitlementBaseInfo.ProductExtra.PackageExtra.Quota.CreditsLimit
}

// traeCreditsOf 把上游用量响应折算成与 WorkBuddy/Qoder 同形的余额块,
// 让前端仪表盘用同一张卡片渲染三个来源。
//
// 上游给的是"总额 + 已用"而非"剩余", 所以 remain = total - used。
// 总额以 usage_summary.total_amount 为准(它等于所有权益包额度之和),
// 已用以 usage_summary.consumed_amount 为准; 两者都缺时退回权益包求和。
func traeCreditsOf(usage map[string]any) *wbCredits {
	var parsed struct {
		UsageSummary traeUsageSummary `json:"usage_summary"`
		Packs        []traeEntPack    `json:"user_entitlement_pack_list"`
	}
	raw, _ := json.Marshal(usage)
	if json.Unmarshal(raw, &parsed) != nil {
		return nil
	}
	out := &wbCredits{Unit: "credits"}
	sumLimit, sumUsed := 0.0, 0.0
	for _, p := range parsed.Packs {
		sumLimit += p.traeCreditsLimitOf()
		sumUsed += p.Usage.CreditsAmount
	}
	out.Total = parsed.UsageSummary.TotalAmount
	out.Used = parsed.UsageSummary.ConsumedAmount
	if out.Total <= 0 {
		out.Total = sumLimit
	}
	if out.Used <= 0 {
		out.Used = sumUsed
	}
	// 上游偶尔只给总额不给已用(或反之), 兜底保证百分比不越界。
	if out.Total < out.Used {
		out.Total = out.Used
	}
	out.Remain = out.Total - out.Used
	if out.Remain < 0 {
		out.Remain = 0
	}
	out.PackageCount = len(parsed.Packs)
	// 套餐名取"带额度的那一档"(赠送/付费包), 不要取列表第一项 ——
	// 第一项往往是额度为 0 的"免费"包, 名字没有信息量。
	for _, p := range parsed.Packs {
		if p.traeCreditsLimitOf() > 0 {
			out.PlanName = p.DisplayDesc
			break
		}
	}
	if out.PlanName == "" && len(parsed.Packs) > 0 {
		out.PlanName = parsed.Packs[0].DisplayDesc
	}
	return out
}

// traeFetchUsage 查询积分用量(端点形态随上游, 这里原样返回解析后的对象供后台展示)。
func traeFetchUsage(a *traeAccount) (map[string]any, error) {
	gw := traeRegionGateways[regionOfTrae(a)]
	req, err := http.NewRequest(http.MethodPost, gw.pay+traeUsagePath, bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, err
	}
	for k, v := range traeUsageHeaders(a) {
		req.Header.Set(k, v)
	}
	client := kit.NewHTTPClient(25 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("用量接口 HTTP %d %s", resp.StatusCode, kit.Truncate(strings.TrimSpace(string(body)), 200))
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("用量响应解析失败: %w", err)
	}
	return out, nil
}

// ============ 后台载荷 ============

func traeStatusPayload() map[string]any {
	cfg := getTraeConfig()
	accounts := traeLoadAccounts()
	list := make([]map[string]any, 0, len(accounts))
	for _, a := range accounts {
		expired := a.ExpiresAt > 0 && time.Now().UnixMilli() >= a.ExpiresAt
		// 能自动续期的条件: 有 refreshToken, 且刷新令牌本身还没过期。
		// 设备密钥对决定走新接口还是旧接口兜底, 没有也能续, 只是降级。
		renewable := a.RefreshToken != "" && (a.RefreshExpireAt == 0 || time.Now().UnixMilli() < a.RefreshExpireAt)
		list = append(list, map[string]any{
			"id":            a.ID,
			"region":        a.Region,
			"edition":       a.Edition,
			"source":        a.Source,
			"userId":        a.UserID,
			"userName":      a.UserName,
			"deviceId":      maskSecret(a.DeviceID),
			"hasToken":      a.AccessToken != "",
			"hasRefresh":    a.RefreshToken != "",
			"hasDeviceKey":  a.PrivateKeyPEM != "" && a.PublicKeyPEM != "",
			"renewable":     renewable,
			"expiresAt":     a.ExpiresAt,
			"expiresAtText": msToText(a.ExpiresAt),
			"expired":       expired,
			"appVersion":    a.AppVersion,
			"lastError":     a.LastError,
		})
	}
	models := traeModelList()
	return map[string]any{
		"id":          "trae",
		"displayName": "Trae",
		"region":      "cn",
		"enabled":     cfg.Enabled,
		"freeOnly":    cfg.FreeOnly,
		"maxRetries":  cfg.MaxRetries,
		"importable":  true, // 支持「从本机导入」(见 trae_local.go)
		"accounts":    list,
		"models":      models,
		"modelCount":  len(models),
	}
}

func traeCreditsPayload() map[string]any {
	out := map[string]any{"id": "trae", "displayName": "Trae", "region": "cn"}
	acc := traePickAccount()
	if acc == nil {
		out["error"] = "无可用 Trae 账号, 请在 /admin 点「从本机导入账号」"
		return out
	}
	out["account"] = maskSecret(acc.UserID)
	if usage, err := traeFetchUsage(acc); err != nil {
		out["creditsError"] = err.Error()
	} else {
		out["usage"] = usage
		// 折算成与 WB/Qoder 同形的余额块, 否则前端仪表盘取不到 p.credits,
		// 只能落到"余额查询失败"兜底文案(接口其实 200 正常)。
		if c := traeCreditsOf(usage); c != nil {
			out["credits"] = c
		}
	}
	if st, err := traeCheckinStatus(acc); err != nil {
		out["checkinError"] = err.Error()
	} else {
		out["checkin"] = map[string]any{
			"active":         st["enabled"],
			"todayCheckedIn": st["checkedIn"],
			"deviceUsed":     st["didCheckedIn"],
		}
	}
	return out
}

func traeRunCheckinForAdmin() (checkinResult, error) {
	acc := traePickAccount()
	if acc == nil {
		return checkinResult{}, fmt.Errorf("无可用 Trae 账号")
	}
	return traeRunCheckin(acc)
}

// traeCredentialInput 一份 Trae 凭据。
// 两条来源: 后台「从本机导入」自动填全(含设备密钥对), 或用户手动粘贴。
type traeCredentialInput struct {
	AccessToken     string `json:"accessToken"`
	RefreshToken    string `json:"refreshToken"`
	UserID          string `json:"userId"`
	AccountName     string `json:"accountName"`
	DeviceID        string `json:"deviceId"`
	MachineID       string `json:"machineId"`
	Edition         string `json:"edition"`
	AppVersion      string `json:"appVersion"`
	BuildVersion    string `json:"buildVersion"`
	Platform        string `json:"platform"`
	Region          string `json:"region"`
	Host            string `json:"host"`
	UserRegion      string `json:"userRegion"`
	PrivateKeyPEM   string `json:"privateKeyPEM"`
	PublicKeyPEM    string `json:"publicKeyPEM"`
	Source          string `json:"source"`
	ExpiresAt       int64  `json:"expiresAt"`
	RefreshExpireAt int64  `json:"refreshExpiresAt"`
}

// traeSyncModelsSafe 后台"同步模型"封装(与 qoderSyncModels 同形)。
func traeSyncModelsSafe() (int, error) { return traeSyncModels() }

// traeBoot 启动时若已有账号则同步模型目录(后台协程)。
func traeBoot() {
	if len(traeLoadAccounts()) > 0 {
		go func() {
			time.Sleep(3 * time.Second)
			if n, err := traeSyncModels(); err != nil {
				log.Printf("  provider: trae models sync failed: %v", err)
			} else {
				log.Printf("  provider: trae %d models ready", n)
			}
		}()
	}
}
