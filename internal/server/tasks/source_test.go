package tasks

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/store"
)

func TestBaselineCopiesCompleteSessionAndRejectsInvalidPaths(t *testing.T) {
	src := &Source{WorkRoot: t.TempDir(), MaxSessionBytes: 4096}
	inst := store.TaskInstance{ID: uuid.New(), AgentID: uuid.New(), TaskID: uuid.New()}
	dir := filepath.Join(src.WorkRoot, "agents", inst.AgentID.String(), "sessions")
	if e := os.MkdirAll(dir, 0o700); e != nil {
		t.Fatal(e)
	}
	session := filepath.Join(dir, "source.jsonl")
	valid := []byte("{\"type\":\"session\",\"id\":\"fixture\"}\n{\"type\":\"message\",\"id\":\"m\"}\n")
	if e := os.WriteFile(session, valid, 0o600); e != nil {
		t.Fatal(e)
	}
	target, e := src.copyBaseline(inst, session)
	if e != nil {
		t.Fatal(e)
	}
	copy, e := os.ReadFile(target)
	if e != nil || !bytes.Equal(copy, valid) {
		t.Fatal("baseline copy differs", e)
	}
	if e = os.WriteFile(session, []byte("changed"), 0o600); e != nil {
		t.Fatal(e)
	}
	copy, _ = os.ReadFile(target)
	if !bytes.Equal(copy, valid) {
		t.Fatal("baseline aliases source")
	}
	for _, invalid := range []string{"", " \n", "{\"type\":\"message\"}\n", "{\"type\":\"session\"}\n{broken"} {
		if e = os.WriteFile(session, []byte(invalid), 0o600); e != nil {
			t.Fatal(e)
		}
		if _, e = src.copyBaseline(inst, session); e == nil {
			t.Fatal("invalid baseline accepted", invalid)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if e = os.WriteFile(outside, valid, 0o600); e != nil {
		t.Fatal(e)
	}
	if _, e = src.copyBaseline(inst, outside); e == nil {
		t.Fatal("other directory accepted")
	}
	link := filepath.Join(dir, "link")
	if e = os.Symlink(outside, link); e != nil {
		t.Fatal(e)
	}
	if _, e = src.copyBaseline(inst, link); e == nil {
		t.Fatal("symlink accepted")
	}
	if e = os.WriteFile(session, bytes.Repeat([]byte("a"), 4097), 0o600); e != nil {
		t.Fatal(e)
	}
	if _, e = src.copyBaseline(inst, session); e == nil {
		t.Fatal("oversize baseline accepted")
	}
}
