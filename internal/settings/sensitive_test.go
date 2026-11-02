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
//
// 覆盖全部迁移里的文本列：设置域的列必须登记（敏感）或进 bookkeepingColumns；
// 非设置域的表必须先在 credentialFreeTables 里声明「按设计不存凭据」。
// 新表/新列的默认结局是红，而不是静默绕过。
func TestEveryTextColumnInMigrationsIsClassified(t *testing.T) {
	all := textColumnsInMigrations(t)

	settingsTables := make(map[string]bool, len(sections))
	for _, section := range sections {
		settingsTables[section.Table()] = true
	}
	for _, column := range all {
		if settingsTables[column.table] || credentialFreeTables[column.table] {
			continue
		}
		t.Errorf("表 %q 的文本列 %q 未分类：设置域要登记字段，非设置域要在 credentialFreeTables 声明无凭据",
			column.table, column.name)
	}

	columns := settingsColumns(all)
	if len(columns) < 20 {
		t.Fatalf("只从迁移里解析出 %d 个设置域文本列：解析器或迁移不对（期望二十多个）", len(columns))
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

// credentialFreeTables 是设置域之外、**按设计不存凭据**的表。
//
// 加表必须在这里显式声明一次；这样「新增文本列未分类即失败」的护栏覆盖整库，
// 而不是只覆盖设置域。
var credentialFreeTables = map[string]bool{
	"task":         true,
	"task_version": true,
	"version_file": true,
	"task_event":   true,
	"side_effect":  true,
	"actor":        true,
	"task_actor":   true,
}

// settingsColumns 只留下落在设置域表里的列（八节各自的表）。
//
// 任务域的表没有凭据，也不需要在这里逐列登记：把整库文本列都塞进设置域的清单，
// 会让「新增一列就红」的护栏变成一份越来越长的噪音。
func settingsColumns(columns []tableColumn) []tableColumn {
	tables := make(map[string]bool, len(sections))
	for _, section := range sections {
		tables[section.Table()] = true
	}
	kept := make([]tableColumn, 0, len(columns))
	for _, column := range columns {
		if tables[column.table] {
			kept = append(kept, column)
		}
	}
	return kept
}

// createTable 只认本项目迁移的固定写法：`CREATE TABLE <name> (` 起，`);` 止。
var createTable = regexp.MustCompile(`(?i)^CREATE TABLE\s+(\w+)\s*\($`)

// 后续迁移的列变更也要被看见，否则「新增 text 列未登记即失败」的保证会漏掉
// 经 ALTER 加进来的列（00004 的 jellyfin.media_root 就是这种）。
var (
	addColumn  = regexp.MustCompile(`(?i)^ALTER TABLE\s+(\w+)\s+ADD COLUMN\s+(\w+)\s+(.+)$`)
	dropColumn = regexp.MustCompile(`(?i)^ALTER TABLE\s+(\w+)\s+DROP COLUMN\s+(\w+)\s*;$`)
)

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
				columns = applyColumnAlter(trimmed, columns)
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
	if len(rest) == 0 {
		return false
	}
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

// applyColumnAlter 把一条 ALTER TABLE ... ADD/DROP COLUMN 应用到已解析的列集上。
//
// 只认本项目用到的两种写法，别的 ALTER 原样掠过。
func applyColumnAlter(line string, columns []tableColumn) []tableColumn {
	if match := addColumn.FindStringSubmatch(line); match != nil {
		if isTextType(strings.Fields(match[3])) {
			return append(columns, tableColumn{table: match[1], name: match[2]})
		}
		return columns
	}
	if match := dropColumn.FindStringSubmatch(line); match != nil {
		kept := columns[:0]
		for _, column := range columns {
			if column.table == match[1] && column.name == match[2] {
				continue
			}
			kept = append(kept, column)
		}
		return kept
	}
	return columns
}
