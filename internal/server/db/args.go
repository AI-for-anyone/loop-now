package db

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

// convertArgs 把 Go 参数换成 SQLite 存储形式：
//   - time.Time → UTC 微秒整数；time.Duration 拒绝（含义不明确，调用方显式换成微秒）；
//   - uuid.UUID → 小写文本；bool → 0/1；
//   - []byte → BLOB；json.RawMessage → 文本；
//   - 其他切片、map、结构体 → JSON 文本（对应 pgx 对 text[] 与 jsonb 参数的编码）；
//   - nil 指针、nil 切片 → NULL；其他指针取所指的值。
func convertArgs(args []any) ([]any, error) {
	if len(args) == 0 {
		return nil, nil
	}
	out := make([]any, len(args))
	for i, a := range args {
		v, err := convertArg(a)
		if err != nil {
			return nil, fmt.Errorf("db: 第 %d 个参数：%w", i+1, err)
		}
		out[i] = v
	}
	return out, nil
}

var errDuration = errors.New("time.Duration 不能直接作参数，请换成微秒整数")

func convertArg(a any) (any, error) {
	switch v := a.(type) {
	case nil:
		return nil, nil
	case string, int64, float64:
		return v, nil
	case int:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case bool:
		if v {
			return int64(1), nil
		}
		return int64(0), nil
	case time.Time:
		return v.UnixMicro(), nil
	case time.Duration:
		return nil, errDuration
	case uuid.UUID:
		return v.String(), nil
	case []byte:
		if v == nil {
			return nil, nil
		}
		return v, nil
	case json.RawMessage:
		if v == nil {
			return nil, nil
		}
		return string(v), nil
	case driver.Valuer:
		rv := reflect.ValueOf(a)
		if rv.Kind() == reflect.Pointer && rv.IsNil() {
			return nil, nil
		}
		dv, err := v.Value()
		if err != nil {
			return nil, err
		}
		if _, again := dv.(driver.Valuer); again {
			return nil, fmt.Errorf("%T 的 Value 又返回了 Valuer", a)
		}
		return convertArg(dv)
	}
	rv := reflect.ValueOf(a)
	switch rv.Kind() {
	case reflect.Pointer:
		if rv.IsNil() {
			return nil, nil
		}
		return convertArg(rv.Elem().Interface())
	case reflect.String:
		return rv.String(), nil
	case reflect.Bool:
		return convertArg(rv.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u := rv.Uint()
		if u > 1<<63-1 {
			return nil, fmt.Errorf("无符号整数 %d 超出 int64", u)
		}
		return int64(u), nil
	case reflect.Float32, reflect.Float64:
		return rv.Float(), nil
	case reflect.Slice:
		if rv.IsNil() {
			return nil, nil
		}
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return rv.Bytes(), nil
		}
		return marshalJSON(a)
	case reflect.Map, reflect.Struct, reflect.Array:
		if rv.Kind() == reflect.Map && rv.IsNil() {
			return nil, nil
		}
		return marshalJSON(a)
	}
	return nil, fmt.Errorf("不支持的参数类型 %T", a)
}

func marshalJSON(a any) (any, error) {
	b, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// isWrite 判断语句是否会写库，决定 Query 走只读池还是写连接。误判为写只会多占写连接，误判为读会被
// 只读连接拒绝（query_only），不会悄悄出错。
func isWrite(query string) bool {
	q := skipSpaceAndComments(query)
	kw := strings.ToUpper(firstWord(q))
	switch kw {
	case "SELECT", "VALUES", "EXPLAIN":
		return false
	case "WITH":
		return containsWriteKeyword(q)
	}
	return true
}

func skipSpaceAndComments(q string) string {
	for {
		q = strings.TrimLeftFunc(q, unicode.IsSpace)
		switch {
		case strings.HasPrefix(q, "--"):
			if i := strings.IndexByte(q, '\n'); i >= 0 {
				q = q[i+1:]
			} else {
				return ""
			}
		case strings.HasPrefix(q, "/*"):
			if i := strings.Index(q, "*/"); i >= 0 {
				q = q[i+2:]
			} else {
				return ""
			}
		default:
			return q
		}
	}
}

func firstWord(q string) string {
	i := strings.IndexFunc(q, func(r rune) bool { return !unicode.IsLetter(r) })
	if i < 0 {
		return q
	}
	return q[:i]
}

// containsWriteKeyword 在引号与注释之外查找 INSERT/UPDATE/DELETE/REPLACE。
func containsWriteKeyword(q string) bool {
	var word strings.Builder
	flush := func() bool {
		w := strings.ToUpper(word.String())
		word.Reset()
		return w == "INSERT" || w == "UPDATE" || w == "DELETE" || w == "REPLACE"
	}
	for i := 0; i < len(q); i++ {
		c := q[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			if flush() {
				return true
			}
			j := strings.IndexByte(q[i+1:], c)
			if j < 0 {
				return false
			}
			i += j + 1
		case c == '-' && i+1 < len(q) && q[i+1] == '-':
			if flush() {
				return true
			}
			j := strings.IndexByte(q[i:], '\n')
			if j < 0 {
				return false
			}
			i += j
		case c == '/' && i+1 < len(q) && q[i+1] == '*':
			if flush() {
				return true
			}
			j := strings.Index(q[i+2:], "*/")
			if j < 0 {
				return false
			}
			i += j + 3
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9':
			word.WriteByte(c)
		default:
			if flush() {
				return true
			}
		}
	}
	return flush()
}

// goid 返回当前 goroutine 的编号（解析 runtime.Stack 的首行），只在嵌套写检测打开时使用。
func goid() int64 {
	var buf [64]byte
	b := buf[:runtime.Stack(buf[:], false)]
	b = bytes.TrimPrefix(b, []byte("goroutine "))
	if i := bytes.IndexByte(b, ' '); i > 0 {
		b = b[:i]
	}
	n, _ := strconv.ParseInt(string(b), 10, 64)
	return n
}

const thisPackage = "loop-now/internal/server/db."

func isThisPackage(fn string) bool {
	return strings.HasPrefix(fn, thisPackage) || strings.HasPrefix(fn, "database/sql.")
}
