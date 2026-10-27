package app_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"loop-now/internal/server/taskmodel"
	"loop-now/internal/testutil/testenv"
)

// Server owns this harness. Web owns only its driver/product files. All data are
// synthetic and live in this test's own schema, Redis prefix and temporary root.
func TestWebMessageImagesIntegration(t *testing.T) {
	if os.Getenv("LOOP_NOW_WEB_IMAGE_APP") != "1" {
		t.Skip("explicit isolated browser slice")
	}
	w := isolatedWorld(t)
	w.cfg.ConsoleOrigins = []string{"http://127.0.0.1:4344"}
	s := w.start(nil)
	defer s.stop()
	ctx := context.Background()
	project := s.a.API.O.Defaults.Project
	s.must(201, "POST", "/projects/"+project.ID.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": "anthropic", "name": "offline image browser", "credential": map[string]string{"type": "api_key", "key": "synthetic-browser-only"}})
	_, agent := s.createAgent("BrowserImages")
	d, p := sleepTask(t, s, " !error=synthetic_initial_failure")
	taskWait(t, s, d.Task.ID, func(v taskmodel.Detail) bool { return v.Task.Status == "blocked" })
	root := testenv.RepoRoot()
	evidence := filepath.Join(root, "web/test-results/image-app", time.Now().UTC().Format("20060102T150405.000000000Z"))
	if e := os.MkdirAll(evidence, 0700); e != nil {
		t.Fatal(e)
	}
	manifest := filepath.Join(t.TempDir(), "input.json")
	raw, _ := json.Marshal(map[string]any{"apiBase": s.url, "projectId": project.ID, "projectName": project.Name, "agentId": agent.ID, "taskId": d.Task.ID, "nodeRunId": d.Nodes[0].ID, "evidenceDir": evidence})
	if e := os.WriteFile(manifest, raw, 0600); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(w.cfg.NodeBin, filepath.Join(root, "web/scripts/image-app-browser.mjs"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "LOOP_NOW_WEB_IMAGE_MANIFEST="+manifest)
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("browser driver: %v %.2000s evidence=%s", e, b, evidence)
	}
	s.waitIdle("/agents/" + agent.ID.String())
	deadline := time.Now().Add(5 * time.Second)
	for {
		var n int
		if e := s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE runtime_id=$1 AND status IN ('queued','starting','running')`, p.RuntimeID).Scan(&n); e != nil {
			t.Fatal(e)
		}
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("visual Inbox did not settle")
		}
		time.Sleep(20 * time.Millisecond)
	}
	png, e := os.ReadFile(filepath.Join(root, "web/test-fixtures/images/pixel.png"))
	if e != nil {
		t.Fatal(e)
	}
	encoded := base64.StdEncoding.EncodeToString(png)
	session, e := s.a.Store.GetAgentSession(ctx, agent.ID)
	if e != nil {
		t.Fatal(e)
	}
	imageSession(t, session.SessionFile, encoded, 1)
	instance, e := s.a.Store.TaskInstance(ctx, p.RuntimeID)
	if e != nil || instance.SessionFile == nil {
		t.Fatal(e)
	}
	imageSession(t, *instance.SessionFile, encoded, 2)
	var images, bound, inbox, read int
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE conversation_message_id IS NOT NULL OR inbox_message_id IS NOT NULL) FROM message_images`).Scan(&images, &bound); e != nil || images != 3 || bound != 3 {
		t.Fatal(images, bound, e)
	}
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE read_at IS NOT NULL) FROM inbox_messages WHERE jsonb_array_length(images)>0`).Scan(&inbox, &read); e != nil || inbox != 1 || read != 0 {
		t.Fatal("delivery changed true read semantics", inbox, read, e)
	}
	t.Logf("browser image references + actual fakePi RPC session normal=1/task=2; images3/bound3/visualInbox1/unread1; evidence=%s", evidence)
}
