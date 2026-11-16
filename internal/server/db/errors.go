package db

import (
	"errors"
	"regexp"
	"slices"
	"strings"

	"modernc.org/sqlite"
)

// SQLite extended result codes (only the ones this project checks for).
const (
	CodeBusy               = 5
	CodeConstraintCheck    = 275
	CodeConstraintFK       = 787
	CodeConstraintNotNull  = 1299
	CodeConstraintPK       = 1555
	CodeConstraintTrigger  = 1811
	CodeConstraintUnique   = 2067
	CodeConstraintDatatype = 3091
	codeConstraintPrimary  = 19 // SQLITE_CONSTRAINT, the low 8 bits of the extended code
	codeMaskPrimaryResult  = 0xff
)

// Code returns the extended result code of the SQLite error in err's chain; it returns 0 when it is not a SQLite
// error.
func Code(err error) int {
	var se *sqlite.Error
	if errors.As(err, &se) {
		return se.Code()
	}
	return 0
}

// IsConstraint reports whether err is any constraint failure (unique, foreign key, CHECK, NOT NULL, trigger RAISE,
// STRICT type).
func IsConstraint(err error) bool {
	return Code(err)&codeMaskPrimaryResult == codeConstraintPrimary
}

var uniqueTarget = regexp.MustCompile(`UNIQUE constraint failed: (.*?) \(\d+\)$`)

// uniqueFailure parses what a unique violation is about: expression indexes report "index 'name'", everything else
// reports "table.column, table.column".
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

// IsUniqueViolation reports whether err is a unique violation (primary key included) on exactly these columns of
// table. With no columns given, only the table is checked.
// For plain unique constraints and partial unique indexes SQLite reports the columns rather than the constraint
// name, so the check goes by table and columns.
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

// IsUniqueIndexViolation reports whether err is a violation of the unique expression index index (for expression
// indexes SQLite reports the index name).
func IsUniqueIndexViolation(err error, index string) bool {
	name, _, ok := uniqueFailure(err)
	return ok && name == index
}

// IsAnyUniqueViolation reports whether err is any unique violation (primary key included).
func IsAnyUniqueViolation(err error) bool {
	_, _, ok := uniqueFailure(err)
	return ok
}

// IsRaise reports whether err is msg raised by a trigger's RAISE(ABORT, msg).
func IsRaise(err error, msg string) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code() == CodeConstraintTrigger && strings.Contains(se.Error(), msg)
}
