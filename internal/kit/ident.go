package kit

import (
	"crypto/rand"
	"sync"
	"time"
)

func WithRetryJitter(delay time.Duration) time.Duration {
	if delay <= 0 {
		return delay
	}
	// 在退避时间上增加 0%~25% 抖动，避免多个请求同时重试。
	jitter := time.Duration(float64(delay) * float64(RandIntn(26)) / 100)
	const maxDuration = time.Duration(1<<63 - 1)
	if delay > maxDuration-jitter {
		return maxDuration
	}
	return delay + jitter
}

// ============================================================================
// 客户端身份: 模拟真实 opencode 客户端
// 官方 identifier 为 prefix_ + 26 字符(前 12 位时间排序 hex + 后 14 位 base62 随机)。
// 2026-10-14 抓包实测(opencode CLI 1.18.31): UA 为 SDK 拼接格式
// "opencode/<ver> ai-sdk/provider-utils/<v> runtime/bun/<v>";
// 免费通道 403 FreeTierError 的真实门槛是请求体形态(tools 须含 bash+read),
// UA/头/ID 算法均不构成独立校验, 但保持与真实客户端一致最稳。
// ============================================================================

// zenBase62 官方 identifier 随机段使用的 base62 字母表
const zenBase62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

const hexLower = "0123456789abcdef"

// 官方 identifier 算法(v1.19.0 packages/opencode/src/id/id.ts, descending):
// now = 毫秒*0x1000 + 同毫秒自增计数, 取按位补码后低 48 位以 6 字节大端编码为
// 12 位 hex, 后接 14 位随机 base62, 共 26 位。低 36 位内嵌毫秒时间戳(约 795 天
// 回绕), 服务端可解码校验新鲜度 —— 因此不能用任意随机 hex 冒充(免费通道实测
// sha 派生形 ID 无法通过, 官方时间戳形可 200)。
var (
	zenIDMu      sync.Mutex
	zenIDLastMS  int64
	zenIDCounter uint64
)

// ZenOfficialID 按官方 id.ts descending 算法生成 ID。
// prefix 为 "ses"/"msg" 等前缀(不带下划线)。
func ZenOfficialID(prefix string) string {
	zenIDMu.Lock()
	ms := time.Now().UnixMilli()
	if ms != zenIDLastMS {
		zenIDLastMS = ms
		zenIDCounter = 0
	}
	zenIDCounter++
	c := zenIDCounter
	zenIDMu.Unlock()

	return zenOfficialIDAt(prefix, ms, c)
}

// zenOfficialIDAt 官方算法核心(时间戳/计数可注入, 便于向量测试)。
func zenOfficialIDAt(prefix string, ms int64, counter uint64) string {
	x48 := ((uint64(ms) << 12) | counter) & 0xFFFFFFFFFFFF
	x48 = ^x48 & 0xFFFFFFFFFFFF // descending: 补码(BigInt 无限补码低 48 位语义)

	out := make([]byte, 0, len(prefix)+27)
	out = append(out, prefix...)
	out = append(out, '_')
	for i := 0; i < 6; i++ {
		b := byte(x48 >> (40 - 8*i))
		out = append(out, hexLower[b>>4], hexLower[b&0x0f])
	}
	rnd := make([]byte, 14)
	rand.Read(rnd)
	for _, b := range rnd {
		out = append(out, zenBase62[int(b)%len(zenBase62)])
	}
	return string(out)
}

// zenClientVersion opencode CLI 版本, 跟随官方发版更新
const zenClientVersion = "1.18.31"

// ZenUserAgent 真实 opencode 客户端 UA(抓包自 CLI 1.18.31):
// "opencode/1.18.31 ai-sdk/provider-utils/4.0.23 runtime/bun/1.4.2"
func ZenUserAgent() string {
	return "opencode/" + zenClientVersion + " ai-sdk/provider-utils/4.0.23 runtime/bun/1.4.2"
}

func RandIntn(n int) int {
	if n <= 0 {
		return 0
	}
	b := make([]byte, 4)
	rand.Read(b)
	v := int(b[0])<<24 | int(b[1])<<16 | int(b[2])<<8 | int(b[3])
	if v < 0 {
		v = -v
	}
	return v % n
}
