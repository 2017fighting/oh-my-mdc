package task_test

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// 待补齐标注的写入口与读模型（#42 / GLOSSARY「待补齐」）。
//
// 写入方只有整理段（入库那一刻覆写）与取数段（补译路径单面摘除）；
// 读模型服务面板聚合区与 /metrics 计数。

func TestSetMissingFacesOverwritesAndClears(t *testing.T) {
	store, database := migrated(t)
	save(t, store, candidate("YwrO3B", "ABP-123", hashOf(1)))

	ctx := context.Background()
	if err := store.SetMissingFaces(ctx, "YwrO3B",
		[]string{task.FaceImages, task.FaceActors}); err != nil {
		t.Fatalf("SetMissingFaces: %v", err)
	}

	var faces []string
	if err := database.Pool().QueryRow(ctx,
		`SELECT missing_faces FROM task WHERE movie_id = 'YwrO3B'`).Scan(&faces); err != nil {
		t.Fatalf("读标注: %v", err)
	}
	sort.Strings(faces)
	if len(faces) != 2 || faces[0] != task.FaceActors || faces[1] != task.FaceImages {
		t.Errorf("faces = %v, want [images actors]", faces)
	}

	// 空集 = 摘标：强制重刮补齐四面后重新入库，走的就是这一条。
	if err := store.SetMissingFaces(ctx, "YwrO3B", []string{}); err != nil {
		t.Fatalf("SetMissingFaces(空集): %v", err)
	}
	if err := database.Pool().QueryRow(ctx,
		`SELECT missing_faces FROM task WHERE movie_id = 'YwrO3B'`).Scan(&faces); err != nil {
		t.Fatalf("读标注: %v", err)
	}
	if len(faces) != 0 {
		t.Errorf("faces = %v, want 空（已摘标）", faces)
	}

	// 未知面当场报错：判据词汇是跨包契约，悄悄落一个拼错的词不如失败。
	if err := store.SetMissingFaces(ctx, "YwrO3B", []string{"subtitles"}); err == nil {
		t.Error("未知面应当报错")
	}
	// 重复面去重，不报错。
	if err := store.SetMissingFaces(ctx, "YwrO3B",
		[]string{task.FaceImages, task.FaceImages}); err != nil {
		t.Fatalf("重复面应当去重: %v", err)
	}
}

func TestClearMissingFaceRemovesOnlyThatFace(t *testing.T) {
	store, database := migrated(t)
	save(t, store, candidate("YwrO3B", "ABP-123", hashOf(1)))

	ctx := context.Background()
	if err := store.SetMissingFaces(ctx, "YwrO3B",
		[]string{task.FaceImages, task.FaceTitleZH}); err != nil {
		t.Fatalf("SetMissingFaces: %v", err)
	}

	// 补译补齐了中文标题：只摘这一面，缺图照旧标着。
	if err := store.ClearMissingFace(ctx, "YwrO3B", task.FaceTitleZH); err != nil {
		t.Fatalf("ClearMissingFace: %v", err)
	}
	var faces []string
	if err := database.Pool().QueryRow(ctx,
		`SELECT missing_faces FROM task WHERE movie_id = 'YwrO3B'`).Scan(&faces); err != nil {
		t.Fatalf("读标注: %v", err)
	}
	if len(faces) != 1 || faces[0] != task.FaceImages {
		t.Errorf("faces = %v, want [images]", faces)
	}

	// 面本来就不在标注里：零写入不是错。
	if err := store.ClearMissingFace(ctx, "YwrO3B", task.FaceOutline); err != nil {
		t.Fatalf("摘不存在的面不该报错: %v", err)
	}
}

func TestIncompleteListsMarkedTasksOnly(t *testing.T) {
	store, _ := migrated(t)
	save(t, store, candidate("YwrO3B", "ABP-123", hashOf(1)))
	save(t, store, candidate("k4EVE4", "SSIS-001", hashOf(2)))
	save(t, store, candidate("8lL2bR", "MIDA-636", hashOf(3)))

	ctx := context.Background()
	if err := store.SetMissingFaces(ctx, "k4EVE4", []string{task.FaceOutline}); err != nil {
		t.Fatalf("SetMissingFaces: %v", err)
	}
	if err := store.SetMissingFaces(ctx, "8lL2bR",
		[]string{task.FaceImages, task.FaceActors, task.FaceTitleZH}); err != nil {
		t.Fatalf("SetMissingFaces: %v", err)
	}

	count, err := store.IncompleteCount(ctx)
	if err != nil {
		t.Fatalf("IncompleteCount: %v", err)
	}
	if count != 2 {
		t.Fatalf("IncompleteCount = %d, want 2", count)
	}

	incomplete, err := store.Incomplete(ctx, 50)
	if err != nil {
		t.Fatalf("Incomplete: %v", err)
	}
	if len(incomplete) != 2 {
		t.Fatalf("Incomplete = %d 行, want 2（没标注的 ABP-123 不出现）", len(incomplete))
	}
	byNumber := map[string]task.IncompleteTask{}
	for _, row := range incomplete {
		byNumber[row.Number] = row
		if row.MovieID == "" || row.UpdatedAt.IsZero() {
			t.Errorf("行缺 movie_id/updated_at: %+v", row)
		}
	}
	if row := byNumber["SSIS-001"]; len(row.MissingFaces) != 1 || row.MissingFaces[0] != task.FaceOutline {
		t.Errorf("SSIS-001 = %+v, want [outline]", row)
	}
	if row := byNumber["MIDA-636"]; len(row.MissingFaces) != 3 {
		t.Errorf("MIDA-636 = %+v, want 三面", row)
	}

	// 截断语义与面板其它区一致：limit+1 才知道后面还有。
	incomplete, err = store.Incomplete(ctx, 1)
	if err != nil {
		t.Fatalf("Incomplete(1): %v", err)
	}
	if len(incomplete) != 1 {
		t.Fatalf("Incomplete(1) = %d 行, want 1", len(incomplete))
	}
}

func TestIncompleteCountWithoutMarkIsZero(t *testing.T) {
	store, _ := migrated(t)
	save(t, store, candidate("YwrO3B", "ABP-123", hashOf(1)))

	count, err := store.IncompleteCount(context.Background())
	if err != nil {
		t.Fatalf("IncompleteCount: %v", err)
	}
	if count != 0 {
		t.Errorf("IncompleteCount = %d, want 0", count)
	}
}

func TestSetMissingFacesRejectsUnknownTask(t *testing.T) {
	store, _ := migrated(t)
	if err := store.SetMissingFaces(context.Background(), "ghost", []string{task.FaceImages}); !errors.Is(err, task.ErrTaskNotFound) {
		t.Errorf("err = %v, want ErrTaskNotFound", err)
	}
}

// hashOf 造一条合法的 40 位十六进制 infohash（测试区分用例用）。
func hashOf(seed int) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 40)
	for i := range out {
		out[i] = hex[(seed+i)%16]
	}
	return string(out)
}
