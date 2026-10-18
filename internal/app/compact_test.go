package app

import (
	"strings"
	"testing"
)

func TestSerializeMsg(t *testing.T) {
	cases := []struct {
		in   map[string]any
		want string
	}{
		{map[string]any{"role": "user", "content": "hello"}, "[User]: hello"},
		{map[string]any{"role": "system", "content": "be nice"}, "[System update]: be nice"},
		{map[string]any{"role": "tool", "content": "42"}, "[Tool result]: 42"},
		{map[string]any{"role": "assistant", "content": "ok"}, "[Assistant]: ok"},
		{map[string]any{"role": "assistant"}, ""},
		{
			map[string]any{
				"role":              "assistant",
				"content":           "thinking",
				"reasoning_content": "hidden",
				"tool_calls": []any{
					map[string]any{
						"function": map[string]any{"name": "read", "arguments": `{"path":"a"}`},
					},
				},
			},
			"[Assistant]: thinking\n[Assistant reasoning]: hidden\n[Assistant tool call]: read({\"path\":\"a\"})",
		},
	}
	for _, c := range cases {
		if got := serializeMsg(c.in); got != c.want {
			t.Errorf("serializeMsg(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSerializeMsgTruncateToolResult(t *testing.T) {
	long := strings.Repeat("x", toolOutputMaxChars+100)
	got := serializeMsg(map[string]any{"role": "tool", "content": long})
	if !strings.HasPrefix(got, "[Tool result]: ") {
		t.Fatalf("unexpected tool serialization: %q", got)
	}
	if !strings.HasSuffix(got, "[truncated]") {
		t.Errorf("tool result not truncated, suffix missing: got length %d", len(got))
	}
}

func TestSelectRecentFitsAll(t *testing.T) {
	msgs := []string{"[User]: a", "[User]: b"}
	res := selectRecent(msgs, 10000)
	// 全部放得进 recent 时 head 为空(无需摘要), 直接返回 nil
	if res != nil {
		t.Errorf("selectRecent all-fit = %+v, want nil", res)
	}
}

func TestSelectRecentSplitsTail(t *testing.T) {
	// 每条约 1000 字节 => estimateText 约 250 token
	big := strings.Repeat("x", 1000)
	msgs := []string{"[User]: " + big, "[Assistant]: " + big, "[User]: " + big}
	res := selectRecent(msgs, 300)
	if res == nil {
		t.Fatal("selectRecent should return a split result")
	}
	if res.split <= 0 {
		t.Fatalf("selectRecent split = %d, want > 0", res.split)
	}
	if len(res.head) == 0 && len(res.recent) == 0 {
		t.Error("selectRecent produced no content")
	}
	// recent 总 token 不超过预算 + 一条消息余量,保证重组后不超限
	if est := estimateText(strings.Join(res.recent, "\n\n")); est > 300+250 {
		t.Errorf("selectRecent recent est=%d, exceeds budget 300", est)
	}
}

func TestEstimateTextAndJSON(t *testing.T) {
	if got := estimateText("abcd"); got != 1 {
		t.Errorf("estimateText = %d, want 1 (len/4)", got)
	}
	if got := estimateJSON(map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi"}}}); got <= 0 {
		t.Errorf("estimateJSON = %d, want > 0", got)
	}
}
