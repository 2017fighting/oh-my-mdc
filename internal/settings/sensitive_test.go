package settings

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/2017fighting/oh-my-mdc/internal/db"
)

// 规格 §4.4：敏感列由代码常量清单驱动，配一条反射单测——新增 string 列未登记即失败。
//
// 「反射」对的是内嵌迁移里的建表语句，不是运行中的库：这样普通的 go test（没有 Postgres、
// 没有 MDC_TEST_DATABASE_URL）也会红，而不是安静跳过。迁移是本项目声明 schema 的唯一地方。

// TestSensitiveColumnsListTheSpecsSecrets 把规格 §4.3 点名的六列钉死：
// 谁把清单里的某一列删了（掩码就随之失效），这条会红。
func TestSensitiveColumnsListTheSpecsSecrets(t *testing.T) {
	want := []string{
		"javdb.token",
		"qbittorrent.password",
		"jellyfin.api_key",
		"mteam.api_key",
		"ai_translation.api_key",
		"telegram.bot_token",
	}

	got := make(map[string]bool, len(SensitiveColumns))
	for _, column := range SensitiveColumns {
		got[column] = true
	}
	for _, column := range want {
		if !got[column] {
			t.Errorf("SensitiveColumns 少了规格点名的敏感列 %q", column)
		}
		delete(got, column)
	}
	for column := range got {
		t.Errorf("SensitiveColumns 多出 %q：规格里没有这一列，别把普通字段当凭据", column)
	}
}

// TestEveryTextColumnInMigrationsIsClassified 是规格 §4.4 的反射单测。
func TestEveryTextColumnInMigrationsIsClassified(t *testing.T) {
	columns := textColumnsInMigrations(t)
	if len(columns) < 20 {
		t.Fatalf("只从迁移里解析出 %d 个文本列：解析器或迁移不对（期望二十多个）", len(columns))
	}

	registered := make(map[string]bool)
	for _, section := range sections {
		for _, field := range section.Fields {
			registered[section.Table()+"."+field.Name] = true
		}
	}
	for _, column := range bookkeepingColumns {
		registered[column] = true
	}

	seen := make(map[string]bool, len(columns))
	for _, column := range columns {
		target := column.table + "." + column.name
		seen[target] = true
		if IsSensitive(column.table, column.name) || registered[target] {
			continue
		}
		t.Errorf("文本列 %q 未登记：敏感列进 SensitiveColumns，程序自写的进 bookkeepingColumns", target)
	}

	// 反向：清单里不许留已经不存在的列（改名/删列后要一起收尾）。
	for _, column := range append(append([]string{}, SensitiveColumns...), bookkeepingColumns...) {
		if !seen[column] {
			t.Errorf("登记的列 %q 在迁移里不存在：清单过期了", column)
		}
	}
}

// TestMaskHidesShortSecretsEntirely 守掩码的边界：短值露头尾等于露出全部。
func TestMaskHidesShortSecretsEntirely(t *testing.T) {
	cases := map[string]string{
		"":       "",
		"a":      "***",
		"abcd":   "***",
		"abcde":  "ab***de",
		"abcdef": "ab***ef",
	}
	for secret, want := range cases {
		if got := Mask(secret); got != want {
			t.Errorf("Mask(%q) = %q, want %q", secret, got, want)
		}
	}
}

type tableColumn struct {
	table string
	name  string
}

// createTable 只认本项目迁移的固定写法：`CREATE TABLE <name> (` 起，`);` 止。
var createTable = regexp.MustCompile(`(?i)^CREATE TABLE\s+(\w+)\s*\($`)

func textColumnsInMigrations(t *testing.T) []tableColumn {
	t.Helper()

	files, err := db.MigrationFiles()
	if err != nil {
		t.Fatalf("MigrationFiles() error = %v", err)
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	columns := make([]tableColumn, 0, 32)
	for _, name := range names {
		table := ""
		for _, line := range strings.Split(string(files[name]), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "--") {
				continue
			}
			if match := createTable.FindStringSubmatch(trimmed); match != nil {
				table = match[1]
				continue
			}
			if trimmed == ");" {
				table = ""
				continue
			}
			if table == "" {
				continue
			}

			fields := strings.Fields(trimmed)
			if len(fields) < 2 || !isTextType(fields[1:]) {
				continue
			}
			columns = append(columns, tableColumn{table: table, name: fields[0]})
		}
	}
	return columns
}

func isTextType(rest []string) bool {
	kind := strings.ToLower(rest[0])
	switch kind {
	case "text", "varchar":
		return true
	case "character":
		return len(rest) > 1 && strings.ToLower(rest[1]) == "varying"
	default:
		return false
	}
}
