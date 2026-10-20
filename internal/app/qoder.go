package app

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"FreeModelRelay/internal/kit"
)

// ============================================================================
// Qoder 上游 (仅 china 区: openapi.qoder.com.cn / gateway.qoder.com.cn)
//
// 凭据 = Qoder 个人访问令牌 (pat-...), 换取 jobToken 后所有业务请求带 Cosy 签名:
//   Authorization: Bearer COSY.<base64(payload)>.<md5(sigInput)>
//   sigInput = payloadB64 \n cosyKey \n ts \n body \n sigPath
// 请求体按 Qoder 自定义字符表编码(Encode=1), 响应为明文 JSON。
// ============================================================================

const (
	qoderIdeVersion        = "1.1.47"
	qoderClientType        = "5"  // chat / quota / userinfo 用 IDE 标识
	qoderCheckinClientType = "10" // 签到(活动/campaigns)接口按 desktop 标识过滤, IDE 标识会返回空列表
	qoderMachineType       = "5"
	qoderDataPolicy        = "disagree"
	qoderLoginVer          = "v2"

	qoderBaseURL    = "https://gateway.qoder.com.cn/"
	qoderOpenAPIURL = "https://openapi.qoder.com.cn"

	qoderChatPath      = "algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_common&Encode=1"
	qoderModelListPath = "algo/api/v2/model/list?Encode=1"
	qoderExchangePath  = "/api/v1/jobToken/exchange"
	qoderUserInfoPath  = "/api/v1/userinfo"

	// Qoder 自定义 base64 字符表(与官方客户端一致, 用于 Encode=1 请求体)
	qoderCustomAlphabet = "_doRTgHZBKcGVjlvpC,@aFSx#DPuNJme&i*MzLOEn)sUrthbf%Y^w.(kIQyXqWA!"

	qoderRSAPublicKey = `-----BEGIN PUBLIC KEY-----
MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDA8iMH5c02LilrsERw9t6Pv5Nc
4k6Pz1EaDicBMpdpxKduSZu5OANqUq8er4GM95omAGIOPOh+Nx0spthYA2BqGz+l
6HRkPJ7S236FZz73In/KVuLnwI8JJ2CbuJap8kvheCCZpmAWpb/cPx/3Vr/J6I17
XcW+ML9FoCI6AOvOzwIDAQAB
-----END PUBLIC KEY-----`
)

var qoderB64Std = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

type qoderAccount struct {
	ID             string `json:"id"`
	Region         string `json:"region"`
	PAT            string `json:"pat"`
	JobToken       string `json:"-"`
	RefreshToken   string `json:"-"`
	TokenExpiresAt int64  `json:"-"`
	UserID         string `json:"userId"`
	Name           string `json:"name"`
	Email          string `json:"email"`
	MachineID      string `json:"machineId"`
	SavedAt        int64  `json:"savedAt"`
	LastError      string `json:"-"`
}

type qoderProviderConfig struct {
	Enabled    bool     `json:"enabled"`
	Proxies    []string `json:"proxies"`
	FreeOnly   bool     `json:"freeOnly"`
	MaxRetries int      `json:"maxRetries"`
}

func defaultQoderConfig() *qoderProviderConfig {
	return &qoderProviderConfig{Enabled: true, MaxRetries: 2}
}

var (
	qoderCfg      = loadQoderConfig()
	qoderCfgMu    sync.Mutex
	qoderAccounts []*qoderAccount
	qoderAccMu    sync.RWMutex
	qoderIdx      int
	qoderModels   = map[string]qoderModelInfo{}
	qoderModelsMu sync.RWMutex
)

type qoderModelInfo struct {
	Key         string // 上游模型 key (如 auto / qfmodel)
	DisplayName string
	Context     int
	Output      int
	PriceFactor float64
	Reasoning   bool
	IsDefault   bool
}

func qoderConfigPath() string { return kit.ResolveDataPath(".qoder-config.json") }

func loadQoderConfig() *qoderProviderConfig {
	cfg := defaultQoderConfig()
	data, err := os.ReadFile(qoderConfigPath())
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

func saveQoderConfig() {
	data, err := json.MarshalIndent(qoderCfg, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(qoderConfigPath(), data, 0600)
}

func getQoderConfig() *qoderProviderConfig {
	qoderCfgMu.Lock()
	defer qoderCfgMu.Unlock()
	return &qoderProviderConfig{
		Enabled: qoderCfg.Enabled, Proxies: append([]string{}, qoderCfg.Proxies...),
		FreeOnly: qoderCfg.FreeOnly, MaxRetries: qoderCfg.MaxRetries,
	}
}

func setQoderConfig(c *qoderProviderConfig) {
	qoderCfgMu.Lock()
	qoderCfg = c
	qoderCfgMu.Unlock()
	saveQoderConfig()
}

// ============ 账号 ============

func qoderAccountsFilePath() string { return kit.ResolveDataPath(".qoder-accounts.json") }

// qoderLoadAccountsLocked 的实现体, 调用方必须已持有 qoderAccMu。
// 注意: 该函数不得在内部再次加锁, 否则同一把非可重入 sync.Mutex 会自锁死。
func qoderLoadAccountsLocked() []*qoderAccount {
	if qoderAccounts != nil {
		return qoderAccounts
	}
	if data, err := os.ReadFile(qoderAccountsFilePath()); err == nil {
		var list []*qoderAccount
		if json.Unmarshal(data, &list) == nil {
			qoderAccounts = list
		}
	}
	if qoderAccounts == nil {
		qoderAccounts = []*qoderAccount{}
	}
	for _, a := range qoderAccounts {
		if a.MachineID == "" {
			a.MachineID = qoderLocalMachineID()
		}
		if a.Region == "" {
			a.Region = "china"
		}
	}
	return qoderAccounts
}

// qoderLoadAccounts 带锁包装: 加载账号列表(首次为空时从磁盘读取)。
func qoderLoadAccounts() []*qoderAccount {
	qoderAccMu.Lock()
	defer qoderAccMu.Unlock()
	return qoderLoadAccountsLocked()
}

// qoderSaveAccountsLocked 的实现体, 调用方必须已持有 qoderAccMu。
func qoderSaveAccountsLocked() {
	data, err := json.MarshalIndent(qoderAccounts, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(qoderAccountsFilePath(), data, 0600)
}

// qoderSaveAccounts 带锁包装: 把内存中的账号列表落盘。
func qoderSaveAccounts() {
	qoderAccMu.Lock()
	defer qoderAccMu.Unlock()
	qoderSaveAccountsLocked()
}

// qoderLocalMachineID 复用本机 Qoder IDE 的 machine_id(与官方客户端一致最稳)。
func qoderLocalMachineID() string {
	home, _ := os.UserHomeDir()
	for _, p := range []string{
		filepath.Join(home, ".qoder-cn", ".auth", "machine_id"),
		filepath.Join(home, ".qoder", ".auth", "machine_id"),
	} {
		if data, err := os.ReadFile(p); err == nil {
			if id := strings.TrimSpace(string(data)); id != "" {
				return id
			}
		}
	}
	// 持久化一份自生成 id
	path := kit.ResolveDataPath(".qoder-machine-id")
	if data, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(data)); id != "" {
			return id
		}
	}
	b := make([]byte, 16)
	rand.Read(b)
	id := fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	os.WriteFile(path, []byte(id), 0600)
	return id
}

func qoderPickAccount() *qoderAccount {
	list := qoderLoadAccounts()
	if len(list) == 0 {
		return nil
	}
	qoderAccMu.Lock()
	idx := qoderIdx % len(list)
	qoderIdx++
	qoderAccMu.Unlock()
	return list[idx]
}

func qoderFindAccount(id string) *qoderAccount {
	for _, a := range qoderLoadAccounts() {
		if a.ID == id {
			return a
		}
	}
	return nil
}

// ============ 令牌交换 ============

// qoderExchange PAT -> jobToken(+ jobRefresh)。region 目前仅 china 可用。
func qoderExchange(a *qoderAccount) error {
	url := qoderOpenAPIURL + qoderExchangePath
	payload, _ := json.Marshal(map[string]any{"personal_token": a.PAT})
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json")
	req.Header.Set("accept-encoding", "identity")
	req.Header.Set("user-agent", "qoder/"+qoderIdeVersion)
	req.Header.Set("cosy-version", "1.0.1")
	req.Header.Set("cosy-clienttype", qoderClientType)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		a.LastError = err.Error()
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		a.LastError = fmt.Sprintf("exchange HTTP %d %s", resp.StatusCode, kit.Truncate(strings.TrimSpace(string(body)), 200))
		return fmt.Errorf("%s", a.LastError)
	}
	var out struct {
		Token            string `json:"token"`
		RefreshToken     string `json:"refresh_token"`
		ExpiresAt        string `json:"expires_at"`
		RefreshExpiresAt string `json:"refresh_token_expires_at"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		a.LastError = "exchange 响应解析失败"
		return err
	}
	if out.Token == "" {
		a.LastError = "exchange 未返回 token"
		return fmt.Errorf("%s", a.LastError)
	}
	a.JobToken = out.Token
	a.RefreshToken = out.RefreshToken
	if t, err := time.Parse(time.RFC3339, out.ExpiresAt); err == nil {
		a.TokenExpiresAt = t.UnixMilli()
	} else {
		a.TokenExpiresAt = time.Now().Add(23 * time.Hour).UnixMilli()
	}
	a.LastError = ""
	return nil
}

// qoderEnsureToken 保证 jobToken 可用(过期前 2 分钟自动换新)。
func qoderEnsureToken(a *qoderAccount) error {
	if a.JobToken != "" && time.Now().UnixMilli() < a.TokenExpiresAt-120000 {
		return nil
	}
	return qoderExchange(a)
}

// qoderFetchUserInfo 取账号身份(userinfo), 用于后台展示与 Cosy 的 uid 字段。
func qoderFetchUserInfo(a *qoderAccount) error {
	if err := qoderEnsureToken(a); err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodGet, qoderOpenAPIURL+qoderUserInfoPath, nil)
	if err != nil {
		return err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("accept-encoding", "identity")
	req.Header.Set("authorization", "Bearer "+a.JobToken)
	req.Header.Set("user-agent", "qoder/"+qoderIdeVersion)
	req.Header.Set("cosy-version", "1.0.1")
	req.Header.Set("cosy-clienttype", qoderClientType)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("userinfo HTTP %d %s", resp.StatusCode, kit.Truncate(strings.TrimSpace(string(body)), 160))
	}
	var out struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return err
	}
	a.UserID = out.ID
	a.Name = out.Name
	a.Email = out.Email
	if a.Email == "" {
		a.Email = out.Name
	}
	return nil
}

// ============ Cosy 签名 ============

func qoderEncodeBody(plain []byte) []byte {
	std := base64.StdEncoding.EncodeToString(plain)
	n := len(std)
	a := n / 3
	rearranged := std[n-a:] + std[a:n-a] + std[:a]
	out := make([]byte, 0, n)
	custom := []byte(qoderCustomAlphabet)
	for i := 0; i < n; i++ {
		c := rearranged[i]
		if idx := strings.IndexByte(qoderB64Std, c); idx >= 0 && idx < len(custom) {
			out = append(out, custom[idx])
			continue
		}
		if c == '=' {
			out = append(out, '$')
			continue
		}
		out = append(out, 0)
	}
	return out
}

func qoderSigPath(rawURL string) string {
	u, err := strconv.Unquote(`"` + rawURL + `"`)
	if err == nil {
		rawURL = u
	}
	idx := strings.Index(rawURL, "://")
	rest := rawURL
	if idx >= 0 {
		rest = rawURL[idx+3:]
	}
	slash := strings.Index(rest, "/")
	if slash < 0 {
		return "/"
	}
	p := rest[slash:]
	if q := strings.Index(p, "?"); q >= 0 {
		p = p[:q]
	}
	if strings.HasPrefix(p, "/algo") {
		p = p[5:]
	}
	return p
}

var qoderRSAPubOnce sync.Once
var qoderRSAPub *rsa.PublicKey

func qoderParseRSAPub() (*rsa.PublicKey, error) {
	var parseErr error
	qoderRSAPubOnce.Do(func() {
		block, err := decodePEM(qoderRSAPublicKey)
		if err != nil {
			parseErr = err
			return
		}
		key, err := x509.ParsePKIXPublicKey(block)
		if err != nil {
			parseErr = err
			return
		}
		rk, ok := key.(*rsa.PublicKey)
		if !ok {
			parseErr = fmt.Errorf("公钥类型异常")
			return
		}
		qoderRSAPub = rk
	})
	return qoderRSAPub, parseErr
}

func decodePEM(pem string) ([]byte, error) {
	const header = "-----BEGIN PUBLIC KEY-----"
	const footer = "-----END PUBLIC KEY-----"
	s := strings.TrimSpace(pem)
	s = strings.TrimPrefix(s, header)
	s = strings.TrimSuffix(s, footer)
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.TrimSpace(s)
	return base64.StdEncoding.DecodeString(s)
}

// qoderAuthHeaders 生成 Cosy 认证请求头。body 为已编码的请求体(nil 表示 GET)。
func qoderAuthHeaders(body []byte, rawURL string, a *qoderAccount) (http.Header, error) {
	// 1) 16 字节 ASCII AES 密钥
	rb := make([]byte, 12)
	if _, err := rand.Read(rb); err != nil {
		return nil, err
	}
	aesKey := hex.EncodeToString(rb)[:16]

	// 2) AES-128-CBC(key==iv) 加密用户信息
	userInfo, _ := json.Marshal(map[string]any{
		"uid":                  a.UserID,
		"security_oauth_token": a.JobToken,
		"name":                 a.Name,
		"aid":                  "",
		"email":                a.Email,
	})
	block, err := aes.NewCipher([]byte(aesKey))
	if err != nil {
		return nil, err
	}
	padded := pkcs7Pad(userInfo, aes.BlockSize)
	encInfo := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, []byte(aesKey)).CryptBlocks(encInfo, padded)
	infoB64 := base64.StdEncoding.EncodeToString(encInfo)

	// 3) RSA 加密 aesKey
	pub, err := qoderParseRSAPub()
	if err != nil {
		return nil, err
	}
	encKey, err := rsa.EncryptPKCS1v15(rand.Reader, pub, []byte(aesKey))
	if err != nil {
		return nil, err
	}
	cosyKey := base64.StdEncoding.EncodeToString(encKey)

	// 4) payload + 签名
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	payload, _ := json.Marshal(map[string]any{
		"version":     "v1",
		"requestId":   newUUID(),
		"info":        infoB64,
		"cosyVersion": qoderIdeVersion,
		"ideVersion":  "",
	})
	payloadB64 := base64.StdEncoding.EncodeToString(payload)
	sp := qoderSigPath(rawURL)
	sigInput := payloadB64 + "\n" + cosyKey + "\n" + ts + "\n" + string(body) + "\n" + sp
	sum := md5.Sum([]byte(sigInput))

	bodyHash := md5.Sum(body)
	bodyLen := "0"
	if len(body) > 0 {
		bodyLen = strconv.Itoa(len(body))
	}

	h := http.Header{}
	h.Set("Authorization", "Bearer COSY."+payloadB64+"."+hex.EncodeToString(sum[:]))
	h.Set("Cosy-Key", cosyKey)
	h.Set("Cosy-User", a.UserID)
	h.Set("Cosy-Date", ts)
	h.Set("Cosy-Version", qoderIdeVersion)
	h.Set("Cosy-Machineid", a.MachineID)
	h.Set("Cosy-Machinetoken", a.MachineID)
	h.Set("Cosy-Machinetype", qoderMachineType)
	h.Set("Cosy-Machineos", "x86_64_windows")
	h.Set("Cosy-Clienttype", qoderClientType)
	h.Set("Cosy-Clientip", "127.0.0.1")
	h.Set("Cosy-Bodyhash", hex.EncodeToString(bodyHash[:]))
	h.Set("Cosy-Bodylength", bodyLen)
	h.Set("Cosy-Sigpath", sp)
	h.Set("Cosy-Data-Policy", qoderDataPolicy)
	h.Set("Cosy-Organization-Id", "")
	h.Set("Cosy-Organization-Tags", "")
	h.Set("Login-Version", qoderLoginVer)
	h.Set("X-Request-Id", newUUID())
	return h, nil
}

func pkcs7Pad(b []byte, size int) []byte {
	n := size - len(b)%size
	out := make([]byte, len(b)+n)
	copy(out, b)
	for i := len(b); i < len(out); i++ {
		out[i] = byte(n)
	}
	return out
}

func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ============ 模型目录 ============

func qoderSyncModels() (int, error) {
	acc := qoderPickAccount()
	if acc == nil {
		return 0, fmt.Errorf("无可用 Qoder 账号")
	}
	if err := qoderFetchUserInfo(acc); err != nil {
		return 0, err
	}
	url := qoderBaseURL + qoderModelListPath
	headers, err := qoderAuthHeaders(nil, url, acc)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header = headers
	req.Header.Set("accept", "application/json")
	req.Header.Set("accept-encoding", "identity")

	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("model list HTTP %d %s", resp.StatusCode, kit.Truncate(strings.TrimSpace(string(body)), 200))
	}
	var parsed struct {
		Chat []struct {
			Key            string  `json:"key"`
			DisplayName    string  `json:"display_name"`
			Enable         bool    `json:"enable"`
			IsDefault      bool    `json:"is_default"`
			IsReasoning    bool    `json:"is_reasoning"`
			PriceFactor    float64 `json:"price_factor"`
			MaxInputTokens int     `json:"max_input_tokens"`
		} `json:"chat"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, err
	}
	cfg := getQoderConfig()
	next := map[string]qoderModelInfo{}
	for _, m := range parsed.Chat {
		if m.Key == "" || !m.Enable {
			continue
		}
		// auto / default 等占位别名不同步入目录(与 WB 同一套规则)。
		if providerIsAliasModel(m.Key) {
			continue
		}
		if cfg.FreeOnly && m.PriceFactor != 0 && !m.IsDefault {
			continue
		}
		next[m.Key] = qoderModelInfo{
			Key: m.Key, DisplayName: m.DisplayName, Context: m.MaxInputTokens,
			Output: 0, PriceFactor: m.PriceFactor, Reasoning: m.IsReasoning, IsDefault: m.IsDefault,
		}
	}
	if len(next) == 0 {
		return 0, fmt.Errorf("上游未返回可用模型")
	}
	qoderModelsMu.Lock()
	qoderModels = next
	qoderModelsMu.Unlock()
	log.Printf("  qoder: %d models synced (account %s)", len(next), maskSecret(acc.Email))
	return len(next), nil
}

func qoderModelList() []map[string]any {
	qoderModelsMu.RLock()
	keys := make([]string, 0, len(qoderModels))
	for k := range qoderModels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	list := make([]qoderModelInfo, 0, len(keys))
	for _, k := range keys {
		list = append(list, qoderModels[k])
	}
	qoderModelsMu.RUnlock()
	// 与 wbModelList 同一套消歧规则: 裸 id 有歧义时对外只能用 qoder/<id>。
	dup := providerPublicIDs()
	out := make([]map[string]any, 0, len(list))
	for _, m := range list {
		publicID := m.Key
		if dup[m.Key] {
			publicID = "qoder/" + m.Key
		}
		image, video := applyModalityProbe(modalityKey("qoder", m.Key), nil, nil)
		out = append(out, map[string]any{
			"id":          publicID,
			"baseId":      m.Key,
			"name":        m.DisplayName,
			"context":     m.Context,
			"output":      m.Output,
			"price":       m.PriceFactor,
			"priceFactor": m.PriceFactor,
			"reason":      m.Reasoning,
			// Qoder 的模型目录不含模态字段, 只能给 null(未知),
			// 由后台"探测"按钮实调补齐, 不能默认成不支持。
			"image": image,
			"video": video,
		})
	}
	return out
}

// qoderModelSnapshot 取当前目录快照, 供 providerPublicIDs 等聚合函数使用。
func qoderModelSnapshot() []qoderModelInfo {
	qoderModelsMu.RLock()
	defer qoderModelsMu.RUnlock()
	out := make([]qoderModelInfo, 0, len(qoderModels))
	for _, m := range qoderModels {
		out = append(out, m)
	}
	return out
}

// qoderResolveDecorated 按完整装饰串精确解析(/v1/models 暴露的原样 id),
// 与 wbResolveDecorated 同理: 同名显示名靠倍率段区分归属; 同时接受带分组
// 前缀的当前格式与早期无分组格式, 保证旧配置继续可用。
func qoderResolveDecorated(id string) (qoderModelInfo, bool) {
	qoderModelsMu.RLock()
	defer qoderModelsMu.RUnlock()
	for _, m := range qoderModels {
		free := m.PriceFactor == 0
		if providerDecorateID("qoder", m.Key, m.DisplayName, m.PriceFactor, free) == id {
			return m, true
		}
		if providerDecorateID("", m.Key, m.DisplayName, m.PriceFactor, false) == id {
			return m, true
		}
	}
	return qoderModelInfo{}, false
}

func qoderResolveModel(id string) (qoderModelInfo, bool) {
	qoderModelsMu.RLock()
	defer qoderModelsMu.RUnlock()
	// 内部路由 key 优先, 上游可读显示名兜底 —— /v1/models 暴露的首段是显示名
	// (如 "GLM-5.3 · x0.8" 剥掉装饰后剩 GLM-5.3), 旧配置的干净 key 继续可用。
	if m, ok := qoderModels[id]; ok {
		return m, true
	}
	for _, m := range qoderModels {
		if strings.TrimSpace(m.DisplayName) == id {
			return m, true
		}
	}
	return qoderModelInfo{}, false
}

// ============ chat ============

// qoderBuildBody 上游请求体: 强制 stream, 透传对话相关字段。
func qoderBuildBody(params map[string]any, m qoderModelInfo) map[string]any {
	body := map[string]any{"model": m.Key, "stream": true}
	for _, k := range []string{"messages", "tools", "tool_choice", "temperature", "top_p", "max_tokens", "stop", "response_format"} {
		if v, ok := params[k]; ok && v != nil {
			body[k] = v
		}
	}
	if _, ok := body["messages"]; !ok {
		body["messages"] = []any{}
	}
	// 角色归一: 上游只认 system/assistant/user/tool/function, developer 直接 400
	if msgs, ok := body["messages"].([]any); ok {
		body["messages"] = cleanMessages(msgs)
	}
	return body
}

func qoderCallChat(params map[string]any, m qoderModelInfo, a *qoderAccount) (*http.Response, error) {
	if err := qoderEnsureToken(a); err != nil {
		return nil, err
	}
	url := qoderBaseURL + qoderChatPath
	raw, err := json.Marshal(qoderBuildBody(params, m))
	if err != nil {
		return nil, err
	}
	encoded := qoderEncodeBody(raw)

	cfg := getQoderConfig()
	attempts := cfg.MaxRetries
	if attempts <= 0 {
		attempts = 2
	}
	var lastErr error
	for i := 0; i <= attempts; i++ {
		headers, err := qoderAuthHeaders(encoded, url, a)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(encoded))
		if err != nil {
			return nil, err
		}
		req.Header = headers
		req.Header.Set("content-type", "application/json")
		req.Header.Set("accept", "text/event-stream")
		req.Header.Set("accept-encoding", "identity")

		client := &http.Client{Timeout: 10 * time.Minute}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
		} else if resp.StatusCode == 200 {
			return resp, nil
		} else {
			errBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d %s", resp.StatusCode, kit.Truncate(strings.TrimSpace(string(errBody)), 300))
			if resp.StatusCode == 401 {
				a.JobToken = ""
				if err := qoderExchange(a); err != nil {
					return nil, err
				}
			}
		}
		if i < attempts {
			time.Sleep(kit.WithRetryJitter(time.Duration(500+400*i) * time.Millisecond))
		}
	}
	return nil, lastErr
}

// qoderAddAccount 新增/更新账号(按 PAT 去重), 并立即验证。
func qoderAddAccount(pat string, region string) (*qoderAccount, error) {
	pat = strings.TrimSpace(pat)
	if pat == "" {
		return nil, fmt.Errorf("PAT 不能为空")
	}
	if region == "" {
		region = "china"
	}
	if region != "china" {
		return nil, fmt.Errorf("目前仅支持 china 区 Qoder 令牌")
	}
	list := qoderLoadAccounts()
	var acc *qoderAccount
	for _, a := range list {
		if a.PAT == pat {
			acc = a
			break
		}
	}
	if acc == nil {
		b := make([]byte, 6)
		rand.Read(b)
		acc = &qoderAccount{
			ID:        "qoder_" + hex.EncodeToString(b),
			Region:    region,
			PAT:       pat,
			MachineID: qoderLocalMachineID(),
			SavedAt:   time.Now().UnixMilli(),
		}
		qoderAccMu.Lock()
		qoderAccounts = append(qoderAccounts, acc)
		qoderAccMu.Unlock()
	}
	if err := qoderFetchUserInfo(acc); err != nil {
		acc.LastError = err.Error()
		qoderSaveAccounts()
		return acc, err
	}
	qoderSaveAccounts()
	return acc, nil
}

func qoderDeleteAccount(id string) bool {
	qoderAccMu.Lock()
	defer qoderAccMu.Unlock()
	list := qoderLoadAccountsLocked()
	out := list[:0]
	removed := false
	for _, a := range list {
		if a.ID == id {
			removed = true
			continue
		}
		out = append(out, a)
	}
	qoderAccounts = out
	qoderSaveAccountsLocked()
	return removed
}
