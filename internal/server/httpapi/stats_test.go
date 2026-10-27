package httpapi_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/store"
)

// GET /stats validates its window and bucket, and a populated report matches
// the contract.
func TestStatsValidationAndContract(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.console()
	window := func(from, to, bucket string) string {
		return "/stats?from=" + from + "&to=" + to + "&bucket=" + bucket
	}
	c.expectError(422, "validation_failed", "GET", window("2026-01-05T00:00:00Z", "2026-01-05T00:00:00Z", "day"), nil)
	c.expectError(422, "validation_failed", "GET", window("2026-01-06T00:00:00Z", "2026-01-05T00:00:00Z", "hour"), nil)
	c.expectError(422, "validation_failed", "GET", window("2026-01-01T00:00:00Z", "2026-03-01T00:00:00Z", "hour"), nil)
	c.expectError(422, "validation_failed", "GET", window("2026-01-05T00:00:00Z", "2026-01-06T00:00:00Z", "week"), nil, "X-Contract-Invalid-Request", "1")
	c.expectError(422, "validation_failed", "GET", "/stats?from=2026-01-05T00:00:00Z&bucket=day", nil, "X-Contract-Invalid-Request", "1")
	c.expectError(422, "validation_failed", "GET", window("2026-01-05T00:00:00Z", "2026-01-06T00:00:00Z", "day")+"&platform=true&projectId="+e.dc.Project.ID.String(), nil)

	a, err := e.st.CreateAgent(ctx, store.ProjectScope(e.dc.Project.ID), "统计", "anthropic", "claude-x", "")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := e.st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	sleep := uuid.New()
	exec(`INSERT INTO agent_sleeps(id,scope_key,scope_kind,mode,agent_id,session_epoch,source_run_id,action_id,body_hash,receipt,state,seconds,entered_at,wake_at,ended_at,end_reason,timer_run_id,wake_run_id,revision)
		VALUES($1,'normal/'||$2::uuid::text,'normal','normal',$2::uuid,1,$3,$4,$5,'{}','woken',60,$6,$6,$6,'deadline',$7,$7,1)`, sleep, a.ID, uuid.New(), uuid.New(), make([]byte, 32), base, uuid.New())
	run := func(origin, status string, at time.Time, errorCode *string, wake *uuid.UUID) uuid.UUID {
		id := uuid.New()
		exec(`INSERT INTO runs(id,agent_id,user_message_id,config_version,priority,status,origin,error_code,queued_at,started_at,running_at,ended_at,wake_sleep_id)
			VALUES($1,$2,$3,1,1,$4,$5,$6,$7,$7,$8,$9,$10)`, id, a.ID, uuid.New(), status, origin, errorCode, at, at.Add(3*time.Second), at.Add(time.Minute), wake)
		return id
	}
	failed := "pi_exited"
	ok := run("console", "completed", base.Add(time.Hour), nil, nil)
	run("console", "failed", base.Add(2*time.Hour), &failed, nil)
	wake := run("sleep_wake", "completed", base.Add(3*time.Hour), nil, &sleep)
	for _, u := range []struct {
		run  uuid.UUID
		kind string
	}{{ok, "assistant"}, {ok, "compaction"}, {wake, "assistant"}} {
		exec(`INSERT INTO usage_records(id,at,agent_id,run_id,kind,provider,model,input,output,cache_read) VALUES($1,$2,$3,$4,$5,'anthropic','claude-x',100,10,400)`,
			uuid.New(), base.Add(4*time.Hour), a.ID, u.run, u.kind)
	}
	exec(`INSERT INTO run_tool_events(run_id,seq,tool_call_id,tool_name,state,at) VALUES($1,1,'c1','bash','started',$2),($1,2,'c1','bash','failed',$2)`, ok, base)
	exec(`INSERT INTO lifecycle_events(id,at,agent_id,type,data) VALUES($1,$2,$3,'pi.started','{"startupMs":250}')`, uuid.New(), base.Add(time.Hour), a.ID)
	for i, state := range []string{"answered", "open"} {
		id := uuid.New()
		exec(`INSERT INTO inbox_messages(id,thread_id,from_kind,from_agent_id,to_kind,text,created_at) VALUES($1,$1,'agent',$2,'console','要继续吗？',$3)`, id, a.ID, base.Add(time.Duration(i+1)*time.Hour))
		exec(`INSERT INTO inbox_questions(message_id,options,expires_at,state,answered_at) VALUES($1,'[]',$2,$3,CASE WHEN $3='answered' THEN $4::timestamptz END)`,
			id, time.Now().Add(time.Hour), state, base.Add(2*time.Hour))
	}

	got := decode[store.StatsReport](t, c.expect(200, "GET", window("2026-01-05T00:00:00Z", "2026-01-06T00:00:00Z", "hour")+"&projectId="+e.dc.Project.ID.String(), nil))
	if got.Bucket != "hour" || len(got.Buckets) != 24 || len(got.Tokens.Series) != 24 || len(got.Concurrency) != 24 || len(got.Wakes) != 1 || len(got.Context.Agents) != 1 ||
		len(got.Questions.Pending) != 1 || got.Questions.Asked != 2 || len(got.Outcomes.Reasons) != 1 || len(got.Agents) != 1 || len(got.Tools.ByName) != 1 ||
		got.Tools.ByName[0].Failures != 1 || got.Process.PiColdStarts != 1 || got.ByOrigin.Totals.SleepWake != 510 {
		t.Fatalf("report: %+v", got)
	}
	_, secret := e.identity("stats-external")
	e.external(secret).expectError(403, "forbidden", "GET", window("2026-01-05T00:00:00Z", "2026-01-06T00:00:00Z", "day"), nil)
}
