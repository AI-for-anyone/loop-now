package store_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/store"
)

// pluginEvents 是写入的插件事件，按 id 排序；data 只取 keys 里的字段。
type pluginEvent struct {
	Type      string
	ProjectID *uuid.UUID
	AgentID   *uuid.UUID
	TaskID    *uuid.UUID
	RunID     *uuid.UUID
	Data      map[string]any
	To        []string
}

func readPluginEvents(t *testing.T, s *store.Store) []pluginEvent {
	t.Helper()
	// SQLITE-PORT(G3): array
	rows, err := s.Pool.Query(context.Background(), `SELECT e.type,e.project_id,e.agent_id,e.task_id,e.run_id,e.data,
		(SELECT array_agg(plugin_id ORDER BY plugin_id) FROM plugin_event_deliveries d WHERE d.event_id=e.id) FROM plugin_events e ORDER BY e.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []pluginEvent
	for rows.Next() {
		var ev pluginEvent
		if err := rows.Scan(&ev.Type, &ev.ProjectID, &ev.AgentID, &ev.TaskID, &ev.RunID, &ev.Data, &ev.To); err != nil {
			t.Fatal(err)
		}
		out = append(out, ev)
	}
	return out
}

func eventTypes(evs []pluginEvent) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Type)
	}
	return out
}

func subscribe(t *testing.T, s *store.Store, id string, events []string) {
	t.Helper()
	m, _ := json.Marshal(map[string]any{"events": events})
	// SQLITE-PORT(G3): '{}' array
	if _, err := s.Pool.Exec(context.Background(), `INSERT INTO plugins(id,display_name,source,version,manifest,install_dir,status) VALUES($1,$1,'{}','1',$2,'/x','installed')`, id, m); err != nil {
		t.Fatal(err)
	}
}
