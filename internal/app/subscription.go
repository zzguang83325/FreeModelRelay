package app

import (
	"context"
	"crypto/sha1"
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
}

// subNodeInfo 一个订阅节点(解析结果 + 分配的本地端口)
type subNodeInfo struct {
	Key       string // 稳定标识: 协议|服务器|端口|凭据哈希, 决定端口分配
	Name      string
	Protocol  string // trojan / vless / hysteria2
	Server    string
	Port      int // 远程端口
	LocalPort int // 本地 socks 端口
	Outbound  string
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
}

type subStatusData struct {
	LastRefresh string        `json:"lastRefresh,omitempty"`
	LastError   string        `json:"lastError,omitempty"`
	Nodes       []subNodeView `json:"nodes"`
}

type subStore struct {
	Config subscriptionConfigData `json:"config"`
	Ports  map[string]int         `json:"ports"`  // 节点Key -> 本地端口(稳定复用)
	Status subStatusData          `json:"status"` // 上次刷新结果快照(重启后 UI 可见)
}

var (
	subStoreMu    sync.Mutex
	subStoreData  = subStore{Config: subscriptionConfigData{IntervalMin: 360}, Ports: map[string]int{}}
	subLivesMu    sync.Mutex
	subLives      = map[string]*subLive{} // urlKey -> live
	subRefreshing atomic.Bool
	subBoxCtxOnce sync.Once
	subBoxCtx     context.Context
)

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
	if st.Ports == nil {
		st.Ports = map[string]int{}
	}
	if st.Config.IntervalMin <= 0 {
		st.Config.IntervalMin = 360
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
	sum := sha1.Sum([]byte(n.Protocol + "|" + n.Server + "|" + strconv.Itoa(n.Port) + "|" + n.Outbound))
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

// portFree 探测本地端口是否可绑定(避免整个 Box 因单端口冲突启动失败)
func portFree(p int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// assignSubPorts 为节点分配本地端口: 已知 Key 复用旧端口(重启稳定), 新节点取空闲端口。
// used 跨订阅共享, 防止同一节点出现在多个订阅时端口冲突。
func assignSubPorts(urlKey string, nodes []subNodeInfo, used map[int]bool) {
	subStoreMu.Lock()
	ports := subStoreData.Ports
	for i := range nodes {
		n := &nodes[i]
		key := urlKey + ":" + n.Key
		p, ok := ports[key]
		if ok && p > 0 && !used[p] && portFree(p) {
			n.LocalPort = p
			used[p] = true
			continue
		}
		np := allocSubPort(used)
		if np == 0 {
			log.Printf("[订阅] 端口池耗尽(%d-%d), 跳过节点 %s", subPortBase, subPortBase+subPortRange-1, n.Name)
			continue
		}
		n.LocalPort = np
		used[np] = true
		ports[key] = np
	}
	subStoreMu.Unlock()
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

// subscriptionProxyURLs 当前生效的订阅 socks 地址(按端口排序, 稳定轮询)
func subscriptionProxyURLs() []string {
	subLivesMu.Lock()
	defer subLivesMu.Unlock()
	type entry struct {
		port int
		url  string
	}
	var entries []entry
	for _, lv := range subLives {
		for _, n := range lv.nodes {
			if n.LocalPort > 0 {
				entries = append(entries, entry{n.LocalPort, fmt.Sprintf("socks5://127.0.0.1:%d", n.LocalPort)})
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].port < entries[j].port })
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.url
	}
	return out
}

// ============ 刷新流程 ============

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

	used := map[int]bool{}
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
			continue // 保留旧实例继续服务
		}
		nodes, parseFailed, firstErr := parseSubscriptionContent(content)
		if parseFailed > 0 {
			log.Printf("[订阅] %s 有 %d 个节点解析失败(首个: %s)", masked, parseFailed, firstErr)
		}
		if len(nodes) == 0 {
			errs = append(errs, masked+" 无可用节点")
			continue
		}
		assignSubPorts(urlKey, nodes, used)
		// 停旧起新: 端口可能复用, 必须先停后起
		subLivesMu.Lock()
		old := subLives[urlKey]
		subLivesMu.Unlock()
		if old != nil {
			stopSubBox(old.box)
			time.Sleep(200 * time.Millisecond) // 等待旧监听释放
		}
		b, err := startSubBox(urlKey, nodes)
		if err != nil {
			errs = append(errs, masked+" 启动失败: "+err.Error())
			log.Printf("[订阅] %s 启动失败: %v", masked, err)
			continue
		}
		subLivesMu.Lock()
		subLives[urlKey] = &subLive{urlKey: urlKey, nodes: nodes, box: b}
		subLivesMu.Unlock()
		total += len(nodes)
		log.Printf("[订阅] %s 启动 %d 个节点", masked, len(nodes))
	}

	// 清理已消失节点的端口记录
	liveKeys := map[string]bool{}
	subLivesMu.Lock()
	for _, lv := range subLives {
		for _, n := range lv.nodes {
			liveKeys[lv.urlKey+":"+n.Key] = true
		}
	}
	subLivesMu.Unlock()
	subStoreMu.Lock()
	for k := range subStoreData.Ports {
		if !liveKeys[k] {
			delete(subStoreData.Ports, k)
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

// subscriptionNodeViews 调用方需持有 subStoreMu(直接写入 Status.Nodes)
func subscriptionNodeViews() []subNodeView {
	var views []subNodeView
	for _, lv := range subLives {
		for _, n := range lv.nodes {
			if n.LocalPort > 0 {
				views = append(views, subNodeView{
					Name: n.Name, Protocol: n.Protocol, Server: n.Server,
					RemotePort: n.Port, LocalPort: n.LocalPort,
				})
			}
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

// ============ 后台定时刷新 ============

func startSubscriptionManager() {
	cfg := getSubConfig()
	if cfg.Enabled && len(cfg.URLs) > 0 {
		// 启动 3 秒后首次刷新(错开账号预热等启动任务)。
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
}

// ============ 后台 API ============

func getSubStatus() map[string]any {
	cfg := getSubConfig()
	subStoreMu.Lock()
	status := subStoreData.Status
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
	return map[string]any{
		"enabled":     cfg.Enabled,
		"urls":        cfg.URLs,
		"intervalMin": cfg.IntervalMin,
		"nodes":       status.Nodes,
		"liveCount":   liveCount,
		"lastRefresh": status.LastRefresh,
		"lastError":   status.LastError,
		"updating":    subRefreshing.Load(),
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
