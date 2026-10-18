package app

import (
	"fmt"
	"strings"
)

// ============================================================================
// RTK 工具输出预压缩 (9Router RTK 保守移植)
// 机制: 发往 zen 上游前, 对超过阈值的 tool_result 按输出类型做确定性过滤/截断,
//       并追加显式省略标记, 模型知道信息不完整时可重跑更精确的命令取回原文。
// 保守原则:
//   - level 0 默认关闭, 不影响任何现有行为
//   - 只处理无歧义的安全类型 (文件列表/目录树/git status), git diff 仅 level>=2
//   - 同一输入恒定输出 (利于上游 prompt cache), 不含时间戳等易变信息
//   - 与 compact.go 互补: RTK 是"单条超长输出提前瘦身"(预防), compact 是
//     "上下文总长超限后摘要重组"(补救)
// ============================================================================

// rtkNoiseKeywords 噪音行特征: 依赖目录/锁文件/构建产物, 与代码逻辑无关
var rtkNoiseKeywords = []string{
	"node_modules/", "/node_modules",
	"/vendor/", "\\vendor\\",
	".min.js", ".min.css",
	"package-lock.json", "yarn.lock", "pnpm-lock.yaml",
	"dist/", "build/", "target/",
}

// rtkClassify 依据输出头部/整体特征判断 tool_result 类型。
// 只识别高置信度类型, 其余一律归为 other (永不压缩)。
func rtkClassify(s string) string {
	head := s
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		head = s[:i]
	}
	head = strings.TrimSpace(head)

	// git diff: 首行 diff --git 或 diff -u 等
	if strings.HasPrefix(head, "diff --git") || strings.HasPrefix(head, "diff -") {
		return "git-diff"
	}
	// git status: 首行固定格式
	if strings.HasPrefix(head, "On branch ") || head == "HEAD detached" ||
		strings.HasPrefix(head, "HEAD detached at") {
		return "git-status"
	}
	// 文件列表类: tree 输出含树枝符号; ls/find 输出多为路径行。
	// 前 5 个非空行中过半匹配路径特征才认定, 避免误伤普通文本。
	lines := strings.Split(s, "\n")
	checked, listHits := 0, 0
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		checked++
		if strings.Contains(ln, "├──") || strings.Contains(ln, "└──") || strings.Contains(ln, "│") {
			listHits++
		} else if strings.HasPrefix(ln, "/") || strings.HasPrefix(ln, "./") ||
			strings.HasPrefix(ln, "../") || rtkLooksLikeFilePath(ln) {
			listHits++
		}
		if checked >= 5 {
			break
		}
	}
	if checked > 0 && listHits*2 >= checked {
		return "file-list"
	}
	return "other"
}

// rtkLooksLikeFilePath 行尾为常见源码/文档扩展名即视为路径行
func rtkLooksLikeFilePath(ln string) bool {
	for _, ext := range []string{".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".rs", ".java",
		".json", ".yaml", ".yml", ".toml", ".md", ".txt", ".html", ".css"} {
		if strings.HasSuffix(ln, ext) {
			return true
		}
	}
	return false
}

// rtkFilterLines 行过滤: 去空行 + 去噪音行 + 去相邻重复行 (均为确定性操作)。
// 仅用于文件列表/git status 类输出, 空行无信息量, 一并去除。
func rtkFilterLines(lines []string, dedup, noise bool) []string {
	out := make([]string, 0, len(lines))
	var prev string
	for _, ln := range lines {
		trimmed := strings.TrimRight(ln, "\r")
		if strings.TrimSpace(trimmed) == "" {
			continue
		}
		if noise && isRTKNoiseLine(trimmed) {
			continue
		}
		if dedup && trimmed == prev && trimmed != "" {
			continue
		}
		prev = trimmed
		out = append(out, trimmed)
	}
	return out
}

func isRTKNoiseLine(line string) bool {
	for _, kw := range rtkNoiseKeywords {
		if strings.Contains(line, kw) {
			return true
		}
	}
	return false
}

// rtkTruncateLines 头尾保留 + 中段替换为省略标记 (不含任何输入相关内容,
// 纯计数, 保证同输入同输出)。调用前 lines 已过滤。
func rtkTruncateLines(lines []string, keepHead, keepTail int) string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if keepHead+keepTail >= len(lines) {
		return strings.Join(lines, "\n") + "\n"
	}
	omitted := len(lines) - keepHead - keepTail
	var b strings.Builder
	for i := 0; i < keepHead && i < len(lines); i++ {
		b.WriteString(lines[i])
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "[... RTK compressed: %d lines omitted. Re-run with a narrower command/scope if you need the full output ...]\n", omitted)
	for i := len(lines) - keepTail; i < len(lines); i++ {
		b.WriteString(lines[i])
		b.WriteByte('\n')
	}
	return b.String()
}

// rtkEffective 返回填补默认值后的有效配置
func (c zenRTKConfig) rtkEffective() zenRTKConfig {
	e := c
	if e.Level < 0 {
		e.Level = 0
	}
	if e.Level > 2 {
		e.Level = 2
	}
	if e.MinChars <= 0 {
		e.MinChars = 20000
	}
	if e.KeepHead <= 0 {
		e.KeepHead = 30
	}
	if e.KeepTail <= 0 {
		e.KeepTail = 15
	}
	return e
}

// rtkCompressToolOutput 单条工具输出压缩。
// 返回 (新内容, 节省的估算 token)。不满足条件时原样返回。
func rtkCompressToolOutput(s string, cfg zenRTKConfig) (string, int) {
	if cfg.Level <= 0 || len(s) < cfg.MinChars {
		return s, 0
	}
	class := rtkClassify(s)
	keepHead, keepTail := cfg.KeepHead, cfg.KeepTail
	var lines []string
	switch class {
	case "git-diff":
		if cfg.Level < 2 {
			return s, 0
		}
		// diff 头部信息量大(文件名/@@ 行号), 保留更多头部
		keepHead, keepTail = keepHead*4, keepTail*2
		lines = strings.Split(s, "\n")
	case "file-list", "git-status":
		lines = rtkFilterLines(strings.Split(s, "\n"), cfg.GrepDedup, cfg.FilterNoise)
	default:
		return s, 0
	}
	out := rtkTruncateLines(lines, keepHead, keepTail)
	before, after := estimateText(s), estimateText(out)
	if after >= before {
		return s, 0
	}
	return out, before - after
}

// applyRTK 对 OpenAI 格式 messages 中所有 tool 消息应用 RTK 压缩。
// 直接原地修改 params, 返回本请求节省的估算 token 总量 (无变化返回 0)。
// Anthropic 路径的 tool_result 已由 anthropicToOpenAI 转为 role:"tool" 字符串,
// 三条入口 (chat/anthropic/responses) 汇合后统一走这里。
func applyRTK(params map[string]any) int {
	cfg := getZenConfig().RTK.rtkEffective()
	if cfg.Level <= 0 {
		return 0
	}
	msgs, _ := params["messages"].([]any)
	if len(msgs) == 0 {
		return 0
	}
	saved := 0
	for _, m := range msgs {
		mm, ok := m.(map[string]any)
		if !ok || strField(mm, "role") != "tool" {
			continue
		}
		switch c := mm["content"].(type) {
		case string:
			nc, s := rtkCompressToolOutput(c, cfg)
			if s > 0 {
				mm["content"] = nc
				saved += s
			}
		case []any:
			// OpenAI tool content 数组形式 [{type:"text",text:"..."}]
			for _, part := range c {
				pb, ok := part.(map[string]any)
				if !ok {
					continue
				}
				if t, ok := pb["text"].(string); ok {
					nt, s := rtkCompressToolOutput(t, cfg)
					if s > 0 {
						pb["text"] = nt
						saved += s
					}
				}
			}
		}
	}
	return saved
}
