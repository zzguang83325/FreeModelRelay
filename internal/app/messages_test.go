package app

import (
	"encoding/json"
	"testing"
)

// TestCleanMessagesDeveloperRole 回归: Codex CLI 走 /v1/responses 时首条消息
// 的 role 是 developer, 原样透传给 qoder 会 400:
//
//	"'developer' is not one of ['system','assistant','user','tool','function']"
func TestCleanMessagesDeveloperRole(t *testing.T) {
	in := []any{
		map[string]any{"role": "developer", "content": "env context"},
		map[string]any{"role": "user", "content": "hi"},
	}
	out := cleanMessages(in)

	if len(out) != 2 {
		t.Fatalf("期望 2 条消息, 得到 %d", len(out))
	}
	first := out[0].(map[string]any)
	if first["role"] != "system" {
		t.Fatalf("developer 应折叠为 system, 得到 %v", first["role"])
	}
	if first["content"] != "env context" {
		t.Fatalf("内容被改动: %v", first["content"])
	}
	// 不能就地修改调用方传入的 map: 重试路径会复用同一份 params
	if in[0].(map[string]any)["role"] != "developer" {
		t.Fatal("cleanMessages 不应修改入参 map")
	}
}

// TestCleanMessagesMergeSystem 多条 system 合并到首条, 顺序保持。
func TestCleanMessagesMergeSystem(t *testing.T) {
	in := []any{
		map[string]any{"role": "system", "content": "A"},
		map[string]any{"role": "developer", "content": "B"},
		map[string]any{"role": "user", "content": "hi"},
	}
	out := cleanMessages(in)

	if len(out) != 2 {
		t.Fatalf("两条 system 应合并为一条, 得到 %d 条", len(out))
	}
	first := out[0].(map[string]any)
	if first["role"] != "system" {
		t.Fatalf("合并后首条应为 system, 得到 %v", first["role"])
	}
	if first["content"] != "A\n\nB" {
		t.Fatalf("合并顺序/分隔符不对: %q", first["content"])
	}
	if out[1].(map[string]any)["role"] != "user" {
		t.Fatal("user 消息应保留在合并后的 system 之后")
	}
}

// TestCleanMessagesSingleSystemUntouched 单条 system 不重排、不改写。
func TestCleanMessagesSingleSystemUntouched(t *testing.T) {
	in := []any{
		map[string]any{"role": "user", "content": "hi"},
		map[string]any{"role": "system", "content": "S"},
	}
	out := cleanMessages(in)

	if len(out) != 2 {
		t.Fatalf("不应增删消息, 得到 %d 条", len(out))
	}
	if out[0].(map[string]any)["role"] != "user" || out[1].(map[string]any)["role"] != "system" {
		t.Fatal("单条 system 不应被移动")
	}
}

// TestCleanMessagesMultimodalSystemNotMerged 含非文本内容的 system 不合并,
// 否则会丢图片。
func TestCleanMessagesMultimodalSystemNotMerged(t *testing.T) {
	in := []any{
		map[string]any{"role": "system", "content": []any{
			map[string]any{"type": "text", "text": "look"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:..."}},
		}},
		map[string]any{"role": "developer", "content": "ctx"},
	}
	out := cleanMessages(in)

	if len(out) != 2 {
		t.Fatalf("含多模态的 system 不应被合并, 得到 %d 条", len(out))
	}
	for i, m := range out {
		if m.(map[string]any)["role"] != "system" {
			t.Fatalf("第 %d 条应为 system, 得到 %v", i, m.(map[string]any)["role"])
		}
	}
}

// TestCleanMessagesPreservesOtherRoles 已合法的角色不得被改写。
func TestCleanMessagesPreservesOtherRoles(t *testing.T) {
	in := []any{
		map[string]any{"role": "system", "content": "S"},
		map[string]any{"role": "assistant", "content": "", "tool_calls": []any{}},
		map[string]any{"role": "tool", "content": "out", "tool_call_id": "c1"},
		map[string]any{"role": "user", "content": "go"},
	}
	out := cleanMessages(in)

	if len(out) != 4 {
		t.Fatalf("不应增删消息, 得到 %d 条", len(out))
	}
	for i, want := range []string{"system", "assistant", "tool", "user"} {
		if got := out[i].(map[string]any)["role"]; got != want {
			t.Fatalf("第 %d 条角色应为 %s, 得到 %v", i, want, got)
		}
	}
}

// TestCleanMessagesEmptyAndOpaque 空输入与非法元素原样返回。
func TestCleanMessagesEmptyAndOpaque(t *testing.T) {
	if out := cleanMessages(nil); len(out) != 0 {
		t.Fatalf("nil 输入应返回空, 得到 %d 条", len(out))
	}
	in := []any{"raw-string", 42}
	out := cleanMessages(in)
	if len(out) != 2 {
		t.Fatalf("非 map 元素应保留, 得到 %d 条", len(out))
	}
}

// TestQoderBodyDeveloperRole Qoder 请求体最终不能再出现 developer。
func TestQoderBodyDeveloperRole(t *testing.T) {
	params := map[string]any{
		"model": "qoder/Qwen3.8-Flash · free",
		"messages": []any{
			map[string]any{"role": "developer", "content": "env"},
			map[string]any{"role": "user", "content": "hi"},
		},
	}
	body := qoderBuildBody(params, qoderModelInfo{Key: "Qwen3.8-Flash"})
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(raw); containsRole(got, "developer") {
		t.Fatalf("qoder 请求体仍含 developer 角色: %s", got)
	}
}

func containsRole(body, role string) bool {
	var parsed struct {
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return true
	}
	for _, m := range parsed.Messages {
		if m.Role == role {
			return true
		}
	}
	return false
}
