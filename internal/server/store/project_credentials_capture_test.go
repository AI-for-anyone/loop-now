package store_test

import (
	"context"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/sqlitetest"
	"strings"
	"sync"
	"testing"
	"time"
)

type projectCaptureGate struct {
	once             sync.Once
	entered, release chan struct{}
}

// hook 让采集停在读 Agent 行之前：采集所在的写事务已经开始（项目已读），还没结束。
func (g *projectCaptureGate) hook(ctx context.Context, q string) {
	if strings.Contains(q, "agents.prompt_version") && strings.Contains(q, "FROM agents WHERE id=$1") {
		g.once.Do(func() {
			close(g.entered)
			select {
			case <-g.release:
			case <-ctx.Done():
			}
		})
	}
}

func TestProjectCredentialCaptureSerializesEffectiveChanges(t *testing.T) {
	for _, mutation := range []string{"replace", "permission", "prompt"} {
		t.Run(mutation, func(t *testing.T) {
			e := projectMigrationFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			id := e.agent(t, "锁内捕获", "openai")
			w := projectCoreWrite(t, e, "synthetic-capture-A")
			account, err := e.s.CreateProjectCredential(ctx, e.project, "model", "openai", "模型A", w)
			if err != nil {
				t.Fatal(err)
			}
			if err = e.s.AddAgentCLI(ctx, id, "gh"); err != nil {
				t.Fatal(err)
			}
			before, err := e.s.CaptureExecutionIdentity(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			gate := &projectCaptureGate{entered: make(chan struct{}), release: make(chan struct{})}
			e.s.Pool.SetQueryHook(gate.hook)
			defer e.s.Pool.SetQueryHook(nil)
			reader, writer := e.s, e.s
			capture := make(chan store.ExecutionIdentity, 1)
			failure := make(chan error, 1)
			var once sync.Once
			release := func() { once.Do(func() { close(gate.release) }) }
			defer release()
			go func() { v, err := reader.CaptureExecutionIdentity(ctx, id); capture <- v; failure <- err }()
			select {
			case <-gate.entered:
			case <-ctx.Done():
				t.Fatal("capture gate", ctx.Err())
			}
			written := make(chan error, 1)
			go func() {
				switch mutation {
				case "replace":
					next := projectCoreWrite(t, e, "synthetic-capture-B")
					_, err := writer.MutateProjectCredential(ctx, e.project, account.View.ID, 1, nil, &next, false)
					written <- err
				case "permission":
					written <- writer.RemoveAgentCLI(ctx, id, "gh")
				case "prompt":
					text := "prompt-B"
					version := int64(1)
					_, err := writer.UpdateAgent(ctx, id, store.AgentPatch{Prompt: &text, ExpectedPromptVersion: &version})
					written <- err
				}
			}()
			// 写入停在等写连接上，证明它到了采集的写事务后面。
			sqlitetest.WaitWriteWaiters(t, ctx, e.s.Pool, 1)
			select {
			case err := <-written:
				t.Fatal("writer bypassed capture fence", err)
			default:
			}
			release()
			old := <-capture
			if err = <-failure; err != nil {
				t.Fatal(err)
			}
			if old.ModelAuth.VersionID != before.ModelAuth.VersionID || old.CredentialIdentity != before.CredentialIdentity || old.Agent.ConfigVersion != before.Agent.ConfigVersion || old.Prompts.AgentPrompt != before.Prompts.AgentPrompt || len(old.CLIs) != 1 {
				t.Fatal("captured mixed effective version")
			}
			if err = <-written; err != nil {
				t.Fatal(err)
			}
			next, err := e.s.CaptureExecutionIdentity(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "replace":
				if next.ModelAuth.VersionID == old.ModelAuth.VersionID || next.CredentialIdentity == old.CredentialIdentity {
					t.Fatal("next capture missed replacement")
				}
			case "permission":
				if len(next.CLIs) != 0 || next.Agent.ConfigVersion <= old.Agent.ConfigVersion {
					t.Fatal("next capture missed permission removal")
				}
			case "prompt":
				if next.Prompts.AgentPrompt != "prompt-B" || next.Agent.ConfigVersion <= old.Agent.ConfigVersion {
					t.Fatal("next capture missed Prompt")
				}
			}
		})
	}
}
