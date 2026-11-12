package conversation_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/conversation"
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
				result store.Run
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
					c := store.ParseCompaction(out.result.Compaction)
					if out.err != nil || c.Outcome != store.CompactionCompacted || c.TokensBefore == nil || *c.TokensBefore != 100 || *c.EstimatedTokensAfter != 40 || c.Instructions != text {
						t.Fatalf("compact 结果不符：%+v %+v", out, c)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("等待 compact 完成超时")
				}
			}
			e.waitIdle()
		})
	}
}

// 发起压缩的请求在驱动读到指令之前断开（刷新页面、代理超时）：指令记在执行上，压缩照常进行，结果事后从执行读取。
func TestCompactSurvivesCancelledRequest(t *testing.T) {
	e := newEnv(t)
	e.mustSend("block 首条", "")
	e.waitStarted("block 首条")
	reached, release := make(chan struct{}), make(chan struct{})
	var armed atomic.Bool
	armed.Store(true)
	conversation.SetTestHookAfterDriveHandoff(func(agentID uuid.UUID) {
		if agentID == e.agent && armed.CompareAndSwap(true, false) {
			close(reached)
			<-release
		}
	})
	t.Cleanup(func() { conversation.SetTestHookAfterDriveHandoff(nil) })
	if !e.ex.signal(e.agent, "release") {
		t.Fatal("放行首条执行失败")
	}
	<-reached
	if _, err := e.st.Pool.Exec(context.Background(), `UPDATE agent_sessions SET state='active', session_file=$2 WHERE agent_id=$1`, e.agent, "/tmp/fake-session.jsonl"); err != nil {
		t.Fatal(err)
	}
	pending := make(chan uuid.UUID, 1)
	conversation.SetTestHookAfterDriverPending(func(runID uuid.UUID) { pending <- runID })
	t.Cleanup(func() { conversation.SetTestHookAfterDriverPending(nil) })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := e.svc.Compact(ctx, e.agent, "只保留决定")
		done <- err
	}()
	var runID uuid.UUID
	select {
	case runID = <-pending: // 压缩执行已经创建，驱动还没有开始它
	case <-time.After(5 * time.Second):
		t.Fatal("等待压缩执行登记超时")
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("请求断开时 Compact 应返回错误")
	}
	close(release)
	e.waitStarted("只保留决定")
	e.waitIdle()
	last, err := e.svc.LastCompaction(context.Background(), e.agent)
	if err != nil || last == nil || last.ID != runID || last.Status != "completed" {
		t.Fatalf("事后应能读到压缩执行：%+v %v", last, err)
	}
	if c := store.ParseCompaction(last.Compaction); c.Instructions != "只保留决定" || c.Outcome != store.CompactionCompacted || c.TokensBefore == nil || *c.TokensBefore != 100 {
		t.Fatalf("压缩结果：%+v", c)
	}
}
