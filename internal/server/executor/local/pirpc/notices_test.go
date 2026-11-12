package pirpc

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// 一轮最多收 50 条、合计 16 KiB：超出的截断（截在 UTF-8 边界）并标记；结束后的通知不收。
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
