package settings

import (
	"encoding/json"
	"fmt"
	"time"
)

// fieldCodec 是每种字段类型在四条路径上的形状：JSON 入参解析、对外输出、SQL 读、SQL 写。
//
// 按类型查表，而不是在五六个地方各写一遍 switch：新增一种字段类型只改这里。
type fieldCodec struct {
	// parse 把 JSON 值校验并翻成 Go 值（TypeDuration → time.Duration）。
	parse func(field Field, raw json.RawMessage) (any, error)
	// output 把 Go 值翻成 JSON 值。
	output func(value any) any
	// zero 是「未配置」时这一列的值。
	zero func() any
	// scan 返回一个扫描目标，以及把目标值写回 values 的函数。
	scan func() (target any, assign func(values map[string]any, name string))
	// selectExpr / writeExpr / bind 是这一列在 SQL 里的读、写、绑定形状。
	selectExpr func(column string) string
	writeExpr  func(position int) string
	bind       func(value any) any
}

var codecs = map[FieldType]fieldCodec{
	TypeText: {
		parse:  parseTextField,
		output: func(value any) any { return value },
		zero:   func() any { return "" },
		scan: func() (any, func(map[string]any, string)) {
			target := new(string)
			return target, func(values map[string]any, name string) { values[name] = *target }
		},
		selectExpr: func(column string) string { return column },
		writeExpr:  func(position int) string { return fmt.Sprintf("$%d", position) },
		bind:       func(value any) any { return value },
	},
	TypeBool: {
		parse:  parseBoolField,
		output: func(value any) any { return value },
		zero:   func() any { return false },
		scan: func() (any, func(map[string]any, string)) {
			target := new(bool)
			return target, func(values map[string]any, name string) { values[name] = *target }
		},
		selectExpr: func(column string) string { return column },
		writeExpr:  func(position int) string { return fmt.Sprintf("$%d", position) },
		bind:       func(value any) any { return value },
	},
	TypeInt: {
		parse:  parseIntField,
		output: func(value any) any { return value },
		zero:   func() any { return 0 },
		scan: func() (any, func(map[string]any, string)) {
			target := new(int)
			return target, func(values map[string]any, name string) { values[name] = *target }
		},
		selectExpr: func(column string) string { return column },
		writeExpr:  func(position int) string { return fmt.Sprintf("$%d", position) },
		bind:       func(value any) any { return value },
	},
	TypeDuration: {
		parse: parseDurationField,
		output: func(value any) any {
			d, _ := value.(time.Duration)
			return d.String()
		},
		zero: func() any { return time.Duration(0).String() },
		scan: func() (any, func(map[string]any, string)) {
			target := new(int64)
			return target, func(values map[string]any, name string) {
				// interval 列读成秒数：pgx 的 interval 码不支持 time.Duration。
				values[name] = time.Duration(*target) * time.Second
			}
		},
		selectExpr: func(column string) string {
			return "EXTRACT(EPOCH FROM " + column + ")::bigint"
		},
		writeExpr: func(position int) string {
			// 裸 $n 在 interval 列上会让 Postgres 把参数判成 interval，
			// 同一个占位符在 UPDATE 里再 ::double precision 就会类型错。
			return fmt.Sprintf("make_interval(secs => $%d::double precision)", position)
		},
		bind: func(value any) any {
			if d, ok := value.(time.Duration); ok {
				return float64(d / time.Second)
			}
			return value
		},
	},
}

func codecOf(field Field) fieldCodec {
	codec, ok := codecs[field.Type]
	if !ok {
		// 只可能是注册表里写了未定义的类型，属于编程错误：早炸比静默丢字段好。
		panic("settings: 字段 " + field.Name + " 的 Type 没有 codec")
	}
	return codec
}
