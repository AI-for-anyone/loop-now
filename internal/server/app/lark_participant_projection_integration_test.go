package app_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
)

func TestLarkParticipantsActualHTTPReadOnlyAndAgentIsolation(t *testing.T) {
	s, in, snapshots, provider := taskLarkSwitchAppReady(t)
	ctx := context.Background()
	type counts struct {
		Version                                      int64
		Actions, Fences, Turns, Prepared, Generation int
	}
	readCounts := func() counts {
		t.Helper()
		var out counts
		if e := s.a.Store.Pool.QueryRow(ctx, `SELECT version,(SELECT count(*) FROM task_lark_switch_actions),(SELECT count(*) FROM task_lark_switch_fences),(SELECT count(*) FROM task_turns),(SELECT count(*) FROM task_instances WHERE prepared),(SELECT coalesce(sum(generation),0) FROM lark_cli_grants) FROM task_records WHERE id=$1`, in.TaskID).Scan(&out.Version, &out.Actions, &out.Fences, &out.Turns, &out.Prepared, &out.Generation); e != nil {
			t.Fatal(e)
		}
		return out
	}
	before := readCounts()
	devices, tokens, refreshes, users := provider.DeviceCalls.Load(), provider.TokenCalls.Load(), provider.RefreshCalls.Load(), provider.UserCalls.Load()
	raw := s.must(200, "GET", "/tasks/"+in.TaskID.String(), nil)
	detail := decode[apitypes.ConsoleTaskDetail](t, raw)
	if detail.LarkSwitchParticipants == nil || len(*detail.LarkSwitchParticipants) != 2 {
		t.Fatal("console omitted frozen members")
	}
	prepared := 0
	for _, p := range *detail.LarkSwitchParticipants {
		want, ok := snapshots[p.RuntimeID]
		if !ok || p.BaseSnapshotSHA != hex.EncodeToString(want[:]) || p.LarkAllowed == nil || !*p.LarkAllowed || !p.Available || p.UnavailableReason != nil || p.BindingRevision != 0 || len(p.StepIDs) != 1 {
			t.Fatal("invalid frozen participant metadata")
		}
		if p.Prepared {
			prepared++
		}
		current, e := s.a.Store.TaskInstance(ctx, p.RuntimeID)
		if e != nil || sha256.Sum256(current.Snapshot) != want {
			t.Fatal("read changed frozen snapshot")
		}
		if current.SessionFile != nil && strings.Contains(string(raw), *current.SessionFile) {
			t.Fatal("session path leaked")
		}
	}
	if prepared != 1 || before != readCounts() {
		t.Fatal("GET prepared or mutated task state")
	}
	if provider.DeviceCalls.Load() != devices || provider.TokenCalls.Load() != tokens || provider.RefreshCalls.Load() != refreshes || provider.UserCalls.Load() != users {
		t.Fatal("GET called credential provider")
	}
	// A genuine runtime token may read its existing task view, without acquiring
	// this console management projection or a new management route.
	token, e := auth.NewToken(auth.AgentPrefix)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.a.Store.InsertAgentToken(ctx, token.ID, in.Members[0].RuntimeID, s.a.InstanceID, token.Hash); e != nil {
		t.Fatal(e)
	}
	request, e := http.NewRequest("GET", strings.TrimSuffix(s.url, "/api/v1")+"/agent/v1/tasks/current", nil)
	if e != nil {
		t.Fatal(e)
	}
	request.Header.Set("Authorization", "Bearer "+token.Plain)
	response, e := http.DefaultClient.Do(request)
	if e != nil {
		t.Fatal(e)
	}
	body, e := io.ReadAll(response.Body)
	response.Body.Close()
	if e != nil || response.StatusCode != 200 {
		t.Fatal("existing Agent task read failed", response.StatusCode)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		t.Fatal("invalid Agent task view")
	}
	if _, ok := fields["larkSwitchParticipants"]; ok {
		t.Fatal("Agent received console management projection")
	}
}
