package settings

import "testing"

// 门禁判据的钉子（规格 §4.4，2026-11 起六判据）：前五项看「行在不在」与目录在盘，
// 第六项「清单验读」看 javdb 账本——登记的想看 id 被一次真实测试读到过。
// 它读的是账本状态而非活体探测，所以这组测试全是纯函数，不需要数据库。
func TestIsInitializedSixCriteria(t *testing.T) {
	configured := func() map[string]SectionState {
		return map[string]SectionState{
			"javdb":       {Configured: true},
			"qbittorrent": {Configured: true},
			"jellyfin":    {Configured: true},
			"list_registry": {Configured: true, Data: map[string]any{
				"entries": []ListEntry{{ListID: "k4EVE4", Purpose: "want_watch", Name: "想看清单"}},
			}},
		}
	}
	withTest := func(states map[string]SectionState, lt *LastTest) {
		javdb := states["javdb"]
		javdb.LastTest = lt
		states["javdb"] = javdb
	}

	t.Run("登记了但从未验读：不齐", func(t *testing.T) {
		states := configured()
		if isInitialized(states, true) {
			t.Fatal("没有 last_test 记录，第六判据不该过")
		}
	})

	t.Run("验读的是旧 id：不齐", func(t *testing.T) {
		states := configured()
		withTest(states, &LastTest{OK: true, ListID: "aB3xY9"})
		if isInitialized(states, true) {
			t.Fatal("账本记的是 aB3xY9，登记的是 k4EVE4，不该过")
		}
	})

	t.Run("验读失败：不齐", func(t *testing.T) {
		states := configured()
		withTest(states, &LastTest{OK: false, ListID: "k4EVE4"})
		if isInitialized(states, true) {
			t.Fatal("last_test.ok = false，不该过")
		}
	})

	t.Run("登记的这张 id 被验读过：齐", func(t *testing.T) {
		states := configured()
		withTest(states, &LastTest{OK: true, ListID: "k4EVE4"})
		if !isInitialized(states, true) {
			t.Fatal("六判据全绿，该齐")
		}
	})

	t.Run("没登记清单：不齐（第五判据本身）", func(t *testing.T) {
		states := configured()
		states["list_registry"] = SectionState{Configured: true, Data: map[string]any{"entries": []ListEntry{}}}
		withTest(states, &LastTest{OK: true, ListID: "k4EVE4"})
		if isInitialized(states, true) {
			t.Fatal("没有登记，不该过")
		}
	})

	t.Run("目录不在盘：不齐（不受第六判据影响）", func(t *testing.T) {
		states := configured()
		withTest(states, &LastTest{OK: true, ListID: "k4EVE4"})
		if isInitialized(states, false) {
			t.Fatal("目录不可见，不该过")
		}
	})
}
