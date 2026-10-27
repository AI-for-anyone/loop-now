package store_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/store"
)

func TestPlatformProjectDirectoryStore(t *testing.T) {
	e := dbConnectionFixture(t) // self-owned TEST schema, no stale-schema sweep
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := e.s.Pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	orgA, orgB := uuid.New(), uuid.New()
	exec(`INSERT INTO organizations(id,name) VALUES($1,'A组织'),($2,'B组织')`, orgA, orgB)
	pA, err := e.s.CreateProject(ctx, orgA, "同名项目")
	if err != nil {
		t.Fatal(err)
	}
	pB, err := e.s.CreateProject(ctx, orgB, "同名项目")
	if err != nil {
		t.Fatal(err)
	}
	a, err := e.s.CreateAgent(ctx, store.ProjectScope(pB.ID), "负责人", "openai", "synthetic", "private-prompt-sentinel")
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.s.CreateAgent(ctx, store.ProjectScope(pB.ID), "替代人", "openai", "synthetic", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.SetProjectManager(ctx, pB.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	get := func() []store.PlatformProjectDirectoryItem {
		t.Helper()
		v, err := e.s.ListPlatformProjectDirectory(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	find := func(id uuid.UUID) store.PlatformProjectDirectoryItem {
		t.Helper()
		for _, v := range get() {
			if v.ID == id {
				return v
			}
		}
		t.Fatal("missing project")
		return store.PlatformProjectDirectoryItem{}
	}
	items := get()
	positions := map[uuid.UUID]int{}
	for i, item := range items {
		positions[item.ID] = i
	}
	again, _ := json.Marshal(get())
	first, _ := json.Marshal(items)
	if len(items) != 3 || positions[pA.ID] >= positions[pB.ID] || string(again) != string(first) || find(pA.ID).Manager != nil {
		t.Fatal("organization ordering/null Manager")
	}
	if m := find(pB.ID).Manager; m == nil || m.ID != a.ID || m.Address != "B组织/同名项目/负责人" {
		t.Fatal("complete manager locator")
	}
	encoded, _ := json.Marshal(items)
	for _, bad := range []string{"prompt", "private-prompt-sentinel", "model", "agentCount", "createdAt"} {
		if strings.Contains(string(encoded), bad) {
			t.Fatal("unsafe field exposed")
		}
	}
	if _, err = e.s.RenameProject(ctx, pB.ID, "新项目"); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE organizations SET name='新组织' WHERE id=$1`, orgB)
	if _, err = e.s.UpdateAgent(ctx, a.ID, store.AgentPatch{Name: ptrDirectoryName("新负责人")}); err != nil {
		t.Fatal(err)
	}
	if find(pB.ID).Manager.Address != "新组织/新项目/新负责人" {
		t.Fatal("rename locator stale")
	}
	if _, err = e.s.SetProjectManager(ctx, pB.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if find(pB.ID).Manager.ID != b.ID {
		t.Fatal("Manager replacement stale")
	}
	if _, err = e.s.ClearProjectManager(ctx, pB.ID); err != nil {
		t.Fatal(err)
	}
	if find(pB.ID).Manager != nil {
		t.Fatal("cancel not null")
	}
	if _, err = e.s.SetProjectManager(ctx, pB.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if err = e.s.SoftDeleteAgent(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if find(pB.ID).Manager != nil {
		t.Fatal("deleted Manager exposed")
	}
	if _, err = e.s.DeleteProject(ctx, pA.ID); err != nil {
		t.Fatal(err)
	}
	for _, v := range get() {
		if v.ID == pA.ID {
			t.Fatal("deleted project exposed")
		}
	}
	exec(`UPDATE projects SET deleted_at=now() WHERE NOT is_default`)
	// Even no rows are serialized as [] rather than null.
	exec(`UPDATE projects SET is_default=false,deleted_at=now()`)
	if v := get(); v == nil || len(v) != 0 {
		t.Fatal("empty directory is not []")
	}
}

func ptrDirectoryName(s string) *string { return &s }
