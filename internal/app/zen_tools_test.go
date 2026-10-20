package app

import "testing"

// TestApplyZenToolAliases 回归: Claude Code 声明的 Bash / Read 必须归一成官方
// 小写名, 否则 applyFreeTierFingerprint 会额外注入一份 bash, 模型可能选中注入
// 的那份, 回给客户端一个它不认识的工具名。
func TestApplyZenToolAliases(t *testing.T) {
	body := map[string]any{
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "Bash"}},
			map[string]any{"type": "function", "function": map[string]any{"name": "Read"}},
			map[string]any{"type": "function", "function": map[string]any{"name": "Edit"}},
			map[string]any{"type": "function", "function": map[string]any{"name": "Grep"}},
		},
		"messages": []any{
			map[string]any{"role": "user", "content": "hi"},
			map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"id": "c1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"ls"}`}},
			}},
		},
	}

	aliases := applyZenToolAliases(body)

	if len(aliases) != 2 {
		t.Fatalf("应记录 2 条映射, 得到 %d: %v", len(aliases), aliases)
	}
	if aliases["bash"] != "Bash" || aliases["read"] != "Read" {
		t.Fatalf("映射方向不对: %v", aliases)
	}

	tools := body["tools"].([]any)
	want := []string{"bash", "read", "Edit", "Grep"}
	for i, w := range want {
		got := tools[i].(map[string]any)["function"].(map[string]any)["name"]
		if got != w {
			t.Fatalf("工具 %d 应为 %s, 得到 %v", i, w, got)
		}
	}

	// 历史 tool_calls 必须同步改, 否则上游按"调用了未声明的工具"报错
	msgs := body["messages"].([]any)
	tc := msgs[1].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	if name := tc["function"].(map[string]any)["name"]; name != "bash" {
		t.Fatalf("历史 tool_calls 应同步为 bash, 得到 %v", name)
	}
}

// TestApplyZenToolAliasesNoop 无冲突时不产生任何改动。
func TestApplyZenToolAliasesNoop(t *testing.T) {
	body := map[string]any{
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "Edit"}},
			map[string]any{"type": "function", "function": map[string]any{"name": "shell"}},
		},
	}
	if aliases := applyZenToolAliases(body); len(aliases) != 0 {
		t.Fatalf("不应产生映射, 得到 %v", aliases)
	}
	tools := body["tools"].([]any)
	if n := tools[0].(map[string]any)["function"].(map[string]any)["name"]; n != "Edit" {
		t.Fatalf("Edit 不应被改动, 得到 %v", n)
	}
	if n := tools[1].(map[string]any)["function"].(map[string]any)["name"]; n != "shell" {
		t.Fatalf("shell 不应被改动, 得到 %v", n)
	}
}

// TestResolveClientToolName 响应侧把上游的小写名还原成客户端写法。
func TestResolveClientToolName(t *testing.T) {
	schemas := map[string]map[string]bool{
		"Bash": {"command": true},
		"Read": {"file_path": true},
	}

	cases := []struct{ in, want string }{
		{"bash", "Bash"},   // 上游归一后的小写名 → 客户端声明
		{"read", "Read"},   // 同上
		{"Bash", "Bash"},   // 精确匹配优先, 不改写
		{"Write", "Write"}, // 客户端没声明, 原样返回
		{"", ""},
	}
	for _, c := range cases {
		if got := resolveClientToolName(c.in, schemas); got != c.want {
			t.Fatalf("resolveClientToolName(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}

	// 无 schemas 时一律原样返回
	if got := resolveClientToolName("bash", nil); got != "bash" {
		t.Fatalf("空 schemas 应原样返回, 得到 %q", got)
	}
}

// TestZenToolAliasRoundTrip 请求侧归一 + 响应侧还原, 名字回到客户端声明。
func TestZenToolAliasRoundTrip(t *testing.T) {
	body := map[string]any{
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "Bash"}},
		},
	}
	applyZenToolAliases(body)

	schemas := map[string]map[string]bool{"Bash": {"command": true}}
	upstreamName := body["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)["name"].(string)
	if got := resolveClientToolName(upstreamName, schemas); got != "Bash" {
		t.Fatalf("往返后应还原为 Bash, 得到 %q", got)
	}
}
