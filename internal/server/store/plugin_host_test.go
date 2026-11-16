package store_test

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"loop-now/internal/server/store"
)

// Event IDs are valid ULIDs: sorting them as strings gives the generation order, and the first 10 characters are the
// millisecond time.
func TestPluginEventIDsAreSortableULIDs(t *testing.T) {
	ulid := regexp.MustCompile(`^[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	before := time.Now().UnixMilli()
	prev := ""
	for range 2000 {
		id := store.NewPluginEventID()
		if !ulid.MatchString(id) || id <= prev {
			t.Fatalf("ID %q (previous %q)", id, prev)
		}
		prev = id
	}
	var ms int64
	for _, c := range prev[:10] {
		ms = ms<<5 | int64(strings.IndexRune("0123456789ABCDEFGHJKMNPQRSTVWXYZ", c))
	}
	// Allow 1 extra millisecond at the upper bound: when the clock has not advanced, UUIDv7 stays increasing by a
	// 12-bit sequence number within the millisecond, and when the sequence runs out it borrows the next millisecond
	// (2000 is less than 4096, so it borrows at most 1); Windows's wall clock is coarse-grained and does reach this
	// case.
	if after := time.Now().UnixMilli() + 1; ms < before || ms > after {
		t.Fatalf("time prefix %d not in [%d, %d]", ms, before, after)
	}
}
