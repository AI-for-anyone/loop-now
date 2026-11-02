package app_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/clicredential"
)

type toolsGoogleLogin func(context.Context, uuid.UUID, func(string) error) (clicredential.Google, error)

func (f toolsGoogleLogin) Login(ctx context.Context, id uuid.UUID, url func(string) error) (clicredential.Google, error) {
	return f(ctx, id, url)
}

type toolsGoogleRefresh func(context.Context, clicredential.Google) (clicredential.Google, error)

func (f toolsGoogleRefresh) Refresh(ctx context.Context, c clicredential.Google) (clicredential.Google, error) {
	return f(ctx, c)
}

type toolsProcessDump struct {
	Env map[string]string `json:"env"`
	Cwd string            `json:"cwd"`
}

func toolsScopeDump(t *testing.T, base string, scope uuid.UUID) toolsProcessDump {
	t.Helper()
	files, e := filepath.Glob(base + ".*")
	if e != nil {
		t.Fatal(e)
	}
	var found toolsProcessDump
	var latest time.Time
	for _, file := range files {
		raw, e := os.ReadFile(file)
		if e != nil {
			continue
		}
		var d toolsProcessDump
		e = json.Unmarshal(raw, &d)
		clear(raw)
		if e != nil || !strings.Contains(d.Env["HOME"], scope.String()) {
			continue
		}
		fi, e := os.Stat(file)
		if e == nil && fi.ModTime().After(latest) {
			latest = fi.ModTime()
			found = d
		}
	}
	if found.Env == nil {
		t.Fatal("private scope process metadata not found")
	}
	return found
}
func toolsBrokerAccess(t *testing.T, d toolsProcessDump) string {
	t.Helper()
	conn, e := net.DialTimeout("unix", d.Env["LOOP_NOW_GCLOUD_BRIDGE_SOCKET"], time.Second)
	if e != nil {
		t.Fatal("captured broker unavailable")
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	json.NewEncoder(conn).Encode(map[string]any{"protocol": 1, "type": "resolve", "requestId": uuid.New(), "capability": d.Env["LOOP_NOW_GCLOUD_BRIDGE_CAPABILITY"]})
	raw, e := io.ReadAll(conn)
	if e != nil {
		t.Fatal("broker did not close complete reply")
	}
	defer clear(raw)
	var reply struct{ Type, AccessToken string }
	if json.Unmarshal(raw, &reply) != nil || reply.Type != "credential" {
		t.Fatal("captured broker did not resolve")
	}
	return reply.AccessToken
}

// Complete official SDK extraction/validation is bounded by the configured
// one-minute install budget, including race instrumentation. Do not reuse the
// lightweight fakePi test helper's shorter thirty-second install assumption.
func toolsWaitIdle(t *testing.T, s *server, path string) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		st := decode[apitypes.AgentStatus](t, s.must(200, "GET", path+"/status", nil))
		owner, e := s.a.Coord.LockOwner(context.Background(), strings.TrimPrefix(path, "/agents/"))
		if !st.Busy && st.Queued.Interrupt == 0 && st.Queued.Normal == 0 && e == nil && owner == "" {
			return
		}
		if time.Now().After(deadline) {
			phase, status, code := "", "", ""
			if st.CurrentRun != nil {
				status = st.CurrentRun.Status
				if st.CurrentRun.Phase != nil {
					phase = *st.CurrentRun.Phase
				}
				if st.CurrentRun.ErrorCode != nil {
					code = *st.CurrentRun.ErrorCode
				}
			}
			t.Fatalf("tool App idle deadline: busy=%t status=%s phase=%s code=%s", st.Busy, status, phase, code)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
