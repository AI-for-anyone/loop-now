package runrecord

import (
	"testing"
	"time"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/store"
)

// 扩展的 ctx.ui 事件进入时间线：通知带级别，状态带键，对话标出已按取消回应。
func TestExtensionUITimelineEntries(t *testing.T) {
	at := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	text := "第 3 轮"
	for _, c := range []struct {
		data map[string]any
		want apitypes.TimelineExtensionUIEntry
	}{
		{map[string]any{"method": "notify", "message": "已拦截", "level": "warning"}, apitypes.TimelineExtensionUIEntry{Method: "notify", Message: strPtr("已拦截"), Level: strPtr("warning")}},
		{map[string]any{"method": "setStatus", "statusKey": "ext", "message": text}, apitypes.TimelineExtensionUIEntry{Method: "setStatus", Message: &text, StatusKey: strPtr("ext")}},
		{map[string]any{"method": "setStatus", "statusKey": "ext", "message": nil}, apitypes.TimelineExtensionUIEntry{Method: "setStatus", StatusKey: strPtr("ext")}},
		{map[string]any{"method": "confirm", "message": "清空？", "answered": "cancelled"}, apitypes.TimelineExtensionUIEntry{Method: "confirm", Message: strPtr("清空？"), Answered: strPtr("cancelled")}},
	} {
		got, ok := eventEntry(store.LifecycleEvent{Type: store.EventExtensionUI, At: at, Data: c.data})
		e, isEntry := got.(apitypes.TimelineExtensionUIEntry)
		c.want.TimelineBase = apitypes.TimelineBase{Type: apitypes.TimelineExtensionUI, At: at}
		if !ok || !isEntry || e.Method != c.want.Method || deref(e.Message) != deref(c.want.Message) || deref(e.Level) != deref(c.want.Level) ||
			deref(e.StatusKey) != deref(c.want.StatusKey) || deref(e.Answered) != deref(c.want.Answered) || e.Type != "extension_ui" {
			t.Fatalf("%v → %+v", c.data, got)
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
