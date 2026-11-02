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

// 单页应用的规则：哈希产物长缓存、缺失时 404；其余文件与 index.html 不缓存；未知路径回退到 index.html。
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
			t.Errorf("%s：%d %q cache=%q type=%q", c.target, rec.Code, rec.Body.String(), rec.Header().Get("Cache-Control"), rec.Header().Get("Content-Type"))
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s：缺少安全响应头", c.target)
		}
	}
	if rec := serve(t, h, "HEAD", "/"); rec.Code != 200 || rec.Body.Len() != 0 {
		t.Errorf("HEAD：%d %q", rec.Code, rec.Body.String())
	}
	if rec := serve(t, h, "POST", "/"); rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("POST：%d %q", rec.Code, rec.Header().Get("Allow"))
	}
}

// 没有控制台文件时说明怎样提供；目录里缺 index.html 时不回退。
func TestHandlerWithoutConsole(t *testing.T) {
	if rec := serve(t, webui.Handler(nil), "GET", "/"); rec.Code != 404 || !strings.Contains(rec.Body.String(), "LOOP_NOW_WEB_DIR") {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
	if rec := serve(t, webui.Handler(fstest.MapFS{"other.txt": {Data: []byte("x")}}), "GET", "/route"); rec.Code != 404 {
		t.Fatalf("%d", rec.Code)
	}
}

// 不带 embedweb 构建时没有内嵌的控制台。
func TestEmbeddedAbsentWithoutTag(t *testing.T) {
	if webui.Embedded() != nil {
		t.Fatal("未带 embedweb 标签却有内嵌控制台")
	}
}
