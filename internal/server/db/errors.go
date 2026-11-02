package db

import (
	"errors"
	"regexp"
	"slices"
	"strings"

	"modernc.org/sqlite"
)

// SQLite 扩展结果码（只列本项目判断的几种）。
const (
	CodeBusy               = 5
	CodeConstraintCheck    = 275
	CodeConstraintFK       = 787
	CodeConstraintNotNull  = 1299
	CodeConstraintPK       = 1555
	CodeConstraintTrigger  = 1811
	CodeConstraintUnique   = 2067
	CodeConstraintDatatype = 3091
	codeConstraintPrimary  = 19 // SQLITE_CONSTRAINT，扩展码的低 8 位
	codeMaskPrimaryResult  = 0xff
)

// Code 返回 err 链上 SQLite 错误的扩展结果码；不是 SQLite 错误时返回 0。
func Code(err error) int {
	var se *sqlite.Error
	if errors.As(err, &se) {
		return se.Code()
	}
	return 0
}

// IsConstraint 报告 err 是否为任一约束失败（唯一、外键、CHECK、NOT NULL、触发器 RAISE、STRICT 类型）。
func IsConstraint(err error) bool {
	return Code(err)&codeMaskPrimaryResult == codeConstraintPrimary
}

var uniqueTarget = regexp.MustCompile(`UNIQUE constraint failed: (.*?) \(\d+\)$`)

// uniqueFailure 解析唯一冲突的对象：表达式索引报 "index 'name'"，其余报 "表.列, 表.列"。
func uniqueFailure(err error) (index string, cols []string, ok bool) {
	var se *sqlite.Error
	if !errors.As(err, &se) || (se.Code() != CodeConstraintUnique && se.Code() != CodeConstraintPK) {
		return "", nil, false
	}
	m := uniqueTarget.FindStringSubmatch(se.Error())
	if m == nil {
		return "", nil, true
	}
	target := m[1]
	if name, found := strings.CutPrefix(target, "index '"); found {
		return strings.TrimSuffix(name, "'"), nil, true
	}
	return "", strings.Split(target, ", "), true
}

// IsUniqueViolation 报告 err 是否为 table 上恰好这些列的唯一冲突（含主键）。不给列时只看表。
// 普通唯一约束与部分唯一索引，SQLite 报的是列而不是约束名，所以按表与列判断。
func IsUniqueViolation(err error, table string, cols ...string) bool {
	_, got, ok := uniqueFailure(err)
	if !ok || len(got) == 0 {
		return false
	}
	want := make([]string, len(cols))
	for i, c := range cols {
		want[i] = table + "." + c
	}
	if len(cols) == 0 {
		return strings.HasPrefix(got[0], table+".")
	}
	return slices.Equal(got, want)
}

// IsUniqueIndexViolation 报告 err 是否为表达式唯一索引 index 的冲突（SQLite 对表达式索引报索引名）。
func IsUniqueIndexViolation(err error, index string) bool {
	name, _, ok := uniqueFailure(err)
	return ok && name == index
}

// IsAnyUniqueViolation 报告 err 是否为任一唯一冲突（含主键）。
func IsAnyUniqueViolation(err error) bool {
	_, _, ok := uniqueFailure(err)
	return ok
}

// IsRaise 报告 err 是否为触发器 RAISE(ABORT, msg) 抛出的 msg。
func IsRaise(err error, msg string) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code() == CodeConstraintTrigger && strings.Contains(se.Error(), msg)
}
