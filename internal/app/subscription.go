package app

import (
	"context"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"

	"FreeModelRelay/internal/kit"
)

// 机场订阅集成:
//   订阅 URL → 拉取(v2rayNG UA) → 解析 trojan/vless/hysteria2 分享链接
//   → 每节点分配本地端口(11200 起) → sing-box 库内起 socks inbound + 对应 outbound
//   → socks5://127.0.0.1:port 注入 zen 代理池(round_robin 轮询 + 冷却复用现有机制)。
// 订阅出口仅用于 zen 通道(对抗 IP 限流); 其他通道继续走统一出口代理, 互不干扰。
// 每个订阅 URL 一个 sing-box Box 实例(多 inbound + 多 outbound + inbound→outbound 路由规则),
// 节点配置由本程序生成, 非法条目在解析阶段剔除。

const (
	subFile      = ".subscriptions.json"
	subPortBase  = 11200 // 本地 socks 端口起点
	subPortRange = 2000  // 端口池大小(最多 2000 节点)
)

type subscriptionConfigData struct {
	Enabled     bool     `json:"enabled"`
	URLs        []string `json:"urls"`
	IntervalMin int      `json:"intervalMin"` // 自动刷新间隔(分钟), 默认 360
	ProbeMin    int      `json:"probeMin"`    // 节点健康检测间隔(分钟), 0=默认 15
}

// subNodeHealth 节点健康状态(持久化, key 为 urlKey:nodeKey)
// Status: ""=未探测(视为可用) / ok=正常 / dead=失联(连续失败,自动恢复) / banned=已封禁(不再使用)
type subNodeHealth struct {
	Status    string `json:"status,omitempty"`
	LastOK    string `json:"lastOk,omitempty"`    // 最近一次探测成功时间
	LastFail  string `json:"lastFail,omitempty"`  // 最近一次探测失败时间
	LastProbe string `json:"lastProbe,omitempty"` // 最近一次探测时间
	Fails     int    `json:"fails,omitempty"`     // 连续失败次数(2 次=失联)
	LatencyMs int    `json:"latencyMs,omitempty"` // 最近成功探测延迟
	BannedAt  string `json:"bannedAt,omitempty"`  // 封禁时间
	BanReason string `json:"banReason,omitempty"` // 封禁原因
	Region    string `json:"region,omitempty"`    // 出口地理位置国家名(经节点出口查询, 如「日本」「香港」)
	ExitIP    string `json:"exitIP,omitempty"`    // 经节点出口查询到的真实出口 IP
}

// subNodeInfo 一个订阅节点(解析结果 + 分配的本地端口)
type subNodeInfo struct {
	Key       string `json:"key"` // 稳定标识: 协议|服务器|端口|凭据哈希, 决定端口分配
	Name      string `json:"name"`
	Protocol  string `json:"protocol"` // trojan / vless / hysteria2
	Server    string `json:"server"`
	Port      int    `json:"port"`      // 远程端口
	LocalPort int    `json:"localPort"` // 本地 socks 端口
	Outbound  string `json:"outbound"`  // sing-box outbound JSON
}

// subLive 一个订阅的运行态
type subLive struct {
	urlKey string
	nodes  []subNodeInfo
	box    *box.Box
}

type subNodeView struct {
	Name       string `json:"name"`
	Protocol   string `json:"protocol"`
	Server     string `json:"server"`
	RemotePort int    `json:"remotePort"`
	LocalPort  int    `json:"localPort"`
	Health     string `json:"health,omitempty"`    // ok / dead / banned / ""(未探测)
	LatencyMs  int    `json:"latencyMs,omitempty"` // 最近成功探测延迟
	LastProbe  string `json:"lastProbe,omitempty"` // 最近探测时间
	Fails      int    `json:"fails,omitempty"`     // 连续失败次数
	BannedAt   string `json:"bannedAt,omitempty"`  // 封禁时间
	BanReason  string `json:"banReason,omitempty"` // 封禁原因
	Region     string `json:"region,omitempty"`    // 出口地理位置国家名
	Preferred  bool   `json:"preferred,omitempty"` // 优先使用(手工大模型检测通过, 已加入优先队列)
}

type subStatusData struct {
	LastRefresh string        `json:"lastRefresh,omitempty"`
	LastError   string        `json:"lastError,omitempty"`
	Nodes       []subNodeView `json:"nodes"`
}

type subStore struct {
	Config    subscriptionConfigData            `json:"config"`
	Nodes     map[string]map[string]subNodeInfo `json:"nodes"`               // urlKey -> 节点Key -> 节点完整定义(持久化, 只增不删; 删除仅由健康检测失效驱动)
	Ports     map[string]int                    `json:"ports"`               // 节点Key -> 本地端口(稳定复用)
	Status    subStatusData                     `json:"status"`              // 上次刷新结果快照(重启后 UI 可见)
	Health    map[string]*subNodeHealth         `json:"health"`              // 节点Key -> 健康状态(持久化)
	BanIPs    map[string]string                 `json:"banIPs,omitempty"`    // 出口 host 黑名单: host -> 封禁原因(上游按出口 IP 封禁, 同 IP 换名不再入库)
	Preferred []string                          `json:"preferred,omitempty"` // 优先使用队列: 手工大模型检测通过节点的代理 URL(按加入顺序, pickZenProxy 优先挑选)
}

var (
	subStoreMu   sync.Mutex
	subStoreData = subStore{
		Config: subscriptionConfigData{IntervalMin: 360},
		Nodes:  map[string]map[string]subNodeInfo{},
		Ports:  map[string]int{},
		Health: map[string]*subNodeHealth{},
	}
	subLivesMu    sync.Mutex
	subLives      = map[string]*subLive{} // urlKey -> live
	subRefreshing atomic.Bool
	subProbing    atomic.Bool
	subBoxCtxOnce sync.Once
	subBoxCtx     context.Context

	// 最近一轮探测汇总(手动/定时统一记录), 供全量检测异步化后前端轮询展示
	subProbeSumMu sync.Mutex
	subProbeSum   *probeSummary
	subProbeSumAt string
)

func setProbeSummary(sum *probeSummary) {
	subProbeSumMu.Lock()
	subProbeSum = sum
	subProbeSumAt = time.Now().Format("2006-01-02 15:04:05")
	subProbeSumMu.Unlock()
}

func getProbeSummary() (*probeSummary, string) {
	subProbeSumMu.Lock()
	defer subProbeSumMu.Unlock()
	return subProbeSum, subProbeSumAt
}

func loadSubscriptionConfig() {
	path := kit.ResolveDataPath(subFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return // 无文件: 默认配置
	}
	var st subStore
	if err := json.Unmarshal(data, &st); err != nil {
		log.Printf("[订阅] 配置文件损坏, 使用默认值: %v", err)
		return
	}
	if st.Nodes == nil {
		st.Nodes = map[string]map[string]subNodeInfo{}
	}
	if st.Ports == nil {
		st.Ports = map[string]int{}
	}
	if st.Health == nil {
		st.Health = map[string]*subNodeHealth{}
	}
	if st.Config.IntervalMin <= 0 {
		st.Config.IntervalMin = 360
	}
	if st.Config.ProbeMin <= 0 {
		st.Config.ProbeMin = 15
	}
	subStoreMu.Lock()
	subStoreData = st
	subStoreMu.Unlock()
}

func saveSubStore() {
	subStoreMu.Lock()
	data, err := json.MarshalIndent(subStoreData, "", "  ")
	subStoreMu.Unlock()
	if err != nil {
		return
	}
	if err := os.WriteFile(kit.ResolveDataPath(subFile), data, 0644); err != nil {
		log.Printf("[订阅] 状态保存失败: %v", err)
	}
}

func getSubConfig() subscriptionConfigData {
	subStoreMu.Lock()
	defer subStoreMu.Unlock()
	return subStoreData.Config
}

// getSubBoxCtx sing-box 上下文(注册表全量注册), 全局复用
func getSubBoxCtx() context.Context {
	subBoxCtxOnce.Do(func() {
		subBoxCtx = include.Context(context.Background())
	})
	return subBoxCtx
}

// ============ 订阅拉取与解析 ============

// fetchSubscription 拉取订阅内容。机场普遍校验 UA, 必须带 v2rayNG。
func fetchSubscription(rawURL string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "v2rayNG/1.8.5")
	client := kit.NewHTTPClient(30 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// decodeSubLines 订阅内容 → 分享链接列表(单行 base64 或明文多行)
func decodeSubLines(content string) []string {
	trimmed := strings.TrimSpace(content)
	// 尝试 base64(可能带换行/无 padding)
	if !strings.Contains(trimmed, "://") {
		for _, enc := range []*base64.Encoding{
			base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
		} {
			if dec, err := enc.DecodeString(strings.Map(func(r rune) rune {
				if r == '\n' || r == '\r' || r == ' ' {
					return -1
				}
				return r
			}, trimmed)); err == nil {
				s := string(dec)
				if strings.Contains(s, "://") {
					return strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' })
				}
			}
		}
	}
	return strings.FieldsFunc(trimmed, func(r rune) bool { return r == '\n' || r == '\r' })
}

// parseSubscriptionContent 解析订阅内容为节点列表
func parseSubscriptionContent(content string) (nodes []subNodeInfo, failed int, firstErr string) {
	for _, line := range decodeSubLines(content) {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "://") {
			continue
		}
		n, err := parseShareURI(line)
		if err != nil {
			failed++
			if firstErr == "" {
				firstErr = err.Error()
			}
			continue
		}
		nodes = append(nodes, n)
	}
	return nodes, failed, firstErr
}

// parseShareURI 解析 trojan/vless/hysteria2 分享链接 → sing-box outbound JSON
func parseShareURI(line string) (subNodeInfo, error) {
	u, err := url.Parse(line)
	if err != nil {
		return subNodeInfo{}, fmt.Errorf("URI 解析失败: %w", err)
	}
	n := subNodeInfo{Name: u.Fragment}
	switch strings.ToLower(u.Scheme) {
	case "trojan":
		n.Protocol = "trojan"
		err = fillTrojanOutbound(u, &n)
	case "vless":
		n.Protocol = "vless"
		err = fillVlessOutbound(u, &n)
	case "hysteria2", "hy2":
		n.Protocol = "hysteria2"
		err = fillHysteria2Outbound(u, &n)
	default:
		return subNodeInfo{}, fmt.Errorf("不支持的协议: %s", u.Scheme)
	}
	if err != nil {
		return subNodeInfo{}, err
	}
	if n.Server == "" || n.Port <= 0 {
		return subNodeInfo{}, fmt.Errorf("缺少服务器地址")
	}
	// 节点身份: 优先「协议+节点名」—— 动态订阅每次拉取会轮换服务器地址
	// (如 CF 优选节点每次返回不同入口 IP), 内容哈希会导致同一逻辑节点
	// 每轮被当成新节点无限膨胀; 无名节点回退内容哈希。
	idSrc := strings.TrimSpace(n.Name)
	if idSrc == "" {
		idSrc = n.Server + "|" + strconv.Itoa(n.Port) + "|" + n.Outbound
	}
	sum := sha1.Sum([]byte(n.Protocol + "|" + idSrc))
	n.Key = hex.EncodeToString(sum[:8])
	return n, nil
}

func uriTLSTLSObj(q url.Values, host string) map[string]any {
	sni := q.Get("sni")
	if sni == "" {
		sni = q.Get("peer")
	}
	if sni == "" {
		sni = host
	}
	obj := map[string]any{"enabled": true, "server_name": sni}
	if q.Get("allowInsecure") == "1" || q.Get("insecure") == "1" || q.Get("insecure") == "true" {
		obj["insecure"] = true
	}
	if fp := q.Get("fp"); fp != "" {
		obj["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
	}
	if alpn := q.Get("alpn"); alpn != "" {
		obj["alpn"] = strings.Split(alpn, ",")
	}
	return obj
}

// uriWSTransport ws/gRPC 传输层; 无传输层参数时返回 nil
func uriWSTransport(q url.Values) map[string]any {
	switch q.Get("type") {
	case "ws", "":
		if q.Get("path") == "" && q.Get("host") == "" {
			return nil
		}
		path := q.Get("path")
		tr := map[string]any{"type": "ws"}
		// ?ed=2048 早期数据约定
		if idx := strings.Index(path, "?ed="); idx >= 0 {
			if ed, err := strconv.Atoi(path[idx+4:]); err == nil && ed > 0 {
				tr["max_early_data"] = ed
				tr["early_data_header_name"] = "Sec-WebSocket-Protocol"
			}
			path = path[:idx]
		}
		if path != "" {
			tr["path"] = path
		}
		if h := q.Get("host"); h != "" {
			tr["headers"] = map[string]any{"Host": h}
		}
		return tr
	case "grpc":
		return map[string]any{"type": "grpc", "service_name": q.Get("serviceName")}
	}
	return nil
}

func fillTrojanOutbound(u *url.URL, n *subNodeInfo) error {
	password := ""
	if u.User != nil {
		password = u.User.Username()
	}
	if password == "" {
		return fmt.Errorf("trojan 缺少密码")
	}
	n.Server = u.Hostname()
	n.Port = uriPort(u, 443)
	tlsObj := uriTLSTLSObj(u.Query(), n.Server)
	if _, ok := tlsObj["enabled"]; !ok {
		tlsObj["enabled"] = true // trojan 默认 TLS
	}
	ob := map[string]any{
		"type": "trojan", "server": n.Server, "server_port": n.Port,
		"password": password, "tls": tlsObj,
	}
	if tr := uriWSTransport(u.Query()); tr != nil {
		ob["transport"] = tr
	}
	return marshalOutbound(ob, n)
}

func fillVlessOutbound(u *url.URL, n *subNodeInfo) error {
	uuid := ""
	if u.User != nil {
		uuid = u.User.Username()
	}
	if uuid == "" {
		return fmt.Errorf("vless 缺少 UUID")
	}
	n.Server = u.Hostname()
	n.Port = uriPort(u, 443)
	q := u.Query()
	security := q.Get("security")
	ob := map[string]any{
		"type": "vless", "server": n.Server, "server_port": n.Port, "uuid": uuid,
	}
	switch security {
	case "tls", "":
		tlsObj := uriTLSTLSObj(q, n.Server)
		if security == "" {
			tlsObj["enabled"] = true
		}
		ob["tls"] = tlsObj
	case "reality":
		tlsObj := uriTLSTLSObj(q, n.Server)
		tlsObj["enabled"] = true
		if _, ok := tlsObj["utls"]; !ok {
			tlsObj["utls"] = map[string]any{"enabled": true, "fingerprint": "chrome"}
		}
		tlsObj["reality"] = map[string]any{
			"enabled":    true,
			"public_key": q.Get("pbk"),
			"short_id":   q.Get("sid"),
		}
		ob["tls"] = tlsObj
	}
	if flow := q.Get("flow"); flow != "" {
		ob["flow"] = flow
	}
	if tr := uriWSTransport(q); tr != nil {
		ob["transport"] = tr
	}
	return marshalOutbound(ob, n)
}

func fillHysteria2Outbound(u *url.URL, n *subNodeInfo) error {
	password := ""
	if u.User != nil {
		password = u.User.Username()
	}
	if password == "" {
		password = u.Query().Get("password")
	}
	if password == "" {
		return fmt.Errorf("hysteria2 缺少密码")
	}
	n.Server = u.Hostname()
	n.Port = uriPort(u, 443)
	q := u.Query()
	tlsObj := uriTLSTLSObj(q, n.Server)
	if q.Get("insecure") == "1" || q.Get("insecure") == "true" {
		tlsObj["insecure"] = true
	}
	// pinSHA256(hex 或逗号分隔多个) → base64 供 sing-box 使用
	var pins []string
	for _, p := range strings.Split(q.Get("pinSHA256"), ",") {
		p = strings.TrimSpace(strings.ReplaceAll(p, ":", ""))
		if p == "" {
			continue
		}
		if b, err := hex.DecodeString(p); err == nil && len(b) == 32 {
			pins = append(pins, base64.StdEncoding.EncodeToString(b))
		} else {
			pins = append(pins, p) // 已是 base64
		}
	}
	if len(pins) > 0 {
		tlsObj["certificate_public_key_sha256"] = pins
	}
	ob := map[string]any{
		"type": "hysteria2", "server": n.Server, "server_port": n.Port,
		"password": password, "tls": tlsObj,
	}
	if q.Get("obfs") == "salamander" {
		ob["obfs"] = map[string]any{"type": "salamander", "password": q.Get("obfs-password")}
	}
	return marshalOutbound(ob, n)
}

func uriPort(u *url.URL, def int) int {
	if p, err := strconv.Atoi(u.Port()); err == nil && p > 0 {
		return p
	}
	return def
}

func marshalOutbound(ob map[string]any, n *subNodeInfo) error {
	data, err := json.Marshal(ob)
	if err != nil {
		return err
	}
	n.Outbound = string(data)
	return nil
}

// ============ 端口分配 ============

func allocSubPort(used map[int]bool) int {
	for p := subPortBase; p < subPortBase+subPortRange; p++ {
		if !used[p] {
			return p
		}
	}
	return 0
}

// ============ Box 生命周期 ============

// buildSubBoxConfig 单订阅 sing-box 配置: N 个 socks inbound + N 个 outbound + 一一对应路由
func buildSubBoxConfig(urlKey string, nodes []subNodeInfo) ([]byte, error) {
	prefix := "s" + urlKey[:6]
	inbounds := make([]any, 0, len(nodes))
	outbounds := make([]any, 0, len(nodes))
	rules := make([]any, 0, len(nodes))
	for i := range nodes {
		n := &nodes[i]
		if n.LocalPort == 0 {
			continue
		}
		inTag := fmt.Sprintf("%s-in-%d", prefix, i)
		outTag := fmt.Sprintf("%s-out-%d", prefix, i)
		inbounds = append(inbounds, map[string]any{
			"type": "socks", "tag": inTag, "listen": "127.0.0.1", "listen_port": n.LocalPort,
		})
		var ob map[string]any
		if err := json.Unmarshal([]byte(n.Outbound), &ob); err != nil {
			return nil, err
		}
		ob["tag"] = outTag
		outbounds = append(outbounds, ob)
		rules = append(rules, map[string]any{"inbound": inTag, "outbound": outTag})
	}
	if len(inbounds) == 0 {
		return nil, fmt.Errorf("无可用节点")
	}
	cfg := map[string]any{
		"log":       map[string]any{"disabled": true},
		"dns":       map[string]any{"servers": []any{map[string]any{"type": "local", "tag": "local-dns"}}},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"route": map[string]any{
			"default_domain_resolver": "local-dns",
			"rules":                   rules,
		},
	}
	return json.Marshal(cfg)
}

// startSubBox 创建并启动一个订阅 Box(先停旧实例, 端口复用时有短暂空窗)
func startSubBox(urlKey string, nodes []subNodeInfo) (*box.Box, error) {
	data, err := buildSubBoxConfig(urlKey, nodes)
	if err != nil {
		return nil, err
	}
	ctx := getSubBoxCtx()
	var opts option.Options
	if err := opts.UnmarshalJSONContext(ctx, data); err != nil {
		return nil, fmt.Errorf("配置解析失败: %w", err)
	}
	b, err := box.New(box.Options{Context: ctx, Options: opts})
	if err != nil {
		return nil, err
	}
	if err := b.Start(); err != nil {
		b.Close()
		return nil, err
	}
	return b, nil
}

func stopSubBox(b *box.Box) {
	if b == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		done := make(chan struct{})
		go func() { b.Close(); close(done) }()
		select {
		case <-done:
		case <-ctx.Done():
			log.Printf("[订阅] Box 关闭超时, 忽略")
		}
	}()
}

func closeAllSubBoxes() {
	subLivesMu.Lock()
	lives := subLives
	subLives = map[string]*subLive{}
	subLivesMu.Unlock()
	for _, lv := range lives {
		stopSubBox(lv.box)
	}
}

// subscriptionProxyURLs 当前生效的订阅 socks 地址(按端口排序, 稳定轮询)。
// 已封禁(banned)与失联(dead)的节点被移出轮询池; dead 节点探测恢复后自动回归。
func subscriptionProxyURLs() []string {
	subLivesMu.Lock()
	type entry struct {
		port int
		key  string
		url  string
	}
	var entries []entry
	for _, lv := range subLives {
		for _, n := range lv.nodes {
			if n.LocalPort > 0 {
				entries = append(entries, entry{
					port: n.LocalPort,
					key:  lv.urlKey + ":" + n.Key,
					url:  fmt.Sprintf("socks5://127.0.0.1:%d", n.LocalPort),
				})
			}
		}
	}
	subLivesMu.Unlock()
	sort.Slice(entries, func(i, j int) bool { return entries[i].port < entries[j].port })
	out := make([]string, 0, len(entries))
	subStoreMu.Lock()
	for _, e := range entries {
		if h := subStoreData.Health[e.key]; h != nil && (h.Status == subHealthBanned || h.Status == subHealthDead) {
			continue
		}
		out = append(out, e.url)
	}
	subStoreMu.Unlock()
	return out
}

// subNodeByPort 按本地 socks 端口反查订阅节点(供使用记录等展示节点名)
func subNodeByPort(port int) (name, protocol string, ok bool) {
	subLivesMu.Lock()
	defer subLivesMu.Unlock()
	for _, lv := range subLives {
		for _, n := range lv.nodes {
			if n.LocalPort == port {
				return n.Name, n.Protocol, true
			}
		}
	}
	return "", "", false
}

// subNodeNetByPort 按本地 socks 端口反查订阅节点, 附带服务器地址与健康表中的
// 出口地理位置/真实出口 IP(供代理节点使用记录展示)。
// 锁序: 先 subLivesMu 取节点快照并释放, 再 subStoreMu 读健康表, 与 subscriptionNodeViews 一致。
func subNodeNetByPort(port int) (name, protocol, server, region, exitIP string, ok bool) {
	subLivesMu.Lock()
	var urlKey, nodeKey string
	for _, lv := range subLives {
		for _, n := range lv.nodes {
			if n.LocalPort == port {
				name, protocol, server = n.Name, n.Protocol, n.Server
				urlKey, nodeKey = lv.urlKey, n.Key
				ok = true
				break
			}
		}
		if ok {
			break
		}
	}
	subLivesMu.Unlock()
	if !ok || nodeKey == "" {
		return name, protocol, server, "", "", ok
	}
	subStoreMu.Lock()
	if h := subStoreData.Health[urlKey+":"+nodeKey]; h != nil {
		region = h.Region
		exitIP = h.ExitIP
	}
	subStoreMu.Unlock()
	return name, protocol, server, region, exitIP, true
}

// mergeSubNodes 将解析出的节点合并进持久化节点表(只增不删):
// 已有节点保持原定义与端口; 新节点分配空闲端口入库; 同一节点重复出现自动去重。
// used 为本轮刷新的全局端口占用表(调用前先预填所有已分配端口)。
// 返回合并后该订阅的全量节点(用于重建 Box)与新增数量。
func mergeSubNodes(urlKey string, parsed []subNodeInfo, used map[int]bool) ([]subNodeInfo, int) {
	subStoreMu.Lock()
	defer subStoreMu.Unlock()
	set := subStoreData.Nodes[urlKey]
	if set == nil {
		set = map[string]subNodeInfo{}
		subStoreData.Nodes[urlKey] = set
	}
	added := 0
	// 黑名单 IP(出口被上游封禁): 存量节点直接删除 —— 同一 IP 换节点名/端口
	// 重现时同样命中, 不再入库, 避免反复撞限。
	purged := 0
	for k, old := range set {
		if _, hit := subStoreData.BanIPs[old.Server]; hit {
			delete(set, k)
			delete(subStoreData.Ports, urlKey+":"+k)
			delete(subStoreData.Health, urlKey+":"+k)
			purged++
		}
	}
	if purged > 0 {
		log.Printf("[订阅] %s 按出口 IP 黑名单清理 %d 个节点", urlKey, purged)
	}
	// 本轮解析命中的节点 Key 集合(用于同名去重时优先保留最新定义)
	parsedKeys := map[string]bool{}
	for i := range parsed {
		parsedKeys[parsed[i].Key] = true
	}
	// 名称索引: 动态订阅同一逻辑节点每轮轮换服务器地址, 同名视为同一节点
	// (定义更新、端口保持); 兼容旧内容哈希 Key 的一次性迁移收敛。
	nameIdx := map[string]string{} // "协议|节点名" -> 节点Key
	for k, old := range set {
		if nm := strings.TrimSpace(old.Name); nm != "" {
			if _, exists := nameIdx[old.Protocol+"|"+nm]; !exists {
				nameIdx[old.Protocol+"|"+nm] = k
			}
		}
	}
	for i := range parsed {
		n := parsed[i]
		// 黑名单 IP: 出口已被上游封禁, 换节点名/端口重现也不入库
		if _, hit := subStoreData.BanIPs[n.Server]; hit {
			continue
		}
		// 同名异 Key(旧 Key 体系/机场改名后哈希变化): 视为同一逻辑节点 —— 沿用端口、更新定义
		if nm := strings.TrimSpace(n.Name); nm != "" {
			if oldKey, ok := nameIdx[n.Protocol+"|"+nm]; ok && oldKey != n.Key {
				if old, ok2 := set[oldKey]; ok2 && old.LocalPort > 0 {
					n.LocalPort = old.LocalPort
					set[n.Key] = n
					delete(set, oldKey)
					delete(subStoreData.Ports, urlKey+":"+oldKey)
					subStoreData.Ports[urlKey+":"+n.Key] = n.LocalPort
					continue
				}
			}
		}
		if old, ok := set[n.Key]; ok && old.LocalPort > 0 {
			// 同 Key: 定义变化时更新(端口保持), 供 Box 重建生效
			if old.Server != n.Server || old.Port != n.Port || old.Outbound != n.Outbound || old.Name != n.Name {
				n.LocalPort = old.LocalPort
				set[n.Key] = n
			}
			continue
		}
		// 迁移兼容: 旧格式仅存于 Ports 表的节点, 优先复用其既有端口
		if p0, ok := subStoreData.Ports[urlKey+":"+n.Key]; ok && p0 > 0 && !used[p0] {
			n.LocalPort = p0
			used[p0] = true
			set[n.Key] = n
			added++
			continue
		}
		p := allocSubPort(used)
		if p == 0 {
			log.Printf("[订阅] 端口池耗尽(%d-%d), 跳过新节点 %s", subPortBase, subPortBase+subPortRange-1, n.Name)
			continue
		}
		n.LocalPort = p
		used[p] = true
		set[n.Key] = n
		subStoreData.Ports[urlKey+":"+n.Key] = p
		added++
	}
	// 同名去重收敛: 同协议同名只保留一个(动态订阅的历史遗留条目为重复身份)。
	// 优先保留本轮解析命中的定义(最新), 否则保留端口更小(更早入库)的。
	keepByName := map[string]string{}
	for k, n := range set {
		nm := strings.TrimSpace(n.Name)
		if nm == "" {
			continue
		}
		id := n.Protocol + "|" + nm
		cur, ok := keepByName[id]
		if !ok {
			keepByName[id] = k
			continue
		}
		if parsedKeys[n.Key] && !parsedKeys[cur] {
			keepByName[id] = k
		} else if !parsedKeys[n.Key] && !parsedKeys[cur] && n.LocalPort < set[cur].LocalPort {
			keepByName[id] = k
		}
	}
	for k, n := range set {
		nm := strings.TrimSpace(n.Name)
		if nm == "" {
			continue
		}
		if keepByName[n.Protocol+"|"+nm] != k {
			delete(set, k)
			delete(subStoreData.Ports, urlKey+":"+k)
			delete(subStoreData.Health, urlKey+":"+k)
		}
	}
	all := make([]subNodeInfo, 0, len(set))
	for _, n := range set {
		if n.LocalPort > 0 {
			all = append(all, n)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].LocalPort < all[j].LocalPort })
	return all, added
}

// ============ 刷新流程 ============

// 刷新语义: 订阅只用于「发现新节点」—— 解析结果合并进持久化节点表, 已有节点
// 不删除(即使订阅里已消失); 节点的移除只由健康检测确认失效(driven, 见 probeSubNodes)。
// 重启后先用文件中的节点直接起 Box, 再走本流程合并增量。
func refreshSubscriptions(trigger string) error {
	if !subRefreshing.CompareAndSwap(false, true) {
		return fmt.Errorf("订阅刷新正在进行中")
	}
	defer subRefreshing.Store(false)

	cfg := getSubConfig()
	startAt := time.Now()
	log.Printf("[订阅] 开始刷新(%s): %d 个订阅", trigger, len(cfg.URLs))

	if !cfg.Enabled || len(cfg.URLs) == 0 {
		closeAllSubBoxes()
		subStoreMu.Lock()
		subStoreData.Status = subStatusData{LastRefresh: time.Now().Format("2006-01-02 15:04:05")}
		subStoreMu.Unlock()
		saveSubStore()
		log.Printf("[订阅] 已禁用或无订阅地址, 全部停止")
		return nil
	}

	// 预填端口占用表(所有已分配端口), 防止新节点分配与已有节点冲突
	used := map[int]bool{}
	subStoreMu.Lock()
	for _, p := range subStoreData.Ports {
		if p > 0 {
			used[p] = true
		}
	}
	subStoreMu.Unlock()

	errs := []string{}
	total := 0
	for _, raw := range cfg.URLs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		urlKey := subURLKey(raw)
		masked := maskSubURL(raw)
		content, err := fetchSubscription(raw)
		if err != nil {
			errs = append(errs, masked+" 拉取失败: "+err.Error())
			log.Printf("[订阅] %s 拉取失败: %v", masked, err)
			continue // 保留文件中的节点继续服务
		}
		parsed, parseFailed, firstErr := parseSubscriptionContent(content)
		if parseFailed > 0 {
			log.Printf("[订阅] %s 有 %d 个节点解析失败(首个: %s)", masked, parseFailed, firstErr)
		}
		if len(parsed) == 0 {
			errs = append(errs, masked+" 无可用节点(保留原有节点)")
			continue
		}
		all, added := mergeSubNodes(urlKey, parsed, used)
		if added > 0 {
			log.Printf("[订阅] %s 新增 %d 个节点(合并后共 %d)", masked, added, len(all))
		}
		// 重建该订阅 Box(节点集可能变化; 先停后起, 端口复用需短暂空窗)
		subLivesMu.Lock()
		old := subLives[urlKey]
		subLivesMu.Unlock()
		if old != nil {
			stopSubBox(old.box)
			time.Sleep(200 * time.Millisecond)
		}
		b, err := startSubBox(urlKey, all)
		if err != nil {
			errs = append(errs, masked+" 启动失败: "+err.Error())
			log.Printf("[订阅] %s 启动失败: %v", masked, err)
			continue
		}
		subLivesMu.Lock()
		subLives[urlKey] = &subLive{urlKey: urlKey, nodes: all, box: b}
		subLivesMu.Unlock()
		total += len(all)
		log.Printf("[订阅] %s 启动 %d 个节点", masked, len(all))
	}

	// 配置中已移除的订阅: 停止其运行实例(文件节点保留, 重新添加订阅后自动恢复)
	cfgKeys := map[string]bool{}
	for _, raw := range cfg.URLs {
		if raw = strings.TrimSpace(raw); raw != "" {
			cfgKeys[subURLKey(raw)] = true
		}
	}
	subLivesMu.Lock()
	var orphans []string
	for urlKey, lv := range subLives {
		if !cfgKeys[urlKey] {
			orphans = append(orphans, urlKey)
			stopSubBox(lv.box)
		}
	}
	for _, urlKey := range orphans {
		delete(subLives, urlKey)
	}
	subLivesMu.Unlock()

	subStoreMu.Lock()
	// 清理无对应节点的端口记录(节点已删除/旧格式迁移遗留), 保持端口池可复用
	for k := range subStoreData.Ports {
		parts := strings.SplitN(k, ":", 2)
		live := false
		if len(parts) == 2 {
			if set := subStoreData.Nodes[parts[0]]; set != nil {
				_, live = set[parts[1]]
			}
		}
		if !live {
			delete(subStoreData.Ports, k)
		}
	}
	// 清理无对应节点的健康记录(封禁标记保留)
	for k, h := range subStoreData.Health {
		if h != nil && h.Status == subHealthBanned {
			continue
		}
		parts := strings.SplitN(k, ":", 2)
		live := false
		if len(parts) == 2 {
			if set := subStoreData.Nodes[parts[0]]; set != nil {
				_, live = set[parts[1]]
			}
		}
		if !live {
			delete(subStoreData.Health, k)
		}
	}
	subStoreData.Status.LastRefresh = time.Now().Format("2006-01-02 15:04:05")
	subStoreData.Status.LastError = strings.Join(errs, "; ")
	subStoreData.Status.Nodes = subscriptionNodeViews()
	subStoreMu.Unlock()
	saveSubStore()

	log.Printf("[订阅] 刷新完成: %d 节点生效, %d 失败, 耗时 %v", total, len(errs), time.Since(startAt).Round(time.Millisecond))
	if len(errs) > 0 && total == 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func subURLKey(rawURL string) string {
	sum := sha1.Sum([]byte(rawURL))
	return hex.EncodeToString(sum[:6])
}

// subscriptionNodeViews 调用方需持有 subStoreMu(直接写入 Status.Nodes)。
// subLives 的写入方(刷新/删除/启动)只持 subLivesMu —— 遍历前先在该锁下做
// 节点快照拷贝, 避免并发读写 map/slice 的数据竞态(调用方均未持 subLivesMu)。
// 封禁(banned)节点等同于黑名单: 不进入本视图, 前端列表不再显示,
// 其封禁记录仍保留在 Health 表中, 供「清除封禁」恢复。
func subscriptionNodeViews() []subNodeView {
	subLivesMu.Lock()
	type liveSnap struct {
		urlKey string
		nodes  []subNodeInfo
	}
	snaps := make([]liveSnap, 0, len(subLives))
	for _, lv := range subLives {
		cp := make([]subNodeInfo, len(lv.nodes))
		copy(cp, lv.nodes)
		snaps = append(snaps, liveSnap{lv.urlKey, cp})
	}
	subLivesMu.Unlock()

	var views []subNodeView
	// 优先队列(手工大模型检测通过): 调用方已持 subStoreMu, 直读填充 ⭐ 标志
	prefSet := map[string]bool{}
	for _, p := range subStoreData.Preferred {
		prefSet[p] = true
	}
	for _, s := range snaps {
		for _, n := range s.nodes {
			if n.LocalPort <= 0 {
				continue
			}
			v := subNodeView{
				Name: n.Name, Protocol: n.Protocol, Server: n.Server,
				RemotePort: n.Port, LocalPort: n.LocalPort,
			}
			if prefSet[fmt.Sprintf("socks5://127.0.0.1:%d", n.LocalPort)] {
				v.Preferred = true
			}
			if h := subStoreData.Health[s.urlKey+":"+n.Key]; h != nil {
				if h.Status == subHealthBanned {
					continue // 黑名单: 封禁节点不再显示
				}
				v.Health = h.Status
				v.LatencyMs = h.LatencyMs
				v.LastProbe = h.LastProbe
				v.Fails = h.Fails
				v.Region = h.Region
				v.BannedAt = h.BannedAt
				v.BanReason = h.BanReason
			}
			views = append(views, v)
		}
	}
	sort.Slice(views, func(i, j int) bool { return views[i].LocalPort < views[j].LocalPort })
	return views
}

func maskSubURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery == "" {
		return raw
	}
	q := u.Query()
	for k := range q {
		q.Set(k, "***")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// ============ 节点健康检测 ============

const (
	subHealthOK     = "ok"
	subHealthDead   = "dead"
	subHealthBanned = "banned"

	subProbeTarget   = "opencode.ai:443" // 探测目标与 zen 实际上游一致
	subProbeTimeout  = 12 * time.Second
	subProbeWorkers  = 12 // 并发探测 worker 数
	subDeadThreshold = 2  // 连续失败 N 次标记失联
)

// probeSubNode 经节点本地 socks 出口探测上游可达性:
// TCP 连接 opencode.ai:443 + TLS 握手(不发 HTTP 请求, 不耗配额)。
// 返回延迟毫秒与错误。
func probeSubNode(localPort int) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), subProbeTimeout)
	defer cancel()
	start := time.Now()
	conn, err := dialViaProxy(ctx, fmt.Sprintf("socks5://127.0.0.1:%d", localPort), "tcp", subProbeTarget)
	if err != nil {
		return 0, err
	}
	// TLS 握手验证节点→上游全链路(排除节点侧 SNI 拦截/劫持)
	tconn := tls.Client(conn, &tls.Config{ServerName: "opencode.ai", MinVersion: tls.VersionTLS12})
	if err := tconn.HandshakeContext(ctx); err != nil {
		conn.Close()
		return 0, err
	}
	tconn.Close()
	return int(time.Since(start).Milliseconds()), nil
}

// 地区查询全局限速(ip-api.com 免费版限 45 req/min)
var (
	subRegionMu   sync.Mutex
	subRegionLast time.Time
)

// subProbeRegion 经节点 socks 出口查询出口 IP 的地理位置国家名(中文, 如「日本」「香港」)
// 与真实出口 IP。走 ip-api.com 免费接口, 失败返回空串(前端显示「-」);
// 全局限速避免打爆免费配额。
func subProbeRegion(localPort int) (string, string) {
	subRegionMu.Lock()
	if d := time.Since(subRegionLast); d < 1400*time.Millisecond {
		time.Sleep(1400*time.Millisecond - d)
	}
	subRegionLast = time.Now()
	subRegionMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	client := &http.Client{
		Timeout: 12 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialViaProxy(ctx, fmt.Sprintf("socks5://127.0.0.1:%d", localPort), network, addr)
			},
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://ip-api.com/json/?fields=status,country,query&lang=zh-CN", nil)
	if err != nil {
		return "", ""
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	var out struct {
		Status  string `json:"status"`
		Country string `json:"country"`
		Query   string `json:"query"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&out); err != nil || out.Status != "success" || out.Country == "" {
		return "", ""
	}
	return out.Country, out.Query
}

// probeSummary 一轮探测的结果汇总
type probeSummary struct {
	Total      int   `json:"total"`
	OK         int   `json:"ok"`
	Dead       int   `json:"dead"`
	Banned     int   `json:"banned"`
	NewDead    int   `json:"newDead"`   // 本轮新失联
	Recovered  int   `json:"recovered"` // 本轮恢复
	DurationMs int64 `json:"durationMs"`
}

// probeSubNodes 对全部运行中节点(跳过已封禁)做一轮并发健康检测,
// 结果写入 Health 并持久化。连续 2 次失败的节点确认失效 —— 从节点表删除
// 并重建受影响订阅的 Box(订阅刷新只增不删, 删除仅由此驱动;
// 若订阅中仍存在该节点, 下次刷新会作为新节点重新加入)。
func probeSubNodes(trigger string) *probeSummary {
	// 订阅刷新进行中会短暂停旧 Box 起新 Box(端口空窗), 此时探测会把健康节点
	// 误记失败 —— 连续两轮都撞上窗口会误删好节点, 直接跳过本轮。
	if subRefreshing.Load() {
		log.Printf("[探测] %s 订阅刷新进行中, 本轮探测跳过", trigger)
		return nil
	}
	if !subProbing.CompareAndSwap(false, true) {
		return nil
	}
	defer subProbing.Store(false)
	startAt := time.Now()

	// 收集待探测节点(排除 banned)
	// 注意: 先在 subLivesMu 下取节点快照并释放, 再在 subStoreMu 下读健康表,
	// 避免与 subscriptionNodeViews(subStoreMu -> subLivesMu) 形成锁序反转。
	subLivesMu.Lock()
	type task struct {
		urlKey     string
		node       subNodeInfo
		needRegion bool // 健康表尚未记录出口地理位置, 探测成功后顺带查询
	}
	var all []task
	for _, lv := range subLives {
		for _, n := range lv.nodes {
			if n.LocalPort <= 0 {
				continue
			}
			all = append(all, task{urlKey: lv.urlKey, node: n})
		}
	}
	subLivesMu.Unlock()

	subStoreMu.Lock()
	var tasks []task
	banned := 0
	for _, t := range all {
		h := subStoreData.Health[t.urlKey+":"+t.node.Key]
		if h != nil && h.Status == subHealthBanned {
			banned++
			continue
		}
		if h == nil || h.Region == "" {
			t.needRegion = true
		}
		tasks = append(tasks, t)
	}
	subStoreMu.Unlock()

	sum := &probeSummary{Total: len(tasks), Banned: banned}
	if len(tasks) == 0 {
		return sum
	}

	// worker pool 并发探测
	type result struct {
		urlKey  string
		nodeKey string
		name    string
		ok      bool
		latency int
		region  string // 探测成功且尚未记录时顺带查询的出口地理位置
		exitIP  string // 同次查询拿到的真实出口 IP
	}
	results := make(chan result, len(tasks))
	var wg sync.WaitGroup
	sem := make(chan struct{}, subProbeWorkers)
	for _, t := range tasks {
		wg.Add(1)
		go func(t task) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			latency, err := probeSubNode(t.node.LocalPort)
			region, exitIP := "", ""
			if err == nil && t.needRegion {
				region, exitIP = subProbeRegion(t.node.LocalPort)
			}
			results <- result{urlKey: t.urlKey, nodeKey: t.node.Key, name: t.node.Name, ok: err == nil, latency: latency, region: region, exitIP: exitIP}
		}(t)
	}
	wg.Wait()
	close(results)

	// 汇总写回健康状态
	now := time.Now().Format("2006-01-02 15:04:05")
	deadKeys := map[string]bool{} // 确认失效待删除的节点 key
	regionUpdated := false
	subStoreMu.Lock()
	for res := range results {
		key := res.urlKey + ":" + res.nodeKey
		h := subStoreData.Health[key]
		if h == nil {
			h = &subNodeHealth{}
			subStoreData.Health[key] = h
		}
		h.LastProbe = now
		if res.ok {
			if h.Status == subHealthDead {
				sum.Recovered++
				log.Printf("[探测] ✅ 节点恢复: %s (%dms)", res.name, res.latency)
			}
			h.Status = subHealthOK
			h.Fails = 0
			h.LastOK = now
			h.LastFail = ""
			h.LatencyMs = res.latency
			if res.region != "" {
				h.Region = res.region
				h.ExitIP = res.exitIP
				regionUpdated = true
			}
			sum.OK++
		} else {
			h.Fails++
			h.LastFail = now
			if h.Fails >= subDeadThreshold {
				deadKeys[key] = true
				sum.NewDead++
				sum.Dead++
				log.Printf("[探测] ❌ 节点失效(连续 %d 次失败), 删除: %s", h.Fails, res.name)
			}
		}
	}
	// 清理 14 天未探测的非封禁残留(节点已不在节点表)
	cutoff := time.Now().AddDate(0, 0, -14).Format("2006-01-02 15:04:05")
	for k, h := range subStoreData.Health {
		if h.Status != subHealthBanned && h.LastProbe < cutoff {
			delete(subStoreData.Health, k)
		}
	}
	subStoreMu.Unlock()
	saveSubStore()
	if regionUpdated {
		// 新增出口地理位置: 刷新视图下发给前端
		subStoreMu.Lock()
		subStoreData.Status.Nodes = subscriptionNodeViews()
		subStoreMu.Unlock()
		saveSubStore()
	}

	// 删除失效节点并重建受影响订阅的 Box(锁外执行, 涉及 Box 重启)
	removed := removeDeadNodes(deadKeys)

	sum.DurationMs = time.Since(startAt).Milliseconds()
	log.Printf("[探测] %s 完成: %d 可用 / %d 失效删除 / %d 恢复 / %d 已封禁, 耗时 %v",
		trigger, sum.OK, removed, sum.Recovered, sum.Banned, time.Duration(sum.DurationMs)*time.Millisecond)
	setProbeSummary(sum)
	return sum
}

// removeDeadNodes 删除确认失效的节点: 从持久化节点表/端口表/健康表移除,
// 同步运行态并重建受影响订阅的 Box(先停后起, 端口复用需短暂空窗)。
func removeDeadNodes(deadKeys map[string]bool) int {
	if len(deadKeys) == 0 {
		return 0
	}
	removedByURL := map[string]int{}
	subStoreMu.Lock()
	removed := 0
	for key := range deadKeys {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 {
			continue
		}
		urlKey, nodeKey := parts[0], parts[1]
		if set := subStoreData.Nodes[urlKey]; set != nil {
			if n, ok := set[nodeKey]; ok {
				// 失效节点退出优先队列(按端口构造代理 URL)
				if port := subStoreData.Ports[key]; port > 0 {
					subPreferredRemoveLocked(fmt.Sprintf("socks5://127.0.0.1:%d", port))
				}
				delete(set, nodeKey)
				delete(subStoreData.Ports, key)
				delete(subStoreData.Health, key)
				removed++
				removedByURL[urlKey]++
				log.Printf("[订阅] 🗑️ 失效节点已删除: %s (%s:%d)", n.Name, n.Server, n.Port)
			}
		}
	}
	subStoreMu.Unlock()
	if removed == 0 {
		return 0
	}
	// 同步运行态节点列表
	subLivesMu.Lock()
	for urlKey := range removedByURL {
		if lv := subLives[urlKey]; lv != nil {
			var keep []subNodeInfo
			for _, n := range lv.nodes {
				if !deadKeys[urlKey+":"+n.Key] {
					keep = append(keep, n)
				}
			}
			lv.nodes = keep
		}
	}
	subLivesMu.Unlock()
	// 重建受影响订阅的 Box
	for urlKey := range removedByURL {
		rebuildSubBox(urlKey)
	}
	// 刷新快照并持久化
	subStoreMu.Lock()
	subStoreData.Status.Nodes = subscriptionNodeViews()
	subStoreMu.Unlock()
	saveSubStore()
	return removed
}

// rebuildSubBox 用运行态节点列表重建单个订阅的 Box。无剩余节点时仅停止并摘除。
func rebuildSubBox(urlKey string) {
	subLivesMu.Lock()
	lv := subLives[urlKey]
	subLivesMu.Unlock()
	if lv == nil {
		return
	}
	stopSubBox(lv.box)
	if len(lv.nodes) == 0 {
		subLivesMu.Lock()
		delete(subLives, urlKey)
		subLivesMu.Unlock()
		log.Printf("[订阅] %s 无剩余节点, 已停止", urlKey[:6])
		return
	}
	time.Sleep(200 * time.Millisecond) // 等待旧监听释放
	b, err := startSubBox(urlKey, lv.nodes)
	if err != nil {
		log.Printf("[订阅] %s Box 重建失败(保留节点表, 下次刷新重试): %v", urlKey[:6], err)
		return
	}
	subLivesMu.Lock()
	lv.box = b
	subLivesMu.Unlock()
	log.Printf("[订阅] %s Box 已重建: %d 节点", urlKey[:6], len(lv.nodes))
}

// subBanNodeByURL 按代理 URL(socks5://127.0.0.1:port) 永久封禁对应订阅节点:
// 立即移出轮询池、持久化标记、UI 可见; 非订阅代理 URL 直接忽略。
func subBanNodeByURL(proxyURL, reason string) bool {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return false
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < subPortBase {
		return false // 手动代理或非法端口: 不处理
	}
	subLivesMu.Lock()
	var target *subNodeInfo
	var urlKey string
	for _, lv := range subLives {
		for i := range lv.nodes {
			if lv.nodes[i].LocalPort == port {
				target = &lv.nodes[i]
				urlKey = lv.urlKey
			}
		}
	}
	var name, nodeKey string
	if target != nil {
		name, nodeKey = target.Name, target.Key
	}
	subLivesMu.Unlock()
	if target == nil {
		return false
	}
	key := urlKey + ":" + nodeKey
	subStoreMu.Lock()
	h := subStoreData.Health[key]
	if h == nil {
		h = &subNodeHealth{}
		subStoreData.Health[key] = h
	}
	already := h.Status == subHealthBanned
	h.Status = subHealthBanned
	h.BannedAt = time.Now().Format("2006-01-02 15:04:05")
	h.BanReason = reason
	// 出口 host 一并拉黑: 后续订阅刷新发现同 IP 节点(换名/换端口)不再入库
	if target.Server != "" {
		if subStoreData.BanIPs == nil {
			subStoreData.BanIPs = map[string]string{}
		}
		subStoreData.BanIPs[target.Server] = reason
	}
	// 封禁节点退出优先队列
	subPreferredRemoveLocked(proxyURL)
	// 封禁即入黑名单: 重建视图, 该节点从列表中移除
	subStoreData.Status.Nodes = subscriptionNodeViews()
	subStoreMu.Unlock()
	clearZenProxyCooldown(proxyURL)
	if !already {
		saveSubStore()
		log.Printf("[订阅] 🚫 节点已封禁并移出轮询池: %s (%s:%d) 原因: %s", name, target.Server, target.Port, reason)
	}
	return true
}

// subUnbanAll 清除全部封禁标记(节点重新进入轮询池, 下轮探测刷新健康状态)
func subUnbanAll() int {
	subStoreMu.Lock()
	n := 0
	for _, h := range subStoreData.Health {
		if h.Status == subHealthBanned {
			h.Status = subHealthOK
			h.Fails = 0
			h.BannedAt = ""
			h.BanReason = ""
			n++
		}
	}
	if n > 0 {
		subStoreData.Status.Nodes = subscriptionNodeViews()
	}
	subStoreData.BanIPs = map[string]string{} // 同步清空出口 IP 黑名单
	subStoreMu.Unlock()
	if n > 0 {
		saveSubStore()
		log.Printf("[订阅] ♻️ 已清除 %d 个节点的封禁标记", n)
	}
	return n
}

// ============ 后台定时刷新 ============

// subMigrateBanIPs 启动迁移: 把存量封禁节点记录的出口 IP 补入黑名单
// (旧版本封禁只记在节点 Health 上, 同 IP 换名重现会重新入库)。
func subMigrateBanIPs() {
	// 注意: saveSubStore 内部会拿 subStoreMu, 须先解锁再保存, 避免自锁死锁。
	subStoreMu.Lock()
	if subStoreData.BanIPs == nil {
		subStoreData.BanIPs = map[string]string{}
	}
	n := 0
	for k, h := range subStoreData.Health {
		if h == nil || h.Status != subHealthBanned || h.BanReason == "" {
			continue
		}
		parts := strings.SplitN(k, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if set := subStoreData.Nodes[parts[0]]; set != nil {
			if node, ok := set[parts[1]]; ok && node.Server != "" {
				if _, exists := subStoreData.BanIPs[node.Server]; !exists {
					subStoreData.BanIPs[node.Server] = h.BanReason
					n++
				}
			}
		}
	}
	subStoreMu.Unlock()
	if n > 0 {
		saveSubStore()
		log.Printf("[订阅] 启动迁移: %d 个封禁节点的出口 IP 已补入黑名单", n)
	}
}

// startSavedBoxes 用持久化节点表直接启动全部 Box(重启后立即可用, 不等订阅拉取)。
// 节点定义(含端口)来自 data/.subscriptions.json; 失效节点已在上次运行中被探测删除,
// 剩余节点直接复用, 随后的订阅刷新只合并增量、健康检测验证有效性。
// 封禁节点同样启动监听(不进轮询池), 解封后立即可用, 无需重建 Box。
func startSavedBoxes() int {
	subMigrateBanIPs()
	subStoreMu.Lock()
	byURL := map[string][]subNodeInfo{}
	for urlKey, set := range subStoreData.Nodes {
		for _, n := range set {
			if n.LocalPort > 0 {
				// 黑名单 IP 节点不起 Box(双保险: 正常情况下刷新时已被清理)
				if _, hit := subStoreData.BanIPs[n.Server]; hit {
					continue
				}
				byURL[urlKey] = append(byURL[urlKey], n)
			}
		}
	}
	subStoreMu.Unlock()
	if len(byURL) == 0 {
		return 0
	}
	total := 0
	for urlKey, nodes := range byURL {
		sort.Slice(nodes, func(i, j int) bool { return nodes[i].LocalPort < nodes[j].LocalPort })
		b, err := startSubBox(urlKey, nodes)
		if err != nil {
			log.Printf("[订阅] %s 文件节点启动失败(等待订阅刷新重试): %v", urlKey[:6], err)
			continue
		}
		subLivesMu.Lock()
		subLives[urlKey] = &subLive{urlKey: urlKey, nodes: nodes, box: b}
		subLivesMu.Unlock()
		total += len(nodes)
	}
	if total > 0 {
		subStoreMu.Lock()
		subStoreData.Status.Nodes = subscriptionNodeViews()
		subStoreMu.Unlock()
		log.Printf("[订阅] 从文件加载并启动 %d 个节点(%d 个订阅)", total, len(byURL))
	}
	return total
}

func startSubscriptionManager() {
	cfg := getSubConfig()
	if cfg.Enabled {
		// 先用文件中持久化的节点直接起 Box, 服务立即可用
		startSavedBoxes()
	}
	if cfg.Enabled && len(cfg.URLs) > 0 {
		// 启动 3 秒后刷新订阅(只合并新增节点)。
		// 换新进程重启时旧实例的监听端口可能尚未完全释放, 全部失败则退避重试。
		go func() {
			time.Sleep(3 * time.Second)
			for i := 0; i < 4; i++ {
				err := refreshSubscriptions("启动")
				if err == nil {
					return
				}
				wait := time.Duration(5*(i+1)) * time.Second
				log.Printf("[订阅] 启动刷新失败, %v 后重试(%d/3): %v", wait, i+1, err)
				time.Sleep(wait)
			}
		}()
	}
	interval := cfg.IntervalMin
	if interval < 30 {
		interval = 30
	}
	if interval > 1440 {
		interval = 1440
	}
	go func() {
		ticker := time.NewTicker(time.Duration(interval) * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			cfg := getSubConfig()
			if !cfg.Enabled || len(cfg.URLs) == 0 {
				continue
			}
			if err := refreshSubscriptions("定时"); err != nil {
				log.Printf("[订阅] 定时刷新失败: %v", err)
			}
		}
	}()
	// 节点健康检测: 启动 2 分钟后首测(等启动刷新就绪), 之后按 ProbeMin 周期循环。
	// 用 sleep 循环而非固定 ticker, 每轮重新读配置, 间隔调整即时生效。
	go func() {
		time.Sleep(2 * time.Minute)
		for {
			cfg := getSubConfig()
			if cfg.Enabled && len(cfg.URLs) > 0 {
				probeSubNodes("定时")
			}
			mins := cfg.ProbeMin
			if mins < 5 {
				mins = 15 // 0/负值=默认
			}
			if mins > 1440 {
				mins = 1440
			}
			time.Sleep(time.Duration(mins) * time.Minute)
		}
	}()
}

// ============ 后台 API ============

func getSubStatus() map[string]any {
	cfg := getSubConfig()
	subStoreMu.Lock()
	status := subStoreData.Status
	banned := 0
	for k, h := range subStoreData.Health {
		if h == nil || h.Status != subHealthBanned {
			continue
		}
		// 仅统计仍存在于节点表中的黑名单节点(订阅已被移除的孤儿记录不计入)
		parts := strings.SplitN(k, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if set := subStoreData.Nodes[parts[0]]; set != nil {
			if _, ok := set[parts[1]]; ok {
				banned++
			}
		}
	}
	subStoreMu.Unlock()
	if status.Nodes == nil {
		status.Nodes = []subNodeView{}
	}
	subLivesMu.Lock()
	liveCount := 0
	for _, lv := range subLives {
		liveCount += len(lv.nodes)
	}
	subLivesMu.Unlock()
	sum, sumAt := getProbeSummary()
	return map[string]any{
		"enabled":        cfg.Enabled,
		"urls":           cfg.URLs,
		"intervalMin":    cfg.IntervalMin,
		"probeMin":       cfg.ProbeMin,
		"nodes":          status.Nodes,
		"liveCount":      liveCount,
		"activeCount":    len(subscriptionProxyURLs()), // 轮询池内节点(剔除失联/封禁)
		"bannedCount":    banned,
		"probing":        subProbing.Load(),
		"probeSummary":   sum,
		"probeSummaryAt": sumAt,
		"lastRefresh":    status.LastRefresh,
		"lastError":      status.LastError,
		"updating":       subRefreshing.Load(),
	}
}

// handleSubscriptions GET /admin/api/subscriptions
func handleSubscriptions(w http.ResponseWriter, r *http.Request) {
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Data: getSubStatus()})
}

// handleSubscriptionsUpdate POST /admin/api/subscriptions
func handleSubscriptionsUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "只支持 POST"})
		return
	}
	var patch struct {
		Enabled     *bool     `json:"enabled"`
		URLs        *[]string `json:"urls"`
		IntervalMin *int      `json:"intervalMin"`
		ProbeMin    *int      `json:"probeMin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeAPI(w, http.StatusBadRequest, apiResponse{Error: "参数解析失败: " + err.Error()})
		return
	}
	subStoreMu.Lock()
	cfg := subStoreData.Config
	subStoreMu.Unlock()
	if patch.Enabled != nil {
		cfg.Enabled = *patch.Enabled
	}
	if patch.URLs != nil {
		var urls []string
		for _, u := range *patch.URLs {
			u = strings.TrimSpace(u)
			if u == "" {
				continue
			}
			if pu, err := url.Parse(u); err != nil || pu.Scheme != "http" && pu.Scheme != "https" {
				writeAPI(w, http.StatusBadRequest, apiResponse{Error: "订阅地址无效: " + maskSubURL(u)})
				return
			}
			urls = append(urls, u)
		}
		cfg.URLs = urls
	}
	if patch.IntervalMin != nil {
		if *patch.IntervalMin < 30 || *patch.IntervalMin > 1440 {
			writeAPI(w, http.StatusBadRequest, apiResponse{Error: "刷新间隔需在 30-1440 分钟之间"})
			return
		}
		cfg.IntervalMin = *patch.IntervalMin
	}
	if patch.ProbeMin != nil {
		if *patch.ProbeMin != 0 && (*patch.ProbeMin < 5 || *patch.ProbeMin > 1440) {
			writeAPI(w, http.StatusBadRequest, apiResponse{Error: "检测间隔需在 5-1440 分钟之间"})
			return
		}
		cfg.ProbeMin = *patch.ProbeMin
	}
	subStoreMu.Lock()
	subStoreData.Config = cfg
	subStoreMu.Unlock()
	saveSubStore()

	// 异步触发刷新, 立即返回新配置状态
	go func() {
		if err := refreshSubscriptions("保存"); err != nil {
			log.Printf("[订阅] 保存后刷新失败: %v", err)
		}
	}()
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Data: getSubStatus()})
}

// handleSubscriptionsRefresh POST /admin/api/subscriptions/refresh (同步等待刷新完成)
func handleSubscriptionsRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "只支持 POST"})
		return
	}
	if err := refreshSubscriptions("手动"); err != nil {
		writeAPI(w, http.StatusInternalServerError, apiResponse{Error: err.Error(), Data: getSubStatus()})
		return
	}
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Data: getSubStatus()})
}

// subPreferredAddLocked / subPreferredRemoveLocked 优先队列维护(调用方须持 subStoreMu)
func subPreferredRemoveLocked(proxyURL string) {
	for i, p := range subStoreData.Preferred {
		if p == proxyURL {
			subStoreData.Preferred = append(subStoreData.Preferred[:i], subStoreData.Preferred[i+1:]...)
			return
		}
	}
}

// subPreferredSnapshot 优先队列副本(pickZenProxy 拨号路径使用)
func subPreferredSnapshot() []string {
	subStoreMu.Lock()
	defer subStoreMu.Unlock()
	return append([]string(nil), subStoreData.Preferred...)
}

// handleSubscriptionsProbe POST /admin/api/subscriptions/probe
// body 空/{} = 全量检测(异步: 立即返回, 结果经 probeSummary 轮询获取);
// {"localPort":N} = 单节点检测(后台节点行的「检测」按钮, 同步等待)
func handleSubscriptionsProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "只支持 POST"})
		return
	}
	var req struct {
		LocalPort int `json:"localPort"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req) // 空 body = 全量
	if req.LocalPort > 0 {
		res, err := probeSingleNode(req.LocalPort)
		if err != nil {
			writeAPI(w, http.StatusBadRequest, apiResponse{Error: err.Error(), Data: getSubStatus()})
			return
		}
		writeAPI(w, http.StatusOK, apiResponse{Success: true, Data: map[string]any{"node": res, "status": getSubStatus()}})
		return
	}
	// 全量检测耗时可达分钟级(节点数×超时/并发), 改为后台执行避免 HTTP 长挂;
	// 重复触发由 probeSubNodes 的 CAS 兜底, 结果统一写入 probeSummary。
	if subProbing.Load() {
		writeAPI(w, http.StatusConflict, apiResponse{Error: "节点检测正在进行中, 请稍后再试", Data: getSubStatus()})
		return
	}
	go probeSubNodes("手动")
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Data: getSubStatus()})
}

// probeSingleNodeResult 单节点检测结果
type probeSingleNodeResult struct {
	LocalPort int    `json:"localPort"`
	Name      string `json:"name"`
	OK        bool   `json:"ok"`
	LatencyMs int    `json:"latencyMs,omitempty"`
	Error     string `json:"error,omitempty"`
	Status    string `json:"status"` // ok / banned / deleted / failed / dead(暂留)
	Region    string `json:"region,omitempty"`
	ExitIP    string `json:"exitIP,omitempty"`
}

// probeSingleNode 单独检测一个节点: 结果写回健康表并持久化;
// 连续失败达到阈值与全量检测同一失效语义(删除); 封禁节点仅探测展示, 状态不变。
func probeSingleNode(localPort int) (*probeSingleNodeResult, error) {
	// 与全量探测/订阅刷新互斥: 并发探测同一节点会互相覆盖健康写回,
	// 刷新中的 Box 重建窗口则会把健康节点误记失败。
	if subProbing.Load() || subRefreshing.Load() {
		return nil, fmt.Errorf("全量检测或订阅刷新进行中, 请稍后再试")
	}
	// 定位节点
	subLivesMu.Lock()
	var urlKey string
	var node *subNodeInfo
	for _, lv := range subLives {
		for i := range lv.nodes {
			if lv.nodes[i].LocalPort == localPort {
				urlKey = lv.urlKey
				node = &lv.nodes[i]
			}
		}
	}
	subLivesMu.Unlock()
	if node == nil {
		return nil, fmt.Errorf("节点不存在或未运行 (本地端口 %d)", localPort)
	}
	key := urlKey + ":" + node.Key
	res := &probeSingleNodeResult{LocalPort: localPort, Name: node.Name}

	subStoreMu.Lock()
	banned := subStoreData.Health[key] != nil && subStoreData.Health[key].Status == subHealthBanned
	subStoreMu.Unlock()

	latency, perr := probeSubNode(localPort)
	now := time.Now().Format("2006-01-02 15:04:05")

	if banned {
		res.Status = subHealthBanned
		if perr == nil {
			res.OK = true
			res.LatencyMs = latency
		} else {
			res.Error = perr.Error()
		}
		return res, nil
	}

	if perr == nil {
		res.OK = true
		res.LatencyMs = latency
		res.Status = subHealthOK
		// 顺带刷新出口地理位置(用户主动检测, 允许覆盖旧值)
		res.Region, res.ExitIP = subProbeRegion(localPort)
		subStoreMu.Lock()
		h := subStoreData.Health[key]
		if h == nil {
			h = &subNodeHealth{}
			subStoreData.Health[key] = h
		}
		h.Status = subHealthOK
		h.Fails = 0
		h.LastOK = now
		h.LastProbe = now
		h.LatencyMs = latency
		if res.Region != "" {
			h.Region = res.Region
			h.ExitIP = res.ExitIP
		}
		subStoreMu.Unlock()
		// 刷新视图(地理位置/健康状态即时可见)并保存
		subStoreMu.Lock()
		subStoreData.Status.Nodes = subscriptionNodeViews()
		subStoreMu.Unlock()
		saveSubStore()
		return res, nil
	}

	res.Error = perr.Error()
	subStoreMu.Lock()
	h := subStoreData.Health[key]
	if h == nil {
		h = &subNodeHealth{}
		subStoreData.Health[key] = h
	}
	h.Fails++
	h.LastFail = now
	h.LastProbe = now
	fails := h.Fails
	if fails >= subDeadThreshold {
		h.Status = subHealthDead
	}
	subStoreMu.Unlock()

	if fails >= subDeadThreshold {
		res.Status = "deleted"
		removeDeadNodes(map[string]bool{key: true}) // 内部刷新视图并保存
		return res, nil
	}
	res.Status = "failed"
	subStoreMu.Lock()
	subStoreData.Status.Nodes = subscriptionNodeViews()
	subStoreMu.Unlock()
	saveSubStore()
	return res, nil
}

// handleSubscriptionsUnban POST /admin/api/subscriptions/unban (清除全部封禁标记)
func handleSubscriptionsUnban(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPI(w, http.StatusMethodNotAllowed, apiResponse{Error: "只支持 POST"})
		return
	}
	cleared := subUnbanAll()
	writeAPI(w, http.StatusOK, apiResponse{Success: true, Data: map[string]any{"cleared": cleared, "status": getSubStatus()}})
}
