package app

// ============================================================================
// Trae 本机登录态读取(与 WorkBuddy 的"从本机导入账号"同定位)
//
// 结论(2026-10-19 实测推翻了此前"读不了"的判断):
//   Trae 桌面端把登录态写进 Electron 的 User/globalStorage/storage.json, 键
//   iCubeAuthInfo://icube.cloudide, 值是一段 base64。它的加密是**完全静态**
//   的 —— 四个硬编码盐值写在客户端里, 不掺任何机器指纹/系统密钥, 所以任何
//   进程只要照抄算法就能解开, 不需要调用 Electron 原生模块, 也不需要用户
//   从 F12 里手抄 token。
//
// 密文布局:  [6 字节头][32 字节随机串][密文]
//   头 74 63 05 10 00 00 (ASCII "tc" + 版本 5) -> salt = SALT_A ^ SALT_B
//   头 12 39 20 20 02 03              -> salt = SALT_C ^ SALT_D (aes-private)
//   密钥派生: first = sha512(random); derived = sha512(first || salt)
//             AES-128-CBC, key = derived[0:16], iv = derived[16:32]
//   明文布局: [64 字节 sha512 摘要][真正的 JSON], 用摘要校验完整性。
//
// 同一份 storage.json 里还有 iCubeAuthInfo://icube-dc:<deviceId>, 用同一套
// 编解码存着这台机器的 ECDSA P-256 设备密钥对 —— 而新版刷新接口
// /trae/api/v3/oauth/ExchangeToken 强制要求用它签 DeviceProof(不签回
// 20405, 用错 key 回 20403)。也就是说: 能证明这台设备是自己人的那把私钥,
// 就躺在它保护的登录信息旁边。所以导入时必须一起读出来, 否则刷新永远失败。
//
// 算法与常量对照 dsh-multi-router v0.6.11 的 upstream/trae/src/{paths,decrypt,
// identity,auth,refresh}.ts(从 lib/*.js.map 的 sourcesContent 里读出)。
// ============================================================================

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	// traeAuthStorageKey 桌面端 storage.json 里存登录态的键。
	traeAuthStorageKey = "iCubeAuthInfo://icube.cloudide"
	// traeDeviceKeyPrefix 存设备密钥对的键前缀, 数字后缀就是 x-device-id。
	traeDeviceKeyPrefix = "iCubeAuthInfo://icube-dc:"
	// traeCliTokenFile Trae CLI(traecli)写的裸 JWT 文件名, 无加密。
	traeCliTokenFile = "trae-jwt-token"
	// traeCliDefaultHost CLI token 不带 host 声明, 用它兜底。
	traeCliDefaultHost = "https://api.trae.cn"
)

// 四个硬编码盐值(各 64 字节), 直接从 Trae 客户端抄来, 见文件头说明。
var (
	traeSaltA = []byte{82, 9, 106, 213, 48, 54, 165, 56, 191, 64, 163, 158, 129, 243, 215, 251, 124, 227, 57, 130, 155, 47, 255, 135, 52, 142, 67, 68, 196, 222, 233, 203, 84, 123, 148, 50, 166, 194, 35, 61, 238, 76, 149, 11, 66, 250, 195, 78, 8, 46, 161, 102, 40, 217, 36, 178, 118, 91, 162, 73, 109, 139, 209, 37}
	traeSaltB = []byte{31, 221, 168, 51, 136, 7, 199, 49, 177, 18, 16, 89, 39, 128, 236, 95, 96, 81, 127, 169, 25, 181, 74, 13, 45, 229, 122, 159, 147, 201, 156, 239, 160, 224, 59, 77, 174, 42, 245, 176, 200, 235, 187, 60, 131, 83, 153, 97, 23, 43, 4, 126, 186, 119, 214, 38, 225, 105, 20, 99, 85, 33, 12, 125}
	traeSaltC = []byte{191, 192, 216, 250, 122, 246, 220, 97, 31, 254, 98, 27, 8, 72, 71, 176, 135, 99, 96, 18, 127, 101, 203, 104, 211, 102, 191, 125, 37, 72, 150, 156, 51, 229, 121, 35, 17, 153, 141, 177, 110, 131, 150, 128, 172, 255, 254, 6, 18, 140, 55, 62, 236, 249, 135, 64, 135, 12, 117, 4, 89, 149, 168, 209}
	traeSaltD = []byte{246, 204, 26, 232, 232, 70, 129, 109, 223, 146, 169, 242, 23, 241, 105, 145, 50, 196, 165, 42, 254, 120, 3, 54, 244, 207, 209, 85, 53, 6, 138, 106, 175, 148, 31, 204, 186, 186, 165, 182, 87, 142, 49, 10, 39, 110, 26, 154, 86, 56, 173, 125, 18, 64, 198, 225, 99, 99, 83, 82, 191, 134, 76, 170}
)

// traeEditions 当前只支持 Trae CN。
//
// SG / SOLO / SOLO-SG 暂不接入: 它们的刷新是另一套 ClientID
// (SOLO 用 en1oxy7wnw8j9n, 桌面用 ono9krqynydwx5), 而正式模型目录
// /api/remote/v1/models 目前返回的正是 SOLO 的花名册 —— 混进来既刷不掉令牌,
// 又会列出本机 appId 调不了的模型。要接入国际版/SOLO 时把这里和下面两张
// 目录名表一起放开, 并给 traeRefreshContractOf 加对应分支。
var traeEditions = []string{"cn"}

// traeWindowsDirNames Windows 上每个版本的 per-user 数据目录名。
// Trae 属 VS Code 家族, 目录名来自 product.json 的 win32DirName; 小写拼法
// 也一起试(Windows 大小写不敏感, 所以 "trae cn" 与 "Trae CN" 是同一个目录,
// 这里只列真正不同的拼法)。
var traeWindowsDirNames = map[string][]string{
	"cn": {"Trae CN", "trae-cn"},
}

// traeMacDirNames macOS 上 application support 下的目录名(目前只扫 cn,
// 见 traeEditions 注释)。
var traeMacDirNames = map[string]string{
	"cn": "Trae CN",
}

// traeLinuxDirNames Linux 上 ~/.config 下的目录名(两种拼法都试)。
var traeLinuxDirNames = map[string][]string{
	"cn": {"trae-cn", "Trae CN", "trae", "Trae"},
}

// traeStorageCandidate 一个待探测的凭据文件。
type traeStorageCandidate struct {
	Edition string // 当前固定 "cn"
	Path    string
	Source  string // desktop / cli
}

// traeLocalAccount 从本机扫出来的一份登录态(还没落进账号池)。
type traeLocalAccount struct {
	Edition          string
	Region           string // cn / global
	Source           string // desktop / cli
	Path             string
	AccessToken      string
	RefreshToken     string
	UserID           string
	AccountName      string
	Host             string
	UserRegion       string
	ExpiresAt        int64 // ms
	RefreshExpiresAt int64 // ms
	MachineID        string
	DeviceID         string
	AppVersion       string
	BuildVersion     string
	PrivateKeyPEM    string
	PublicKeyPEM     string
}

// traeXor 逐字节异或(生成派生用的盐)。
func traeXor(a, b []byte) []byte {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	out := make([]byte, n)
	copy(out, a)
	for i := range b {
		out[i] ^= b[i]
	}
	return out
}

// traeDecryptStorageValue 解开 storage.json 里的一段 base64 密文。
func traeDecryptStorageValue(encoded string) (string, error) {
	buf, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return "", fmt.Errorf("base64 解码失败: %w", err)
	}
	if len(buf) <= 102 {
		return "", fmt.Errorf("密文过短(%d 字节)", len(buf))
	}
	var salt []byte
	switch {
	case bytes.Equal(buf[:6], []byte{0x74, 0x63, 0x05, 0x10, 0x00, 0x00}):
		salt = traeXor(traeSaltA, traeSaltB)
	case bytes.Equal(buf[:6], []byte{18, 57, 32, 32, 2, 3}):
		salt = traeXor(traeSaltC, traeSaltD)
	default:
		return "", fmt.Errorf("不支持的加密头 %x", buf[:6])
	}
	random := buf[6:38]
	encrypted := buf[38:]
	if len(encrypted) == 0 || len(encrypted)%aes.BlockSize != 0 {
		return "", fmt.Errorf("密文长度 %d 不是 16 的整数倍", len(encrypted))
	}
	first := sha512.Sum512(random)
	derived := sha512.Sum512(append(append([]byte{}, first[:]...), salt...))
	block, err := aes.NewCipher(derived[:16])
	if err != nil {
		return "", err
	}
	plain := make([]byte, len(encrypted))
	cipher.NewCBCDecrypter(block, derived[16:32]).CryptBlocks(plain, encrypted)
	plain, err = traeStripPKCS7(plain)
	if err != nil {
		return "", err
	}
	if len(plain) < 64 {
		return "", fmt.Errorf("明文过短(%d 字节)", len(plain))
	}
	// 前 64 字节是明文正文的 sha512, 对不上说明密钥/盐值不适用于这份安装。
	actual := sha512.Sum512(plain[64:])
	if !bytes.Equal(plain[:64], actual[:]) {
		return "", fmt.Errorf("完整性校验失败(盐值或密文不匹配)")
	}
	return string(plain[64:]), nil
}

// traeStripPKCS7 去掉 PKCS#7 填充(Node 的 createDecipheriv 默认做法)。
func traeStripPKCS7(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("空明文")
	}
	n := int(data[len(data)-1])
	if n == 0 || n > aes.BlockSize || n > len(data) {
		return nil, fmt.Errorf("PKCS#7 填充非法")
	}
	for _, b := range data[len(data)-n:] {
		if int(b) != n {
			return nil, fmt.Errorf("PKCS#7 填充非法")
		}
	}
	return data[:len(data)-n], nil
}

// traeParseAuthValue 解出一段登录态值(可能是明文 JSON, 也可能是密文)。
func traeParseAuthValue(value string) (map[string]any, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, fmt.Errorf("登录态为空")
	}
	plaintext := trimmed
	if !strings.HasPrefix(trimmed, "{") {
		var err error
		plaintext, err = traeDecryptStorageValue(trimmed)
		if err != nil {
			return nil, err
		}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(plaintext), &out); err != nil {
		return nil, fmt.Errorf("登录态 JSON 解析失败: %w", err)
	}
	return out, nil
}

// traeTimeToMs 上游时间字段可能是秒/毫秒的数字, 也可能是时间字符串。
func traeTimeToMs(v any) int64 {
	switch t := v.(type) {
	case float64:
		if t > 0 {
			if t > 1e12 {
				return int64(t)
			}
			return int64(t * 1000)
		}
	case json.Number:
		if f, err := t.Float64(); err == nil && f > 0 {
			if f > 1e12 {
				return int64(f)
			}
			return int64(f * 1000)
		}
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil && f > 0 {
			if f > 1e12 {
				return int64(f)
			}
			return int64(f * 1000)
		}
		// 本机 storage.json 里的 expiredAt / refreshExpiredAt 实测是
		// "2026-10-25T03:16:27.228Z" 这种 ISO 串, 不是数字。
		if ts, err := time.Parse(time.RFC3339, s); err == nil {
			return ts.UnixMilli()
		}
	}
	return 0
}

// traeStorageCandidates 列出本机所有可能的凭据文件路径(桌面端 + CLI)。
func traeStorageCandidates() []traeStorageCandidate {
	var out []traeStorageCandidate
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		roots := traeDedupNonEmpty(os.Getenv("APPDATA"), filepath.Join(home, "AppData", "Roaming"))
		for _, ed := range traeEditions {
			for _, root := range roots {
				for _, dir := range traeWindowsDirNames[ed] {
					p := filepath.Join(root, dir, "User", "globalStorage", "storage.json")
					out = append(out, traeStorageCandidate{Edition: ed, Path: p, Source: "desktop"})
				}
			}
		}
	case "darwin":
		root := filepath.Join(home, "Library", "Application Support")
		for _, ed := range traeEditions {
			if dir := traeMacDirNames[ed]; dir != "" {
				p := filepath.Join(root, dir, "User", "globalStorage", "storage.json")
				out = append(out, traeStorageCandidate{Edition: ed, Path: p, Source: "desktop"})
			}
		}
	default:
		root := os.Getenv("XDG_CONFIG_HOME")
		if root == "" {
			root = filepath.Join(home, ".config")
		}
		for _, ed := range traeEditions {
			for _, dir := range traeLinuxDirNames[ed] {
				p := filepath.Join(root, dir, "User", "globalStorage", "storage.json")
				out = append(out, traeStorageCandidate{Edition: ed, Path: p, Source: "desktop"})
			}
		}
	}
	// CLI 的裸 JWT: 桌面端没装时它可能是唯一的登录态来源。
	cliRoots := []string{home}
	if runtime.GOOS == "windows" {
		cliRoots = traeDedupNonEmpty(os.Getenv("USERPROFILE"), home)
	}
	for _, root := range cliRoots {
		// .trae-cn 是 CN 版 CLI 的家(.trae 是国际版的, 当前不接)。
		out = append(out, traeStorageCandidate{
			Edition: "cn", Path: filepath.Join(root, ".trae-cn", traeCliTokenFile), Source: "cli",
		})
	}
	return out
}

func traeDedupNonEmpty(vals ...string) []string {
	var out []string
	for _, v := range vals {
		if v == "" {
			continue
		}
		dup := false
		for _, o := range out {
			if strings.EqualFold(o, v) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, v)
		}
	}
	return out
}

// traeReadProductJSON 读安装目录里的 product.json 取真实 appVersion。
// 读不到不报错 —— 版本号只是请求头里的装饰, 缺了不至于让整个导入失败。
func traeReadProductJSON(edition string) map[string]any {
	var paths []string
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		if dir := traeMacDirNames[edition]; dir != "" {
			paths = append(paths, filepath.Join("/Applications", dir+".app", "Contents", "Resources", "app", "product.json"))
		}
	case "windows":
		roots := traeDedupNonEmpty(os.Getenv("LOCALAPPDATA"), filepath.Join(home, "AppData", "Local"))
		for _, root := range roots {
			for _, dir := range traeWindowsDirNames[edition] {
				paths = append(paths, filepath.Join(root, "Programs", dir, "resources", "app", "product.json"))
			}
		}
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var m map[string]any
		if json.Unmarshal(data, &m) == nil {
			return m
		}
	}
	return nil
}

// traeDeviceCenterID 取 iCubeAuthInfo://icube-dc:<id> 的数字后缀。
// 官方 CN 聊天日志里的 x-device-id 就是这个数字, 所以必须用它而不是自己造。
func traeDeviceCenterID(storage map[string]any) string {
	var ids []string
	for k := range storage {
		if strings.HasPrefix(k, traeDeviceKeyPrefix) {
			if id := strings.TrimSpace(strings.TrimPrefix(k, traeDeviceKeyPrefix)); id != "" {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 1 {
		return ids[0]
	}
	return ""
}

// traeDeviceKeyPair 读出设备密钥对(新版刷新签名要用)。
// 老版本安装可能没有, 此时返回 nil, 刷新降级到旧接口而不是整个失败。
func traeDeviceKeyPair(storage map[string]any) (priv, pub string) {
	for k, v := range storage {
		if !strings.HasPrefix(k, traeDeviceKeyPrefix) {
			continue
		}
		encoded, ok := v.(string)
		if !ok || strings.TrimSpace(encoded) == "" {
			continue
		}
		m, err := traeParseAuthValue(encoded)
		if err != nil {
			continue
		}
		p, _ := m["privateKeyPEM"].(string)
		u, _ := m["publicKeyPEM"].(string)
		// 两半缺一不可: 私钥用来签, 公钥是当初注册时绑给这台设备的。
		if strings.TrimSpace(p) != "" && strings.TrimSpace(u) != "" {
			return strings.TrimSpace(p), strings.TrimSpace(u)
		}
	}
	return "", ""
}

// traeIdentityFromStorage 从一份 storage.json 里还原本机身份(机器号/设备号/版本)。
func traeIdentityFromStorage(storage map[string]any, storagePath, edition string) (machineID, deviceID, appVersion, buildVersion string) {
	appRoot := filepath.Dir(filepath.Dir(filepath.Dir(storagePath)))
	telemetryMachine, _ := storage["telemetry.machineId"].(string)
	devDevice, _ := storage["telemetry.devDeviceId"].(string)
	machineID = strings.TrimSpace(telemetryMachine)
	if machineID == "" {
		if data, err := os.ReadFile(filepath.Join(appRoot, "machineid")); err == nil {
			machineID = strings.TrimSpace(string(data))
		}
	}
	deviceID = traeDeviceCenterID(storage)
	if deviceID == "" {
		deviceID = strings.TrimSpace(devDevice)
	}
	if deviceID == "" && machineID != "" {
		// 实在没有就由机器号派生, 保证同一台机器每次都一样。
		sum := sha256.Sum256([]byte(machineID))
		deviceID = hex.EncodeToString(sum[:])[:32]
	}
	buildVersion, _ = storage["iCubeLastVersion"].(string)
	buildVersion = strings.TrimSpace(buildVersion)
	if product := traeReadProductJSON(edition); product != nil {
		appVersion, _ = product["appVersion"].(string)
		appVersion = strings.TrimSpace(appVersion)
	}
	return
}

// traeRegionOfCredential 判定一个凭据属于 CN 还是国际区。
// userRegion.region 是官方给的声明(SG/CN), host 是兜底依据。
func traeRegionOfCredential(userRegion, host string) string {
	r := strings.ToUpper(strings.TrimSpace(userRegion))
	if r == "SG" || r == "OVERSEA" {
		return "global"
	}
	if strings.Contains(strings.ToLower(host), "trae.ai") {
		return "global"
	}
	return "cn"
}

// traeScanLocal 扫描本机所有 Trae 安装, 返回读到的登录态与失败原因。
// 失败原因只含路径和错误文本, 绝不带 token 内容(后台要直接展示)。
func traeScanLocal() ([]traeLocalAccount, []string) {
	var found []traeLocalAccount
	var notes []string
	seen := map[string]bool{}
	for _, cand := range traeStorageCandidates() {
		data, err := os.ReadFile(cand.Path)
		if err != nil {
			continue // 没装这个版本, 静默跳过
		}
		if cand.Source == "cli" {
			acc, err := traeParseCliToken(string(data), cand)
			if err != nil {
				notes = append(notes, fmt.Sprintf("%s: %v", cand.Path, err))
				continue
			}
			key := acc.Edition + "|" + acc.UserID
			if seen[key] {
				continue
			}
			seen[key] = true
			found = append(found, acc)
			continue
		}
		var storage map[string]any
		if err := json.Unmarshal(data, &storage); err != nil {
			notes = append(notes, fmt.Sprintf("%s: storage.json 解析失败", cand.Path))
			continue
		}
		raw, ok := storage[traeAuthStorageKey].(string)
		if !ok || strings.TrimSpace(raw) == "" {
			notes = append(notes, fmt.Sprintf("%s: 没有 %s(该版本未登录)", cand.Path, traeAuthStorageKey))
			continue
		}
		cred, err := traeParseAuthValue(raw)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: %v", cand.Path, err))
			continue
		}
		token, _ := cred["token"].(string)
		if token == "" {
			token, _ = cred["accessToken"].(string)
		}
		if strings.TrimSpace(token) == "" {
			notes = append(notes, fmt.Sprintf("%s: 登录态里没有 token", cand.Path))
			continue
		}
		userID, _ := cred["userId"].(string)
		host, _ := cred["host"].(string)
		var accountName string
		if acc, ok := cred["account"].(map[string]any); ok {
			accountName, _ = acc["username"].(string)
		}
		var userRegion string
		switch v := cred["userRegion"].(type) {
		case string:
			userRegion = v
		case map[string]any:
			userRegion, _ = v["region"].(string)
		}
		machineID, deviceID, appVersion, buildVersion := traeIdentityFromStorage(storage, cand.Path, cand.Edition)
		if machineID == "" {
			notes = append(notes, fmt.Sprintf("%s: 读不到稳定的机器标识", cand.Path))
			continue
		}
		priv, pub := traeDeviceKeyPair(storage)
		refreshToken, _ := cred["refreshToken"].(string)
		key := cand.Edition + "|" + userID
		if seen[key] {
			continue
		}
		seen[key] = true
		found = append(found, traeLocalAccount{
			Edition:          cand.Edition,
			Region:           traeRegionOfCredential(userRegion, host),
			Source:           cand.Source,
			Path:             cand.Path,
			AccessToken:      strings.TrimSpace(token),
			RefreshToken:     strings.TrimSpace(refreshToken),
			UserID:           userID,
			AccountName:      accountName,
			Host:             strings.TrimRight(strings.TrimSpace(host), "/"),
			UserRegion:       userRegion,
			ExpiresAt:        traeTimeToMs(cred["expiredAt"]),
			RefreshExpiresAt: traeTimeToMs(cred["refreshExpiredAt"]),
			MachineID:        machineID,
			DeviceID:         deviceID,
			AppVersion:       appVersion,
			BuildVersion:     buildVersion,
			PrivateKeyPEM:    priv,
			PublicKeyPEM:     pub,
		})
	}
	return found, notes
}

// traeParseCliToken 解析 traecli 写的裸 JWT(没有 iCubeAuthInfo 包装, 也没加密)。
func traeParseCliToken(text string, cand traeStorageCandidate) (traeLocalAccount, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return traeLocalAccount{}, fmt.Errorf("token 文件为空")
	}
	token := trimmed
	if strings.HasPrefix(trimmed, "{") {
		var env map[string]any
		if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
			return traeLocalAccount{}, fmt.Errorf("token 文件不是合法 JSON")
		}
		for _, k := range []string{"token", "accessToken", "jwt"} {
			if s, ok := env[k].(string); ok && strings.TrimSpace(s) != "" {
				token = strings.TrimSpace(s)
				break
			}
		}
	}
	claims := jwtClaims(token)
	if claims == nil {
		return traeLocalAccount{}, fmt.Errorf("不是三段式 JWT")
	}
	var userID string
	if d, ok := claims["data"].(map[string]any); ok {
		userID, _ = d["user_id"].(string)
	}
	if userID == "" {
		return traeLocalAccount{}, fmt.Errorf("JWT 里没有 data.user_id")
	}
	expireAt := traeTimeToMs(claims["exp"])
	return traeLocalAccount{
		Edition:     cand.Edition,
		Region:      traeRegionOfCredential("", traeCliDefaultHost),
		Source:      cand.Source,
		Path:        cand.Path,
		AccessToken: token,
		UserID:      userID,
		Host:        traeCliDefaultHost,
		ExpiresAt:   expireAt,
		// CLI 没有 desktop 的 storage.json, 机器/设备号只能由 userID 派生,
		// 保证同一账号每次导入得到同一组标识(不能每次随机, 否则上游会当成新设备)。
		MachineID: traeDerivedID("trae-cli-machine", userID, 64),
		DeviceID:  traeDerivedID("trae-cli-device", userID, 32),
	}, nil
}

// traeDerivedID 由稳定输入派生一个十六进制标识(CLI 场景无 storage.json 时用)。
func traeDerivedID(kind, seed string, hexLen int) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + seed))
	s := hex.EncodeToString(sum[:])
	if hexLen >= len(s) {
		return s
	}
	return s[:hexLen]
}

// traeImportLocalAccounts 扫描本机并把结果并入账号池。
// 已存在的账号(按 版本+userId 认)只更新令牌和身份, 不新增重复条目。
func traeImportLocalAccounts() ([]*traeAccount, []string, error) {
	found, notes := traeScanLocal()
	if len(found) == 0 {
		if len(notes) == 0 {
			return nil, nil, fmt.Errorf("本机没有找到任何 Trae 安装(或未登录)")
		}
		return nil, notes, fmt.Errorf("本机没有读到可用的 Trae 登录态")
	}
	var imported []*traeAccount
	for _, f := range found {
		acc, err := traeAddAccount(traeCredentialInput{
			AccessToken:     f.AccessToken,
			RefreshToken:    f.RefreshToken,
			UserID:          f.UserID,
			AccountName:     f.AccountName,
			DeviceID:        f.DeviceID,
			MachineID:       f.MachineID,
			Edition:         f.Edition,
			AppVersion:      f.AppVersion,
			BuildVersion:    f.BuildVersion,
			Platform:        runtime.GOOS,
			Region:          f.Region,
			Host:            f.Host,
			UserRegion:      f.UserRegion,
			PrivateKeyPEM:   f.PrivateKeyPEM,
			PublicKeyPEM:    f.PublicKeyPEM,
			ExpiresAt:       f.ExpiresAt,
			RefreshExpireAt: f.RefreshExpiresAt,
			Source:          f.Source,
		})
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: %v", f.Path, err))
			continue
		}
		imported = append(imported, acc)
	}
	if len(imported) == 0 {
		return nil, notes, fmt.Errorf("没有账号可以导入")
	}
	// 顺手把模型目录拉下来, 让 /v1/models 立刻可用。
	if n, err := traeSyncModelsSafe(); err == nil {
		notes = append(notes, fmt.Sprintf("已同步 %d 个模型", n))
	}
	return imported, notes, nil
}
