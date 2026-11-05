package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"
)

// ErrNoRows 是 QueryRow 没有结果时 Scan 返回的错误（即 sql.ErrNoRows）。
var ErrNoRows = sql.ErrNoRows

// Rows 是查询结果，用法同 pgx.Rows：Next、Scan、Err、Close（Close 没有返回值，可重复调用）。
type Rows struct {
	rows *sql.Rows
	// finish 结束 Query 在写连接上开的事务：参数是迭代中的错误，返回提交（或回滚）的结果。
	finish    func(error) error
	finishErr error
	closed    bool
}

// Next 前进到下一行；没有更多行或出错时返回 false 并自动关闭。
func (r *Rows) Next() bool {
	if r.closed {
		return false
	}
	if r.rows.Next() {
		return true
	}
	r.Close()
	return false
}

// Scan 把当前行读入 dest，按 Go 类型做解码：
//   - INTEGER → time.Time（UTC 微秒）；
//   - TEXT/BLOB → json.RawMessage；TEXT → 切片、map、结构体（JSON 反序列化）；
//   - 指针的指针遇 NULL 置 nil。其余交给 database/sql。
func (r *Rows) Scan(dest ...any) error {
	return scanInto(r.rows, dest)
}

// Err 返回迭代中遇到的错误；写语句的结果集关闭后还包括提交的错误。
func (r *Rows) Err() error {
	if r.finishErr != nil {
		return r.finishErr
	}
	return r.rows.Err()
}

// Close 关闭结果集；若它是写连接上的写语句，同时执行钩子、提交并归还写连接。
func (r *Rows) Close() {
	if r.closed {
		return
	}
	r.closed = true
	_ = r.rows.Close()
	if r.finish != nil {
		r.finishErr = r.finish(r.rows.Err())
	}
}

// Columns 返回列名。
func (r *Rows) Columns() ([]string, error) { return r.rows.Columns() }

// Row 是 QueryRow 的结果。
type Row struct {
	rows *Rows
	err  error
}

// Scan 读入第一行并关闭结果；没有行时返回 ErrNoRows。写语句（… RETURNING）的提交错误也由它返回。
func (r *Row) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	err := r.scan(dest)
	if err != nil && r.rows.finish != nil && !r.rows.closed {
		// 扫描失败：写语句的事务回滚。
		r.rows.closed = true
		_ = r.rows.rows.Close()
		_ = r.rows.finish(err)
		return err
	}
	r.rows.Close()
	if err == nil {
		err = r.rows.finishErr
	}
	return err
}

func (r *Row) scan(dest []any) error {
	if !r.rows.rows.Next() {
		if err := r.rows.rows.Err(); err != nil {
			return err
		}
		return ErrNoRows
	}
	if err := r.rows.Scan(dest...); err != nil {
		return err
	}
	// 读完剩余结果，确保写语句（… RETURNING）执行完毕、错误不被吞掉。
	for r.rows.rows.Next() {
	}
	return r.rows.rows.Err()
}

var (
	scannerType = reflect.TypeFor[sql.Scanner]()
	timeType    = reflect.TypeFor[time.Time]()
	rawType     = reflect.TypeFor[json.RawMessage]()
	bytesType   = reflect.TypeFor[[]byte]()
)

// decoder 把一列的原始值写进目标。
type decoder func(src any) error

func scanInto(rows *sql.Rows, dest []any) error {
	holders := make([]any, len(dest))
	decoders := make([]decoder, len(dest))
	for i, d := range dest {
		if dec := decoderFor(d); dec != nil {
			var raw any
			holders[i] = &raw
			decoders[i] = dec
		} else {
			holders[i] = d
		}
	}
	if err := rows.Scan(holders...); err != nil {
		return err
	}
	for i, dec := range decoders {
		if dec == nil {
			continue
		}
		if err := dec(*holders[i].(*any)); err != nil {
			return fmt.Errorf("db: 第 %d 列：%w", i+1, err)
		}
	}
	return nil
}

// decoderFor 为需要本包解码的目标返回解码函数；交给 database/sql 处理的返回 nil。
func decoderFor(d any) decoder {
	pv := reflect.ValueOf(d)
	if pv.Kind() != reflect.Pointer || pv.IsNil() {
		return nil
	}
	t := pv.Type().Elem()
	if t.Kind() == reflect.Pointer {
		inner := t.Elem()
		if !needsDecoder(inner) {
			return nil
		}
		return func(src any) error {
			if src == nil {
				pv.Elem().Set(reflect.Zero(t))
				return nil
			}
			nv := reflect.New(inner)
			if err := decodeValue(nv.Elem(), src); err != nil {
				return err
			}
			pv.Elem().Set(nv)
			return nil
		}
	}
	if !needsDecoder(t) {
		return nil
	}
	return func(src any) error { return decodeValue(pv.Elem(), src) }
}

func needsDecoder(t reflect.Type) bool {
	if reflect.PointerTo(t).Implements(scannerType) || t.Implements(scannerType) {
		return false
	}
	switch {
	case t == timeType, t == rawType:
		return true
	case t == bytesType:
		return false
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Map, reflect.Struct, reflect.Array:
		return t.Kind() != reflect.Slice || t.Elem().Kind() != reflect.Uint8
	}
	return false
}

func decodeValue(v reflect.Value, src any) error {
	t := v.Type()
	switch t {
	case timeType:
		n, ok := src.(int64)
		if !ok {
			return fmt.Errorf("时间列应为整数微秒，得到 %T", src)
		}
		v.Set(reflect.ValueOf(time.UnixMicro(n).UTC()))
		return nil
	case rawType:
		switch s := src.(type) {
		case nil:
			v.Set(reflect.Zero(t))
		case string:
			v.SetBytes([]byte(s))
		case []byte:
			v.SetBytes(append([]byte(nil), s...))
		default:
			return fmt.Errorf("JSON 列应为文本，得到 %T", src)
		}
		return nil
	}
	var b []byte
	switch s := src.(type) {
	case nil:
		v.Set(reflect.Zero(t))
		return nil
	case string:
		b = []byte(s)
	case []byte:
		b = s
	default:
		return fmt.Errorf("%s 应从 JSON 文本解码，得到 %T", t, src)
	}
	nv := reflect.New(t)
	if err := json.Unmarshal(b, nv.Interface()); err != nil {
		return fmt.Errorf("解码 %s：%w", t, err)
	}
	v.Set(nv.Elem())
	return nil
}

// Scanner 是能把当前行读入目标的东西：*Row、*Rows 都是。扫描函数的参数用它，同一个函数可用于单行与多行查询。
type Scanner interface {
	Scan(dest ...any) error
}

// CollectableRow 是 CollectRows 回调拿到的当前行（同 pgx 的名字）。
type CollectableRow = Scanner

// CollectRows 逐行调用 fn 并收集结果，最后关闭 rows。形状同 pgx.CollectRows。
func CollectRows[T any](rows *Rows, fn func(CollectableRow) (T, error)) ([]T, error) {
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		v, err := fn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// RowTo 把只有一列的行读成 T，配合 CollectRows 使用。
func RowTo[T any](row CollectableRow) (T, error) {
	var v T
	err := row.Scan(&v)
	return v, err
}

// ForEachRow 对每一行把列读入 scans 后调用 fn，最后关闭 rows；返回值里的 RowsAffected 是读到的行数。
// 形状同 pgx.ForEachRow。
func ForEachRow(rows *Rows, scans []any, fn func() error) (Result, error) {
	defer rows.Close()
	var n int64
	for rows.Next() {
		if err := rows.Scan(scans...); err != nil {
			return Result{}, err
		}
		n++
		if err := fn(); err != nil {
			return Result{}, err
		}
	}
	return Result{rows: n}, rows.Err()
}

// IsNoRows 报告 err 是否为没有结果。
func IsNoRows(err error) bool { return errors.Is(err, ErrNoRows) }
