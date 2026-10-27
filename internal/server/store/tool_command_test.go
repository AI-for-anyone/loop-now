package store_test

import (
	"context"
	"github.com/google/uuid"
	"io"
	"log/slog"
	"testing"
	"time"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/runrecord"
	"loop-now/internal/server/store"
	"loop-now/internal/server/tasks"
)

func assertCommand(t *testing.T, entries []any, command *string) {
	t.Helper()
	count := 0
	for _, entry := range entries {
		if tool, ok := entry.(apitypes.TimelineToolEntry); ok {
			count++
			if tool.State != "succeeded" || tool.DurationMs == nil || (command == nil) != (tool.Command == nil) || command != nil && *tool.Command != *command {
				t.Fatal("tool command/state mismatch", tool)
			}
		}
	}
	if count != 1 {
		t.Fatal("command became extra/absent tool row", count)
	}
}

func TestToolCommandPersistedNormalAndTaskTimeline(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	command := "git status --short"
	t.Run("normal and legacy", func(t *testing.T) {
		e := newRecEnv(t)
		run := e.send(true, "")
		at := time.Now().UTC()
		for i, state := range []string{"started", "succeeded"} {
			if err := e.s.AddToolEvent(ctx, run.ID, store.ToolEvent{ToolCallID: "bash-one", ToolName: "bash", State: state, At: at.Add(time.Duration(i) * time.Second)}); err != nil {
				t.Fatal(err)
			}
		}
		service := &runrecord.Service{Store: e.s}
		legacy, err := service.Timeline(ctx, e.agent, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		assertCommand(t, legacy.Entries, nil)
		recorder := &runrecord.Recorder{Store: e.s, Log: log}
		recorder.Event(e.agent, &run.ID, executor.Event{Type: "tool.command", At: at, Data: map[string]any{"toolCallId": "bash-one", "toolName": "bash", "command": command}})
		page, err := service.Timeline(ctx, e.agent, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		assertCommand(t, page.Entries, &command)
		if _, err = service.Timeline(ctx, e.agent, uuid.New()); err == nil {
			t.Fatal("unrelated run accepted")
		}
	})
	t.Run("task scoped historical observer", func(t *testing.T) {
		s := taskConsoleStore(t)
		task, node, _, turn := taskConsoleRecord(t, s)
		observer := &tasks.Observer{Store: s, Normal: &runrecord.Recorder{Store: s, Log: log}, Timeout: time.Second * 5, Log: log}
		at := time.Now().UTC()
		observer.Event(turn.RuntimeID, &turn.ID, executor.Event{Type: "tool.command", At: at, Data: map[string]any{"toolCallId": "bash-one", "toolName": "bash", "command": command}})
		for i, state := range []string{"started", "succeeded"} {
			observer.Event(turn.RuntimeID, &turn.ID, executor.Event{Type: "tool.changed", At: at.Add(time.Duration(i) * time.Second), Data: map[string]any{"toolCallId": "bash-one", "toolName": "bash", "state": state}})
		}
		page, err := (&runrecord.Service{Store: s}).TaskNodeTimeline(ctx, task, node, turn.ID, "", 100)
		if err != nil {
			t.Fatal(err)
		}
		assertCommand(t, page.Entries, &command)
	})
}
