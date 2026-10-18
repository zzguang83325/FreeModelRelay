package app

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"FreeModelRelay/internal/kit"
)

// ============================================================================
// WorkBuddy 上游 (CN = codebuddy.cn / INTL = workbuddy.ai)
//
// 本机已登录的官方桌面端把凭据写进共享的 CodeBuddyExtension auth 目录,
// 令牌字段是 $wbEncrypted 信封 (AES-256-GCM + 认证 AAD), 密钥由官方 Electron
// 二进制的原生模块持有。这里复用官方实现的三步:
//   1) 以 ELECTRON_RUN_AS_NODE=1 跑 Electron 打印 {version, atRestSecretKey};
//   2) protectorKey = sha256(atRestSecretKey utf8);
//   3) AES-256-GCM 打开信封, AAD 见 buildWBAAD (与 desktop-credential-protection 一致)。
// ============================================================================

type wbVariant struct {
	ID              string // 配置/模型 source 标识
	Provider        string // /v1/models 的 owned_by
	DisplayName     string
	Region          string // cn / global
	DesktopFilename string // auth 目录下的文件名
	ChatBase        string
	Origin          string
	Platform        string // UA 的平台段(global 用错段会 403 11140)
	AcceptLanguage  string
	ElectronEnv     string
	ElectronExe     string   // 优先匹配的可执行文件名(仅用于提示/命中优先级)
	ElectronExes    []string // 可执行文件名候选(实际装机为 WorkBuddyAI.exe)
}

var (
	wbVariantCN = &wbVariant{
		ID: "workbuddy", Provider: "workbuddy", DisplayName: "WorkBuddy",
		Region: "cn", DesktopFilename: "workbuddy-desktop.info",
		ChatBase: "https://copilot.tencent.com", Origin: "https://www.codebuddy.cn",
		Platform: "WorkBuddy", AcceptLanguage: "zh-CN",
		ElectronEnv: "WORKBUDDY_ELECTRON_BIN", ElectronExe: "WorkBuddyAI.exe",
		ElectronExes: []string{"WorkBuddy.exe", "WorkBuddyAI.exe", "workbuddyai.exe"},
	}
	wbVariantINTL = &wbVariant{
		ID: "workbuddy-ai", Provider: "workbuddy-ai", DisplayName: "WorkBuddy AI",
		Region: "global", DesktopFilename: "workbuddy-desktop-ai.info",
		ChatBase: "https://www.workbuddy.ai", Origin: "https://www.workbuddy.ai",
		Platform: "WorkBuddy AI", AcceptLanguage: "en-US",
		ElectronEnv: "WORKBUDDY_AI_ELECTRON_BIN", ElectronExe: "WorkBuddyAI.exe",
		ElectronExes: []string{"workbuddyai.exe", "WorkBuddyAI.exe", "WorkBuddy.exe"},
	}
	wbVariants = []*wbVariant{wbVariantCN, wbVariantINTL}
)

func wbVariantByID(id string) *wbVariant {
	for _, v := range wbVariants {
		if v.ID == id {
			return v
		}
	}
	return nil
}

// 官方客户端版本段(UA 的 WorkBuddy/<cv> + X-IDE-Version)
const (
	wbClientVersion = "5.8.4"
	wbCliVersion    = "2.137.1"
)

// wbHelperScript 官方桌面端内部模块读取 at-rest 密钥的表达式。
// 以 ELECTRON_RUN_AS_NODE=1 运行, 输出 {"version":1,"atRestSecretKey":"<b64 32B>"}。
const wbHelperScript = `process.stdout.write(String(process._linkedBinding("electron_browser_workbuddy_storage").loggerGet()))`

type wbAccount struct {
	Variant         string `json:"variant"`
	UID             string `json:"uid"`
	Uin             string `json:"uin"`
	Email           string `json:"email"`
	Nickname        string `json:"nickname"`
	AccessToken     string `json:"accessToken"`
	RefreshToken    string `json:"refreshToken"`
	ExpiresAt       int64  `json:"expiresAt"` // ms epoch
	RefreshExpireAt int64  `json:"refreshExpiresAt"`
	Domain          string `json:"domain"`
	MachineID       string `json:"machineID"`
	SavedAt         int64  `json:"savedAt"`
	LastError       string `json:"lastError,omitempty"`
}

// ============ 凭据信封解密 ============

type wbEnvelope struct {
	Suite      int    `json:"suite"`
	KeyID      string `json:"keyId"`
	Nonce      string `json:"nonce"`
	AuthTag    string `json:"authTag"`
	Ciphertext string `json:"ciphertext"`
}

type wbWrapped struct {
	WBEncrypted int         `json:"$wbEncrypted"`
	Envelope    string      `json:"envelope"`
	Raw         interface{} `json:"-"`
}

func wbLenPrefixed(v string) []byte {
	b := []byte(v)
	out := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(out, uint32(len(b)))
	copy(out[4:], b)
	return out
}

// buildWBAAD 复刻 desktop-credential-protection 的认证附加数据字节序:
// "WB-AAD\0" + 0x01 + lenPrefixed("WBEV1") + lenPrefixed("sym-v1") +
// uint32BE(suite) + lenPrefixed(keyId) + 0x02 + 0x00 + 0x00
func buildWBAAD(keyID string, suite int) []byte {
	var buf bytes.Buffer
	buf.Write([]byte("WB-AAD\x00"))
	buf.WriteByte(1)
	buf.Write(wbLenPrefixed("WBEV1"))
	buf.Write(wbLenPrefixed("sym-v1"))
	var s [4]byte
	binary.BigEndian.PutUint32(s[:], uint32(suite))
	buf.Write(s[:])
	buf.Write(wbLenPrefixed(keyID))
	buf.Write([]byte{2, 0, 0})
	return buf.Bytes()
}

// wbOpenField 打开一个 $wbEncrypted 信封为明文。
func wbOpenField(key []byte, wrapped *wbWrapped) (string, error) {
	innerRaw, err := base64.StdEncoding.DecodeString(wrapped.Envelope)
	if err != nil {
		return "", fmt.Errorf("envelope base64: %w", err)
	}
	var env wbEnvelope
	if err := json.Unmarshal(innerRaw, &env); err != nil {
		return "", fmt.Errorf("envelope json: %w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return "", fmt.Errorf("nonce: %w", err)
	}
	tag, err := base64.StdEncoding.DecodeString(env.AuthTag)
	if err != nil {
		return "", fmt.Errorf("authTag: %w", err)
	}
	ct, err := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		return "", fmt.Errorf("ciphertext: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	sealed := append(append([]byte{}, ct...), tag...)
	plain, err := gcm.Open(nil, nonce, sealed, buildWBAAD(env.KeyID, env.Suite))
	if err != nil {
		return "", fmt.Errorf("gcm open: %w", err)
	}
	return string(plain), nil
}

// wbField 读取 auth 文档里的一个字段, 字符串直接返回, 信封则解密。
func wbField(key []byte, raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var wrapped wbWrapped
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return "", nil
	}
	if wrapped.WBEncrypted != 1 || wrapped.Envelope == "" {
		return "", nil
	}
	return wbOpenField(key, &wrapped)
}

// ============ 本机凭据发现 ============

// wbAuthDirs 官方桌面端共享的 auth 目录候选(与官方 auth.ts 一致)。
func wbAuthDirs() []string {
	home, _ := os.UserHomeDir()
	rel := filepath.Join("CodeBuddyExtension", "Data", "Public", "auth")
	var out []string
	if runtime.GOOS == "windows" {
		out = append(out,
			filepath.Join(home, "AppData", "Local", rel),
			filepath.Join(home, "AppData", "Roaming", rel),
		)
	} else if runtime.GOOS == "darwin" {
		out = append(out, filepath.Join(home, "Library", "Application Support", rel))
	} else {
		out = append(out, filepath.Join(home, ".config", rel))
	}
	return out
}

// wbFindElectron 定位官方 Electron 二进制(用于读取 at-rest 密钥)。
func wbFindElectron(v *wbVariant) string {
	if p := strings.TrimSpace(os.Getenv(v.ElectronEnv)); p != "" {
		if kit.FileExists(p) {
			return p
		}
	}
	exes := v.ElectronExes
	if len(exes) == 0 && v.ElectronExe != "" {
		exes = []string{v.ElectronExe}
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		dirs := []string{
			filepath.Join(local, "Programs", "WorkBuddy"),
			filepath.Join(local, "Programs", "WorkBuddy AI"),
			filepath.Join(local, "Programs", "WorkBuddyAI"),
		}
		for _, d := range dirs {
			for _, exe := range exes {
				c := filepath.Join(d, exe)
				if kit.FileExists(c) {
					return c
				}
			}
		}
	}
	// 注册表卸载项里的 DisplayIcon 兜底
	if runtime.GOOS == "windows" {
		roots := []string{
			`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`,
			`HKLM\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`,
			`HKCU\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`,
		}
		for _, root := range roots {
			out, err := exec.Command("reg", "query", root, "/s", "/f", "WorkBuddy").Output()
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(out), "\n") {
				line = strings.TrimSpace(line)
				if !strings.Contains(strings.ToLower(line), ".exe") {
					continue
				}
				p := line
				if i := strings.Index(p, "REG_SZ"); i >= 0 {
					p = strings.TrimSpace(p[i+len("REG_SZ"):])
				}
				p = strings.Trim(p, `"`)
				for _, sep := range []string{`",`, `" `, `,`} {
					if i := strings.Index(p, sep); i > 0 {
						p = p[:i]
					}
				}
				if !strings.HasSuffix(strings.ToLower(p), ".exe") {
					continue
				}
				hit := false
				for _, exe := range exes {
					if strings.EqualFold(filepath.Base(p), exe) {
						hit = true
						break
					}
				}
				if hit && kit.FileExists(p) {
					return p
				}
			}
		}
	}
	return ""
}

// wbAtRestSecret 通过官方 Electron 读取 at-rest 密钥(32 字节 base64)。
var (
	wbSecretMu      sync.Mutex
	wbSecretByRealm = map[string]string{} // realm -> protectorKey hex
)

func wbAtRestSecret(v *wbVariant) (string, error) {
	wbSecretMu.Lock()
	if s, ok := wbSecretByRealm[v.Region]; ok {
		wbSecretMu.Unlock()
		return s, nil
	}
	wbSecretMu.Unlock()

	bin := wbFindElectron(v)
	if bin == "" {
		return "", fmt.Errorf("%s 桌面端 Electron 未找到 (可设 %s 指定路径)", v.DisplayName, v.ElectronEnv)
	}
	cmd := exec.Command(bin, "-e", wbHelperScript)
	cmd.Env = append(os.Environ(), "ELECTRON_RUN_AS_NODE=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return "", fmt.Errorf("读取 at-rest 密钥失败: %v %s", err, msg)
	}
	var payload struct {
		Version         int    `json:"version"`
		AtRestSecretKey string `json:"atRestSecretKey"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &payload); err != nil {
		return "", fmt.Errorf("解析 at-rest 输出失败: %w", err)
	}
	raw, err := base64.StdEncoding.DecodeString(payload.AtRestSecretKey)
	if err != nil || len(raw) != 32 {
		return "", fmt.Errorf("at-rest 密钥形态异常")
	}
	sum := sha256.Sum256([]byte(payload.AtRestSecretKey))
	keyHex := hex.EncodeToString(sum[:])
	wbSecretMu.Lock()
	wbSecretByRealm[v.Region] = keyHex
	wbSecretMu.Unlock()
	return keyHex, nil
}

// wbReadLocalAccounts 读取本机官方桌面端已登录账号(CN/INTL 各一)。
func wbReadLocalAccounts() ([]*wbAccount, []string) {
	var accounts []*wbAccount
	var notes []string
	for _, v := range wbVariants {
		var path string
		for _, dir := range wbAuthDirs() {
			p := filepath.Join(dir, v.DesktopFilename)
			if kit.FileExists(p) {
				path = p
				break
			}
		}
		if path == "" {
			notes = append(notes, fmt.Sprintf("%s: 未找到本机登录文件 (%s)", v.DisplayName, v.DesktopFilename))
			continue
		}
		keyHex, err := wbAtRestSecret(v)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: %v", v.DisplayName, err))
			continue
		}
		key, _ := hex.DecodeString(keyHex)
		acc, err := wbParseAuthFile(path, key, v)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: %v", v.DisplayName, err))
			continue
		}
		accounts = append(accounts, acc)
	}
	return accounts, notes
}

func wbParseAuthFile(path string, key []byte, v *wbVariant) (*wbAccount, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("auth 文件不是 JSON: %w", err)
	}
	var auth map[string]json.RawMessage
	if a, ok := doc["auth"]; ok {
		if err := json.Unmarshal(a, &auth); err != nil {
			return nil, fmt.Errorf("auth 段解析失败: %w", err)
		}
	} else {
		auth = doc
	}

	acc := &wbAccount{Variant: v.ID, SavedAt: time.Now().UnixMilli()}
	if at, err := wbField(key, auth["accessToken"]); err == nil {
		acc.AccessToken = at
	}
	if rt, err := wbField(key, auth["refreshToken"]); err == nil {
		acc.RefreshToken = rt
	}
	if acc.AccessToken == "" || acc.RefreshToken == "" {
		return nil, fmt.Errorf("accessToken/refreshToken 解密失败")
	}
	var num int64
	if json.Unmarshal(auth["expiresAt"], &num) == nil {
		acc.ExpiresAt = num
	}
	if json.Unmarshal(auth["refreshExpiresAt"], &num) == nil {
		acc.RefreshExpireAt = num
	}
	var domain string
	if json.Unmarshal(auth["domain"], &domain) == nil {
		acc.Domain = domain
	}

	// 账号身份: account.uid / account.uin / nickname(信封) / 手机号(信封)
	var acct map[string]json.RawMessage
	if a, ok := doc["account"]; ok {
		json.Unmarshal(a, &acct)
	}
	if acct != nil {
		var s string
		if json.Unmarshal(acct["uid"], &s) == nil {
			acc.UID = s
		}
		if json.Unmarshal(acct["uin"], &num) == nil && num != 0 {
			acc.Uin = fmt.Sprintf("%d", num)
		}
		if n, err := wbField(key, acct["nickname"]); err == nil && n != "" {
			acc.Nickname = n
		}
	}
	// JWT claims 兜底(邮箱/昵称)
	if claims := wbJWTClaims(acc.AccessToken); claims != nil {
		if acc.UID == "" {
			if s, _ := claims["sub"].(string); s != "" {
				acc.UID = s
			}
		}
		for _, k := range []string{"email", "preferred_username", "nickname", "name"} {
			if s, _ := claims[k].(string); s != "" {
				acc.Email = s
				break
			}
		}
		if acc.Nickname == "" {
			if s, _ := claims["nickname"].(string); s != "" {
				acc.Nickname = s
			} else if s, _ := claims["name"].(string); s != "" {
				acc.Nickname = s
			}
		}
	}
	acc.MachineID = wbDeviceID(v)
	return acc, nil
}

func wbJWTClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.StdEncoding.DecodeString(parts[1])
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

// wbDeviceID 官方请求头 X-Device-Token; 本机没有独立设备令牌时用稳定随机值。
var (
	wbDeviceMu   sync.Mutex
	wbDeviceIDMu = map[string]string{}
)

func wbDeviceID(v *wbVariant) string {
	wbDeviceMu.Lock()
	defer wbDeviceMu.Unlock()
	if id, ok := wbDeviceIDMu[v.ID]; ok {
		return id
	}
	// 官方客户端在 keyblob 里记录的设备 uuid, 读取失败则生成并持久化
	path := kit.ResolveDataPath(".workbuddy-device.json")
	if data, err := os.ReadFile(path); err == nil {
		var m map[string]string
		if json.Unmarshal(data, &m) == nil && m[v.ID] != "" {
			wbDeviceIDMu[v.ID] = m[v.ID]
			return m[v.ID]
		}
	}
	b := make([]byte, 16)
	rand.Read(b)
	id := fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	m := map[string]string{}
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &m)
	}
	m[v.ID] = id
	if data, err := json.MarshalIndent(m, "", "  "); err == nil {
		os.WriteFile(path, data, 0600)
	}
	wbDeviceIDMu[v.ID] = id
	return id
}
