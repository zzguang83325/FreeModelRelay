package app

import (
	"encoding/json"
	"testing"
)

// 上游把额度字段写成字符串("1406.78")或数字(1406.78)两种形态都出现过,
// 解析必须都能吃下, 否则余额面板会长期显示 0。
func TestWBParseAmountAcceptsStringAndNumber(t *testing.T) {
	cases := []struct {
		raw  string
		want float64
	}{
		{`"1406.78"`, 1406.78},
		{`1406.78`, 1406.78},
		{`"0"`, 0},
		{`0`, 0},
		{`""`, 0},
		{`null`, 0},
		{`"abc"`, 0},
	}
	for _, c := range cases {
		if got := wbParseAmount(json.RawMessage(c.raw)); got != c.want {
			t.Errorf("wbParseAmount(%s) = %v, 期望 %v", c.raw, got, c.want)
		}
	}
}

// 上游对重复签到返回 code!=0 + 中文文案。必须按文案判成幂等成功, 否则用户
// 点第二次签到就被报成失败(而实际状态是正确的)。
func TestWBIsAlreadyCheckin(t *testing.T) {
	already := []string{"今日已签到", "今天已签到", "already checked in", "已领取", "ALREADY"}
	for _, m := range already {
		if !wbIsAlreadyCheckin(m) {
			t.Errorf("wbIsAlreadyCheckin(%q) = false, 期望 true", m)
		}
	}
	notYet := []string{"签到成功", "ok", "活动未开启", ""}
	for _, m := range notYet {
		if wbIsAlreadyCheckin(m) {
			t.Errorf("wbIsAlreadyCheckin(%q) = true, 期望 false", m)
		}
	}
}

// 上游目录里 9 个模型一调就 11102("service info not found"), 目录本身没有任何
// 字段能区分它们 —— 所以 /v1/models 必须靠探测结果把它们挡掉, 否则客户端选中
// 就是必然失败的请求。
func TestWBModelListAvailableFiltersUnwired(t *testing.T) {
	wbModelsMu.Lock()
	prev := wbModels
	wbModels = map[string]wbModelInfo{
		wbModelKey(wbVariantCN.ID, "glm-5.3"):      {ID: "glm-5.3", Variant: wbVariantCN.ID, Available: true},
		wbModelKey(wbVariantCN.ID, "glm-4.6"):      {ID: "glm-4.6", Variant: wbVariantCN.ID, Available: false, ProbeNote: "service info not found"},
		wbModelKey(wbVariantINTL.ID, "glm-5.3"):    {ID: "glm-5.3", Variant: wbVariantINTL.ID, Available: true},
		wbModelKey(wbVariantINTL.ID, "deep-model"): {ID: "deep-model", Variant: wbVariantINTL.ID, Available: false},
	}
	wbModelsMu.Unlock()
	t.Cleanup(func() {
		wbModelsMu.Lock()
		wbModels = prev
		wbModelsMu.Unlock()
	})

	// 后台要看到全部 4 条(含不可用), 便于排查"为什么某个模型没出现在目录里"。
	if n := len(wbModelList()); n != 4 {
		t.Errorf("wbModelList() 长度 = %d, 期望 4 (后台需看到全部)", n)
	}
	avail := wbModelListAvailable()
	if len(avail) != 2 {
		t.Fatalf("wbModelListAvailable() 长度 = %d, 期望 2", len(avail))
	}
	for _, m := range avail {
		if m["id"] == "glm-4.6" || m["id"] == "deep-model" {
			t.Errorf("/v1/models 里出现了上游未接入的模型 %v", m["id"])
		}
	}
}

// 同名模型跨变体(CN 与 INTL 都有 hy3)解析必须确定落到已启用变体, 不能因为
// 遍历顺序漂移到另一个计费池; 也不允许解析不到就静默转给 cline。
func TestWBResolveModelPrefersEnabledVariant(t *testing.T) {
	wbModelsMu.Lock()
	prev := wbModels
	wbModels = map[string]wbModelInfo{
		wbModelKey(wbVariantCN.ID, "hy3"):   {ID: "hy3", Variant: wbVariantCN.ID},
		wbModelKey(wbVariantINTL.ID, "hy3"): {ID: "hy3", Variant: wbVariantINTL.ID},
	}
	wbModelsMu.Unlock()
	t.Cleanup(func() {
		wbModelsMu.Lock()
		wbModels = prev
		wbModelsMu.Unlock()
	})

	got, ok := wbResolveModel("hy3")
	if !ok {
		t.Fatal("wbResolveModel(\"hy3\") 未解析到模型")
	}
	// 两个变体默认都启用, 按 wbVariants 顺序取第一个 => CN。
	if got.Variant != wbVariantCN.ID {
		t.Errorf("裸 hy3 落到变体 %q, 期望 %q (按 wbVariants 顺序确定)", got.Variant, wbVariantCN.ID)
	}
	// 显式前缀必须能拿到另一变体。
	if m, ok := wbResolveModelIn(wbVariantINTL.ID, "hy3"); !ok || m.Variant != wbVariantINTL.ID {
		t.Errorf("wbResolveModelIn(intl, hy3) = %+v, ok=%v", m, ok)
	}
	if _, ok := wbResolveModel("__no_such_model__"); ok {
		t.Error("未知模型被解析成功, 会导致静默改道")
	}
}

// wbVariantExists 用于区分"前缀写错"(400) 与"模型不在目录"(另行处理)。
func TestWBVariantExists(t *testing.T) {
	if !wbVariantExists(wbVariantCN.ID) {
		t.Errorf("wbVariantExists(%q) = false", wbVariantCN.ID)
	}
	if !wbVariantExists(wbVariantINTL.ID) {
		t.Errorf("wbVariantExists(%q) = false", wbVariantINTL.ID)
	}
	if wbVariantExists("__nope__") {
		t.Error("wbVariantExists(__nope__) = true")
	}
}
