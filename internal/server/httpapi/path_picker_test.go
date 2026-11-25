package httpapi_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/pathpicker"
)

// fakeDialog stands for osascript on a Mac in a desktop session: each dialog answers with answer.
type fakeDialog struct {
	mu     sync.Mutex
	answer func(ctx context.Context) ([]byte, error)
}

func (d *fakeDialog) set(answer func(ctx context.Context) ([]byte, error)) {
	d.mu.Lock()
	d.answer = answer
	d.mu.Unlock()
}

func withPathPicker(e *env) *fakeDialog {
	e.t.Helper()
	osascript := filepath.Join(e.t.TempDir(), "osascript")
	if err := os.WriteFile(osascript, nil, 0o755); err != nil {
		e.t.Fatal(err)
	}
	d := &fakeDialog{}
	e.api.PathPicker = &pathpicker.Picker{GOOS: "darwin", OSAScript: osascript, Run: func(ctx context.Context, name string, _ []string) ([]byte, []byte, error) {
		if filepath.Base(name) == "launchctl" {
			return []byte("Aqua\n"), nil, nil
		}
		d.mu.Lock()
		answer := d.answer
		d.mu.Unlock()
		out, err := answer(ctx)
		return out, nil, err
	}}
	return d
}

func answers(out string) func(context.Context) ([]byte, error) {
	return func(context.Context) ([]byte, error) { return []byte(out), nil }
}

// Context says whether the console can ask for a dialog; POST /path-picks returns the chosen path or null, one dialog
// at a time, and is for the console only.
func TestPathPicksAPI(t *testing.T) {
	e := newEnv(t)
	if c := decode[apitypes.Context](t, e.expect(200, "GET", "/context", nil)); c.PathPicker {
		t.Fatal("no picker: want pathPicker false")
	}
	e.expectError(409, "path_picker_unavailable", "POST", "/path-picks", map[string]any{"kind": "directory"})

	d := withPathPicker(e)
	if c := decode[apitypes.Context](t, e.expect(200, "GET", "/context", nil)); !c.PathPicker {
		t.Fatal("a Mac in a desktop session: want pathPicker true")
	}
	_, tok := e.identity("ci-bot")
	ext := e.external(tok)
	if c := decode[apitypes.Context](t, ext.expect(200, "GET", "/context", nil)); c.PathPicker {
		t.Fatal("an external identity: want pathPicker false")
	}
	ext.expectError(403, "forbidden", "POST", "/path-picks", map[string]any{"kind": "directory"})

	d.set(answers(`{"path":"/Users/a/project"}` + "\n"))
	if p := decode[apitypes.PathPick](t, e.expect(200, "POST", "/path-picks", map[string]any{"kind": "directory", "defaultPath": "relative"})); p.Path == nil || *p.Path != "/Users/a/project" {
		t.Fatalf("chosen: %+v", p)
	}
	d.set(answers(`{"path":null}` + "\n"))
	if b := e.expect(200, "POST", "/path-picks", map[string]any{"kind": "file"}); !bytes.Equal(bytes.TrimSpace(b), []byte(`{"path":null}`)) {
		t.Fatalf("cancelled: %s", b)
	}

	e.expectError(422, "validation_failed", "POST", "/path-picks", map[string]any{"kind": "file", "defaultPath": "/a\x00b"})
	invalidRequest(e, 422, "validation_failed", "POST", "/path-picks", map[string]any{"kind": "folder"})
	invalidRequest(e, 422, "validation_failed", "POST", "/path-picks", map[string]any{})

	d.set(func(context.Context) ([]byte, error) { return nil, errors.New("exit status 1") })
	e.expectError(409, "path_picker_unavailable", "POST", "/path-picks", map[string]any{"kind": "directory"})
}

// While a dialog is open another request is busy; closing the request closes the dialog, and the next one opens.
func TestPathPicksBusyAndClosed(t *testing.T) {
	e := newEnv(t)
	d := withPathPicker(e)
	opened, stopped := make(chan struct{}), make(chan struct{})
	d.set(func(ctx context.Context) ([]byte, error) {
		close(opened)
		<-ctx.Done()
		close(stopped)
		return nil, errors.New("signal: killed")
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", e.srv.URL+"/api/v1/path-picks", bytes.NewReader([]byte(`{"kind":"directory"}`)))
	req.Header.Set("Content-Type", "application/json")
	done := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
		done <- err
	}()
	<-opened
	e.expectError(409, "path_picker_busy", "POST", "/path-picks", map[string]any{"kind": "file"})
	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the dialog was not stopped when the request closed")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("the request: %v", err)
	}

	d.set(answers(`{"path":"/next"}`))
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, body, _ := e.do("POST", "/path-picks", map[string]any{"kind": "directory"})
		if status == 200 {
			if p := decode[apitypes.PathPick](t, body); p.Path == nil || *p.Path != "/next" {
				t.Fatalf("next: %s", body)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the picker stayed busy: %d %s", status, body)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
