// Package loomy 对接科大讯飞 Loomy 桌面助手的两个上游：
//
//   - 账号服务 account.xfinfr.com：HMAC-SHA1 签名的登录 API，密码登录用
//     RSA/PKCS#1 v1.5 加密（getPuKey 返回的 rcode 是服务端 nonce，不是验证码，
//     因此密码登录可完全脚本化）；签发 14 天 session，无 refresh token。
//   - 模型网关 loomyad.xunfei.cn/api/v1：OpenAI 兼容接口，登录 session 即
//     Bearer token（客户端 useSessionAuth: true，不存 API key）。
//
// 协议细节来自对官方 Electron 客户端(0.9.38)的逆向整理，与开源项目
// loomy2api 的 docs/PROTOCOL.md 一致。实现上特别注意三点：
//  1. 账号服务的 stringToSign 固定 9 行，无自定义 x-* 头时最后两行是空串，
//     不能省略；
//  2. 模型网关每个请求必须带 traceparent 头，缺失时上游会挂起直到超时；
//  3. 所有出站请求使用调用方注入的 *http.Client（app 层统一传
//     kit.HTTPClient），出口代理遵循全局「手动 > 系统代理 > 环境变量 > 直连」
//     规则，本包不做任何代理特判。
package loomy

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// 上游地址与客户端常量（与官方客户端 resources/.env.prod 一致，可通过
// Client 的字段覆盖）。
const (
	DefaultModelBase    = "https://loomyad.xunfei.cn/api/v1"
	DefaultAccountBase  = "https://account.xfinfr.com"
	DefaultAppID        = "GM3LOOMY"
	DefaultAccessKeyID  = "2thryby66wxi53sk"
	DefaultAccessSecret = "zsak6eadrbawz683wf5r3m2snrwj868r"
	DefaultLoomyVersion = "0.9.38"

	DeviceID     = "web"
	WebModelID   = "Web"
	ClientVer    = "1.0.0"
	ClientUA     = "Loomy|Desktop|Electron|macOS"
	CampusPrefix = "loomy-campus-"

	// SessionExpireSeconds 密码登录显式申请的 session 有效期：14 天。
	SessionExpireSeconds = 14 * 24 * 3600
)

// ============================================================================
// 设备身份
// ============================================================================

// Identity 一个账号的设备身份。协议里真正设备相关的字段只有请求信封的
// devid/ua/modelid/version 加 promotions 设备 id（后者只出现在 /points/activation
// 与 /points/first-login 两个请求体里）。per-account 绑定独立身份只是账号
// 隔离，不改变出口 IP —— 风控主要看 IP，这不是防封保证。
type Identity struct {
	Devid        string `json:"devid"`
	UA           string `json:"ua"`
	ModelID      string `json:"modelid"`
	Version      string `json:"version"`
	CampusDevice string `json:"campusDeviceId,omitempty"`
	CreatedAtSec int64  `json:"createdAt"`
}

// NewIdentity 生成一份新的设备身份：devid 用 web-<16hex> 与真实客户端的
// 常量 "web" 区分开，ua/modelid/version 保持真实客户端的值（编造 UA 比重复
// 更显眼），traceid 不属于身份 —— 真实客户端每个请求都重新生成。
func NewIdentity() *Identity {
	return &Identity{
		Devid:        "web-" + randHex(8),
		UA:           ClientUA,
		ModelID:      WebModelID,
		Version:      ClientVer,
		CampusDevice: CampusPrefix + uuid4(),
		CreatedAtSec: time.Now().Unix(),
	}
}

// Normalize 补齐缺失字段（老版本数据文件可能只有部分字段）。
func (id *Identity) Normalize() {
	if id.Devid == "" {
		id.Devid = DeviceID
	}
	if id.UA == "" {
		id.UA = ClientUA
	}
	if id.ModelID == "" {
		id.ModelID = WebModelID
	}
	if id.Version == "" {
		id.Version = ClientVer
	}
}

// ============================================================================
// 错误
// ============================================================================

// Error 账号服务/网关的业务错误。Retryable 标记是否值得换号或原号重试。
type Error struct {
	Msg       string
	Code      string
	Status    int
	Retryable bool
}

func (e *Error) Error() string { return e.Msg }

func errf(code string, retryable bool, format string, args ...any) *Error {
	return &Error{Msg: fmt.Sprintf(format, args...), Code: code, Retryable: retryable}
}

// AsError 把任意 error 断言为 *Error，便于调用方按 Retryable/Status 分流。
func AsError(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// ============================================================================
// 签名（账号服务）
// ============================================================================

// escapeRFC3986 按 RFC 3986 编码：仅保留 A-Z a-z 0-9 - _ . ~，其余按 UTF-8
// 字节转成 %XX。与 signer.py 的 quote(value, safe="-_.~") 对齐。
func escapeRFC3986(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		default:
			b.WriteString(fmt.Sprintf("%%%02X", c))
		}
	}
	return b.String()
}

// escapedPath 按 "/" 分段逐段转义（路径分隔符本身不编码）。
func escapedPath(p string) string {
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if len(p) > 1 && strings.HasSuffix(p, "/") {
		p = p[:len(p)-1]
	}
	segs := strings.Split(p, "/")
	for i, seg := range segs {
		segs[i] = escapeRFC3986(seg)
	}
	return strings.Join(segs, "/")
}

// contentMD5 base64(md5(body))，空 body 返回空串。
func contentMD5(body string) string {
	if body == "" {
		return ""
	}
	sum := md5.Sum([]byte(body))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// stringToSign 固定 9 行：METHOD / ESCAPED_PATH / ESCAPED_QUERY / Content-MD5 /
// Content-Type / Date / Nonce / SignedHeaders / CanonicalizedHeaders。
// 无自定义 x-* 头时最后两行是空串，但行不能少 —— 少两行签名必错
// （上游会回 "HMAC signature does not match"）。
func stringToSign(method, path, query, body, contentType, date, nonce, signedHeaders, canonical string) string {
	return strings.Join([]string{
		strings.ToUpper(method),
		escapedPath(path),
		query,
		contentMD5(body),
		contentType,
		date,
		nonce,
		signedHeaders,
		canonical,
	}, "\n")
}

// signAuth 计算 Authorization 的签名部分：base64(HMAC-SHA1(secret, stringToSign))。
func signAuth(secret string, sts string) string {
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(sts))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// buildAccountHeaders 构造账号服务请求头（含签名）。
// customHeaders 目前没有调用方使用，保留签名串里两行空行的行为。
func buildAccountHeaders(akID, akSecret, method, rawPath, body string) http.Header {
	const contentType = "application/json"
	date := time.Now().UTC().Format(http.TimeFormat) // Mon, 02 Jan 2006 15:04:05 GMT
	nonce := uuid4()
	sig := signAuth(akSecret, stringToSign(method, rawPath, "", body, contentType, date, nonce, "", ""))
	h := http.Header{}
	h.Set("Authorization", "account "+akID+":"+sig)
	h.Set("Date", date)
	h.Set("Nonce", nonce)
	h.Set("Content-Type", contentType)
	if md5 := contentMD5(body); md5 != "" {
		h.Set("Content-MD5", md5)
	}
	return h
}

// ============================================================================
// 客户端
// ============================================================================

// Client 一个 Loomy 上游客户端。零值不可用，用 NewClient 创建。
type Client struct {
	HTTP           *http.Client // 出站统一走它（app 层传 kit.HTTPClient）
	AccountBase    string
	ModelBase      string
	AccessKeyID    string
	AccessSecret   string
	LoomyVersion   string
	AccountTimeout time.Duration
	ModelTimeout   time.Duration
}

// NewClient 用默认常量创建客户端；http 传 nil 时用 http.DefaultTransport 兜底。
func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &Client{
		HTTP:           httpClient,
		AccountBase:    DefaultAccountBase,
		ModelBase:      DefaultModelBase,
		AccessKeyID:    DefaultAccessKeyID,
		AccessSecret:   DefaultAccessSecret,
		LoomyVersion:   DefaultLoomyVersion,
		AccountTimeout: 30 * time.Second,
		ModelTimeout:   60 * time.Second,
	}
}

// ---- 账号服务信封 ----

// EnvelopeBase 请求信封的 base 段，announce "这是哪台设备"。
type EnvelopeBase struct {
	AppID   string `json:"appid"`
	ModelID string `json:"modelid"`
	Version string `json:"version"`
	Devid   string `json:"devid"`
	UA      string `json:"ua"`
	TraceID string `json:"traceid"` // 每个请求重新生成
}

func (c *Client) envelopeBase(ident *Identity) EnvelopeBase {
	if ident == nil {
		ident = NewIdentity()
	}
	ident.Normalize()
	return EnvelopeBase{
		AppID:   DefaultAppID,
		ModelID: ident.ModelID,
		Version: ident.Version,
		Devid:   ident.Devid,
		UA:      ident.UA,
		TraceID: randHex(32),
	}
}

type accountResp struct {
	Code      string         `json:"code"`
	ErrorCode string         `json:"errorCode"`
	Desc      string         `json:"desc"`
	Message   string         `json:"message"`
	Data      map[string]any `json:"data"`
}

// callAccount 发一次带签名的账号服务 POST，处理业务码信封。
// code 非空且非 "000000" 即业务失败；"100001"/"100002" 视为可重试。
func (c *Client) callAccount(path string, body map[string]any) (*accountResp, error) {
	var bodyStr string
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, errf("MARSHAL", false, "构造账号服务请求失败: %v", err)
		}
		bodyStr = string(raw)
	}
	url := strings.TrimRight(c.AccountBase, "/") + path
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(bodyStr))
	if err != nil {
		return nil, errf("REQUEST", false, "构造账号服务请求失败: %v", err)
	}
	req.Header = buildAccountHeaders(c.AccessKeyID, c.AccessSecret, http.MethodPost, path, bodyStr)

	timeout := c.AccountTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	client := c.HTTP
	if timeout > 0 {
		cc := *client
		cc.Timeout = timeout
		client = &cc
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errf("NETWORK", true, "账号服务不可达: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, errf("NETWORK", true, "读取账号服务响应失败: %v", err)
	}
	var out accountResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errf(fmt.Sprintf("HTTP_%d", resp.StatusCode), resp.StatusCode >= 500,
			"账号服务返回 HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	code := out.Code
	if code == "" {
		code = out.ErrorCode
	}
	if code != "" && code != "000000" {
		msg := out.Desc
		if msg == "" {
			msg = out.Message
		}
		if msg == "" {
			msg = code
		}
		return nil, errf(code, code == "100001" || code == "100002", "%s 失败: %s", path, msg)
	}
	if resp.StatusCode >= 400 && out.Message != "" {
		return nil, errf(fmt.Sprintf("HTTP_%d", resp.StatusCode), resp.StatusCode >= 500,
			"%s HTTP %d: %s", path, resp.StatusCode, out.Message)
	}
	return &out, nil
}

// ---- 登录 ----

// Session 登录成功后的凭据。expire 由调用方按 SessionExpireSeconds 推算。
type Session struct {
	Session string
	UserID  string
	Phone   string
}

// GetPublicKey → (pukey base64 DER, rcode 服务端 nonce)。无副作用，适合做连通性探测。
func (c *Client) GetPublicKey(ident *Identity) (pukey, rcode string, err error) {
	resp, err := c.callAccount("/login/account/getPuKey", map[string]any{
		"base": c.envelopeBase(ident),
	})
	if err != nil {
		return "", "", err
	}
	data := resp.Data
	if data == nil {
		return "", "", errf("NO_PUKEY", false, "getPuKey 未返回数据")
	}
	pukey, _ = data["pukey"].(string)
	rcode, _ = data["rcode"].(string)
	if pukey == "" {
		return "", "", errf("NO_PUKEY", false, "getPuKey 未返回公钥")
	}
	return pukey, rcode, nil
}

// LoginByPassword 密码登录全流程：getPuKey 拿 RSA 公钥 + rcode → RSA/PKCS#1 v1.5
// 加密密码 → byPwd 换 session。rcode 是服务端 nonce，整个过程无需人工介入。
func (c *Client) LoginByPassword(loginid, password string, ident *Identity) (*Session, error) {
	pukey, rcode, err := c.GetPublicKey(ident)
	if err != nil {
		return nil, err
	}
	encrypted, err := rsaEncrypt(pukey, password)
	if err != nil {
		return nil, errf("RSA", false, "密码加密失败: %v", err)
	}
	resp, err := c.callAccount("/login/account/byPwd", map[string]any{
		"base": c.envelopeBase(ident),
		"param": map[string]any{
			"loginid":  loginid,
			"password": encrypted,
			"rcode":    rcode,
			"type":     1,
			"expire":   SessionExpireSeconds,
		},
	})
	if err != nil {
		return nil, err
	}
	return extractSession(resp)
}

// SendSMSCode 发送短信验证码 → msgid。发码本身不建立会话。
func (c *Client) SendSMSCode(phone string, ident *Identity) (msgid string, err error) {
	resp, err := c.callAccount("/login/phone/sendMsgCode", map[string]any{
		"base": c.envelopeBase(ident),
		"param": map[string]any{
			"ccode":  "86",
			"phone":  phone,
			"expire": 300,
		},
	})
	if err != nil {
		return "", err
	}
	if resp.Data != nil {
		msgid, _ = resp.Data["msgid"].(string)
	}
	if msgid == "" {
		return "", errf("NO_MSGID", false, "sendMsgCode 未返回 msgid")
	}
	return msgid, nil
}

// LoginBySMS 短信验证码登录。needs msgid 来自 SendSMSCode。
func (c *Client) LoginBySMS(phone, code, msgid string, ident *Identity) (*Session, error) {
	resp, err := c.callAccount("/login/phone/checkCode", map[string]any{
		"base": c.envelopeBase(ident),
		"param": map[string]any{
			"ccode":  "86",
			"phone":  phone,
			"mcode":  code,
			"msgid":  msgid,
			"expire": SessionExpireSeconds,
		},
	})
	if err != nil {
		return nil, err
	}
	return extractSession(resp)
}

// extractSession 从登录响应里抠 session/userid/phone（字段名有几种变体，
// data 缺失时信封本身就是节点）。
func extractSession(resp *accountResp) (*Session, error) {
	node := map[string]any{}
	if resp.Data != nil {
		node = resp.Data
	}
	session, _ := node["session"].(string)
	if session == "" {
		return nil, errf("NO_SESSION", false, "登录成功但未返回 session: %v", resp.Desc)
	}
	userid, _ := node["userid"].(string)
	if userid == "" {
		userid, _ = node["userId"].(string)
	}
	phone, _ := node["phone"].(string)
	if phone == "" {
		phone, _ = node["loginid"].(string)
	}
	return &Session{Session: session, UserID: userid, Phone: phone}, nil
}

// rsaEncrypt RSA/PKCS#1 v1.5 加密。pukey 是 base64(DER SubjectPublicKeyInfo)，
// 服务端经常省略 base64 padding，两种形态都要认。
func rsaEncrypt(pukeyB64, plaintext string) (string, error) {
	s := strings.TrimSpace(pukeyB64)
	der, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		der, err = base64.RawStdEncoding.DecodeString(s)
	}
	if err != nil {
		return "", fmt.Errorf("公钥 base64 解码失败: %w", err)
	}
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return "", fmt.Errorf("公钥 DER 解析失败: %w", err)
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return "", fmt.Errorf("公钥类型异常: %T", pub)
	}
	ct, err := rsa.EncryptPKCS1v15(rand.Reader, rsaPub, []byte(plaintext))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ct), nil
}

// ============================================================================
// 模型网关
// ============================================================================

// ModelHeaders 模型网关必需请求头。Authorization 与 token 双写（客户端
// useSessionAuth 场景下两者都发）；traceparent 缺失时上游会挂起直到超时。
func (c *Client) ModelHeaders(session string) http.Header {
	h := http.Header{}
	h.Set("Accept", "application/json")
	h.Set("Content-Type", "application/json")
	h.Set("Authorization", "Bearer "+session)
	h.Set("token", session)
	h.Set("traceparent", fmt.Sprintf("00-%s-%s-01", randHex(32), randHex(16)))
	if c.LoomyVersion != "" {
		h.Set("loomy-version", c.LoomyVersion)
	}
	return h
}

// ModelInfo 上游 /models 的单个条目。
type ModelInfo struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ContextLength   int    `json:"context_length"`
	MaxOutputTokens int    `json:"max_output_tokens"`
	Capabilities    struct {
		Reasoning       bool `json:"reasoning"`
		Vision          bool `json:"vision"`
		FunctionCalling bool `json:"function_calling"`
		Streaming       bool `json:"streaming"`
	} `json:"capabilities"`
	ReasoningEfforts json.RawMessage `json:"reasoning_efforts,omitempty"`
	Modalities       []string        `json:"modalities,omitempty"`
}

// FetchModels 拉取模型目录。响应可能是裸数组 / {"data":[...]} /
// {"data":{"data":[...]}} 三种形态，全部兼容。
func (c *Client) FetchModels(session string) ([]ModelInfo, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(c.ModelBase, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header = c.ModelHeaders(session)

	client := c.clientWithTimeout(c.ModelTimeout)
	resp, err := client.Do(req)
	if err != nil {
		return nil, errf("NETWORK", true, "拉取模型目录失败: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, errf("NETWORK", true, "读取模型目录失败: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errf(fmt.Sprintf("HTTP_%d", resp.StatusCode), resp.StatusCode >= 500,
			"/models HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	return parseModelList(raw)
}

func parseModelList(raw []byte) ([]ModelInfo, error) {
	// 形态 1: 裸数组
	var list []ModelInfo
	if err := json.Unmarshal(raw, &list); err == nil && list != nil {
		return list, nil
	}
	// 形态 2/3: 信封包裹，data 可能是一层或两层
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope.Data) == 0 {
		return nil, fmt.Errorf("模型目录响应格式无法识别: %s", truncate(string(raw), 200))
	}
	if err := json.Unmarshal(envelope.Data, &list); err == nil && list != nil {
		return list, nil
	}
	var inner struct {
		Data []ModelInfo `json:"data"`
	}
	if err := json.Unmarshal(envelope.Data, &inner); err == nil && inner.Data != nil {
		return inner.Data, nil
	}
	return nil, fmt.Errorf("模型目录响应格式无法识别: %s", truncate(string(raw), 200))
}

// Points 点数账本（/points/records 的 data 段）。
type Points struct {
	Balance          float64 `json:"balance"`
	DailyBalance     float64 `json:"dailyBalance"`
	AvailableBalance float64 `json:"availableBalance"`
}

// FetchPoints 查询账号点数余额。available = balance(永久) + dailyBalance(每日赠送，
// 优先消耗，跨日重置)。
func (c *Client) FetchPoints(session string) (*Points, error) {
	url := strings.TrimRight(c.ModelBase, "/") + "/points/records?record_type=all&page_no=1&page_size=1"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header = c.ModelHeaders(session)

	client := c.clientWithTimeout(30 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return nil, errf("NETWORK", true, "查询点数失败: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, errf("NETWORK", true, "读取点数响应失败: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errf(fmt.Sprintf("HTTP_%d", resp.StatusCode), resp.StatusCode >= 500,
			"/points/records HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var out struct {
		Data *Points `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Data == nil {
		return nil, fmt.Errorf("点数响应格式无法识别: %s", truncate(string(raw), 200))
	}
	return out.Data, nil
}

// NewUpstreamRequest 构造一个打到模型网关的请求（app 层 chat/图片转发共用）。
// body 为 nil 表示 GET。调用方用 kit.HTTPClient 发出并接管 *http.Response。
func (c *Client) NewUpstreamRequest(session, method, suffix string, body []byte) (*http.Request, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	url := strings.TrimRight(c.ModelBase, "/") + "/" + strings.TrimLeft(suffix, "/")
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		return nil, err
	}
	req.Header = c.ModelHeaders(session)
	return req, nil
}

func (c *Client) clientWithTimeout(timeout time.Duration) *http.Client {
	client := c.HTTP
	if client == nil {
		client = &http.Client{}
	}
	if timeout <= 0 {
		return client
	}
	cc := *client
	cc.Timeout = timeout
	return &cc
}

// ============================================================================
// 小工具
// ============================================================================

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// uuid4 RFC 4122 v4 UUID（Nonce / campus_device_id 用）。
func uuid4() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
