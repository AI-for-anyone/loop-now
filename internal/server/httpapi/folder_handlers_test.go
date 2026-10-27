package httpapi_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
)

// invalid 标记故意违反契约的请求，契约桩不把它们记为失败。
var invalid = []string{"X-Contract-Invalid-Request", "1"}

// 三类文件夹（Agent、项目上下文、全局上下文）共用同一组接口；响应都经契约校验。
func TestFolderEndpoints(t *testing.T) {
	e := newEnv(t)
	agent := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "文件夹", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	roots := map[string]string{
		"agent":   "/agents/" + agent.ID.String() + "/folder",
		"project": "/projects/" + e.dc.Project.ID.String() + "/context-folder",
		"global":  "/context-folder",
	}
	for name, base := range roots {
		t.Run(name, func(t *testing.T) {
			created := decode[apitypes.FolderEntry](t, e.expect(200, "PUT", base+"/content?path=docs/AGENTS.md&create=true", []byte("# 入口\n内容")))
			if created.Type != "file" || created.Path != "docs/AGENTS.md" || *created.Size != int64(len("# 入口\n内容")) {
				t.Fatalf("新建：%+v", created)
			}
			e.expect(409, "PUT", base+"/content?path=docs/AGENTS.md&create=true", []byte("x"))
			e.expect(422, "PUT", base+"/content?path=docs/AGENTS.md&create=maybe", []byte("x"), invalid...)
			e.expect(413, "PUT", base+"/content?path=big.bin", bytes.Repeat([]byte("x"), 2<<20))
			e.expect(422, "PUT", base+"/content?path=../escape", []byte("x"))
			_, body, hdr := e.do("GET", base+"/content?path=docs/AGENTS.md", nil)
			if string(body) != "# 入口\n内容" || !strings.Contains(hdr.Get("Content-Disposition"), "attachment") {
				t.Fatalf("下载：%q %v", body, hdr)
			}
			code, part, _ := e.do("GET", base+"/content?path=docs/AGENTS.md", nil, "Range", "bytes=0-1")
			if code != 206 || string(part) != "# " {
				t.Fatalf("Range：%d %q", code, part)
			}
			e.expect(422, "GET", base+"/content?path=docs", nil)
			e.expect(201, "POST", base+"/directories", map[string]any{"path": "empty/inner"})
			page := decode[apitypes.FolderEntryPage](t, e.expect(200, "GET", base+"/entries", nil))
			if page.Path != "" || len(page.Entries) != 2 || page.Entries[0].Name != "docs" || page.Entries[1].Name != "empty" || page.NextCursor != nil {
				t.Fatalf("根目录：%+v", page)
			}
			first := decode[apitypes.FolderEntryPage](t, e.expect(200, "GET", base+"/entries?limit=1", nil))
			if len(first.Entries) != 1 || first.NextCursor == nil {
				t.Fatalf("分页：%+v", first)
			}
			next := decode[apitypes.FolderEntryPage](t, e.expect(200, "GET", base+"/entries?limit=1&cursor="+*first.NextCursor, nil))
			if len(next.Entries) != 1 || next.Entries[0].Name != "empty" {
				t.Fatalf("下一页：%+v", next)
			}
			e.expect(422, "GET", base+"/entries?limit=0", nil, invalid...)
			e.expect(404, "GET", base+"/entries?path=missing", nil)
			moved := decode[apitypes.FolderEntry](t, e.expect(200, "POST", base+"/move", map[string]any{"from": "docs/AGENTS.md", "to": "AGENTS.md"}))
			if moved.Path != "AGENTS.md" {
				t.Fatalf("移动：%+v", moved)
			}
			e.expect(409, "POST", base+"/move", map[string]any{"from": "empty", "to": "AGENTS.md"})
			e.expect(422, "POST", base+"/move", map[string]any{"from": "empty", "to": "empty/inner/x"})
			e.expect(204, "DELETE", base+"/entries?path=empty", nil)
			e.expect(404, "DELETE", base+"/entries?path=empty", nil)
			e.expect(422, "DELETE", base+"/entries?path=", nil, invalid...)
		})
	}
	e.expect(404, "GET", "/agents/"+uuid.NewString()+"/folder/entries", nil)
	e.expect(404, "GET", "/projects/"+uuid.NewString()+"/context-folder/entries", nil)

	// Git 拉取：校验、列表与查询（实际克隆见 folders 包的测试）。
	a := roots["agent"]
	e.expect(422, "POST", a+"/git-pulls", map[string]any{"actionId": uuid.New(), "url": "https://user:pw@example.com/r.git", "branch": "main", "path": "repo"})
	e.expect(409, "POST", a+"/git-pulls", map[string]any{"actionId": uuid.New(), "url": "https://example.com/r.git", "branch": "main", "path": "AGENTS.md"})
	if list := decode[apitypes.FolderGitPullList](t, e.expect(200, "GET", a+"/git-pulls", nil)); len(list.Pulls) != 0 {
		t.Fatalf("同步冲突不应留下拉取记录：%+v", list)
	}
	accepted := decode[apitypes.FolderGitPull](t, e.expect(202, "POST", a+"/git-pulls", map[string]any{"actionId": uuid.New(), "url": "file:///nonexistent/r.git", "branch": "main", "path": "repo"}))
	list := decode[apitypes.FolderGitPullList](t, e.expect(200, "GET", a+"/git-pulls", nil))
	if len(list.Pulls) != 1 || list.Pulls[0].ID != accepted.ID {
		t.Fatalf("受理的拉取应在列表中：%+v", list)
	}
	got := decode[apitypes.FolderGitPull](t, e.expect(200, "GET", a+"/git-pulls/"+accepted.ID.String(), nil))
	if got.ID != accepted.ID {
		t.Fatalf("查询：%+v", got)
	}
	e.expect(422, "GET", a+"/git-pulls?limit=101", nil, invalid...)
	e.expect(404, "GET", a+"/git-pulls/"+uuid.NewString(), nil)
}
