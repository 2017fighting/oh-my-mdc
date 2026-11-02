// Package settings 拥有设置域：六张集成表 + 单行 app_config + list_registry（规格 §4.3）。
//
// 三条不变式由这里守：
//   - 行存在＝已配置；可选集成（mteam / ai_translation / telegram）另带 enabled。
//   - 敏感列只出 {configured, masked}，原文永不回显。SensitiveColumns 是唯一真相，
//     反射测试拿 information_schema 对照，新增文本列漏登记就红。
//   - revision 是一节可编辑内容的指纹，用来拦下两个标签页的并发编辑；
//     last_test_* 只是测试账本，不进指纹（否则测试一次就把别的标签页判成冲突）。
package settings

// Kind 是一节设置落在哪种表里。
type Kind int

const (
	// KindIntegration 是六张集成表：单行 id=1，行存在＝已配置。
	KindIntegration Kind = iota
	// KindApp 是单行 app_config：间隔、重试、暂停开关。
	KindApp
	// KindLists 是 list_registry：按用途登记清单 id，可有多行。
	KindLists
)

// FieldType 决定一列怎么校验、怎么出 JSON、怎么写回 Postgres。
type FieldType int

const (
	// TypeText 是 text 列。
	TypeText FieldType = iota
	// TypeBool 是 boolean 列。
	TypeBool
	// TypeInt 是整数列。
	TypeInt
	// TypeDuration 是 interval 列；JSON 上是 Go 时长字符串（"15m"）。
	TypeDuration
)

// Field 是一个可编辑字段。Name 同时是 JSON 键与列名。
//
// 敏感与否**不在这里**：唯一真相是 SensitiveColumns（表.列），见 sensitive.go。
type Field struct {
	Name string
	Type FieldType
	// URL 表示非空时必须是 http(s) 地址。
	URL bool
	// Enum 表示非空时必须在集合内。
	Enum []string
	// Min/Max 对 TypeInt 是取值范围，对 TypeDuration 是秒数范围。
	Min, Max int
	// MaxLen 覆盖默认的文本长度上限（0 即 maxTextLen）。
	MaxLen int
}

// Section 是设置域的一节；Name 同时是 REST 路径里的 {section}。
type Section struct {
	Name   string
	Kind   Kind
	Fields []Field
	// Required 是「已配置」必须非空的字段：首次保存要给全，之后不许改成空。
	Required []string
}

// Table 返回这一节落在哪张表。
func (s Section) Table() string {
	if s.Kind == KindApp {
		return "app_config"
	}
	return s.Name
}

// Field 按名字取字段。
func (s Section) Field(name string) (Field, bool) {
	for _, f := range s.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// Optional 表示这一节带 enabled 列，配了也能关着。
func (s Section) Optional() bool {
	_, ok := s.Field("enabled")
	return ok
}

// 向导顺序 = 本清单顺序。票要求八步、一步一张表，所以规格 §8.1 的「JavDB 账号与清单」
// 拆成「账号」「清单登记」两步；其余段的先后不变。
var sections = []Section{
	{
		Name: "javdb",
		Kind: KindIntegration,
		Fields: []Field{
			{Name: "token", Type: TypeText},
		},
		Required: []string{"token"},
	},
	{
		Name: "list_registry",
		Kind: KindLists,
		Fields: []Field{
			// list_id 是 JavDB 的清单 id，实测是 6 字符 nanoid 字符串（#16），不是整数。
			{Name: "list_id", Type: TypeText, MaxLen: 64},
			{Name: "purpose", Type: TypeText, Enum: []string{"want_watch", "want_watch_zh"}},
			{Name: "name", Type: TypeText, MaxLen: 64},
		},
		Required: []string{"list_id", "purpose"},
	},
	{
		Name: "qbittorrent",
		Kind: KindIntegration,
		Fields: []Field{
			{Name: "url", Type: TypeText, URL: true},
			{Name: "username", Type: TypeText, MaxLen: 128},
			{Name: "password", Type: TypeText},
			{Name: "category", Type: TypeText, MaxLen: 64},
			{Name: "download_root", Type: TypeText, MaxLen: 1024},
			{Name: "webhook_base_url", Type: TypeText, URL: true},
		},
		Required: []string{"url"},
	},
	{
		Name: "jellyfin",
		Kind: KindIntegration,
		Fields: []Field{
			{Name: "url", Type: TypeText, URL: true},
			{Name: "api_key", Type: TypeText},
			// media_root 是「Jellyfin 可写」的一半（#22）：三容器同路径，刷新与硬链接都靠它。
			// 它原来在 app_config，搬到本表是为了让向导的 Jellyfin 步一次存完。
			{Name: "media_root", Type: TypeText, MaxLen: 1024},
		},
		Required: []string{"url", "api_key", "media_root"},
	},
	{
		Name: "mteam",
		Kind: KindIntegration,
		Fields: []Field{
			{Name: "api_key", Type: TypeText},
			{Name: "enabled", Type: TypeBool},
		},
		Required: []string{"api_key"},
	},
	{
		Name: "ai_translation",
		Kind: KindIntegration,
		Fields: []Field{
			{Name: "base_url", Type: TypeText, URL: true},
			{Name: "api_key", Type: TypeText},
			{Name: "model", Type: TypeText, MaxLen: 128},
			{Name: "enabled", Type: TypeBool},
		},
		Required: []string{"base_url", "api_key", "model"},
	},
	{
		Name: "telegram",
		Kind: KindIntegration,
		Fields: []Field{
			{Name: "bot_token", Type: TypeText},
			{Name: "chat_id", Type: TypeText, MaxLen: 128},
			{Name: "enabled", Type: TypeBool},
			{Name: "notify_download_started", Type: TypeBool},
			{Name: "notify_filed", Type: TypeBool},
			{Name: "notify_processing_failed", Type: TypeBool},
			{Name: "notify_download_stalled", Type: TypeBool},
		},
		Required: []string{"bot_token", "chat_id"},
	},
	{
		Name: "app",
		Kind: KindApp,
		Fields: []Field{
			{Name: "poll_interval", Type: TypeDuration, Min: 30, Max: 24 * 3600},
			{Name: "reconcile_interval", Type: TypeDuration, Min: 30, Max: 24 * 3600},
			{Name: "sidefx_interval", Type: TypeDuration, Min: 10, Max: 3600},
			{Name: "max_attempts", Type: TypeInt, Min: 1, Max: 100},
			{Name: "paused", Type: TypeBool},
		},
	},
}

// Lookup 按名字找一节。
func Lookup(name string) (Section, bool) {
	for _, s := range sections {
		if s.Name == name {
			return s, true
		}
	}
	return Section{}, false
}
