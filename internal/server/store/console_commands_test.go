package store_test

import (
	"slices"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/store"
)

// /copy 取最近一条有文字的成功回复：没有文字的回复（Pi 直接处理的命令）和失败的回复都跳过，都没有时为 nil。
func TestLatestCompletedAssistantSkipsEmptyReplies(t *testing.T) {
	e := newRecEnv(t)
	reply := func(status, text string) {
		t.Helper()
		r := e.send(true, "")
		if _, _, ok, err := e.s.FinishRun(e.ctx, r.ID, store.RunEnd{Status: status, Text: text}); err != nil || !ok {
			t.Fatalf("结束执行：%v %v", ok, err)
		}
	}
	latest := func() *store.Message {
		t.Helper()
		m, err := e.s.LatestCompletedAssistant(e.ctx, e.agent)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	reply("completed", "")
	reply("completed", " \n\t")
	if m := latest(); m != nil {
		t.Fatalf("只有空回复时应为 nil：%+v", m)
	}
	reply("completed", "有内容的回复")
	reply("completed", "")
	reply("failed", "失败时的部分回复")
	if m := latest(); m == nil || m.Text != "有内容的回复" {
		t.Fatalf("应取最近一条有文字的成功回复：%+v", m)
	}
}

// compactRun 插入一次手动压缩并以 end 结束。
func (e *recEnv) compactRun(instructions string, end store.RunEnd) store.Run {
	e.t.Helper()
	mustExec(e.t, e.s, `UPDATE agent_sessions SET state = 'active', session_file = 'session.jsonl' WHERE agent_id = $1`, e.agent)
	r, err := e.s.InsertConsoleCompactRun(e.ctx, e.agent, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), instructions)
	if err != nil {
		e.t.Fatal(err)
	}
	if c := store.ParseCompaction(r.Compaction); c.Instructions != instructions || c.Outcome != "" {
		e.t.Fatalf("开始时应只记下指令：%+v", c)
	}
	ended, msg, ok, err := e.s.FinishRun(e.ctx, r.ID, end)
	if err != nil || !ok || msg != nil {
		e.t.Fatalf("结束压缩：%v %v %+v", ok, err, msg)
	}
	return ended
}

// 手动压缩的指令在开始时记下，结果在结束时并入，执行记录里一直能读到；失败或被取消时没有结果。
func TestCompactionRecordedOnRun(t *testing.T) {
	e := newRecEnv(t)
	before, after := int64(4200), int64(350)
	done := e.compactRun("保留决定", store.RunEnd{Status: "completed", Compaction: &store.Compaction{Outcome: store.CompactionCompacted, TokensBefore: &before, EstimatedTokensAfter: &after}})
	c := store.ParseCompaction(done.Compaction)
	if c.Instructions != "保留决定" || c.Outcome != store.CompactionCompacted || *c.TokensBefore != 4200 || *c.EstimatedTokensAfter != 350 {
		t.Fatalf("压缩结果：%+v", c)
	}
	skipped := e.compactRun("", store.RunEnd{Status: "completed", Compaction: &store.Compaction{Outcome: store.CompactionSkipped, SkipReason: "too_small"}})
	if c := store.ParseCompaction(skipped.Compaction); c.Outcome != store.CompactionSkipped || c.SkipReason != "too_small" || c.TokensBefore != nil {
		t.Fatalf("没有压缩：%+v", c)
	}
	failed := e.compactRun("x", store.RunEnd{Status: "failed", ErrorCode: "pi_error", ErrorMessage: "模型出错"})
	if c := store.ParseCompaction(failed.Compaction); c.Instructions != "x" || c.Outcome != "" {
		t.Fatalf("失败时没有结果：%+v", c)
	}
	last, err := e.s.LatestCompactRun(e.ctx, e.agent)
	if err != nil || last == nil || last.ID != failed.ID {
		t.Fatalf("最近一次手动压缩：%+v %v", last, err)
	}
}

// 手动压缩不是回复：不进概览的“上一条回复”；被取消的不算异常，失败的照常列出。
func TestCompactionInOverviewAndAnomalies(t *testing.T) {
	e := newRecEnv(t)
	normal := e.send(true, "")
	if _, _, _, err := e.s.FinishRun(e.ctx, normal.ID, store.RunEnd{Status: "failed", ErrorCode: "pi_error"}); err != nil {
		t.Fatal(err)
	}
	cancelled := e.compactRun("", store.RunEnd{Status: "interrupted", EndReason: "user_interrupt"})
	failed := e.compactRun("", store.RunEnd{Status: "failed", ErrorCode: "pi_error", ErrorMessage: "模型出错"})
	e.compactRun("", store.RunEnd{Status: "completed", Compaction: &store.Compaction{Outcome: store.CompactionSkipped, SkipReason: "already_compacted"}})
	last, err := e.s.LastEndedRuns(e.ctx, []uuid.UUID{e.agent})
	if err != nil || last[e.agent].ID != normal.ID {
		t.Fatalf("上一条回复应跳过手动压缩：%+v %v", last[e.agent], err)
	}
	rows, total, _, err := e.s.Anomalies(e.ctx, store.ScopeFilter{}, nil, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	var ids []uuid.UUID
	for _, r := range rows {
		ids = append(ids, r.Run.ID)
	}
	if total != 2 || !slices.Contains(ids, normal.ID) || !slices.Contains(ids, failed.ID) || slices.Contains(ids, cancelled.ID) {
		t.Fatalf("异常列表：%d %v", total, ids)
	}
}

// 定位到手动压缩：它没有对话消息，以它开始时的位置为中心；之后没有消息时只有之前的部分。
func TestAroundCompactionRun(t *testing.T) {
	e := newRecEnv(t)
	reply := func() store.Run {
		r := e.send(true, "")
		if _, _, _, err := e.s.FinishRun(e.ctx, r.ID, store.RunEnd{Status: "completed", Text: "回复"}); err != nil {
			t.Fatal(err)
		}
		return r
	}
	reply()
	a := reply()
	compact := e.compactRun("", store.RunEnd{Status: "completed", Compaction: &store.Compaction{Outcome: store.CompactionSkipped, SkipReason: "too_small"}})
	tail, err := e.s.ListMessages(e.ctx, e.agent, store.MessageQuery{AroundRun: &compact.ID, Limit: 3})
	if err != nil || len(tail.Items) != 3 || tail.HasMoreAfter || !tail.HasMore || tail.Items[2].Run.ID != a.ID {
		t.Fatalf("压缩之后没有消息时应取之前的部分：%+v %v", tail, err)
	}
	b := reply()
	page, err := e.s.ListMessages(e.ctx, e.agent, store.MessageQuery{AroundRun: &compact.ID, Limit: 3})
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("定位压缩：%+v %v", page, err)
	}
	if page.Items[0].Run.ID != a.ID || page.Items[0].Role != "assistant" || page.Items[1].Run.ID != b.ID || page.Items[1].Role != "user" {
		t.Fatalf("应以压缩开始的位置为中心：%+v", page.Items)
	}
	other, err := e.s.CreateAgent(e.ctx, store.ProjectScope(e.dc.Project.ID), "B", "anthropic", "claude-x", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.ListMessages(e.ctx, other.ID, store.MessageQuery{AroundRun: &compact.ID, Limit: 3}); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("别的 Agent 定位这次压缩应是 not_found：%v", err)
	}
}
