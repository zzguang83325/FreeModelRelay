package app

import (
	"testing"
)

// 启动期自动签到的结果必须能通过 /providers/credits 暴露给前端, 否则用户
// 看到"程序已经自动领过"却没有依据。快照要带 lastRunAt 与逐来源结果。
func TestAutoCheckinSnapshotExposesResults(t *testing.T) {
	autoCheckinMu.Lock()
	autoCheckinResults = map[string]*autoCheckinOutcome{}
	autoCheckinRunAt = 0
	autoCheckinMu.Unlock()

	snap := autoCheckinResultsSnapshot()
	if snap["lastRunAt"].(int64) != 0 {
		t.Fatalf("未执行时 lastRunAt 应为 0, 实际 %v", snap["lastRunAt"])
	}

	setAutoCheckinOutcome(&autoCheckinOutcome{
		Provider: "workbuddy", Label: "WorkBuddy", Status: "success", Message: "签到成功 +100 积分", Reward: 100,
	})

	snap = autoCheckinResultsSnapshot()
	if snap["lastRunAt"].(int64) == 0 {
		t.Error("执行后 lastRunAt 应被写入")
	}
	ps := snap["providers"].(map[string]any)
	o, ok := ps["workbuddy"].(*autoCheckinOutcome)
	if !ok {
		t.Fatalf("providers.workbuddy 缺失或类型错误: %v", ps)
	}
	if o.Status != "success" || o.Reward != 100 {
		t.Errorf("结果内容不对: %+v", o)
	}
	if o.At == 0 {
		t.Error("结果应带执行时间戳")
	}
}
