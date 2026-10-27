package local

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/executor"
)

type larkGateNoNetwork struct{ attempts atomic.Int32 }

func (n *larkGateNoNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	n.attempts.Add(1)
	return nil, errors.New("TEST network forbidden")
}

func TestLarkDynamicBindingWithoutFactoryFailsBeforeLaunch(t *testing.T) {
	for _, task := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "task_runtime"}[task], func(t *testing.T) {
			pi := filepath.Join(t.TempDir(), "pi-trap")
			marker := []byte("#!/bin/sh\nprintf started > \"$0.started\"\nexit 98\n")
			if err := os.WriteFile(pi, marker, 0o700); err != nil {
				t.Fatal(err)
			}
			h := newHarness(t, func(o *Options) { o.PiBin = pi }, "")
			// Even a regression must not download/run a real CLI or provider.
			network := &larkGateNoNetwork{}
			h.e.inst = clitemplate.NewInstaller(h.root, time.Second, &http.Client{Transport: network})
			lark, ok := h.e.tmpl.Get("lark-cli")
			if !ok {
				t.Fatal("builtin Lark template missing")
			}
			native := filepath.Join(h.e.inst.BinDir(lark), lark.Binary)
			if os.MkdirAll(filepath.Dir(native), 0o700) != nil || os.WriteFile(native, marker, 0o700) != nil {
				t.Fatal("native trap fixture failed")
			}
			// A valid legacy bot payload is deliberately present as a fallback
			// trap. The dynamic binding must dominate it before any prepare.
			payload, _, err := lark.Render(map[string]string{"appId": "synthetic-bot-app", "appSecret": "synthetic-bot-secret"})
			if err != nil {
				t.Fatal("bot fixture invalid")
			}
			id := h.agent(func(s *agentconfig.Snapshot) {
				s.ConfigVersion = 17
				agent := s.AgentID
				if task {
					s.TaskID, s.ConfigurationAgentID = uuid.New(), uuid.New()
					agent = s.ConfigurationAgentID
				}
				s.CLIs = []string{"lark-cli"}
				s.CLIPayloads["lark-cli"] = payload
				s.LarkBinding = &clicredential.LarkBinding{ProjectID: uuid.New(), AccountID: uuid.New(), VersionID: uuid.New(), AgentID: agent, Brand: "feishu", AppID: "synthetic-user-app", OpenID: "synthetic-user", RuntimeVariantVersion: clicredential.LarkUserVariant}
				if !s.LarkBinding.Valid() {
					t.Fatal("dynamic binding fixture invalid")
				}
			})
			sink := newSink()
			result, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: uuid.New(), Message: "must not dispatch"}, sink)
			if err != nil || result.Outcome != executor.OutcomeFailed || result.ErrorCode != executor.ErrCredentialUnavailable || result.ConfigVersion != 17 {
				t.Fatalf("missing runtime factory did not fail closed: outcome=%s code=%s err=%v", result.Outcome, result.ErrorCode, err)
			}
			if len(sink.phases) != 0 || sink.running != 0 || len(sink.tools) != 0 || network.attempts.Load() != 0 {
				t.Fatal("dynamic request entered prepare/native dispatch")
			}
			for _, p := range []string{pi + ".started", native + ".started", h.e.dirs(id).root} {
				if _, err := os.Stat(p); !os.IsNotExist(err) {
					t.Fatal("Pi/native/workspace side effect before factory admission")
				}
			}
			h.e.mu.Lock()
			leaked := len(h.e.procs) != 0 || len(h.e.runs) != 0 || len(h.e.live) != 0
			h.e.mu.Unlock()
			if leaked {
				t.Fatal("failed dynamic admission retained a process/run")
			}
		})
	}
}
