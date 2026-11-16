package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/providerstate"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/sqlitetest"
)

// platformPluginLogin records one plugin provider login for a platform Agent (the same database commit as a successful
// login) and creates its state directory.
func platformPluginLogin(t *testing.T, st *store.Store, states *providerstate.Manager, agent store.Agent) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	cur, err := st.GetAgent(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	op, err := st.NewModelAuthOperation(ctx, agent.ID, agent.ModelProvider, cur.ConfigVersion, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	credential, err := modelcredential.PluginStateOf("grokish")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := credential.Encode()
	if err != nil {
		t.Fatal(err)
	}
	version := uuid.Must(uuid.NewV7())
	if _, v, err := st.CompletePluginModelAuthOperation(ctx, op.ID, version, plain); err != nil || v == nil {
		t.Fatalf("提交登录：%v %v", v, err)
	}
	if err := os.MkdirAll(states.Dir(version), 0o700); err != nil {
		t.Fatal(err)
	}
	return version
}

// A platform Agent's own plugin provider login (plugins/SPEC.md §13): the sweeper keeps the state directory of its
// current version; uninstalling the plugin with data deletion clears the platform Agent logins under those providers
// (same as clearing the credential, and notifies that the Agent changed) and leaves other providers alone; the sweeper
// deletes the old directories afterwards.
func TestPlatformProviderLoginsSweepAndUninstall(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.NewStore(t)
	if _, err := st.EnsureDefaults(ctx, "组织", "项目"); err != nil {
		t.Fatal(err)
	}
	states := &providerstate.Manager{Root: t.TempDir()}
	auth := modelauth.NewService(st, nil, nil, nil, modelauth.ServiceOptions{}, nil)
	var changed []uuid.UUID
	auth.Changed = func(id uuid.UUID) { changed = append(changed, id) }
	a := &App{Store: st, ProviderStates: states, ModelAuth: auth, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	grok, err := st.CreateAgent(ctx, store.PlatformScope(), "平台助手", "fake-oauth", "fake-1", "")
	if err != nil {
		t.Fatal(err)
	}
	version := platformPluginLogin(t, st, states, grok)
	keyed, err := st.CreateAgent(ctx, store.PlatformScope(), "用 Key 的", "anthropic", "claude-x", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutModelAuthKey(ctx, keyed.ID, "anthropic", nil, []byte(`{"type":"api_key","key":"fixture"}`)); err != nil {
		t.Fatal(err)
	}
	stray := uuid.New()
	if err := os.MkdirAll(states.Dir(stray), 0o700); err != nil {
		t.Fatal(err)
	}

	a.sweepProviderStates(ctx, true)
	if _, err := os.Stat(states.Dir(version)); err != nil {
		t.Fatalf("平台 Agent 当前版本的状态目录要保留：%v", err)
	}
	if _, err := os.Stat(states.Dir(stray)); !os.IsNotExist(err) {
		t.Fatalf("没人用的状态目录应删除：%v", err)
	}

	if err := a.deleteProviderLogins(ctx, []string{"other-provider"}); err != nil || len(changed) != 0 {
		t.Fatalf("别的提供方不动：%v %v", changed, err)
	}
	before, err := st.GetAgent(ctx, grok.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.deleteProviderLogins(ctx, []string{"fake-oauth"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(changed, []uuid.UUID{grok.ID}) {
		t.Fatalf("应通知被清掉登录的 Agent：%v", changed)
	}
	if v, exists, err := st.CurrentModelAuthVersion(ctx, grok.ID); err != nil || !exists || v != nil {
		t.Fatalf("平台 Agent 的登录应清掉：%v %v %v", v, exists, err)
	}
	if after, _ := st.GetAgent(ctx, grok.ID); after.ConfigVersion != before.ConfigVersion+1 {
		t.Fatalf("与清除凭证相同，配置版本加一：%d → %d", before.ConfigVersion, after.ConfigVersion)
	}
	if v, _, err := st.CurrentModelAuthVersion(ctx, keyed.ID); err != nil || v == nil {
		t.Fatalf("别的提供方的凭证不动：%v %v", v, err)
	}
	a.sweepProviderStates(ctx, false)
	if _, err := os.Stat(states.Dir(version)); !os.IsNotExist(err) {
		t.Fatalf("清掉后状态目录由清理器删除：%v", err)
	}
}
