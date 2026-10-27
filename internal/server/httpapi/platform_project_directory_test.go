package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/contracttest"
	"loop-now/internal/server/httpapi"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
)

func platformDirectoryHTTPStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	u := pgtest.URL(t)
	c, err := pgx.Connect(ctx, u)
	if err != nil {
		t.Fatal("TEST connection unavailable")
	}
	schema := "platform_directory_http_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = c.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer c.Close(ctx)
		if _, err := c.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	st, err := store.Open(ctx, u, store.OpenOptions{SearchPath: schema})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err = st.Migrate(ctx, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestPlatformProjectDirectoryHTTP(t *testing.T) {
	ctx := context.Background()
	st := platformDirectoryHTTPStore(t)
	dc, err := st.EnsureDefaults(ctx, "组织", "项目")
	if err != nil {
		t.Fatal(err)
	}
	makeAgent := func(scope store.Scope, name string) store.Agent {
		t.Helper()
		a, err := st.CreateAgent(ctx, scope, name, "openai", "synthetic", "private-prompt")
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	platform := makeAgent(store.PlatformScope(), "平台发现者")
	manager := makeAgent(store.ProjectScope(dc.Project.ID), "项目负责人")
	if _, err = st.SetProjectManager(ctx, dc.Project.ID, manager.ID); err != nil {
		t.Fatal(err)
	}
	token := func(a uuid.UUID) auth.Token {
		t.Helper()
		v, err := auth.NewToken(auth.AgentPrefix)
		if err != nil {
			t.Fatal(err)
		}
		if err = st.InsertAgentToken(ctx, v.ID, a, uuid.New(), v.Hash); err != nil {
			t.Fatal(err)
		}
		return v
	}
	pt, mt := token(platform.ID), token(manager.ID)
	api := &httpapi.Server{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), O: httpapi.Options{AllowedHosts: []string{"127.0.0.1", "localhost"}}}
	srv := httptest.NewServer(api.Handler())
	defer srv.Close()
	v := contracttest.NewAgent(t, srv.URL)
	client := v.Client()
	request := func(path, tok string, status int) []byte {
		t.Helper()
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != status {
			t.Fatalf("directory status=%d want=%d body=%s", resp.StatusCode, status, b)
		}
		return b
	}
	b := request("/agent/v1/projects", pt.Plain, 200)
	var response struct {
		Projects []store.PlatformProjectDirectoryItem `json:"projects"`
	}
	if err = json.Unmarshal(b, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Projects) != 1 || response.Projects[0].Manager == nil || response.Projects[0].Manager.ID != manager.ID || response.Projects[0].Manager.Address != "组织/项目/项目负责人" {
		t.Fatal("wrong safe directory")
	}
	for _, bad := range []string{"prompt", "private-prompt", "model", "credential", "agentCount"} {
		if strings.Contains(string(b), bad) {
			t.Fatal("unsafe directory field")
		}
	}
	request("/agent/v1/projects", mt.Plain, 403)
	request("/agent/v1/projects?platform=true&agentId="+platform.ID.String(), mt.Plain, 403)
	request("/agent/v1/projects", "", 401)
	if err = st.RevokeAgentToken(ctx, pt.ID); err != nil {
		t.Fatal(err)
	}
	request("/agent/v1/projects", pt.Plain, 401)
	pt = token(platform.ID)
	// Test a stale identity by changing only isolated fixture persistence; the
	// authenticated token cannot override the current non-NULL ownership.
	if _, err = st.Pool.Exec(ctx, `UPDATE agents SET project_id=$2 WHERE id=$1`, platform.ID, dc.Project.ID); err != nil {
		t.Fatal(err)
	}
	request("/agent/v1/projects", pt.Plain, 403)
	if _, err = st.Pool.Exec(ctx, `UPDATE agents SET project_id=NULL WHERE id=$1`, platform.ID); err != nil {
		t.Fatal(err)
	}
	request("/agent/v1/projects", pt.Plain, 200)
	// Management paths remain absent from the Agent namespace.
	for _, path := range []string{"/projects/" + dc.Project.ID.String(), "/projects/" + dc.Project.ID.String() + "/credential-accounts", "/agents/" + manager.ID.String()} {
		req, _ := http.NewRequest("GET", srv.URL+"/agent/v1"+path, nil)
		req.Header.Set("Authorization", "Bearer "+pt.Plain)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Fatal("Agent management route widened")
		}
	}
	if err = st.SoftDeleteAgent(ctx, platform.ID); err != nil {
		t.Fatal(err)
	}
	request("/agent/v1/projects", pt.Plain, 401)
	st.Close()
	failed := request("/agent/v1/projects", mt.Plain, 500)
	if strings.Contains(string(failed), "private-prompt") || strings.Contains(string(failed), mt.Plain) {
		t.Fatal("internal failure exposed private data")
	}
}
