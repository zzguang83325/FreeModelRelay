package app

import (
	"strings"
	"testing"
)

func TestRTKClassify(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"git diff", "diff --git a/main.go b/main.go\nindex 123..456\n--- a/main.go", "git-diff"},
		{"git status", "On branch main\nYour branch is up to date.\n\nnothing to commit", "git-status"},
		{"tree", "src/\n├── main.go\n├── pool.go\n└── util.go\n\n1 directory", "file-list"},
		{"find paths", "/root/a/main.go\n/root/b/util.go\n/root/c/x.py\n/root/d/README.md", "file-list"},
		{"plain text", "hello world\nthis is just a sentence\nnothing special here at all\nbye", "other"},
		{"error message", "Error: build failed\nexit status 1\nSee logs for details\nat pkg/x.go:1", "other"},
	}
	for _, c := range cases {
		if got := rtkClassify(c.in); got != c.want {
			t.Errorf("%s: rtkClassify = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRTKFilterLines(t *testing.T) {
	in := []string{
		"src/main.go",
		"node_modules/react/index.js",
		"src/main.go", // 相邻重复
		"",
		"package-lock.json",
		"src/pool.go",
	}
	got := rtkFilterLines(in, true, true)
	want := []string{"src/main.go", "src/pool.go"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("rtkFilterLines = %v, want %v", got, want)
	}
}

func TestRTKTruncateLines(t *testing.T) {
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "line"
	}
	out := rtkTruncateLines(lines, 30, 15)
	if !strings.Contains(out, "RTK compressed: 55 lines omitted") {
		t.Errorf("marker with count missing: %q", out)
	}
	// 头 30 行 + 尾 15 行 + 1 行标记 = 46 行
	if got := strings.Count(out, "\n"); got != 46 {
		t.Errorf("line count = %d, want 46", got)
	}
	// 行数不足时原样返回
	small := []string{"a", "b", "c"}
	if out := rtkTruncateLines(small, 30, 15); out != "a\nb\nc\n" {
		t.Errorf("small input = %q, want unchanged", out)
	}
}

func TestRTKCompressToolOutput(t *testing.T) {
	cfg := zenRTKConfig{Level: 1, MinChars: 100, KeepHead: 5, KeepTail: 3, GrepDedup: true, FilterNoise: true}

	// 低于阈值: 原样
	short := "src/a.go\nsrc/b.go\n"
	if out, saved := rtkCompressToolOutput(short, cfg); out != short || saved != 0 {
		t.Errorf("short output should be untouched, got saved=%d", saved)
	}

	// 超阈值文件列表: 压缩且节省 > 0 (用不同路径, 避免被去重折叠)
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("/root/pkg/file" + strings.Repeat("x", i%7) + ".go\n")
	}
	big := b.String()
	out, saved := rtkCompressToolOutput(big, cfg)
	if saved <= 0 {
		t.Fatalf("big file-list should be compressed, saved=%d", saved)
	}
	if !strings.Contains(out, "RTK compressed") {
		t.Errorf("marker missing in output: %q", out)
	}
	if strings.Contains(out, "node_modules") && !strings.Contains(big, "node_modules") {
		t.Error("unexpected content introduced")
	}

	// git diff 在 level 1 不处理
	diff := "diff --git a/x b/x\n" + strings.Repeat("+code\n", 200)
	if _, saved := rtkCompressToolOutput(diff, cfg); saved != 0 {
		t.Errorf("git-diff should be untouched at level 1, saved=%d", saved)
	}
	// level 2 处理 git diff
	cfg2 := cfg
	cfg2.Level = 2
	if _, saved := rtkCompressToolOutput(diff, cfg2); saved <= 0 {
		t.Errorf("git-diff should be compressed at level 2, saved=%d", saved)
	}

	// 普通文本永不处理
	text := strings.Repeat("plain sentence line\n", 100)
	if out, saved := rtkCompressToolOutput(text, cfg2); out != text || saved != 0 {
		t.Errorf("plain text must never be compressed, saved=%d", saved)
	}
}

func TestRTKDeterministic(t *testing.T) {
	cfg := zenRTKConfig{Level: 2, MinChars: 50, KeepHead: 5, KeepTail: 3, FilterNoise: true}
	var b strings.Builder
	b.WriteString("src/\n├── main.go\n")
	for i := 0; i < 100; i++ {
		b.WriteString("├── node_modules/pkg/x.js\n")
	}
	b.WriteString("└── pool.go\n")
	s := b.String()
	a1, s1 := rtkCompressToolOutput(s, cfg)
	a2, s2 := rtkCompressToolOutput(s, cfg)
	if a1 != a2 || s1 != s2 {
		t.Error("RTK must be deterministic: same input, same output")
	}
}

func TestApplyRTKDisabled(t *testing.T) {
	// 默认配置 level 0: applyRTK 必须零改动零开销
	params := map[string]any{
		"messages": []any{
			map[string]any{"role": "tool", "content": strings.Repeat("/root/x.go\n", 5000)},
		},
	}
	// 显式确保默认 level 0 (测试环境配置可能被其它测试改动)
	orig := zenConfig
	zenConfig = defaultZenConfig()
	defer func() { zenConfig = orig }()
	if saved := applyRTK(params); saved != 0 {
		t.Errorf("applyRTK at default level 0 should be no-op, saved=%d", saved)
	}
}

func TestApplyRTKToolMessages(t *testing.T) {
	orig := zenConfig
	zenConfig = defaultZenConfig()
	zenConfig.RTK.Level = 1
	zenConfig.RTK.MinChars = 100
	defer func() { zenConfig = orig }()

	// 100 行不同的长路径行
	var lb strings.Builder
	for i := 0; i < 100; i++ {
		lb.WriteString("/root/dir" + strings.Repeat("y", i%9) + "/file.go\n")
	}
	params := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "list files"},
			map[string]any{"role": "assistant", "content": "running ls"},
			map[string]any{"role": "tool", "tool_call_id": "t1", "content": lb.String()},
		},
	}
	saved := applyRTK(params)
	if saved <= 0 {
		t.Fatalf("applyRTK should compress oversized tool output, saved=%d", saved)
	}
	msgs := params["messages"].([]any)
	toolMsg := msgs[2].(map[string]any)
	content := toolMsg["content"].(string)
	if !strings.Contains(content, "RTK compressed") {
		t.Errorf("compressed content missing marker: %q", content[:200])
	}
	// user/assistant 消息不受影响
	if msgs[0].(map[string]any)["content"] != "list files" {
		t.Error("user message must not be touched")
	}
}

func TestRTKEffective(t *testing.T) {
	e := zenRTKConfig{}.rtkEffective()
	if e.MinChars != 20000 || e.KeepHead != 30 || e.KeepTail != 15 {
		t.Errorf("defaults not applied: %+v", e)
	}
	e2 := zenRTKConfig{Level: 9, MinChars: -1}.rtkEffective()
	if e2.Level != 2 {
		t.Errorf("level clamp = %d, want 2", e2.Level)
	}
}
