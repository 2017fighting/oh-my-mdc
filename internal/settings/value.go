package settings

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// Masked 是敏感字段对外的唯一形态：有没有值、一段掩码；原文永不回显。
type Masked struct {
	Configured bool   `json:"configured"`
	Masked     string `json:"masked"`
}

// LastTest 是集成表的 last_test_* 四列，即「上次测试连接」的账本。
//
// ListID 只有 javdb 的测试会写：这次测试实际读到的登记清单 id（没读清单为空）。
// 它是门禁第六判据「清单验读」的凭据——账本是状态记录，不是活体探测；
// 测试失败不清旧值，已验记录不被一次网络抖动抹掉（见 RecordTest）。
type LastTest struct {
	At     time.Time `json:"at"`
	OK     bool      `json:"ok"`
	Error  string    `json:"error,omitempty"`
	ListID string    `json:"list_id,omitempty"`
}

// ListEntry 是 list_registry 的一行：一张清单的 id 与用途，名字只是快照。
//
// ListID 是字符串：实测 JavDB 的清单 id 是 6 字符 nanoid（#16），不是整数。
type ListEntry struct {
	ListID  string `json:"list_id"`
	Purpose string `json:"purpose"`
	Name    string `json:"name"`
}

// SectionState 是一节设置对外的样子（GET /api/config 的每节、PATCH 的回执）。
type SectionState struct {
	Configured bool           `json:"configured"`
	Enabled    *bool          `json:"enabled,omitempty"`
	Revision   string         `json:"revision"`
	Data       map[string]any `json:"data"`
	LastTest   *LastTest      `json:"last_test,omitempty"`
}

// ValidationError 是一次保存被字段规则拒绝（HTTP 400）。
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return e.Field + ": " + e.Message
}

// RevisionConflictError 说明这一节在别的标签页/别人手里已经变了（HTTP 409）。
type RevisionConflictError struct {
	Current string
}

func (e RevisionConflictError) Error() string {
	return "revision 冲突：这一节已被别处改过"
}

// DecodePatch 把 {revision?, ...字段} 的 JSON 体解成一次部分更新。
//
// 规则照规格 §8.4：省略＝不改；敏感字段空串也＝不改（页面上那个掩码框没被碰过）；
// 其余字段**逐字段**校验，第一个不合规的字段决定 400 的文案。
func DecodePatch(sec Section, body []byte) (Patch, error) {
	var raw map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&raw); err != nil {
		return Patch{}, ValidationError{Message: "请求体不是 JSON 对象: " + err.Error()}
	}
	if raw == nil {
		return Patch{}, ValidationError{Message: "请求体不是 JSON 对象"}
	}

	patch := Patch{Values: make(map[string]any, len(raw))}
	for key, value := range raw {
		if key == "revision" {
			if err := json.Unmarshal(value, &patch.Revision); err != nil {
				return Patch{}, ValidationError{Field: key, Message: "revision 必须是字符串"}
			}
			continue
		}

		field, ok := sec.Field(key)
		if !ok {
			return Patch{}, ValidationError{Field: key, Message: "这一节没有这个字段"}
		}
		if string(bytes.TrimSpace(value)) == "null" {
			// null 不是「清空」：省略字段才是「不改」，清空有它自己的语义（敏感列要 DELETE 整节）。
			return Patch{}, ValidationError{Field: key, Message: "不能为 null；不改就省略这个字段"}
		}

		parsed, err := parseField(field, value)
		if err != nil {
			return Patch{}, err
		}
		if IsSensitive(sec.Table(), key) {
			// 敏感字段空串＝保持原值：空串永远不是「清空凭据」，那要走 DELETE 整节。
			if text, ok := parsed.(string); !ok || text == "" {
				continue
			}
		}
		patch.Values[key] = parsed
	}
	return patch, nil
}

// Patch 是一次部分更新。
type Patch struct {
	// Revision 为空表示不做冲突检测（首次保存时还没有指纹）。
	Revision string
	// Values 是已校验的字段值；TypeDuration 已转成 time.Duration。
	Values map[string]any
}

func parseField(field Field, value json.RawMessage) (any, error) {
	return codecOf(field).parse(field, value)
}

func parseTextField(field Field, value json.RawMessage) (any, error) {
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return nil, ValidationError{Field: field.Name, Message: "需要字符串"}
	}
	if limit := field.MaxLen; limit > 0 && utf8.RuneCountInString(text) > limit {
		return nil, ValidationError{Field: field.Name, Message: fmt.Sprintf("最多 %d 个字符", limit)}
	}
	if field.URL && text != "" {
		u, err := url.Parse(text)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, ValidationError{Field: field.Name, Message: "需要 http(s) 地址"}
		}
	}
	if len(field.Enum) > 0 && text != "" && !contains(field.Enum, text) {
		return nil, ValidationError{Field: field.Name, Message: "只能是 " + strings.Join(field.Enum, " / ")}
	}
	return text, nil
}

func parseBoolField(field Field, value json.RawMessage) (any, error) {
	var parsed bool
	if err := json.Unmarshal(value, &parsed); err != nil {
		return nil, ValidationError{Field: field.Name, Message: "需要 true/false"}
	}
	return parsed, nil
}

func parseIntField(field Field, value json.RawMessage) (any, error) {
	var parsed int
	if err := json.Unmarshal(value, &parsed); err != nil {
		return nil, ValidationError{Field: field.Name, Message: "需要整数"}
	}
	if field.Min != 0 || field.Max != 0 {
		if parsed < field.Min || (field.Max != 0 && parsed > field.Max) {
			return nil, ValidationError{Field: field.Name, Message: rangeText(field)}
		}
	}
	return parsed, nil
}

// parseDurationField 接受两种写法：时长字符串（"15m"）或秒数（900）。
func parseDurationField(field Field, value json.RawMessage) (any, error) {
	var text string
	if err := json.Unmarshal(value, &text); err == nil {
		d, err := time.ParseDuration(text)
		if err != nil {
			return nil, ValidationError{Field: field.Name, Message: `需要时长，如 "15m"`}
		}
		return checkDurationRange(field, d)
	}

	var seconds float64
	if err := json.Unmarshal(value, &seconds); err != nil {
		return nil, ValidationError{Field: field.Name, Message: `需要时长，如 "15m"`}
	}
	return checkDurationRange(field, time.Duration(seconds*float64(time.Second)))
}

func checkDurationRange(field Field, d time.Duration) (any, error) {
	seconds := d / time.Second
	if d%time.Second != 0 || seconds < time.Duration(field.Min) || seconds > time.Duration(field.Max) {
		return nil, ValidationError{Field: field.Name, Message: rangeText(field)}
	}
	return d, nil
}

func rangeText(field Field) string {
	if field.Type == TypeDuration {
		return fmt.Sprintf("时长需在 %s – %s 之间", time.Duration(field.Min)*time.Second, time.Duration(field.Max)*time.Second)
	}
	return fmt.Sprintf("取值需在 %d – %d 之间", field.Min, field.Max)
}

func contains(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

// row 是一节设置的原文：values 里可能含凭据原文，只许在 settings 包内流转。
type row struct {
	configured bool
	values     map[string]any
	lastTest   *LastTest
	lists      []ListEntry
}

// state 把原文转成对外形态：敏感字段只出掩码，可选集成的 enabled 提到节一级
// （规格 §8.4 的 {revision, configured, enabled?, data}），不在 data 里重复一遍。
func (r row) state(sec Section) SectionState {
	data := make(map[string]any, len(sec.Fields))
	for _, field := range sec.Fields {
		if sec.Kind == KindLists {
			// 清单登记的内容是若干行，见下面的 entries；没有「一行的列」可言。
			break
		}
		if field.Name == "enabled" {
			continue
		}
		value := r.values[field.Name]
		if IsSensitive(sec.Table(), field.Name) {
			text, _ := value.(string)
			data[field.Name] = Masked{Configured: text != "", Masked: Mask(text)}
			continue
		}
		if value == nil {
			data[field.Name] = codecOf(field).zero()
			continue
		}
		data[field.Name] = codecOf(field).output(value)
	}
	if sec.Kind == KindLists {
		entries := r.lists
		if entries == nil {
			entries = []ListEntry{}
		}
		data["entries"] = entries
	}

	state := SectionState{
		Configured: r.configured,
		Revision:   r.revision(sec),
		Data:       data,
		LastTest:   r.lastTest,
	}
	if enabled, ok := r.values["enabled"].(bool); ok && sec.Optional() && r.configured {
		state.Enabled = &enabled
	}
	return state
}

// revision 是一节内容（含敏感原文）的规范指纹：前 12 位 sha256。
//
// 用 encoding/json 的规范形式（对象键按字典序），比手写拼接少一套「怎么写才稳定」的规矩。
func (r row) revision(sec Section) string {
	payload := make(map[string]any, len(sec.Fields)+1)
	if sec.Kind == KindLists {
		payload["entries"] = r.lists
	} else {
		for _, field := range sec.Fields {
			payload[field.Name] = r.values[field.Name]
		}
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		// values 只装 string/bool/int/time.Duration，编码失败属于编程错误。
		panic("settings: 计算 " + sec.Name + " 的 revision 失败: " + err.Error())
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])[:12]
}
