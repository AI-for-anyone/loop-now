package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"
)

// ErrNoRows is the error Scan returns when QueryRow has no result (that is, sql.ErrNoRows).
var ErrNoRows = sql.ErrNoRows

// Rows is a query result, used like pgx.Rows: Next, Scan, Err, Close (Close has no return value and may be called
// more than once).
type Rows struct {
	rows *sql.Rows
	// finish ends the transaction Query opened on the write connection: its argument is the error from iteration,
	// and it returns the result of the commit (or rollback).
	finish    func(error) error
	finishErr error
	closed    bool
}

// Next advances to the next row; it returns false and closes the rows automatically when there are no more rows or
// an error occurred.
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

// Scan reads the current row into dest, decoding by Go type:
//   - INTEGER -> time.Time (UTC microseconds);
//   - TEXT/BLOB -> json.RawMessage; TEXT -> slices, maps, structs (JSON unmarshalling);
//   - a pointer to a pointer is set to nil on NULL. Everything else is left to database/sql.
func (r *Rows) Scan(dest ...any) error {
	return scanInto(r.rows, dest)
}

// Err returns the error met during iteration; for the result set of a write statement, once it is closed, this
// also includes the commit error.
func (r *Rows) Err() error {
	if r.finishErr != nil {
		return r.finishErr
	}
	return r.rows.Err()
}

// Close closes the result set; if it belongs to a write statement on the write connection, it also runs the hooks,
// commits and returns the write connection.
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

// Columns returns the column names.
func (r *Rows) Columns() ([]string, error) { return r.rows.Columns() }

// Row is the result of QueryRow.
type Row struct {
	rows *Rows
	err  error
}

// Scan reads the first row and closes the result; it returns ErrNoRows when there is no row. It also returns the
// commit error of a write statement (... RETURNING).
func (r *Row) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	err := r.scan(dest)
	if err != nil && r.rows.finish != nil && !r.rows.closed {
		// Scan failed: roll back the write statement's transaction.
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
	// Drain the remaining results so a write statement (... RETURNING) runs to completion and its errors are not
	// swallowed.
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

// decoder writes a column's raw value into its destination.
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
			return fmt.Errorf("db: column %d: %w", i+1, err)
		}
	}
	return nil
}

// decoderFor returns a decode function for destinations this package must decode; it returns nil for those left to
// database/sql.
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
			return fmt.Errorf("a time column must hold integer microseconds, got %T", src)
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
			return fmt.Errorf("a JSON column must hold text, got %T", src)
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
		return fmt.Errorf("%s must be decoded from JSON text, got %T", t, src)
	}
	nv := reflect.New(t)
	if err := json.Unmarshal(b, nv.Interface()); err != nil {
		return fmt.Errorf("decode %s: %w", t, err)
	}
	v.Set(nv.Elem())
	return nil
}

// Scanner is anything that can read the current row into destinations: both *Row and *Rows are. Scan functions take
// it as their parameter so the same function works for single-row and multi-row queries.
type Scanner interface {
	Scan(dest ...any) error
}

// CollectableRow is the current row handed to the CollectRows callback (same name as in pgx).
type CollectableRow = Scanner

// CollectRows calls fn for each row and collects the results, then closes rows. Shaped like pgx.CollectRows.
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

// RowTo reads a single-column row as T, for use with CollectRows.
func RowTo[T any](row CollectableRow) (T, error) {
	var v T
	err := row.Scan(&v)
	return v, err
}

// ForEachRow reads each row's columns into scans and then calls fn, and finally closes rows; RowsAffected in the
// returned value is the number of rows read.
// Shaped like pgx.ForEachRow.
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

// IsNoRows reports whether err means there was no result.
func IsNoRows(err error) bool { return errors.Is(err, ErrNoRows) }
