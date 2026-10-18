package app

import "testing"

func TestResponsesToChat(t *testing.T) {
	body := map[string]any{
		"model":             "deepseek/deepseek-v4-flash",
		"stream":            true,
		"max_output_tokens": 4096.0,
		"instructions":      "you are helpful",
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": "hi"},
			map[string]any{"type": "function_call", "call_id": "call_1", "name": "bash", "arguments": map[string]any{"cmd": "ls"}},
			map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "file.txt"},
		},
	}
	out := responsesToChat(body)
	if out["model"] != "deepseek/deepseek-v4-flash" {
		t.Errorf("model = %v", out["model"])
	}
	if out["max_tokens"] != 4096 {
		t.Errorf("max_tokens = %v", out["max_tokens"])
	}
	msgs, ok := out["messages"].([]any)
	if !ok || len(msgs) != 4 {
		t.Fatalf("messages = %v, want 4 (system + 3)", msgs)
	}
	sys, _ := msgs[0].(map[string]any)
	if sys["role"] != "system" || sys["content"] != "you are helpful" {
		t.Errorf("first message not system: %v", sys)
	}
	tool, _ := msgs[2].(map[string]any)
	if tool["role"] != "assistant" {
		t.Errorf("function_call not mapped to assistant: %v", tool)
	}
}

func TestResponsesInputString(t *testing.T) {
	if msgs := responsesInputToMessages("just text"); len(msgs) != 1 {
		t.Fatalf("string input msgs = %v", msgs)
	} else if msgs[0].(map[string]any)["content"] != "just text" {
		t.Errorf("string input content = %v", msgs[0])
	}
}

func TestStringifyResponsesContent(t *testing.T) {
	if got := stringifyResponsesContent("abc"); got != "abc" {
		t.Errorf("plain string = %q", got)
	}
	if got := stringifyResponsesContent([]any{map[string]any{"type": "text", "text": "a"}, map[string]any{"type": "text", "text": "b"}}); got != "a\nb" {
		t.Errorf("block array = %q", got)
	}
}
