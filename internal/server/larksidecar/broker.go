package larksidecar

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Binding is trusted capture metadata; it is never populated from HTTP headers
// or CLI arguments. Refresh generation may advance within VersionID only.
type Binding struct {
	ProjectID, AccountID, VersionID, AgentID uuid.UUID
	RuntimeID, AppID, UserID, Brand          string
}

// TokenSource must be bound to Binding by the caller. It must return only a
// committed token for that exact version. The PG coordinator owns independent,
// bounded refresh/persistence; cancellation here cancels waiting, not rotation.
type TokenSource interface {
	AccessToken(context.Context) (string, error)
}
type TokenSourceFunc func(context.Context) (string, error)

func (f TokenSourceFunc) AccessToken(ctx context.Context) (string, error) { return f(ctx) }

type Limits struct {
	RequestBytes, ResponseBytes int64
	RequestTimeout, IdleTimeout time.Duration
	Concurrent, ReplayEntries   int
}

func defaults(l Limits) Limits {
	if l.RequestBytes <= 0 {
		l.RequestBytes = 1 << 20
	}
	if l.ResponseBytes <= 0 {
		l.ResponseBytes = 16 << 20
	}
	if l.RequestTimeout <= 0 {
		l.RequestTimeout = 30 * time.Second
	}
	if l.IdleTimeout <= 0 {
		l.IdleTimeout = 5 * time.Second
	}
	if l.Concurrent <= 0 {
		l.Concurrent = 8
	}
	if l.ReplayEntries <= 0 {
		l.ReplayEntries = 4096
	}
	return l
}

type Broker struct {
	binding            Binding
	key, host, address string
	address6           string
	execHandler        http.Handler
	source             TokenSource
	limits             Limits
	client             *http.Client
	server             *http.Server
	cancel             context.CancelFunc
	slots              chan struct{}
	mu                 sync.Mutex
	replay             map[string]int64
	closed             bool
	frozen             bool
	active             int
	idle               chan struct{}
	connections        int
	connectionsIdle    chan struct{}
	serveDone          chan struct{}
	closeOnce          sync.Once
	closeErr           error
}

// Start creates one private numeric-loopback listener/capability per consumer.
// No public router or default mux is modified.
func Start(ctx context.Context, binding Binding, source TokenSource, limits Limits) (*Broker, error) {
	return start(ctx, binding, source, limits, nil)
}

// testTransport is package-private: production cannot configure a fixture URL,
// insecure TLS or environment proxy. Tests supply a loopback-only TLS dialer.
func start(ctx context.Context, binding Binding, source TokenSource, limits Limits, testTransport http.RoundTripper) (*Broker, error) {
	if ctx == nil || ctx.Err() != nil || source == nil || binding.ProjectID == uuid.Nil || binding.AccountID == uuid.Nil || binding.VersionID == uuid.Nil || binding.AgentID == uuid.Nil || binding.RuntimeID == "" || binding.AppID == "" || binding.UserID == "" {
		return nil, errors.New("invalid lark scope")
	}
	host := "open.feishu.cn"
	if binding.Brand == "lark" {
		host = "open.larksuite.com"
	} else if binding.Brand != "feishu" {
		return nil, errors.New("invalid lark scope")
	}
	raw := make([]byte, 32)
	if _, e := rand.Read(raw); e != nil {
		return nil, errors.New("capability unavailable")
	}
	l := defaults(limits)
	if l.RequestBytes == math.MaxInt64 || l.ResponseBytes == math.MaxInt64 {
		return nil, errors.New("invalid sidecar limits")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableCompression = true
	transport.ResponseHeaderTimeout = l.IdleTimeout
	transport.MaxResponseHeaderBytes = 32 << 10
	var rt http.RoundTripper = transport
	if testTransport != nil {
		rt = testTransport
	}
	child, cancel := context.WithCancel(ctx)
	b := &Broker{binding: binding, key: hex.EncodeToString(raw), host: host, source: source, limits: l, cancel: cancel, slots: make(chan struct{}, l.Concurrent), replay: make(map[string]int64), client: &http.Client{Transport: rt, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	b.idle = make(chan struct{})
	close(b.idle)
	b.connectionsIdle = make(chan struct{})
	close(b.connectionsIdle)
	b.serveDone = make(chan struct{})
	ln, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		cancel()
		return nil, errors.New("sidecar unavailable")
	}
	b.address = "http://" + ln.Addr().String()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	ln6, e := net.Listen("tcp6", net.JoinHostPort("::1", port))
	if e != nil {
		_ = ln.Close()
		cancel()
		return nil, errors.New("dual loopback unavailable")
	}
	b.address6 = ln6.Addr().String()
	b.server = &http.Server{Handler: b, ReadHeaderTimeout: l.IdleTimeout, ReadTimeout: l.RequestTimeout, WriteTimeout: l.RequestTimeout, IdleTimeout: l.IdleTimeout, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return child }, ConnState: b.connectionState}
	go func() {
		var serving sync.WaitGroup
		serving.Add(2)
		go func() { defer serving.Done(); _ = b.server.Serve(ln); _ = b.Close() }()
		go func() { defer serving.Done(); _ = b.server.Serve(ln6); _ = b.Close() }()
		serving.Wait()
		close(b.serveDone)
	}()
	go func() {
		select {
		case <-child.Done():
			_ = b.Close()
		case <-b.serveDone:
		}
	}()
	return b, nil
}
func (b *Broker) URL() string { return b.address }

// Capability is a private launcher input, never argv/ordinary metadata/logging.
func (b *Broker) Capability() string { return b.key }
func (b *Broker) Close() error {
	b.closeOnce.Do(func() {
		b.mu.Lock()
		b.closed = true
		b.frozen = true
		b.mu.Unlock()
		b.cancel()
		b.closeErr = b.server.Close()
		b.client.CloseIdleConnections()
	})
	return b.closeErr
}
func fail(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, "{\"error\":\"lark_sidecar_rejected\"}\n")
}
func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	if b.closed || b.frozen {
		b.mu.Unlock()
		fail(w, 503)
		return
	}
	select {
	case b.slots <- struct{}{}:
		if b.active == 0 {
			b.idle = make(chan struct{})
		}
		b.active++
		b.mu.Unlock()
		defer b.handlerDone()
	default:
		b.mu.Unlock()
		fail(w, 503)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), b.limits.RequestTimeout)
	defer cancel()
	// The independent execution capability never shares the official HMAC
	// provider interface. Its handler still participates in FreezeIdle/Wait.
	if r.URL.Path == execPath {
		b.mu.Lock()
		handler := b.execHandler
		b.mu.Unlock()
		if handler == nil {
			fail(w, 503)
			return
		}
		handler.ServeHTTP(w, r)
		return
	}
	// Standalone handlers also respect deadline while reading a slow request body.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(b.limits.IdleTimeout))
	for _, name := range []string{headerVersion, headerTarget, headerIdentity, headerSignature, headerTimestamp, headerBodySHA, headerAuth} {
		if !single(r.Header, name) {
			fail(w, 403)
			return
		}
	}
	if r.Header.Get(headerVersion) != "v1" || r.Header.Get(headerIdentity) != "user" || r.Header.Get(headerAuth) != "Authorization" || !validTime(r.Header.Get(headerTimestamp), time.Now()) || !allowedRequest(r) || r.URL.IsAbs() || r.URL.Fragment != "" {
		fail(w, 403)
		return
	}
	target, e := url.Parse(r.Header.Get(headerTarget))
	if e != nil || target.Scheme != "https" || target.Host != b.host || target.User != nil || target.Path != "" && target.Path != "/" || target.RawQuery != "" || target.ForceQuery || target.Fragment != "" {
		fail(w, 403)
		return
	}
	if r.Header.Get("Authorization") != "" || r.Header.Get("X-Lark-MCP-UAT") != "" || r.Header.Get("X-Lark-MCP-TAT") != "" {
		fail(w, 403)
		return
	}
	if r.ContentLength > b.limits.RequestBytes {
		fail(w, 413)
		return
	}
	body, e := io.ReadAll(io.LimitReader(r.Body, b.limits.RequestBytes+1))
	if e != nil {
		fail(w, 400)
		return
	}
	if int64(len(body)) > b.limits.RequestBytes {
		fail(w, 413)
		return
	}
	digest := bodyHash(body)
	sig := r.Header.Get(headerSignature)
	if digest != r.Header.Get(headerBodySHA) || len(sig) != 64 || !hmac.Equal([]byte(signature(b.key, r, b.host, digest)), []byte(sig)) {
		fail(w, 403)
		return
	}
	if r.Method != "GET" { // Reserve BEFORE token lookup/forward; unknown writes are never retried.
		now := time.Now().Unix()
		b.mu.Lock()
		for k, expiry := range b.replay {
			if expiry < now {
				delete(b.replay, k)
			}
		}
		_, seen := b.replay[sig]
		full := len(b.replay) >= b.limits.ReplayEntries
		if !seen && !full {
			ts, _ := strconv.ParseInt(r.Header.Get(headerTimestamp), 10, 64)
			b.replay[sig] = ts + 60
		}
		b.mu.Unlock()
		if seen {
			fail(w, 409)
			return
		}
		if full {
			fail(w, 503)
			return
		}
	}
	token, e := b.source.AccessToken(ctx)
	if e != nil || token == "" || strings.ContainsAny(token, "\r\n") || ctx.Err() != nil {
		fail(w, 503)
		return
	}
	// Construct a fresh request. No client-controlled routing/auth/proxy headers,
	// GetBody, idempotency retry hint or environment proxy reaches the trusted hop.
	upstream := &url.URL{Scheme: "https", Host: b.host, Path: r.URL.Path, RawQuery: r.URL.RawQuery, ForceQuery: r.URL.ForceQuery}
	req, e := http.NewRequestWithContext(ctx, r.Method, upstream.String(), io.NopCloser(strings.NewReader(string(body))))
	if e != nil {
		fail(w, 400)
		return
	}
	req.ContentLength = int64(len(body))
	req.GetBody = nil
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, e := b.client.Do(req)
	if e != nil {
		fail(w, 502)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		fail(w, 502)
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// An upstream error can reflect its Authorization header. Preserve only
		// the numeric HTTP status, never its body, headers or raw error details.
		status := resp.StatusCode
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		fail(w, status)
		return
	}
	if resp.ContentLength > b.limits.ResponseBytes {
		fail(w, 502)
		return
	}
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && !strings.EqualFold(encoding, "identity") {
		// Finite QA APIs return JSON. Do not pass an encoded stream whose decoded
		// content would bypass exact-token filtering, or reflect upstream headers.
		fail(w, 502)
		return
	}
	mask, ok := tokenMask([]byte(token))
	if !ok {
		fail(w, 502)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if e := http.NewResponseController(w).Flush(); e != nil {
		return
	}
	idle := time.AfterFunc(b.limits.IdleTimeout, cancel)
	defer idle.Stop()
	buf := make([]byte, 32<<10)
	var count, emitted int64
	redactor := exactTokenRedactor{secret: []byte(token), replacement: mask, emit: func(p []byte) error {
		if int64(len(p)) > b.limits.ResponseBytes-emitted {
			return errors.New("response limit")
		}
		emitted += int64(len(p))
		if _, e := w.Write(p); e != nil {
			return e
		}
		return http.NewResponseController(w).Flush()
	}}
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if int64(n) > b.limits.ResponseBytes-count {
				panic(http.ErrAbortHandler)
			}
			count += int64(n)
			idle.Reset(b.limits.IdleTimeout)
			if redactor.write(buf[:n]) != nil {
				panic(http.ErrAbortHandler)
			}
		}
		if err == io.EOF {
			if ctx.Err() != nil || redactor.finish() != nil {
				panic(http.ErrAbortHandler)
			}
			return
		}
		if err != nil || ctx.Err() != nil {
			panic(http.ErrAbortHandler)
		}
	}
}
