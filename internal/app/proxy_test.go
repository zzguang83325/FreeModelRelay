package app

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseHumanDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"17h 59m", 17*time.Hour + 59*time.Minute},
		{"2h", 2 * time.Hour},
		{"59m", 59 * time.Minute},
		{"30s", 30 * time.Second},
		{"1d 2h 30m", 26*time.Hour + 30*time.Minute},
		{"5ms", 5 * time.Millisecond},
		{"", 0},
		{"garbage", 0},
		{"0", 0},
	}
	for _, c := range cases {
		if got := parseHumanDuration(c.in); got != c.want {
			t.Errorf("parseHumanDuration(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseInferenceCapDuration(t *testing.T) {
	cases := []struct {
		body string
		want time.Duration
	}{
		{`{"error":{"message":"Inference cap reached. Try again in 17h 59m"}}`, 17*time.Hour + 59*time.Minute},
		{`Try again in 2h`, 2 * time.Hour},
		{`no duration here`, 0},
		{``, 0},
	}
	for _, c := range cases {
		if got := parseInferenceCapDuration(c.body); got != c.want {
			t.Errorf("parseInferenceCapDuration(%q) = %v, want %v", c.body, got, c.want)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"120", 120 * time.Second},
		{"", 0},
		{"0", 0},
		{"not-a-number", 0},
	}
	for _, c := range cases {
		if got := parseRetryAfter(c.in); got != c.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	// HTTP 日期(RFC 7231 要求 GMT): 未来时间返回剩余时长, 过去时间返回 0
	future := time.Now().Add(1 * time.Hour).UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT")
	if got := parseRetryAfter(future); got <= 0 || got > 2*time.Hour {
		t.Errorf("parseRetryAfter(future date) = %v, want in (0, 2h]", got)
	}
}

func TestParseToolArgs(t *testing.T) {
	cases := []struct {
		in   string
		want string // JSON 序列化后期望值的不同键存在性/值
		fail bool
	}{
		{`{"file_path":"a.txt"}`, `{"file_path":"a.txt"}`, false},
		{`""{"file_path":"a.txt"}`, `{"file_path":"a.txt"}`, false}, // 杂散引号前缀
		{`{"k":"v"}`, `{"k":"v"}`, false},
		{``, `{}`, false},
		{`"{"double":"wrapped"}"`, `{"double":"wrapped"}`, false},
		{`whatever{"k":1}`, `{"k":1}`, false}, // 从杂散内容提取
		{`not json at all`, ``, true},
	}
	for _, c := range cases {
		got, err := parseToolArgs(c.in)
		if c.fail {
			if err == nil {
				t.Errorf("parseToolArgs(%q) expected error, got %v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseToolArgs(%q) unexpected error: %v", c.in, err)
			continue
		}
		b, _ := json.Marshal(got)
		if string(b) != c.want {
			t.Errorf("parseToolArgs(%q) = %s, want %s", c.in, b, c.want)
		}
	}
}

func TestGetNested(t *testing.T) {
	obj := map[string]any{
		"a": map[string]any{
			"b": []any{map[string]any{"c": "deep"}},
		},
	}
	if got := getNested(obj, "a", "b", 0, "c"); got != "deep" {
		t.Errorf("getNested deep = %v, want deep", got)
	}
	if got := getNested(obj, "a", "missing"); got != nil {
		t.Errorf("getNested missing = %v, want nil", got)
	}
	if got := getNested(obj, "a", "b", 5); got != nil {
		t.Errorf("getNested out of range = %v, want nil", got)
	}
}
