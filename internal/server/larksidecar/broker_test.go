package larksidecar

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func testBinding() Binding {
	return Binding{ProjectID: uuid.New(), AccountID: uuid.New(), VersionID: uuid.New(), AgentID: uuid.New(), RuntimeID: uuid.NewString(), AppID: "cli_synthetic", UserID: "ou_synthetic", Brand: "feishu"}
}
func tlsFixture(t *testing.T, h http.Handler) (*httptest.Server, *http.Transport) {
	t.Helper()
	s := httptest.NewTLSServer(h)
	t.Cleanup(s.Close)
	tr := s.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = nil
	u, _ := url.Parse(s.URL)
	tr.TLSClientConfig = tr.TLSClientConfig.Clone()
	tr.TLSClientConfig.ServerName = "127.0.0.1"
	tr.TLSClientConfig.MinVersion = tls.VersionTLS12
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "open.feishu.cn:443" {
			return nil, errors.New("nonfixture target")
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", u.Host)
	}
	return s, tr
}
func signed(b *Broker, method, path string, body []byte) *http.Request {
	r, _ := http.NewRequest(method, b.URL()+path, bytes.NewReader(body))
	r.Header.Set(headerVersion, "v1")
	r.Header.Set(headerTarget, "https://open.feishu.cn")
	r.Header.Set(headerIdentity, "user")
	r.Header.Set(headerAuth, "Authorization")
	r.Header.Set(headerTimestamp, strconv.FormatInt(time.Now().Unix(), 10))
	r.Header.Set(headerBodySHA, bodyHash(body))
	r.Header.Set(headerSignature, signature(b.key, r, b.host, bodyHash(body)))
	return r
}
func status(t *testing.T, r *http.Request) int {
	t.Helper()
	c := &http.Client{Timeout: 3 * time.Second}
	res, e := c.Do(r)
	if e != nil {
		t.Fatal("local request failed")
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	return res.StatusCode
}
func fixtureBroker(t *testing.T, h http.Handler, limits Limits) (*Broker, *atomic.Int32) {
	t.Helper()
	_, tr := tlsFixture(t, h)
	var calls atomic.Int32
	b, e := start(context.Background(), testBinding(), TokenSourceFunc(func(context.Context) (string, error) { calls.Add(1); return "synthetic-broker-token", nil }), limits, tr)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b, &calls
}
func TestV1SignatureScopeReplay(t *testing.T) {
	var forwarded atomic.Int32
	b, calls := fixtureBroker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded.Add(1)
		if r.Host != "open.feishu.cn" || r.Header.Get("Authorization") != "Bearer synthetic-broker-token" || r.Header.Get(headerSignature) != "" {
			t.Error("upstream identity/header boundary")
		}
		_, _ = io.WriteString(w, `{"code":0}`)
	}), Limits{})
	good := func() *http.Request {
		return signed(b, "POST", "/open-apis/im/v1/messages?receive_id_type=chat_id", []byte(`{"content":"synthetic"}`))
	}
	cases := map[string]func(*http.Request){
		"method": func(r *http.Request) { r.Method = "GET" }, "host": func(r *http.Request) { r.Header.Set(headerTarget, "https://open.larksuite.com") }, "path": func(r *http.Request) { r.URL.Path += "/om_other" }, "query": func(r *http.Request) { r.URL.RawQuery = "changed=1" }, "body": func(r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(`{"content":"changedxx"}`))
			r.ContentLength = -1
		}, "identity": func(r *http.Request) { r.Header.Set(headerIdentity, "bot") }, "authHeader": func(r *http.Request) { r.Header.Set(headerAuth, "X-Lark-MCP-UAT") }, "version": func(r *http.Request) { r.Header.Set(headerVersion, "v2") }, "time": func(r *http.Request) { r.Header.Set(headerTimestamp, "-9223372036854775808") }, "duplicateHeader": func(r *http.Request) { r.Header.Add(headerVersion, "v1") }, "rawAuth": func(r *http.Request) { r.Header.Set("Authorization", "Bearer synthetic-wrong") }, "targetQuery": func(r *http.Request) { r.Header.Set(headerTarget, "https://open.feishu.cn?x=1") }, "targetUserinfo": func(r *http.Request) { r.Header.Set(headerTarget, "https://synthetic@open.feishu.cn") }, "management": func(r *http.Request) { r.URL.Path = "/open-apis/authen/v2/oauth/token" }, "wrongScope": func(r *http.Request) { r.Header.Set(headerSignature, strings.Repeat("a", 64)) }, "hexDecodedKey": func(r *http.Request) {
			k, _ := hex.DecodeString(b.key)
			r.Header.Set(headerSignature, signature(string(k), r, b.host, bodyHash([]byte(`{"content":"synthetic"}`))))
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := good()
			mutate(r)
			if got := status(t, r); got != 403 {
				t.Fatalf("status %d", got)
			}
		})
	}
	if calls.Load() != 0 || forwarded.Load() != 0 {
		t.Fatal("rejected input reached token source/upstream")
	}
	if status(t, good()) != 200 || status(t, good()) != 409 || forwarded.Load() != 1 || calls.Load() != 1 {
		t.Fatal("write replay was forwarded")
	}
	// GETs within the same second are legitimate and not treated as write replay.
	for range 2 {
		if status(t, signed(b, "GET", "/open-apis/im/v1/messages?page_token=A%2BB", nil)) != 200 {
			t.Fatal("read query rejected")
		}
	}
}
func TestLimitsRedirectAndUnknownWrite(t *testing.T) {
	for _, code := range []int{301, 302, 303, 307, 308} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			var calls atomic.Int32
			b, _ := fixtureBroker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "http://open.feishu.cn/open-apis/im/v1/messages")
				w.Header().Set(headerSignature, "synthetic-secret-header")
				w.WriteHeader(code)
			}), Limits{})
			res, e := http.DefaultClient.Do(signed(b, "POST", "/open-apis/im/v1/messages", []byte(`{}`)))
			if e != nil {
				t.Fatal(e)
			}
			defer res.Body.Close()
			if res.StatusCode != 502 || res.Header.Get("Location") != "" || res.Header.Get(headerSignature) != "" || calls.Load() != 1 {
				t.Fatal("redirect escaped")
			}
		})
	}
	t.Run("largeRequest", func(t *testing.T) {
		b, calls := fixtureBroker(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("large request forwarded") }), Limits{RequestBytes: 32})
		for _, chunked := range []bool{false, true} {
			r := signed(b, "POST", "/open-apis/im/v1/messages", bytes.Repeat([]byte("x"), 33))
			if chunked {
				r.ContentLength = -1
			}
			if status(t, r) != 413 {
				t.Fatal("body limit missing")
			}
		}
		if calls.Load() != 0 {
			t.Fatal("large input token lookup")
		}
	})
	t.Run("unknownPostNoRetry", func(t *testing.T) {
		var n atomic.Int32
		b, _ := fixtureBroker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n.Add(1)
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
		}), Limits{})
		if status(t, signed(b, "POST", "/open-apis/im/v1/messages", []byte(`{}`))) != 502 || n.Load() != 1 {
			t.Fatal("unknown POST retried")
		}
	})
	t.Run("truncatedRequest", func(t *testing.T) {
		b, calls := fixtureBroker(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("truncated request forwarded") }), Limits{})
		u, _ := url.Parse(b.URL())
		c, e := net.Dial("tcp", u.Host)
		if e != nil {
			t.Fatal(e)
		}
		defer c.Close()
		r := signed(b, "POST", "/open-apis/im/v1/messages", []byte(`{}`))
		r.ContentLength = 100
		r.Header.Set("Connection", "close")
		var raw bytes.Buffer
		_ = r.Write(&raw)
		_, _ = c.Write(raw.Bytes())
		_ = c.(*net.TCPConn).CloseWrite()
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		_, _ = io.ReadAll(c)
		if calls.Load() != 0 {
			t.Fatal("read error forwarded")
		}
	})
}
func TestResponseBoundsAndCancellation(t *testing.T) {
	t.Run("oversizeStream", func(t *testing.T) {
		b, _ := fixtureBroker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			_, _ = w.Write(bytes.Repeat([]byte("x"), 100))
			w.(http.Flusher).Flush()
		}), Limits{ResponseBytes: 32})
		res, e := http.DefaultClient.Do(signed(b, "GET", "/open-apis/im/v1/messages", nil))
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		data, e := io.ReadAll(res.Body)
		if e == nil || len(data) > 32 {
			t.Fatal("oversize stream appeared complete")
		}
	})
	t.Run("cancelStream", func(t *testing.T) {
		released := make(chan struct{})
		b, _ := fixtureBroker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			close(released)
		}), Limits{IdleTimeout: 100 * time.Millisecond})
		ctx, cancel := context.WithCancel(context.Background())
		r := signed(b, "GET", "/open-apis/im/v1/messages", nil).WithContext(ctx)
		res, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		cancel()
		res.Body.Close()
		select {
		case <-released:
		case <-time.After(time.Second):
			t.Fatal("upstream not released")
		}
	})
}

func TestScopeLifecycleAndCapacity(t *testing.T) {
	t.Run("crossScope", func(t *testing.T) {
		b1, _ := fixtureBroker(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("wrong scope forwarded") }), Limits{})
		b2, calls := fixtureBroker(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("wrong scope forwarded") }), Limits{})
		r := signed(b1, "GET", "/open-apis/im/v1/messages", nil)
		u, _ := url.Parse(b2.URL())
		r.URL.Host = u.Host
		if status(t, r) != 403 || calls.Load() != 0 {
			t.Fatal("capability selected another scope")
		}
	})
	t.Run("replayCapacity", func(t *testing.T) {
		b, _ := fixtureBroker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"code":0}`) }), Limits{ReplayEntries: 1})
		if status(t, signed(b, "POST", "/open-apis/im/v1/messages", []byte(`{"uuid":"one"}`))) != 200 || status(t, signed(b, "POST", "/open-apis/im/v1/messages", []byte(`{"uuid":"two"}`))) != 503 {
			t.Fatal("replay cache evicted live protection")
		}
	})
	t.Run("concurrencyCancel", func(t *testing.T) {
		entered := make(chan struct{})
		released := make(chan struct{})
		b, _ := fixtureBroker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(released) }), Limits{Concurrent: 1})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan struct{})
		go func() {
			defer close(done)
			r := signed(b, "GET", "/open-apis/im/v1/messages", nil).WithContext(ctx)
			res, _ := http.DefaultClient.Do(r)
			if res != nil {
				res.Body.Close()
			}
		}()
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("fixture did not start")
		}
		if status(t, signed(b, "GET", "/open-apis/im/v1/messages", nil)) != 503 {
			t.Fatal("concurrency not bounded")
		}
		_ = b.Close()
		select {
		case <-released:
		case <-time.After(time.Second):
			t.Fatal("broker exit leaked upstream")
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("broker exit leaked consumer")
		}
	})
	t.Run("idleStream", func(t *testing.T) {
		released := make(chan struct{})
		b, _ := fixtureBroker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			close(released)
		}), Limits{IdleTimeout: 50 * time.Millisecond})
		res, e := http.DefaultClient.Do(signed(b, "GET", "/open-apis/im/v1/messages", nil))
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		_, e = io.ReadAll(res.Body)
		if e == nil {
			t.Fatal("idle stream appeared complete")
		}
		select {
		case <-released:
		case <-time.After(time.Second):
			t.Fatal("idle stream leaked")
		}
	})
}
