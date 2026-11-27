package httpapi_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loop-now/internal/server/apitypes"
)

const rawSandbox = "sandbox allow-scripts allow-forms allow-popups allow-modals allow-downloads"

// checkRawHeaders checks the headers that keep a file of an Agent folder from acting as the console; every response of
// the raw endpoints carries them, errors included.
func checkRawHeaders(t *testing.T, what string, h http.Header) {
	t.Helper()
	if h.Get("Content-Security-Policy") != rawSandbox || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("%s: sandbox headers missing: %v", what, h)
	}
	if strings.Contains(h.Get("Content-Security-Policy"), "allow-same-origin") {
		t.Errorf("%s: the page must not keep the console's origin", what)
	}
}

// TestFolderRawFile serves files of an Agent folder as themselves, sandboxed, under a URL path that mirrors the folder,
// so a page's relative URLs reach the files beside it.
func TestFolderRawFile(t *testing.T) {
	e := newEnv(t)
	agent := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "Pages", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	base := "/agents/" + agent.ID.String() + "/folder"
	loc := decode[apitypes.FolderLocation](t, e.expect(200, "GET", base, nil))
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(loc.Path, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("site/index.html", `<link rel="stylesheet" href="style.css"><img src="img/dot.png"><a href="../notes.md">notes</a>`)
	write("site/style.css", "body{color:red}")
	write("site/app.js", "console.log(1)")
	write("site/img/dot.png", "\x89PNG\r\n\x1a\n")
	write("site/image.svg", `<svg xmlns="http://www.w3.org/2000/svg"></svg>`)
	write("site/data.json", `{"a":1}`)
	write("notes.md", "# Notes 中文") // check-english: zh (UTF-8 text must be served as UTF-8)
	write("Makefile", "all:\n\techo hi\n")
	write("blob.bin", "\x00\x01\x02\xff")
	write("big.html", strings.Repeat("x", 2048)) // the test limit (textEdit.maxBytes) is 1024
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(loc.Path, "escape.txt")); err != nil {
		t.Fatal(err)
	}

	// The contract router matches the path parameter as one URL segment, so contract-checked requests encode the
	// slashes; a browser sends them as they are (checked below without the contract client).
	types := map[string]string{
		"site%2Findex.html":    "text/html; charset=utf-8",
		"site%2Fstyle.css":     "text/css; charset=utf-8",
		"site%2Fapp.js":        "text/javascript; charset=utf-8",
		"site%2Fimg%2Fdot.png": "image/png",
		"site%2Fimage.svg":     "image/svg+xml",
		"site%2Fdata.json":     "application/json",
		"notes.md":             "text/plain; charset=utf-8",
		"Makefile":             "text/plain; charset=utf-8",
		"blob.bin":             "application/octet-stream",
	}
	for p, want := range types {
		code, _, h := e.do("GET", base+"/raw/"+p, nil)
		if code != 200 || h.Get("Content-Type") != want {
			t.Errorf("%s: %d %q, want %q", p, code, h.Get("Content-Type"), want)
		}
		checkRawHeaders(t, p, h)
		if h.Get("Content-Disposition") != "" {
			t.Errorf("%s: shown in the tab, not downloaded: %v", p, h)
		}
	}
	code, body, _ := e.do("GET", base+"/raw/notes.md", nil)
	if code != 200 || string(body) != "# Notes 中文" { // check-english: zh (UTF-8 text must be served as UTF-8)
		t.Fatalf("bytes as they are: %d %q", code, body)
	}
	code, part, h := e.do("GET", base+"/raw/site%2Fstyle.css", nil, "Range", "bytes=0-3")
	if code != 206 || string(part) != "body" {
		t.Fatalf("Range: %d %q", code, part)
	}
	checkRawHeaders(t, "range", h)

	// HEAD probes on raw files return Content-Length without a body so file cards can display sizes.
	headReq, err := http.NewRequest("HEAD", e.srv.URL+"/api/v1"+base+"/raw/site/style.css", nil)
	if err != nil {
		t.Fatal(err)
	}
	headResp, err := (&http.Client{}).Do(headReq)
	if err != nil {
		t.Fatal(err)
	}
	headBody, _ := io.ReadAll(headResp.Body)
	headResp.Body.Close()
	if headResp.StatusCode != 200 || len(headBody) != 0 || headResp.Header.Get("Content-Length") != "15" {
		t.Fatalf("HEAD raw: %d len=%d content-length=%q", headResp.StatusCode, len(headBody), headResp.Header.Get("Content-Length"))
	}
	checkRawHeaders(t, "head", headResp.Header)

	// Path rules, each answer sandboxed too.
	for _, c := range []struct {
		path string
		code int
		hdr  []string
	}{
		{"missing.html", 404, nil},
		{"site", 422, nil},
		{"escape.txt", 422, nil},
		{"big.html", 413, nil},
		{"site%2F..%2F..%2Fsecret.txt", 422, invalid},
		{"%2Fetc%2Fpasswd", 422, invalid},
	} {
		code, out, h := e.do("GET", base+"/raw/"+c.path, nil, c.hdr...)
		if code != c.code {
			t.Errorf("%s: want %d, got %d: %s", c.path, c.code, code, out)
		}
		checkRawHeaders(t, c.path, h)
		if strings.Contains(string(out), "secret") {
			t.Errorf("%s: a file outside the folder was served", c.path)
		}
	}

	// As a browser asks: one URL segment per folder level, and the relative URLs of index.html resolve beside it.
	plain := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(p string) (int, string, http.Header) {
		t.Helper()
		resp, err := plain.Get(e.srv.URL + "/api/v1" + base + "/raw/" + p)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header
	}
	for p, want := range map[string]string{"site/style.css": "body{color:red}", "site/img/dot.png": "\x89PNG\r\n\x1a\n", "notes.md": "# Notes 中文"} { // check-english: zh (UTF-8 text must be served as UTF-8)
		if code, body, h := get(p); code != 200 || body != want {
			t.Errorf("sibling %s: %d %q", p, code, body)
		} else {
			checkRawHeaders(t, p, h)
		}
	}
	// A ".." in the URL never reaches outside the raw view: the router cleans the path first, and an encoded one is
	// refused by the folder's path rules.
	if code, body, _ := get("site/%2E%2E/%2E%2E/%2E%2E/secret.txt"); code == 200 || strings.Contains(body, "secret") {
		t.Errorf("encoded ..: %d %q", code, body)
	}

	// The folder of a task node has the same endpoint; an unknown node is 404, still sandboxed.
	code, _, h = e.do("GET", "/tasks/0192f1a2-0000-7000-8000-000000000001/nodes/0192f1a2-0000-7000-8000-000000000002/folder/raw/index.html", nil)
	if code != 404 {
		t.Errorf("unknown task node: %d", code)
	}
	checkRawHeaders(t, "task node", h)
}

// TestAPIRefusesOtherOrigins: a page of an Agent folder runs sandboxed as an opaque origin, so its requests to the API
// carry "Origin: null". Every route refuses them, reads included, as it refuses any origin other than the console's.
func TestAPIRefusesOtherOrigins(t *testing.T) {
	e := newEnv(t)
	agent := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "Origins", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	base := "/agents/" + agent.ID.String()
	e.expect(200, "PUT", base+"/folder/content?path=page.html", []byte("<p>hi</p>"))
	plain := &http.Client{}
	send := func(method, path string, hdr ...string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, e.srv.URL+"/api/v1"+path, nil)
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		resp, err := plain.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	for _, origin := range []string{"null", "http://evil.example", "http://127.0.0.1:1"} {
		for _, c := range []struct{ method, path string }{
			{"GET", "/context"},
			{"GET", base},
			{"GET", base + "/folder/entries"},
			{"GET", base + "/folder/content?path=page.html"},
			{"GET", base + "/folder/raw/page.html"},
			{"POST", base + "/messages"},
			{"PUT", base + "/folder/content?path=x.txt"},
			{"OPTIONS", base + "/folder/content?path=x.txt"},
		} {
			code, out := send(c.method, c.path, "Origin", origin, "Access-Control-Request-Method", "PUT")
			if code != http.StatusForbidden || !strings.Contains(out, "forbidden_origin") {
				t.Errorf("Origin %s, %s %s: want 403 forbidden_origin, got %d %s", origin, c.method, c.path, code, out)
			}
		}
	}
	// The console itself (same origin, or the dev server on the allowlist) still works.
	for _, origin := range []string{"http://localhost:5173", e.srv.URL, ""} {
		if code, out := send("GET", base+"/folder/raw/page.html", "Origin", origin); code != 200 || out != "<p>hi</p>" {
			t.Errorf("Origin %q: %d %s", origin, code, out)
		}
	}
}
