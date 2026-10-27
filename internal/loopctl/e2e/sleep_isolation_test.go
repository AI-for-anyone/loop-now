package e2e_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/sleepmodel"
)

func TestSleepCLIRealAppScopeIsolation(t *testing.T) {
	w := newSleepWorld(t)
	w.cfg.PiMaxProcesses = 4 // 常态、两个任务实例与Manager通知分别有名额。
	s := w.start(sleepFixtureConfig)
	defer s.stop()
	_, manager := newTaskManager(t, s, "sleep隔离Manager")
	worker := s.createAgent("sleep隔离Worker")
	normal := testTaskClient(t, s, worker)
	s.console(202, "POST", "/agents/"+worker+"/messages", map[string]any{"text": " !sleep=800ms !text=normal_scope"})
	run := waitRunningSleepCLI(t, s, normal, false, uuid.Nil)
	_, ordinary := requestSleepCLI(t, normal, *run.CurrentRunID, 120, "")
	waitSleepCLI(t, normal, func(st sleepmodel.State) bool { return st.Sleep != nil && st.Sleep.State == "sleeping" })
	ids := []string{}
	clients := []*loopctl{}
	receipts := []sleepmodel.Receipt{}
	for i := 0; i < 2; i++ {
		created := taskCLIInput(t, manager, taskTextRequest(worker, "!sleep=800ms !text=task_scope"), "task", "create")
		id := created["task"].(map[string]any)["id"].(string)
		node := currentCLINode(t, waitRunningCLITask(t, s, id))
		l := testTaskClient(t, s, node.RuntimeID.String())
		st := waitRunningSleepCLI(t, s, l, true, uuid.Nil)
		_, r := requestSleepCLI(t, l, *st.CurrentRunID, 120, "")
		st = waitSleepCLI(t, l, func(st sleepmodel.State) bool { return st.Sleep != nil && st.Sleep.State == "sleeping" })
		if st.Sleep.TaskID == nil || st.Sleep.TaskID.String() != id || st.Sleep.RuntimeID == nil || *st.Sleep.RuntimeID != node.RuntimeID || st.Sleep.ID == ordinary.Sleep.ID {
			t.Fatal("task scope lost")
		}
		ids = append(ids, id)
		clients = append(clients, l)
		receipts = append(receipts, r)
	}
	if receipts[0].Sleep.ID == receipts[1].Sleep.ID || *receipts[0].Sleep.RuntimeID == *receipts[1].Sleep.RuntimeID {
		t.Fatal("task instances merged")
	}
	if st := sleepCLIState(t, normal); st.Sleep == nil || st.Sleep.ID != ordinary.Sleep.ID || st.Sleep.TaskID != nil {
		t.Fatal("task sleep replaced normal state")
	}
	// 真正消息只唤醒寻址的任务实例；该轮自行显式重新登记，不恢复旧计时。
	s.console(201, "POST", "/inbox/messages", map[string]any{"to": "默认项目/sleep隔离Worker#" + ids[0], "text": "isolated work", "whenBusy": "queue"})
	st := waitRunningSleepCLI(t, s, clients[0], true, receipts[0].Sleep.SourceRunID)
	_, next := requestSleepCLI(t, clients[0], *st.CurrentRunID, 120, "")
	waitSleepCLI(t, clients[0], func(st sleepmodel.State) bool {
		return st.Sleep != nil && st.Sleep.ID == next.Sleep.ID && st.Sleep.State == "sleeping"
	})
	if st = sleepCLIState(t, clients[1]); st.Sleep == nil || st.Sleep.ID != receipts[1].Sleep.ID {
		t.Fatal("other task sleep changed")
	}
	if st = sleepCLIState(t, normal); st.Sleep == nil || st.Sleep.ID != ordinary.Sleep.ID {
		t.Fatal("normal sleep changed")
	}
	var active, timers int
	if err := s.a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM agent_sleeps WHERE agent_id=$1 AND state='sleeping'`, uuid.MustParse(worker)).Scan(&active); err != nil || active != 3 {
		t.Fatal("missing separate scopes", active, err)
	}
	if err := s.a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM task_turns WHERE task_id IN ($1,$2) AND origin='sleep_wake'`, uuid.MustParse(ids[0]), uuid.MustParse(ids[1])).Scan(&timers); err != nil || timers != 0 {
		t.Fatal("unexpected timer", timers, err)
	}
	sleepNativeTurns(t, waitCLISessionPath(t, w, uuid.MustParse(worker)), 1)
	// 两份任务会话各继承常态已有的1个user turn；实际新增分别2、1轮。
	sleepNativeTurns(t, waitCLISessionPath(t, w, *receipts[0].Sleep.RuntimeID), 3)
	sleepNativeTurns(t, waitCLISessionPath(t, w, *receipts[1].Sleep.RuntimeID), 2)
	for i, id := range ids {
		cancelCLITask(t, s, manager, id)
		clients[i].run("sleep", "status").fails(t, "unauthenticated", 5)
		if st = sleepCLIState(t, normal); st.Sleep == nil || st.Sleep.ID != ordinary.Sleep.ID {
			t.Fatal("task end invalidated normal sleep")
		}
		if i == 0 {
			if other := sleepCLIState(t, clients[1]); other.Sleep == nil || other.Sleep.ID != receipts[1].Sleep.ID {
				t.Fatal("ending first task invalidated second")
			}
		}
	}
	t.Logf("normal sleep=%s; task sleeps=%s,%s; task1 replacement=%s; three scopes, no timer dispatch; schema=%s Redis=%s", ordinary.Sleep.ID, receipts[0].Sleep.ID, receipts[1].Sleep.ID, next.Sleep.ID, w.schema, w.cfg.RedisKeyPrefix)
}
