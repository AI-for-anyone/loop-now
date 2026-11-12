package conversation_test

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/conversation"
)

// 控制台收到一次执行的终态事件（手动压缩还有 /compact 请求返回）就当 Agent 空闲了。那时忙闲锁必须已经放开：
// 紧接着发送的消息应立即开始，不能被拒成 agent_busy（而且这时也没有可给出的当前执行）。
// CI 上 TestPlatformConsoleCompactCopyExportAndEndEventContract 偶发失败就是这样：驱动先发终态事件、再放锁。
func TestRunEndIsPublishedAfterLockRelease(t *testing.T) {
	for _, kind := range []string{"reply", "compact"} {
		t.Run(kind, func(t *testing.T) {
			e := newEnv(t)
			ctx := context.Background()
			reached, release := make(chan uuid.UUID, 1), make(chan struct{})
			var armed atomic.Bool
			armed.Store(true)
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			conversation.SetTestHookAfterRunEndPublished(func(runID uuid.UUID) {
				if armed.CompareAndSwap(true, false) {
					reached <- runID
					<-release
				}
			})
			t.Cleanup(func() {
				conversation.SetTestHookAfterRunEndPublished(nil)
				unblock()
			})

			var first uuid.UUID
			if kind == "reply" {
				first = e.mustSend("第一条", "").Run.ID
				e.waitStarted("第一条")
			} else {
				if _, err := e.st.Pool.Exec(ctx, `UPDATE agent_sessions SET state='active', session_file=$2 WHERE agent_id=$1`, e.agent, "/tmp/fake-session.jsonl"); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() {
					run, err := e.svc.Compact(ctx, e.agent, "保留关键决定")
					first = run.ID
					done <- err
				}()
				e.waitStarted("保留关键决定")
				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("压缩失败：%v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("压缩结束后 /compact 请求应返回")
				}
			}
			select {
			case id := <-reached:
				if id != first {
					t.Fatalf("结束的执行不对：%s，期望 %s", id, first)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("等待执行结束超时")
			}
			if !hasTerminalEvent(t, e, first) {
				t.Fatal("执行结束的通知里应已有终态事件")
			}

			msg, err := e.send("紧接着的消息", "")
			if err != nil {
				var details any
				if ae, ok := apperr.As(err); ok {
					details = ae.Details
				}
				t.Fatalf("终态事件发出之后发送应立即开始：%v %+v", err, details)
			}
			if msg.Run.Status != "starting" || msg.Run.WhenBusy != nil {
				t.Fatalf("紧接着的消息应立即开始、不排队：%+v", msg.Run)
			}
			unblock()
			e.waitStarted("紧接着的消息")
			e.waitIdle()
			if h := e.history(); len(h) == 0 || h[len(h)-1].Text != "回复：紧接着的消息" {
				t.Fatalf("紧接着的消息应得到回复：%+v", h)
			}
		})
	}
}

// hasTerminalEvent 判断事件流里有没有这次执行的终态事件：普通回复是 run.ended，手动压缩是终态的 run.updated。
func hasTerminalEvent(t *testing.T, e *env, runID uuid.UUID) bool {
	t.Helper()
	for _, ev := range e.newestEvents() {
		var run apitypes.Run
		switch ev.Type {
		case apitypes.EventRunEnded:
			var ended apitypes.SseRunEnded
			if err := json.Unmarshal(ev.Data, &ended); err != nil {
				t.Fatal(err)
			}
			run = ended.Run
		case apitypes.EventRunUpdated:
			var updated apitypes.SseRunUpdated
			if err := json.Unmarshal(ev.Data, &updated); err != nil {
				t.Fatal(err)
			}
			run = updated.Run
		default:
			continue
		}
		if run.ID == runID {
			switch run.Status {
			case "completed", "failed", "interrupted":
				return true
			}
		}
	}
	return false
}
