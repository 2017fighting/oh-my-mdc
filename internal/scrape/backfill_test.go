package scrape_test

import (
	"context"
	"strings"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 补录（#43）与取数段的接缝：source=backfill 的版本也走同一条 ProcessPending，
// 但**不问 JavDB**——补录任务的主键是番号本身，不是 JavDB 的 movie_id（App API
// 没有番号→movie_id 的检索端点），拿它调详情只会得到 404 类协议错、被当永久错
// 打成 failed(scrape)。DMM 按番号取数是补录唯一的上游；演员面对补录是结构性
// 永缺、不计待补齐（规格 §6.3），其余面的缺席由整理段标「待补齐」（#42），
// 修复口径是面板的强制重刮。
//
// 断言经由公开面：fakeJavDB 的命中数（补录版本必须零命中）与已落库的元数据值。

func TestBackfillVersionFetchesDMMOnly(t *testing.T) {
	f := newFixture(t)
	number := "SONE-682"
	hash := task.BackfillInfohash(number, number+".mkv")
	seedBackfill(t, f, number, hash)

	progress, err := f.runner.ProcessPending(context.Background())
	if err != nil {
		t.Fatalf("ProcessPending: %v", err)
	}
	if progress.Fetched != 1 {
		t.Fatalf("progress = %+v, want Fetched=1", progress)
	}
	if f.javdb.hit() != 0 {
		t.Fatalf("补录版本碰了 JavDB 详情 %d 次, want 0 次", f.javdb.hit())
	}
	if f.dmm.hit() != 1 || f.dmm.calls[0] != number {
		t.Fatalf("DMM 读了 %v, want 按番号读一次 [%s]", f.dmm.calls, number)
	}

	// DMM 的字段照常落库：title_ja 归 DMM，provenance 写得明明白白。
	meta, exists, err := f.store.Metadata(context.Background(), number)
	if err != nil || !exists {
		t.Fatalf("Metadata: exists=%v err=%v", exists, err)
	}
	if meta.TitleJA == "" {
		t.Errorf("title_ja 是空的：DMM 取到的值没落库")
	}
	if string(meta.Provenance) == "" || !strings.Contains(string(meta.Provenance), "dmm") {
		t.Errorf("provenance 里没有 dmm：%s", meta.Provenance)
	}
}

// seedBackfill 直接种一条补录登记出来的版本（processing、force_rescrape、无 add_pending）：
// 与 task.SaveBackfill 落出的形状一致，但这里只关心取数段怎么对它。
func seedBackfill(t *testing.T, f *fixture, number, infohash string) {
	t.Helper()

	if _, err := f.database.Pool().Exec(context.Background(),
		`INSERT INTO task (movie_id, number, list_id) VALUES ($1, $1, NULL)`,
		number,
	); err != nil {
		t.Fatalf("种补录任务: %v", err)
	}
	if _, err := f.database.Pool().Exec(context.Background(),
		`INSERT INTO task_version
		     (infohash, movie_id, source, delivery, status, force_rescrape, add_pending, payload)
		 VALUES ($1, $2, 'backfill', 'none', 'processing', true, false, '')`,
		infohash, number,
	); err != nil {
		t.Fatalf("种补录版本: %v", err)
	}
}
