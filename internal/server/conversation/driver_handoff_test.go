package conversation_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/conversation"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
)

// 空队列 Handoff 已释放锁、旧驱动尚未删除 drivers 标记时，新运行已经持锁，
// 退出后的 kick 无法接管它；旧驱动必须直接接续这次启动。
func TestNewRunStartsWhenHandoffPrecedesDriverRemoval(t *testing.T) {
	for _, kind := range []string{"send", "compact"} {
		t.Run(kind, func(t *testing.T) {
			e := newEnv(t)
			e.mustSend("block 首条", "")
			e.waitStarted("block 首条")

			reached := make(chan struct{})
			release := make(chan struct{})
			var armed atomic.Bool
			armed.Store(true)
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			conversation.SetTestHookAfterDriveHandoff(func(agentID uuid.UUID) {
				if agentID == e.agent && armed.CompareAndSwap(true, false) {
					close(reached)
					<-release
				}
			})
			t.Cleanup(func() {
				conversation.SetTestHookAfterDriveHandoff(nil)
				unblock()
			})
			if !e.ex.signal(e.agent, "release") {
				t.Fatal("放行首条执行失败")
			}
			select {
			case <-reached:
			case <-time.After(5 * time.Second):
				t.Fatal("等待空队列 Handoff 超时")
			}

			text := "交接后的普通消息"
			origin := "console"
			type compactOutcome struct {
				result executor.CompactionResult
				err    error
			}
			var compactDone chan compactOutcome
			var pending chan uuid.UUID
			if kind == "compact" {
				text = "保留关键决定"
				origin = "console_compact"
				if _, err := e.st.Pool.Exec(context.Background(), `UPDATE agent_sessions SET state='active', session_file=$2 WHERE agent_id=$1`, e.agent, "/tmp/fake-session.jsonl"); err != nil {
					t.Fatal(err)
				}
				pending = make(chan uuid.UUID, 1)
				conversation.SetTestHookAfterDriverPending(func(runID uuid.UUID) { pending <- runID })
				t.Cleanup(func() { conversation.SetTestHookAfterDriverPending(nil) })
				compactDone = make(chan compactOutcome, 1)
				go func() {
					result, err := e.svc.Compact(context.Background(), e.agent, text)
					compactDone <- compactOutcome{result: result, err: err}
				}()
			} else if _, err := e.svc.Send(context.Background(), e.agent, text, ""); err != nil {
				t.Fatal(err)
			}

			var active *store.Run
			deadline := time.Now().Add(5 * time.Second)
			for active == nil && time.Now().Before(deadline) {
				run, err := e.st.ActiveRun(context.Background(), e.agent)
				if err != nil {
					t.Fatal(err)
				}
				if run != nil && run.Origin == origin && run.Status == "starting" {
					active = run
					break
				}
				select {
				case out := <-compactDone:
					t.Fatalf("新运行开始前 compact 已返回：%+v", out)
				default:
				}
				time.Sleep(10 * time.Millisecond)
			}
			if active == nil {
				t.Fatal("新运行没有进入 starting")
			}
			if owner, err := e.co.LockOwner(context.Background(), e.agent.String()); err != nil || owner == "" {
				t.Fatalf("新运行应已持有忙闲锁：%q %v", owner, err)
			}
			if pending != nil {
				select {
				case runID := <-pending:
					if runID != active.ID {
						t.Fatalf("pending run = %s，active run = %s", runID, active.ID)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("等待 compact 登记 pendingDrivers 超时")
				}
			}
			select {
			case got := <-e.ex.started:
				t.Fatalf("旧驱动退出前不应执行新运行：%q", got)
			default:
			}

			unblock()
			e.waitStarted(text)
			if compactDone != nil {
				select {
				case out := <-compactDone:
					if out.err != nil || out.result.TokensBefore != 100 || out.result.EstimatedTokensAfter != 40 {
						t.Fatalf("compact 结果不符：%+v", out)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("等待 compact 完成超时")
				}
			}
			e.waitIdle()
		})
	}
}
