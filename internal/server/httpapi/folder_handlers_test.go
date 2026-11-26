package httpapi_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
)

// invalid marks requests that deliberately violate the contract, so the contract stub does not record them as failures.
var invalid = []string{"X-Contract-Invalid-Request", "1"}

// The three kinds of folders (Agent, project context, global context) share one set of endpoints; all responses are
// validated against the contract.
func TestFolderEndpoints(t *testing.T) {
	e := newEnv(t)
	agent := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "Folder", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	roots := map[string]string{
		"agent":   "/agents/" + agent.ID.String() + "/folder",
		"project": "/projects/" + e.dc.Project.ID.String() + "/context-folder",
		"global":  "/context-folder",
	}
	for name, base := range roots {
		t.Run(name, func(t *testing.T) {
			created := decode[apitypes.FolderEntry](t, e.expect(200, "PUT", base+"/content?path=docs/AGENTS.md&create=true", []byte("# Entry\nContent")))
			if created.Type != "file" || created.Path != "docs/AGENTS.md" || *created.Size != int64(len("# Entry\nContent")) {
				t.Fatalf("create: %+v", created)
			}
			e.expect(409, "PUT", base+"/content?path=docs/AGENTS.md&create=true", []byte("x"))
			e.expect(422, "PUT", base+"/content?path=docs/AGENTS.md&create=maybe", []byte("x"), invalid...)
			e.expect(413, "PUT", base+"/content?path=big.bin", bytes.Repeat([]byte("x"), 2<<20))
			e.expect(422, "PUT", base+"/content?path=../escape", []byte("x"))
			_, body, hdr := e.do("GET", base+"/content?path=docs/AGENTS.md", nil)
			if string(body) != "# Entry\nContent" || !strings.Contains(hdr.Get("Content-Disposition"), "attachment") {
				t.Fatalf("download: %q %v", body, hdr)
			}
			code, part, _ := e.do("GET", base+"/content?path=docs/AGENTS.md", nil, "Range", "bytes=0-1")
			if code != 206 || string(part) != "# " {
				t.Fatalf("Range: %d %q", code, part)
			}
			e.expect(422, "GET", base+"/content?path=docs", nil)
			e.expect(201, "POST", base+"/directories", map[string]any{"path": "empty/inner"})
			page := decode[apitypes.FolderEntryPage](t, e.expect(200, "GET", base+"/entries", nil))
			if page.Path != "" || len(page.Entries) != 2 || page.Entries[0].Name != "docs" || page.Entries[1].Name != "empty" || page.NextCursor != nil {
				t.Fatalf("root directory: %+v", page)
			}
			first := decode[apitypes.FolderEntryPage](t, e.expect(200, "GET", base+"/entries?limit=1", nil))
			if len(first.Entries) != 1 || first.NextCursor == nil {
				t.Fatalf("paging: %+v", first)
			}
			next := decode[apitypes.FolderEntryPage](t, e.expect(200, "GET", base+"/entries?limit=1&cursor="+*first.NextCursor, nil))
			if len(next.Entries) != 1 || next.Entries[0].Name != "empty" {
				t.Fatalf("next page: %+v", next)
			}
			e.expect(422, "GET", base+"/entries?limit=0", nil, invalid...)
			e.expect(404, "GET", base+"/entries?path=missing", nil)
			moved := decode[apitypes.FolderEntry](t, e.expect(200, "POST", base+"/move", map[string]any{"from": "docs/AGENTS.md", "to": "AGENTS.md"}))
			if moved.Path != "AGENTS.md" {
				t.Fatalf("move: %+v", moved)
			}
			e.expect(409, "POST", base+"/move", map[string]any{"from": "empty", "to": "AGENTS.md"})
			e.expect(422, "POST", base+"/move", map[string]any{"from": "empty", "to": "empty/inner/x"})
			e.expect(204, "DELETE", base+"/entries?path=empty", nil)
			e.expect(404, "DELETE", base+"/entries?path=empty", nil)
			e.expect(422, "DELETE", base+"/entries?path=", nil, invalid...)
		})
	}
	e.expect(404, "GET", "/agents/"+uuid.NewString()+"/folder/entries", nil)

	// Where the Agent folder is: the absolute path the files above were written under, and the same with symlinks
	// resolved (the temporary directory may sit behind one).
	loc := decode[apitypes.FolderLocation](t, e.expect(200, "GET", roots["agent"], nil))
	if !filepath.IsAbs(loc.Path) || !strings.HasSuffix(loc.Path, filepath.Join("agents", agent.ID.String(), "workspace")) {
		t.Fatalf("folder location: %+v", loc)
	}
	if b, err := os.ReadFile(filepath.Join(loc.Path, "AGENTS.md")); err != nil || string(b) != "# Entry\nContent" {
		t.Fatalf("the path should be the folder the endpoints write to: %q %v", b, err)
	}
	if real, _ := filepath.EvalSymlinks(loc.Path); loc.RealPath != real {
		t.Fatalf("realPath: %+v, want %q", loc, real)
	}
	e.expect(404, "GET", "/agents/"+uuid.NewString()+"/folder", nil)
	e.expect(404, "GET", "/projects/"+uuid.NewString()+"/context-folder/entries", nil)

	// Git pull: validation, list and query (actual cloning is covered by the folders package tests).
	a := roots["agent"]
	e.expect(422, "POST", a+"/git-pulls", map[string]any{"actionId": uuid.New(), "url": "https://user:pw@example.com/r.git", "branch": "main", "path": "repo"})
	e.expect(409, "POST", a+"/git-pulls", map[string]any{"actionId": uuid.New(), "url": "https://example.com/r.git", "branch": "main", "path": "AGENTS.md"})
	if list := decode[apitypes.FolderGitPullList](t, e.expect(200, "GET", a+"/git-pulls", nil)); len(list.Pulls) != 0 {
		t.Fatalf("a sync conflict should leave no pull record: %+v", list)
	}
	accepted := decode[apitypes.FolderGitPull](t, e.expect(202, "POST", a+"/git-pulls", map[string]any{"actionId": uuid.New(), "url": "file:///nonexistent/r.git", "branch": "main", "path": "repo"}))
	list := decode[apitypes.FolderGitPullList](t, e.expect(200, "GET", a+"/git-pulls", nil))
	if len(list.Pulls) != 1 || list.Pulls[0].ID != accepted.ID {
		t.Fatalf("an accepted pull should be in the list: %+v", list)
	}
	got := decode[apitypes.FolderGitPull](t, e.expect(200, "GET", a+"/git-pulls/"+accepted.ID.String(), nil))
	if got.ID != accepted.ID {
		t.Fatalf("query: %+v", got)
	}
	e.expect(422, "GET", a+"/git-pulls?limit=101", nil, invalid...)
	e.expect(404, "GET", a+"/git-pulls/"+uuid.NewString(), nil)
}
