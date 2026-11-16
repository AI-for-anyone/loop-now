package pirpc

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// At most 50 notices and 16 KiB in total per turn: anything beyond is truncated (at a UTF-8 boundary) and flagged;
// notices after the turn ends are not collected.
func TestTurnNoticesLimits(t *testing.T) {
	tr := newTurn("p", nil)
	for i := 0; i < MaxCommandNotices+5; i++ {
		tr.addNotice("info", "x")
	}
	if n, cut := tr.commandNotices(); len(n) != MaxCommandNotices || !cut {
		t.Fatalf("条数上限：%d %v", len(n), cut)
	}
	tr = newTurn("p", nil)
	tr.addNotice("warning", strings.Repeat("a", MaxCommandNoticeBytes-1))
	tr.addNotice("error", "额外的中文")
	tr.addNotice("info", "after")
	n, cut := tr.commandNotices()
	if len(n) != 1 || !cut || n[0].Level != "warning" {
		t.Fatalf("字节上限：%d %v", len(n), cut)
	}
	tr = newTurn("p", nil)
	tr.addNotice("info", strings.Repeat("a", MaxCommandNoticeBytes-2)+"中文")
	n, cut = tr.commandNotices()
	if len(n) != 1 || !cut || !utf8.ValidString(n[0].Message) || len(n[0].Message) > MaxCommandNoticeBytes {
		t.Fatalf("截在字符边界：%d %v", len(n[0].Message), cut)
	}
	tr = newTurn("p", nil)
	tr.mu.Lock()
	tr.finished = true
	tr.mu.Unlock()
	tr.addNotice("info", "late")
	if n, _ := tr.commandNotices(); len(n) != 0 {
		t.Fatal("结束后的通知不收")
	}
}
