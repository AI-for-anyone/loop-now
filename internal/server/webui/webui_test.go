package webui_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"loop-now/internal/server/webui"
)

func serve(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

// Single-page app rules: hashed assets are cached long and get a 404 when missing; other files and index.html are not
// cached; unknown paths fall back to index.html.
func TestHandlerServesSinglePageApp(t *testing.T) {
	h := webui.Handler(fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html><title>console</title>")},
		"favicon.svg":          {Data: []byte("<svg/>")},
		"assets/app-1a2b3c.js": {Data: []byte("console.log(1)")},
	})
	cases := []struct {
		target, body, cache, typ string
		status                   int
	}{
		{"/", "console", "no-cache", "text/html", 200},
		{"/p/%E9%A1%B9%E7%9B%AE/tasks/x", "console", "no-cache", "text/html", 200},
		{"/a.b/c", "console", "no-cache", "text/html", 200},
		{"/favicon.svg", "<svg/>", "no-cache", "image/svg+xml", 200},
		{"/assets/app-1a2b3c.js", "console.log(1)", "public, max-age=31536000, immutable", "javascript", 200},
		{"/assets/missing.js", "", "", "", 404},
		{"/assets", "console", "no-cache", "text/html", 200},
		{"/../index.html", "console", "no-cache", "text/html", 200},
	}
	for _, c := range cases {
		rec := serve(t, h, "GET", c.target)
		if rec.Code != c.status || !strings.Contains(rec.Body.String(), c.body) || rec.Header().Get("Cache-Control") != c.cache ||
			!strings.Contains(rec.Header().Get("Content-Type"), c.typ) {
			t.Errorf("%s: %d %q cache=%q type=%q", c.target, rec.Code, rec.Body.String(), rec.Header().Get("Cache-Control"), rec.Header().Get("Content-Type"))
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s: missing security response headers", c.target)
		}
	}
	if rec := serve(t, h, "HEAD", "/"); rec.Code != 200 || rec.Body.Len() != 0 {
		t.Errorf("HEAD: %d %q", rec.Code, rec.Body.String())
	}
	if rec := serve(t, h, "POST", "/"); rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("POST: %d %q", rec.Code, rec.Header().Get("Allow"))
	}
}

// Without console files the response explains how to provide them; a directory without index.html gets no fallback.
func TestHandlerWithoutConsole(t *testing.T) {
	if rec := serve(t, webui.Handler(nil), "GET", "/"); rec.Code != 404 || !strings.Contains(rec.Body.String(), "LOOP_NOW_WEB_DIR") {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
	if rec := serve(t, webui.Handler(fstest.MapFS{"other.txt": {Data: []byte("x")}}), "GET", "/route"); rec.Code != 404 {
		t.Fatalf("%d", rec.Code)
	}
}

// Without embedweb there is no embedded console.
func TestEmbeddedAbsentWithoutTag(t *testing.T) {
	if webui.Embedded() != nil {
		t.Fatal("embedded console present without the embedweb tag")
	}
}
