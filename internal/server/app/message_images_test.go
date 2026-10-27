package app_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/contracttest"
	"loop-now/internal/server/messageimage"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
)

func imageFixture(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	im := image.NewRGBA(image.Rect(0, 0, 2, 3))
	im.Set(0, 0, color.RGBA{R: 200, A: 255})
	if e := png.Encode(&b, im); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func imageUpload(t *testing.T, s *server, path string, data []byte, status int) messageimage.Ref {
	t.Helper()
	req, _ := http.NewRequest("PUT", s.url+path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "image/png")
	r, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	if r.StatusCode != status {
		t.Fatalf("upload status %d: %s", r.StatusCode, b)
	}
	if status != 201 {
		return messageimage.Ref{}
	}
	return decode[messageimage.Ref](t, b)
}
func imageSession(t *testing.T, path string, encoded string, count int) {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if got := bytes.Count(b, []byte(encoded)); got != count {
		t.Fatalf("session image copies %d want %d", got, count)
	}
}
func TestMessageImagesAppPersistenceQueueAndTaskInbox(t *testing.T) {
	w := isolatedWorld(t) // own TEST schema/prefix only, Pi1.4 catalog + isolated fakePi.
	var logs bytes.Buffer
	w.logTo = &logs
	defer func() {
		if t.Failed() {
			for _, line := range strings.Split(logs.String(), "\n") {
				if strings.Contains(line, "level=ERROR") {
					t.Log(line)
				}
			}
		}
	}()
	s := w.start(nil)
	defer s.stop()
	ctx := context.Background()
	project := s.a.API.O.Defaults.Project.ID.String()
	s.must(201, "POST", "/projects/"+project+"/credential-accounts", map[string]any{"purpose": "model", "provider": "anthropic", "name": "offline image fixture", "credential": map[string]string{"type": "api_key", "key": "synthetic-image-only"}})
	path, a := s.createAgent("ImageAgent")
	other, _ := s.createAgent("OtherImageAgent")
	data := imageFixture(t)
	encoded := base64.StdEncoding.EncodeToString(data)
	id := uuid.New()
	upload := path + "/message-images/" + id.String()
	ref := imageUpload(t, s, upload, data, 201)
	imageUpload(t, s, upload, data, 201)
	if ref.ID != id || ref.Width != 2 || ref.Height != 3 {
		t.Fatal(ref)
	}
	s.must(404, "GET", "/message-images/"+id.String(), nil)
	s.must(404, "POST", other+"/messages", map[string]any{"text": "", "imageIds": []uuid.UUID{id}})
	s.must(422, "POST", path+"/messages", map[string]any{"text": "", "imageIds": nil})
	s.must(422, "POST", path+"/messages", map[string]any{"text": ""})
	s.must(422, "POST", path+"/messages", map[string]any{"text": "", "imageIds": []uuid.UUID{id, id}})
	var before int
	s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE agent_id=$1`, a.ID).Scan(&before)
	if before != 0 {
		t.Fatal("failed binding wrote runs", before)
	}
	s.must(202, "POST", path+"/messages", map[string]any{"text": "", "imageIds": []uuid.UUID{id}})
	s.waitIdle(path)
	if got := s.must(200, "GET", "/message-images/"+id.String(), nil); !bytes.Equal(data, got) {
		t.Fatal("image roundtrip")
	}
	sess, e := s.a.Store.GetAgentSession(ctx, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	imageSession(t, sess.SessionFile, encoded, 1)
	// A queued visual round retains its own association; a text interrupt gets no images.
	s.must(202, "POST", path+"/messages", map[string]any{"text": " !sleep=1s !text=held"})
	s.waitRunning(path)
	id2 := uuid.New()
	imageUpload(t, s, path+"/message-images/"+id2.String(), data, 201)
	s.must(202, "POST", path+"/messages", map[string]any{"text": "queued visual", "whenBusy": "queue", "imageIds": []uuid.UUID{id2}})
	s.waitIdle(path)
	imageSession(t, sess.SessionFile, encoded, 2)
	id3 := uuid.New()
	imageUpload(t, s, path+"/message-images/"+id3.String(), data, 201)
	s.must(202, "POST", path+"/messages", map[string]any{"text": " !sleep=1h !text=held"})
	s.waitRunning(path)
	s.must(202, "POST", path+"/messages", map[string]any{"text": "interrupt visual", "whenBusy": "interrupt", "imageIds": []uuid.UUID{id3}})
	s.waitIdle(path)
	imageSession(t, sess.SessionFile, encoded, 3)
	history := s.must(200, "GET", path+"/messages", nil)
	if bytes.Contains(history, []byte(encoded)) || !bytes.Contains(history, []byte(`"images"`)) {
		t.Fatal("history must use references")
	}
	s.must(409, "POST", path+"/messages", map[string]any{"text": "reuse", "imageIds": []uuid.UUID{id}})
	// Task uses the Inbox route and original action/run lineage, never the old direct route.
	d, p := sleepTask(t, s, " !sleep=1h !text=original_task")
	sleepWaitTurn(t, s, p.RuntimeID, uuid.Nil)
	node := d.Nodes[0]
	prefix := "/tasks/" + d.Task.ID.String() + "/nodes/" + node.ID.String()
	tid := uuid.New()
	imageUpload(t, s, prefix+"/message-images/"+tid.String(), data, 201)
	in := map[string]any{"actionId": uuid.New(), "expectedAgentId": node.AgentID, "expectedRuntimeId": node.RuntimeID, "text": "", "whenBusy": "interrupt", "imageIds": []uuid.UUID{tid}}
	s.must(422, "POST", prefix+"/messages", in)
	in["imageIds"] = []uuid.UUID{id}
	s.must(404, "POST", prefix+"/inbox-messages", in)
	in["imageIds"] = []uuid.UUID{tid}
	raw := s.must(201, "POST", prefix+"/inbox-messages", in)
	receipt := decode[taskmodel.TaskNodeInboxReceipt](t, raw)
	var wireReceipt any
	json.Unmarshal(raw, &wireReceipt)
	if e := contracttest.Load(t).Components.Schemas["TaskNodeInboxReceipt"].Value.VisitJSON(wireReceipt, openapi3.EnableJSONSchema2020()); e != nil {
		t.Fatal("actual visual receipt violates API", e)
	}
	replay := s.must(201, "POST", prefix+"/inbox-messages", in)
	if !bytes.Equal(raw, replay) {
		t.Fatal("action replay drift")
	}
	if !bytes.Contains(receipt.Message, []byte(tid.String())) || bytes.Contains(receipt.Message, []byte(encoded)) {
		t.Fatal("receipt image refs")
	}
	var inboxID uuid.UUID

	var wire struct {
		ID uuid.UUID `json:"id"`
	}
	json.Unmarshal(receipt.Message, &wire)
	inboxID = wire.ID
	deadline := time.Now().Add(8 * time.Second)
	for {
		var count int
		e = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE runtime_id=$1 AND status IN ('queued','starting','running')`, p.RuntimeID).Scan(&count)
		if e != nil {
			t.Fatal(e)
		}
		if count == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("task image reminder not settled")
		}
		time.Sleep(20 * time.Millisecond)
	}
	inst, e := s.a.Store.TaskInstance(ctx, p.RuntimeID)
	if e != nil || inst.SessionFile == nil {
		t.Fatal(e)
	}
	imageSession(t, *inst.SessionFile, encoded, 1)
	var readAt *time.Time
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT read_at FROM inbox_messages WHERE id=$1`, inboxID).Scan(&readAt); e != nil || readAt != nil {
		t.Fatal("visual delivery must not acknowledge Inbox", e, readAt)
	}
	s.must(200, "GET", "/message-images/"+tid.String(), nil)
	page := s.must(200, "GET", prefix+"/inbox-messages", nil)
	if strings.Contains(string(page), encoded) || !bytes.Contains(page, []byte(tid.String())) {
		t.Fatal("task history")
	}
	// Ending a TEST task preserves its existing Inbox image history without sending it elsewhere.
	if _, e = s.a.Tasks.Cancel(ctx, auth.Console(), d.Task.ID, taskmodel.CancelInput{ActionID: uuid.New()}); e != nil {
		t.Fatal(e)
	}
	s.must(200, "GET", "/message-images/"+tid.String(), nil)
	imageUpload(t, s, prefix+"/message-images/"+uuid.NewString(), data, 409)
	// Previous Agent pure text API still works and images did not spill into its normal runtime.
	st := decode[apitypes.AgentStatus](t, s.must(200, "GET", path+"/status", nil))
	if st.Busy {
		t.Fatal("normal runtime leaked busy")
	}
}

func TestMessageImagesTaskQueueBatchRetainsScope(t *testing.T) {
	t.Setenv("FAKEPI_PROMPT_DELAY", "600ms")
	w := isolatedWorld(t)
	w.cfg.ExecEnvPassthrough = []string{"FAKEPI_PROMPT_DELAY"}
	var logs bytes.Buffer
	w.logTo = &logs
	defer func() {
		if t.Failed() {
			for _, line := range strings.Split(logs.String(), "\n") {
				if strings.Contains(line, "level=ERROR") {
					t.Log(line)
				}
			}
		}
	}()
	s := w.start(nil)
	defer s.stop()
	ctx := context.Background()
	s.must(201, "POST", "/projects/"+s.a.API.O.Defaults.Project.ID.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": "anthropic", "name": "queue image fixture", "credential": map[string]string{"type": "api_key", "key": "synthetic-only"}})
	d, p := sleepTask(t, s, " !sleep=50ms !error=blocked_visual_queue")
	taskWait(t, s, d.Task.ID, func(v taskmodel.Detail) bool { return v.Task.Status == "blocked" })
	n := d.Nodes[0]
	prefix := "/tasks/" + d.Task.ID.String() + "/nodes/" + n.ID.String()
	data := imageFixture(t)
	send := func(count int) taskmodel.TaskNodeInboxReceipt {
		t.Helper()
		ids := []uuid.UUID{}
		for i := 0; i < count; i++ {
			id := uuid.New()
			imageUpload(t, s, prefix+"/message-images/"+id.String(), data, 201)
			ids = append(ids, id)
		}
		return decode[taskmodel.TaskNodeInboxReceipt](t, s.must(201, "POST", prefix+"/inbox-messages", map[string]any{"actionId": uuid.New(), "expectedAgentId": n.AgentID, "expectedRuntimeId": n.RuntimeID, "text": "", "whenBusy": "queue", "imageIds": ids}))
	}
	send(1)
	sleepWaitTurn(t, s, p.RuntimeID, uuid.Nil)
	// Remaining 2+2 coalesce, the fifth image needs another bounded turn.
	for _, count := range []int{2, 2, 1} {
		send(count)
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		var count int
		if e := s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE runtime_id=$1 AND status IN ('queued','starting','running')`, p.RuntimeID).Scan(&count); e != nil {
			t.Fatal(e)
		}
		if count == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("queued visual turns not drained")
		}
		time.Sleep(20 * time.Millisecond)
	}
	rows, e := s.a.Store.Pool.Query(ctx, `SELECT sum(jsonb_array_length(m.images))::int FROM task_node_inbox_messages b JOIN inbox_messages m ON m.id=b.message_id WHERE b.runtime_id=$1 GROUP BY b.reminder_run_id ORDER BY sum(jsonb_array_length(m.images))`, p.RuntimeID)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	var counts []int
	for rows.Next() {
		var v int
		if e = rows.Scan(&v); e != nil {
			t.Fatal(e)
		}
		counts = append(counts, v)
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	if len(counts) != 3 || counts[0] != 1 || counts[1] != 1 || counts[2] != 4 {
		t.Fatal("expected bounded merged groups 1/1/4", counts)
	}
	inst, e := s.a.Store.TaskInstance(ctx, p.RuntimeID)
	if e != nil || inst.SessionFile == nil {
		t.Fatal(e)
	}
	imageSession(t, *inst.SessionFile, base64.StdEncoding.EncodeToString(data), 6)
}

func TestMessageImagesUnsupportedModelAndQueuedConfigChange(t *testing.T) {
	w := isolatedWorld(t)
	s := w.start(nil)
	defer s.stop()
	ctx := context.Background()
	var provider, model string
	for _, p := range s.a.API.Models.Providers {
		for _, m := range p.Models {
			if !s.a.API.Models.SupportsImages(p.ID, m.ID) {
				for _, method := range p.AuthMethods {
					if method == "api_key" {
						provider, model = p.ID, m.ID
						break
					}
				}
			}
			if provider != "" {
				break
			}
		}
		if provider != "" {
			break
		}
	}
	if provider == "" {
		t.Fatal("installed catalog lacks text-only api-key fixture model")
	}
	project := s.a.API.O.Defaults.Project.ID.String()
	for _, pr := range []string{"anthropic", provider} {
		s.must(201, "POST", "/projects/"+project+"/credential-accounts", map[string]any{"purpose": "model", "provider": pr, "name": "offline " + pr, "credential": map[string]string{"type": "api_key", "key": "synthetic-only"}})
	}
	path, a := s.createAgent("QueuedModelImages")
	data := imageFixture(t)
	id := uuid.New()
	imageUpload(t, s, path+"/message-images/"+id.String(), data, 201)
	s.must(202, "POST", path+"/messages", map[string]any{"text": " !sleep=800ms !text=old_model_round"})
	s.waitRunning(path)
	receipt := decode[apitypes.SendMessageResult](t, s.must(202, "POST", path+"/messages", map[string]any{"text": "", "whenBusy": "queue", "imageIds": []uuid.UUID{id}}))
	if _, e := s.a.Store.UpdateAgent(ctx, a.ID, store.AgentPatch{ModelProvider: &provider, ModelID: &model}); e != nil {
		t.Fatal(e)
	}
	s.waitIdle(path)
	run, e := s.a.Store.GetRun(ctx, receipt.Run.ID)
	if e != nil || run.Status != "failed" || run.ErrorCode != "image_unsupported" {
		t.Fatalf("queued images must fail explicitly with actual model: %+v %v", run, e)
	}
	imageUpload(t, s, path+"/message-images/"+uuid.NewString(), data, 422)
	s.must(422, "POST", path+"/messages", map[string]any{"text": "", "imageIds": []uuid.UUID{id}})
	sess, e := s.a.Store.GetAgentSession(ctx, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	imageSession(t, sess.SessionFile, base64.StdEncoding.EncodeToString(data), 0)
}

func TestMessageImagesQueuedRestart(t *testing.T) {
	w := isolatedWorld(t)
	s := w.start(nil)
	defer func() {
		if s != nil {
			s.stop()
		}
	}()
	ctx := context.Background()
	s.must(201, "POST", "/projects/"+s.a.API.O.Defaults.Project.ID.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": "anthropic", "name": "restart image fixture", "credential": map[string]string{"type": "api_key", "key": "synthetic-only"}})
	path, a := s.createAgent("RestartImages")
	data := imageFixture(t)
	s.must(202, "POST", path+"/messages", map[string]any{"text": " !sleep=1h !text=interrupted_by_server_restart"})
	s.waitRunning(path)
	id := uuid.New()
	imageUpload(t, s, path+"/message-images/"+id.String(), data, 201)
	receipt := decode[apitypes.SendMessageResult](t, s.must(202, "POST", path+"/messages", map[string]any{"text": "", "whenBusy": "queue", "imageIds": []uuid.UUID{id}}))
	s.stop()
	s = nil
	s = w.start(nil)
	s.waitIdle(path)
	run, e := s.a.Store.GetRun(ctx, receipt.Run.ID)
	if e != nil || run.Status != "completed" {
		t.Fatalf("queued image restart %+v %v", run, e)
	}
	sess, e := s.a.Store.GetAgentSession(ctx, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	imageSession(t, sess.SessionFile, base64.StdEncoding.EncodeToString(data), 1)
}
