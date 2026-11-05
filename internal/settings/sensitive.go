package settings

// SensitiveColumns 是设置域里唯一允许存放凭据原文的列（table.column）。
//
// 它是掩码与日志脱敏的唯一真相：GET /api/config 只会把这些列呈现成
// {configured, masked}，其余路径一律不许把它们的值写进响应或日志。
// 新增 text 列必须在这里（敏感）或 bookkeepingColumns（程序自己写的）里登记，
// 否则 TestEveryTextColumnInTheSettingsDomainIsClassified 会失败（规格 §4.4）。
var SensitiveColumns = []string{
	"javdb.token",
	"qbittorrent.password",
	"jellyfin.api_key",
	"mteam.api_key",
	"ai_translation.api_key",
	"telegram.bot_token",
}

// bookkeepingColumns 是设置域里程序自己写、既非凭据也非用户可编辑字段的文本列。
//
// last_test_error 记上游错误的分类与摘要，不含凭据原文；
// last_test_list_id 只有 javdb 的测试会写（读到的登记清单 id，门禁第六判据的凭据），
// 其余表恒为空串。
var bookkeepingColumns = []string{
	"javdb.last_test_error",
	"qbittorrent.last_test_error",
	"jellyfin.last_test_error",
	"mteam.last_test_error",
	"ai_translation.last_test_error",
	"telegram.last_test_error",
	"javdb.last_test_list_id",
	"qbittorrent.last_test_list_id",
	"jellyfin.last_test_list_id",
	"mteam.last_test_list_id",
	"ai_translation.last_test_list_id",
	"telegram.last_test_list_id",
}

// IsSensitive 判断某一列是不是凭据列。
func IsSensitive(table, column string) bool {
	target := table + "." + column
	for _, c := range SensitiveColumns {
		if c == target {
			return true
		}
	}
	return false
}

// Mask 把凭据原文变成可回显的掩码："ab***yz"。
//
// 短于 5 个字符的值一律只回 "***"：短值露头尾等于露出全部。
func Mask(secret string) string {
	runes := []rune(secret)
	if len(runes) == 0 {
		return ""
	}
	if len(runes) <= 4 {
		return "***"
	}
	return string(runes[:2]) + "***" + string(runes[len(runes)-2:])
}
