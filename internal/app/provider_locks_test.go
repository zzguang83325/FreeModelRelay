package app

import (
	"testing"
	"time"
)

// 回归测试: qoder 账号池曾因在同一把非可重入 sync.Mutex 内嵌套调用带锁函数而永久死锁
// (qoderDeleteAccount 持锁后调用 qoderLoadAccounts)。此处用超时兜住任何此类自锁,
// 失败时表现为测试失败而不是整个测试进程无声挂起。
func withTimeout(t *testing.T, name string, d time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s: 疑似死锁(持锁后嵌套调用带锁函数), %v 未返回", name, d)
	}
}

// isolateDataDir 把测试的工作目录切到临时目录: 账号/配置落盘走 kit.ResolveDataPath
// 的相对 "data" 路径, 切目录后不会碰到真实账号文件。
func isolateDataDir(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
}

// seedWBAccounts 预置内存账号池, 避免 wbLoadAccounts 走"从本机桌面端导入"分支
// (那会拉起 Electron, 既慢又不该出现在单测里)。
func seedWBAccounts(t *testing.T) {
	t.Helper()
	wbAccMu.Lock()
	prev := wbAccounts
	wbAccounts = []*wbAccount{{Variant: wbVariantCN.ID, Email: "test@example.com"}}
	wbAccMu.Unlock()
	t.Cleanup(func() {
		wbAccMu.Lock()
		wbAccounts = prev
		wbAccMu.Unlock()
	})
}

func TestQoderAccountDeleteNoDeadlock(t *testing.T) {
	isolateDataDir(t)
	// 对不存在的 id 删除必须快速返回 false, 且不留下任何持有中的锁。
	withTimeout(t, "qoderDeleteAccount", 3*time.Second, func() {
		if qoderDeleteAccount("__no_such_account__") {
			t.Errorf("qoderDeleteAccount(不存在的 id) 返回 true, 期望 false")
		}
	})
	// 删除返回后账号池必须仍可被其它请求访问(死锁时这里会挂住)。
	withTimeout(t, "qoderLoadAccounts after delete", 3*time.Second, func() {
		_ = qoderLoadAccounts()
	})
	withTimeout(t, "qoderSaveAccounts after delete", 3*time.Second, func() {
		qoderSaveAccounts()
	})
	withTimeout(t, "qoderModelList after delete", 3*time.Second, func() {
		_ = qoderModelList()
	})
	withTimeout(t, "qoderPickAccount after delete", 3*time.Second, func() {
		_ = qoderPickAccount()
	})
}

// 同一把锁内的 Locked 变体必须能在已持锁的情况下被调用(不再二次加锁)。
func TestQoderLockedVariantsReentrant(t *testing.T) {
	isolateDataDir(t)
	withTimeout(t, "locked variants", 3*time.Second, func() {
		qoderAccMu.Lock()
		defer qoderAccMu.Unlock()
		_ = qoderLoadAccountsLocked()
		qoderSaveAccountsLocked()
	})
}

func TestWBAccountPoolNoDeadlock(t *testing.T) {
	isolateDataDir(t)
	seedWBAccounts(t)
	withTimeout(t, "wbLoadAccounts", 3*time.Second, func() {
		_ = wbLoadAccounts()
	})
	withTimeout(t, "wbActiveAccounts", 3*time.Second, func() {
		_ = wbActiveAccounts("")
	})
	withTimeout(t, "wbSaveAccounts", 3*time.Second, func() {
		wbSaveAccounts()
	})
	withTimeout(t, "wbModelList", 3*time.Second, func() {
		_ = wbModelList()
	})
}

// 模型目录按 variant/id 复合键存放: 同名模型跨变体不得互相覆盖。
func TestWBModelKeyNamespaced(t *testing.T) {
	if got := wbModelKey(wbVariantCN.ID, "hy3"); got != "workbuddy/hy3" {
		t.Errorf("wbModelKey(workbuddy, hy3) = %q, 期望 \"workbuddy/hy3\"", got)
	}
	if a, b := wbModelKey(wbVariantCN.ID, "hy3"), wbModelKey(wbVariantINTL.ID, "hy3"); a == b {
		t.Errorf("两个变体的同名模型键相同(%q), 会互相覆盖", a)
	}
}
