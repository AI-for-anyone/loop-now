package tasks

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
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

// Claude Code 运行时的分叉基线：只读 claude 会话记录目录下一层的那个 <会话 ID>.jsonl，复制到平台自己的目录；
// 不在那里、名字不是会话 ID、有不完整的行或超限时拒绝；没有配置会话记录目录时拒绝。
func TestCopyClaudeBaseline(t *testing.T) {
	work, projects := t.TempDir(), t.TempDir()
	src := &Source{WorkRoot: work, MaxSessionBytes: 1 << 10, ClaudeProjects: func(context.Context) string { return projects }}
	inst := store.TaskInstance{ID: uuid.New(), TaskID: uuid.New(), AgentID: uuid.New()}
	id := uuid.NewString()
	dir := filepath.Join(projects, "-work-agents-x-workspace")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(session, []byte("{\"type\":\"user\"}\n{\"type\":\"assistant\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target, err := src.copyClaudeBaseline(context.Background(), inst, session)
	if err != nil || filepath.Dir(target) != filepath.Join(work, "task-baselines", inst.TaskID.String()) {
		t.Fatalf("基线：%q %v", target, err)
	}
	if b, _ := os.ReadFile(target); string(b) != "{\"type\":\"user\"}\n{\"type\":\"assistant\"}\n" {
		t.Fatalf("基线内容：%q", b)
	}
	outside := filepath.Join(t.TempDir(), id+".jsonl")
	_ = os.WriteFile(outside, []byte("{}\n"), 0o600)
	notID := filepath.Join(dir, "notes.jsonl")
	_ = os.WriteFile(notID, []byte("{}\n"), 0o600)
	broken := filepath.Join(dir, uuid.NewString()+".jsonl")
	_ = os.WriteFile(broken, []byte("{\"type\":\"user\"}\n{\"type\":"), 0o600)
	big := filepath.Join(dir, uuid.NewString()+".jsonl")
	_ = os.WriteFile(big, []byte(strings.Repeat("{}\n", 1000)), 0o600)
	for name, path := range map[string]string{"不在会话记录目录": outside, "名字不是会话 ID": notID, "不完整的行": broken, "超限": big, "嵌套太深": filepath.Join(dir, "x", id+".jsonl")} {
		if _, err := src.copyClaudeBaseline(context.Background(), inst, path); err == nil {
			t.Errorf("%s 应拒绝", name)
		}
	}
	src.ClaudeProjects = nil
	if _, err := src.copyClaudeBaseline(context.Background(), inst, session); err == nil {
		t.Fatal("没有配置会话记录目录时应拒绝")
	}
}
