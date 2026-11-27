package httpapi

import (
	"mime"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"

	"loop-now/internal/server/apperr"
)

// RawFileSandbox is the Content-Security-Policy of every response of the raw file endpoints. A file in an Agent folder
// may have been written by an Agent (or by whatever it read), and it is served on the console's own host and port. The
// sandbox without allow-same-origin makes the page an opaque origin: it cannot read the console's storage, and its
// requests to the API carry "Origin: null", which cors refuses on every route. Scripts, forms, popups, dialogs and
// downloads still work, so pages and animations an Agent made run as the person expects.
const RawFileSandbox = "sandbox allow-scripts allow-forms allow-popups allow-modals allow-downloads"

// rawFilePath is the part of the URL path the raw file endpoints share; rawHeaders recognises them by it.
const rawFilePath = "/folder/raw/"

// rawHeaders puts the sandbox headers on every response of the raw file endpoints, including the errors other
// middleware writes before the handler runs, so nothing at such a URL is ever shown without them.
func rawHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, rawFilePath) {
			h := w.Header()
			h.Set("Content-Security-Policy", RawFileSandbox)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "no-referrer")
		}
		next.ServeHTTP(w, r)
	})
}

// rawContentTypes are the types of the files a page is commonly made of; they do not depend on this computer's MIME
// tables. Other extensions fall back to mime.TypeByExtension.
var rawContentTypes = map[string]string{
	".html": "text/html; charset=utf-8", ".htm": "text/html; charset=utf-8",
	".css": "text/css; charset=utf-8",
	".js":  "text/javascript; charset=utf-8", ".mjs": "text/javascript; charset=utf-8",
	".json": "application/json", ".map": "application/json",
	".txt": "text/plain; charset=utf-8", ".md": "text/plain; charset=utf-8", ".markdown": "text/plain; charset=utf-8",
	".csv": "text/csv; charset=utf-8", ".log": "text/plain; charset=utf-8",
	".xml": "text/xml; charset=utf-8",
	".svg": "image/svg+xml", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".webp": "image/webp", ".avif": "image/avif", ".ico": "image/x-icon", ".bmp": "image/bmp",
	".woff": "font/woff", ".woff2": "font/woff2", ".ttf": "font/ttf", ".otf": "font/otf",
	".mp4": "video/mp4", ".webm": "video/webm", ".mp3": "audio/mpeg", ".wav": "audio/wav", ".ogg": "audio/ogg",
	".pdf": "application/pdf", ".wasm": "application/wasm",
}

// rawContentType chooses the type from the extension, text in UTF-8. A file with an unknown extension is shown as plain
// text when its start is UTF-8 text and offered as bytes otherwise; its content never makes it HTML.
func rawContentType(name string, head []byte) string {
	ext := strings.ToLower(path.Ext(name))
	if t, ok := rawContentTypes[ext]; ok {
		return t
	}
	if t := mime.TypeByExtension(ext); t != "" {
		if strings.HasPrefix(t, "text/") && !strings.Contains(t, "charset") {
			t += "; charset=utf-8"
		}
		return t
	}
	if looksLikeText(head) {
		return "text/plain; charset=utf-8"
	}
	return "application/octet-stream"
}

// looksLikeText reports whether b is UTF-8 without NUL bytes; a character cut at the end of b does not count against it.
func looksLikeText(b []byte) bool {
	for len(b) > 0 {
		r, n := utf8.DecodeRune(b)
		if r == 0 || (r == utf8.RuneError && n == 1 && (len(b) >= utf8.UTFMax || utf8.FullRune(b))) {
			return false
		}
		b = b[n:]
	}
	return true
}

// rawFolderFile serves one file of a folder as itself for a browser tab (getAgentFolderRawFile and
// getTaskNodeFolderRawFile). The path comes from the URL path, so a page's relative URLs resolve beside it; the folder's
// own rules keep it inside the folder (no "..", no symlink out), and files above the preview limit are refused.
func (s *Server) rawFolderFile(of folderOf) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, err := of(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		name := r.PathValue("path")
		file, info, err := f.Open(name)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		defer file.Close()
		if max := s.O.Limits.TextEdit.MaxBytes; max > 0 && info.Size() > max {
			s.fail(w, r, apperr.TooLarge("The file is larger than the preview limit (Context.limits.textEdit.maxBytes)."))
			return
		}
		head := make([]byte, 512)
		n, _ := file.ReadAt(head, 0)
		w.Header().Set("Content-Type", rawContentType(name, head[:n]))
		// The file may change while its tab is open; reloading shows the current content.
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeContent(w, r, "", info.ModTime(), file)
	}
}
