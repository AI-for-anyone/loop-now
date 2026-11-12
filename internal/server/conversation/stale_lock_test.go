package conversation_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/conversation"
)

// 对账读到一次进行中的执行、还没补设锁时，那次执行结束、驱动放开了锁；之后补设的锁属于已结束的执行，
// 没有人会放开它，Agent 会一直显示忙（对账只为进行中的执行补锁、只为有排队的 Agent 交接）。
// CI 上 TestCancelRacesDequeueExactlyOnce 偶发卡住就是这样：队列空了，锁却一直被占。
func TestReconcileDoesNotLeaveLockOfFinishedRun(t *testing.T) {
	e, _ := newMaintEnv(t) // 对账一小时一次，只有下面手动的这一次
	e.mustSend("block 当前", "")
	e.waitStarted("block 当前")
	reached, release := make(chan uuid.UUID, 1), make(chan struct{})
	var once sync.Once
	conversation.SetTestHookBeforeRestoreLock(func(runID uuid.UUID) {
		once.Do(func() {
			reached <- runID
			<-release
		})
	})
	t.Cleanup(func() { conversation.SetTestHookBeforeRestoreLock(nil) })
	done := make(chan struct{})
	go func() {
		e.svc.ReconcileForTest(context.Background())
		close(done)
	}()
	select {
	case <-reached: // 对账已读到进行中的执行，正要补锁
	case <-time.After(5 * time.Second):
		t.Fatal("对账没有读到进行中的执行")
	}
	e.ex.signal(e.agent, "release")
	deadline := time.Now().Add(5 * time.Second)
	for {
		owner, _ := e.co.LockOwner(context.Background(), e.agent.String())
		if owner == "" {
			break // 执行已结束，驱动放开了锁
		}
		if time.Now().After(deadline) {
			t.Fatalf("执行结束后锁应放开：%q", owner)
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	<-done
	e.waitIdle()
	e.mustSend("下一条", "")
	e.waitStarted("下一条")
	e.waitIdle()
}
