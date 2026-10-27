package app_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/apitypes"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/larkauth/larktest"
	"loop-now/internal/server/store"
)

// larkCaptureBarrier pauses the first model grant read, which happens after
// the execution identity capture committed and before CLI credentials decode.
type larkCaptureBarrier struct {
	*store.Store
	once             sync.Once
	entered, release chan struct{}
}

func (b *larkCaptureBarrier) GetModelAuthVersion(ctx context.Context, id uuid.UUID) (store.ModelAuthVersion, error) {
	b.once.Do(func() {
		close(b.entered)
		select {
		case <-b.release:
		case <-ctx.Done():
		}
	})
	return b.Store.GetModelAuthVersion(ctx, id)
}

func TestLarkAppCaptureDoesNotMixConfigurationWithNextAccount(t *testing.T) {
	w := githubAppWorld(t)
	p := larktest.New(t)
	s := w.start(nil)
	defer s.stop()
	s.a.CLIAuth.Lark = p.Client
	_, base, accounts, source := larkAppSource(t, s)
	s.must(201, "POST", accounts, map[string]any{"purpose": "model", "provider": "anthropic", "name": "model", "credential": map[string]string{"type": "api_key", "key": "synthetic-capture-key"}})
	users := []cm.Account{}
	for _, name := range []string{"first", "second"} {
		a := p.Add("ou-"+name, name, nil, "")
		a.Release()
		id := uuid.New()
		s.must(202, "POST", base, map[string]any{"operationId": id, "cli": "lark-cli", "authType": "lark_user", "sourceAccountId": source.ID, "expectedSourceRevision": source.Revision})
		result := waitLarkApp(t, s, base, id, "succeeded")
		users = append(users, *result.Account)
	}
	path, agent := s.createAgent("capture-race")
	s.must(200, "PUT", path+"/clis/lark-cli", nil)
	current := decode[apitypes.Agent](t, s.must(200, "GET", path, nil))
	s.must(200, "PUT", path+"/credential-selections", map[string]any{"purpose": "cli", "cli": "lark-cli", "accountId": users[0].ID, "expectedConfigVersion": current.ConfigVersion})
	current = decode[apitypes.Agent](t, s.must(200, "GET", path, nil))
	coordinator := s.a.Config.ModelAuth.Coordinator
	barrier := &larkCaptureBarrier{Store: s.a.Store, entered: make(chan struct{}), release: make(chan struct{})}
	original := coordinator.Store
	coordinator.Store = barrier
	defer func() { coordinator.Store = original }()
	type result struct {
		snapshot agentconfig.Snapshot
		err      error
	}
	done := make(chan result, 1)
	go func() { v, e := s.a.Config.ExecutionSnapshot(context.Background(), agent.ID); done <- result{v, e} }()
	select {
	case <-barrier.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("execution capture never reached the post-capture barrier")
	}
	// Capture has committed its references; this changes the live selection
	// before secret decoding resumes. It must not change that earlier capture.
	s.must(200, "PUT", path+"/credential-selections", map[string]any{"purpose": "cli", "cli": "lark-cli", "accountId": users[1].ID, "expectedConfigVersion": current.ConfigVersion})
	close(barrier.release)
	first := <-done
	if first.err != nil || first.snapshot.ConfigVersion != current.ConfigVersion || first.snapshot.LarkBinding == nil || first.snapshot.LarkBinding.AccountID != users[0].ID || first.snapshot.LarkBinding.VersionID != *users[0].CurrentVersionID || len(first.snapshot.CLIPayloads["lark-cli"].Files) != 0 {
		t.Fatal("capture mixed old configuration with new credential")
	}
	next, e := s.a.Config.ExecutionSnapshot(context.Background(), agent.ID)
	latest := decode[apitypes.Agent](t, s.must(200, "GET", path, nil))
	if e != nil || next.ConfigVersion != latest.ConfigVersion || next.ConfigVersion <= first.snapshot.ConfigVersion || next.LarkBinding == nil || next.LarkBinding.AccountID != users[1].ID {
		t.Fatal("next capture failed to adopt explicit next selection")
	}
}
