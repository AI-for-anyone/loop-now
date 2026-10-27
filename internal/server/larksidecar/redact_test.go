package larksidecar

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestExactTokenRedactionEveryBoundary(t *testing.T) {
	secret := "synthetic-current-access-token-only"
	input := []byte(`{"msg":"Bearer ` + secret + `","repeat":"` + secret + secret + `","normal":"other-token-like-text","partial":"synthetic-current"}`)
	expected := bytes.ReplaceAll(input, []byte(secret), []byte("****"))
	for width := 1; width <= len(input); width++ {
		var out bytes.Buffer
		mask, ok := tokenMask([]byte(secret))
		if !ok {
			t.Fatal("mask unavailable")
		}
		r := exactTokenRedactor{secret: []byte(secret), replacement: mask, emit: func(p []byte) error { _, e := out.Write(p); return e }}
		for start := 0; start < len(input); start += width {
			end := min(start+width, len(input))
			if r.write(input[start:end]) != nil || len(r.pending) >= len(secret) {
				t.Fatal("redactor write or bounded lookbehind failed")
			}
		}
		if r.finish() != nil || !bytes.Equal(out.Bytes(), expected) {
			t.Fatal("cross-chunk exact redaction changed unrelated text or leaked matched token")
		}
	}
}

func TestExactTokenMaskCannotRecreateSecret(t *testing.T) {
	for _, secret := range []string{"ab[redacted]cd", "ab****cd", "ab####cd"} {
		var out bytes.Buffer
		mask, ok := tokenMask([]byte(secret))
		if !ok || bytes.ContainsAny(mask, "\"\\\r\n") {
			t.Fatal("unsafe JSON mask")
		}
		r := exactTokenRedactor{secret: []byte(secret), replacement: mask, emit: func(p []byte) error { _, e := out.Write(p); return e }}
		// A fixed [redacted] marker could recreate the first secret here.
		if r.write([]byte("ab"+secret+"cd")) != nil || r.finish() != nil || bytes.Contains(out.Bytes(), []byte(secret)) {
			t.Fatal("replacement boundary recreated the secret")
		}
	}
}

func TestBrokerReflectedAccessTokenNeverReturned(t *testing.T) {
	for _, code := range []int{401, 403, 500, 200} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			b, calls := fixtureBroker(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				token := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
				// Deliberately malicious synthetic provider: reflect both header
				// and error-shaped JSON, even under HTTP 200. Never a real token.
				w.Header().Set("Content-Type", "application/json; reflected="+token)
				w.Header().Set("X-Provider-Error", token)
				w.WriteHeader(code)
				parts := []string{`{"code":9999,"msg":"Bearer `, token[:7], token[7:15], token[15:], `","next_page":"synthetic-page"}`}
				for _, part := range parts {
					_, _ = io.WriteString(w, part)
					w.(http.Flusher).Flush()
				}
			}), Limits{})
			client := &http.Client{}
			defer client.CloseIdleConnections()
			res, e := client.Do(signed(b, "GET", "/open-apis/im/v1/messages", nil))
			if e != nil {
				t.Fatal("isolated broker request")
			}
			defer res.Body.Close()
			raw, e := io.ReadAll(res.Body)
			if e != nil || res.StatusCode != code || calls.Load() != 1 || bytes.Contains(raw, []byte("synthetic-broker-token")) || strings.Contains(fmt.Sprint(res.Header), "synthetic-broker-token") {
				t.Fatal("reflected synthetic credential escaped or response failed")
			}
			if code != 200 {
				if string(raw) != "{\"error\":\"lark_sidecar_rejected\"}\n" || bytes.Contains(raw, []byte("next_page")) {
					t.Fatal("raw provider error body was returned")
				}
			} else if string(raw) != `{"code":9999,"msg":"Bearer ****","next_page":"synthetic-page"}` {
				t.Fatal("HTTP 200 reflected token not exactly redacted")
			}
		})
	}
}

func TestBrokerNormalPaginationPreservedAndEncodedResponseRejected(t *testing.T) {
	for _, encoded := range []bool{false, true} {
		t.Run(fmt.Sprint(encoded), func(t *testing.T) {
			const normal = `{"code":0,"data":{"items":[{"content":"other-token-like-text"}],"has_more":true,"page_token":"synthetic-next-page"}}`
			b, _ := fixtureBroker(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if encoded {
					w.Header().Set("Content-Encoding", "gzip")
					z := gzip.NewWriter(w)
					_, _ = io.WriteString(z, strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer "))
					_ = z.Close()
					return
				}
				_, _ = io.WriteString(w, normal)
			}), Limits{})
			client := &http.Client{}
			defer client.CloseIdleConnections()
			res, e := client.Do(signed(b, "GET", "/open-apis/im/v1/messages", nil))
			if e != nil {
				t.Fatal("isolated broker request")
			}
			defer res.Body.Close()
			raw, e := io.ReadAll(res.Body)
			if e != nil {
				t.Fatal("response read")
			}
			if encoded {
				if res.StatusCode != 502 || string(raw) != "{\"error\":\"lark_sidecar_rejected\"}\n" {
					t.Fatal("encoded response bypassed exact filter")
				}
			} else if res.StatusCode != 200 || string(raw) != normal {
				t.Fatal("normal QA pagination changed")
			}
		})
	}
}
