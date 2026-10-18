package app

import (
	"testing"
)

func TestParseCatalogModels(t *testing.T) {
	body := []byte(`{
		"opencode": {
			"models": {
				"big-pickle": {"cost": {"input": 0}, "status": "active",
					"limit": {"context": 200000, "output": 32000}},
				"mimo-v2.5-free": {"cost": {"input": 0}, "status": "active"},
				"union-alpha": {"cost": {"input": 0}, "status": "active",
					"provider": {"npm": "@ai-sdk/anthropic"}},
				"deepseek-v4-flash-free": {"cost": {"input": 0}, "status": "deprecated"},
				"beta-thing": {"cost": {"input": 0}, "status": "alpha"},
				"paid-model": {"cost": {"input": 0.5}, "status": "active"},
				"no-cost-field": {"status": "active"},
				"": {"cost": {"input": 0}}
			}
		}
	}`)
	free, err := parseCatalogModels(body)
	if err != nil {
		t.Fatalf("parseCatalogModels: %v", err)
	}
	// 免费集合: cost=0 且非 deprecated/alpha → big-pickle, mimo-v2.5-free, union-alpha
	if len(free) != 3 {
		t.Fatalf("free count = %d (%v), want 3", len(free), free)
	}
	bp, ok := free["big-pickle"]
	if !ok {
		t.Fatal("big-pickle missing")
	}
	if bp.Context != 200000 || bp.Output != 32000 {
		t.Errorf("big-pickle limits = %d/%d, want 200000/32000", bp.Context, bp.Output)
	}
	if m := free["mimo-v2.5-free"]; m.Anthropic {
		t.Error("mimo should not be anthropic")
	}
	ua, ok := free["union-alpha"]
	if !ok || !ua.Anthropic {
		t.Error("union-alpha should be free + anthropic protocol")
	}
	// 已下架/alpha/付费/无 cost 字段的模型不得进入免费集合
	for _, id := range []string{"deepseek-v4-flash-free", "beta-thing", "paid-model", "no-cost-field"} {
		if _, ok := free[id]; ok {
			t.Errorf("%s should not be in free set", id)
		}
	}
}

func TestCatalogIsFreePriority(t *testing.T) {
	// 模拟目录加载成功后的判定优先级
	orig := catalogData
	origReady := catalogReady
	defer func() {
		catalogMu.Lock()
		catalogData = orig
		catalogReady = origReady
		catalogMu.Unlock()
	}()
	setCatalogModels(map[string]catalogEntry{
		"mimo-v2.5-free": {},
		"union-alpha":    {Anthropic: true},
	})
	if !catalogIsFree("mimo-v2.5-free") {
		t.Error("mimo-v2.5-free should be free per catalog")
	}
	if catalogIsFree("union-alpha") {
		t.Error("anthropic-protocol model must not route via zen")
	}
	if catalogIsFree("deepseek-v4-flash-free") {
		t.Error("stale seed model absent from catalog must not be free")
	}
}
