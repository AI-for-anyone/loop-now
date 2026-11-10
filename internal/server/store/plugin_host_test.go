package store_test

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"loop-now/internal/server/store"
)

// 事件 ID 是合法的 ULID：按字符串排序就是生成顺序，前 10 位是毫秒时间。
func TestPluginEventIDsAreSortableULIDs(t *testing.T) {
	ulid := regexp.MustCompile(`^[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	before := time.Now().UnixMilli()
	prev := ""
	for range 2000 {
		id := store.NewPluginEventID()
		if !ulid.MatchString(id) || id <= prev {
			t.Fatalf("ID %q（前一个 %q）", id, prev)
		}
		prev = id
	}
	var ms int64
	for _, c := range prev[:10] {
		ms = ms<<5 | int64(strings.IndexRune("0123456789ABCDEFGHJKMNPQRSTVWXYZ", c))
	}
	// 上界多留 1 毫秒：时钟没前进时 UUIDv7 靠毫秒内 12 位序号保持递增，序号用完会借下一毫秒
	//（2000 个不到 4096，最多借 1）；Windows 的墙上时钟粒度粗，会走到这里。
	if after := time.Now().UnixMilli() + 1; ms < before || ms > after {
		t.Fatalf("时间前缀 %d 不在 [%d, %d]", ms, before, after)
	}
}
