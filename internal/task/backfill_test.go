package task_test

import (
	"context"
	"strings"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 测试缝：task.Store 的补录落库（#43，GLOSSARY「补录」）。跑在真 Postgres 上，
// 断言全部经由公开读模型（Detail / VersionFiles），不偷看库内行。

// TestSaveBackfillRegistersVersionsAsProcessing 是补录登记的主缝：一个事务里
// 任务行 + 每个视频一条版本（source=backfill、delivery=none、processing、
// force_rescrape、无 add_pending）+ 分片账 + 时间线事件。
func TestSaveBackfillRegistersVersionsAsProcessing(t *testing.T) {
	store, _ := migrated(t)
	ctx := context.Background()

	inserted, err := store.SaveBackfill(ctx, task.Backfill{
		Number: "SONE-682",
		Videos: []task.BackfillVideo{
			{RelPath: "SONE-682.mkv", SizeBytes: 111, PartIndex: 1, IsPrimary: true},
			{RelPath: "SONE-682-C.mkv", SizeBytes: 222, PartIndex: 2, Label: "中文"},
		},
	})
	if err != nil {
		t.Fatalf("SaveBackfill: %v", err)
	}
	if !inserted {
		t.Fatalf("inserted = false, want true（首次补录）")
	}

	row, err := store.Detail(ctx, "SONE-682", 50)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if row.Number != "SONE-682" {
		t.Errorf("number = %q, want SONE-682", row.Number)
	}
	if row.ListID != "" {
		t.Errorf("list_id = %q, want 空（补录没有清单来源）", row.ListID)
	}
	if len(row.Versions) != 2 {
		t.Fatalf("版本数 = %d, want 2（每个视频一条版本）", len(row.Versions))
	}

	hashMain := task.BackfillInfohash("SONE-682", "SONE-682.mkv")
	hashCN := task.BackfillInfohash("SONE-682", "SONE-682-C.mkv")
	if hashMain == hashCN {
		t.Fatalf("两条视频合成了同一个 infohash %s", hashMain)
	}

	byHash := map[string]task.Version{}
	for _, version := range row.Versions {
		byHash[version.Infohash] = version
	}
	main, ok := byHash[hashMain]
	if !ok {
		t.Fatalf("infohash %s 不在版本里：%v", hashMain, row.Versions)
	}
	if main.Source != task.SourceBackfill {
		t.Errorf("source = %q, want backfill", main.Source)
	}
	if main.Delivery != task.DeliveryNone {
		t.Errorf("delivery = %q, want none（零下载，无交付形态）", main.Delivery)
	}
	if main.Status != task.StatusProcessing {
		t.Errorf("status = %q, want processing（等强制重刮重写 NFO）", main.Status)
	}
	if !main.ForceRescrape {
		t.Errorf("force_rescrape = false, want true（补录的取数不复用任何旧元数据）")
	}
	if main.AddPending {
		t.Errorf("add_pending = true, want false（补录不过 qBittorrent，轮询不得捡起它）")
	}
	if main.Payload != "" {
		t.Errorf("payload = %q, want 空（无交付材料）", main.Payload)
	}
	if main.Label != "" {
		t.Errorf("label = %q, want 空", main.Label)
	}

	cn := byHash[hashCN]
	if cn.Label != "中文" {
		t.Errorf("label = %q, want 中文", cn.Label)
	}

	files, err := store.VersionFiles(ctx, hashMain)
	if err != nil {
		t.Fatalf("VersionFiles: %v", err)
	}
	if len(files) != 1 || files[0].RelPath != "SONE-682.mkv" || files[0].SizeBytes != 111 ||
		files[0].PartIndex != 1 || !files[0].IsPrimary {
		t.Fatalf("分片账 = %+v, want SONE-682.mkv/111/part1/primary", files)
	}

	// 任务状态由版本聚合：补录登记完是加工中（等重刮），不是已入库。
	if row.State != string(task.StatusProcessing) {
		t.Errorf("task_state = %q, want processing", row.State)
	}

	// 时间线要说清这条版本从哪来：补录登记，不是下载发现。
	found := false
	for _, event := range row.Events {
		if event.Kind == task.EventStateChange && strings.Contains(event.Reason, "补录") {
			found = true
		}
	}
	if !found {
		t.Errorf("时间线里没有补录登记事件：%v", row.Events)
	}
}

// TestSaveBackfillIsIdempotent 是幂等缝：重复补录零写入——不重复建任务，
// 不重复建版本，也不重复发登记事件。
func TestSaveBackfillIsIdempotent(t *testing.T) {
	store, _ := migrated(t)
	ctx := context.Background()

	first := task.Backfill{
		Number: "SONE-682",
		Videos: []task.BackfillVideo{
			{RelPath: "SONE-682.mkv", SizeBytes: 111, PartIndex: 1, IsPrimary: true},
		},
	}
	if _, err := store.SaveBackfill(ctx, first); err != nil {
		t.Fatalf("第一次 SaveBackfill: %v", err)
	}

	inserted, err := store.SaveBackfill(ctx, first)
	if err != nil {
		t.Fatalf("第二次 SaveBackfill: %v", err)
	}
	if inserted {
		t.Errorf("inserted = true, want false（番号已有任务）")
	}

	row, err := store.Detail(ctx, "SONE-682", 100)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if len(row.Versions) != 1 {
		t.Fatalf("版本数 = %d, want 1（重复补录不重复建版本）", len(row.Versions))
	}
	events := 0
	for _, event := range row.Events {
		if event.Kind == task.EventStateChange && strings.Contains(event.Reason, "补录") {
			events++
		}
	}
	if events != 1 {
		t.Fatalf("补录登记事件 = %d 条, want 1", events)
	}
}

// TestSaveBackfillRejectsBadInput 是输入形状缝：缺番号、缺视频、路径分隔符都当场拒绝。
func TestSaveBackfillRejectsBadInput(t *testing.T) {
	store, _ := migrated(t)
	ctx := context.Background()

	cases := map[string]task.Backfill{
		"缺番号": {Number: "", Videos: []task.BackfillVideo{{RelPath: "a.mkv"}}},
		"缺视频": {Number: "SONE-682"},
		"番号带路径分隔符": {
			Number: "../etc",
			Videos: []task.BackfillVideo{{RelPath: "a.mkv"}},
		},
		"视频路径逃出目录": {
			Number: "SONE-682",
			Videos: []task.BackfillVideo{{RelPath: "../SONE-682.mkv", PartIndex: 1, IsPrimary: true}},
		},
	}
	for name, backfill := range cases {
		if _, err := store.SaveBackfill(ctx, backfill); err == nil {
			t.Errorf("%s：SaveBackfill 竟然成功", name)
		}
	}
}

// TestBackfillInfohashIsDeterministic 钉住合成规则：sha256(番号 + rel_path)，
// 同输入同输出，跨进程可复算（重复补录靠它认出同一条视频）。
func TestBackfillInfohashIsDeterministic(t *testing.T) {
	a := task.BackfillInfohash("SONE-682", "SONE-682.mkv")
	b := task.BackfillInfohash("SONE-682", "SONE-682.mkv")
	if a != b {
		t.Fatalf("同输入不同输出：%s vs %s", a, b)
	}
	if len(a) != 64 || strings.ToLower(a) != a {
		t.Errorf("infohash %q 不是 64 位小写十六进制", a)
	}
	if task.BackfillInfohash("SONE-682", "SONE-682.mkv") == task.BackfillInfohash("SONE-683", "SONE-682.mkv") {
		t.Errorf("不同番号撞了同一个 infohash")
	}
}
