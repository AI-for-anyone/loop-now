package cliauth

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/clicredential"
)

type GrantResolver interface {
	Resolve(context.Context, clicredential.Binding) (clicredential.Google, error)
}

// Broker capability is minted for one captured execution scope. Its protocol
// never accepts an Agent/project/account/version supplied by the child.
type Broker struct {
	path, capability, dir string
	version               uuid.UUID
	resolver              func(context.Context) (clicredential.Google, error)
	listener              net.Listener
	ctx                   context.Context
	cancel                context.CancelFunc
	done                  chan struct{}
	workers               sync.WaitGroup
	closeOnce             sync.Once
}

func NewBroker(binding clicredential.Binding, resolver GrantResolver) (*Broker, error) {
	if !binding.Valid() || resolver == nil {
		return nil, ErrUnavailable
	}
	return newBroker(binding.VersionID, func(ctx context.Context) (clicredential.Google, error) { return resolver.Resolve(ctx, binding) })
}

type VerificationGrantResolver interface {
	ResolveVerification(context.Context, clicredential.VerificationGrantBinding) (clicredential.Google, error)
}

// NewVerificationBroker binds a management principal server-side; the private
// wire protocol still accepts no identity or version selected by the child.
func NewVerificationBroker(binding clicredential.VerificationGrantBinding, resolver VerificationGrantResolver) (*Broker, error) {
	if !binding.Valid() || resolver == nil {
		return nil, ErrUnavailable
	}
	return newBroker(binding.VersionID, func(ctx context.Context) (clicredential.Google, error) {
		return resolver.ResolveVerification(ctx, binding)
	})
}
func newBroker(version uuid.UUID, resolver func(context.Context) (clicredential.Google, error)) (*Broker, error) {
	parent := os.TempDir()
	if runtime.GOOS == "darwin" {
		parent = "/private/tmp"
	}
	dir, e := os.MkdirTemp(parent, "ln-g-")
	if e != nil {
		return nil, ErrUnavailable
	}
	cleanup := true
	defer func() {
		if cleanup {
			os.RemoveAll(dir)
		}
	}()
	if e = os.Chmod(dir, 0700); e != nil {
		return nil, ErrUnavailable
	}
	path := filepath.Join(dir, "b")
	listener, e := net.Listen("unix", path)
	if e != nil {
		return nil, ErrUnavailable
	}
	if e = os.Chmod(path, 0600); e != nil {
		listener.Close()
		return nil, ErrUnavailable
	}
	var secret [32]byte
	if _, e = rand.Read(secret[:]); e != nil {
		listener.Close()
		return nil, ErrUnavailable
	}
	ctx, cancel := context.WithCancel(context.Background())
	b := &Broker{path: path, dir: dir, capability: base64.RawURLEncoding.EncodeToString(secret[:]), version: version, resolver: resolver, listener: listener, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	clear(secret[:])
	cleanup = false
	go b.serve()
	return b, nil
}
func (b *Broker) Environment() map[string]string {
	return map[string]string{"LOOP_NOW_GCLOUD_BRIDGE_SOCKET": b.path, "LOOP_NOW_GCLOUD_BRIDGE_CAPABILITY": b.capability, "LOOP_NOW_GCLOUD_GRANT_VERSION": b.version.String()}
}
func (b *Broker) serve() {
	defer close(b.done)
	slots := make(chan struct{}, 32)
	for {
		conn, e := b.listener.Accept()
		if e != nil {
			return
		}
		select {
		case slots <- struct{}{}:
			b.workers.Add(1)
			go func() { defer b.workers.Done(); defer func() { <-slots }(); b.handle(conn) }()
		default:
			conn.Close()
		}
	}
}
func (b *Broker) handle(conn net.Conn) {
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-b.ctx.Done():
			_ = conn.Close()
		case <-finished:
		}
	}()
	defer conn.Close() // Runtime waits for EOF after exactly one JSONL response.
	_ = conn.SetDeadline(time.Now().Add(35 * time.Second))
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	if !scanner.Scan() {
		return
	}
	var request struct {
		Protocol   int       `json:"protocol"`
		Type       string    `json:"type"`
		RequestID  uuid.UUID `json:"requestId"`
		Capability string    `json:"capability"`
	}
	d := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
	d.DisallowUnknownFields()
	if d.Decode(&request) != nil || d.Decode(new(any)) != io.EOF || request.RequestID == uuid.Nil {
		return
	}
	replyError := func(code string) {
		_ = json.NewEncoder(conn).Encode(struct {
			Type      string    `json:"type"`
			RequestID uuid.UUID `json:"requestId"`
			Code      string    `json:"code"`
		}{"error", request.RequestID, code})
	}
	if request.Protocol != 1 || request.Type != "resolve" || subtle.ConstantTimeCompare([]byte(request.Capability), []byte(b.capability)) != 1 {
		replyError("forbidden")
		return
	}
	ctx, cancel := context.WithTimeout(b.ctx, 30*time.Second)
	defer cancel()
	c, e := b.resolver(ctx)
	if e != nil || !c.Valid() || len(c.AccessToken) > clicredential.MaxBytes || c.ExpiresAt <= time.Now().UnixMilli() {
		code := "unavailable"
		if errors.Is(e, context.DeadlineExceeded) {
			code = "timeout"
		}
		var safe *SafeError
		if errors.As(e, &safe) && safe.Code == "requires_login" {
			code = "requires_login"
		}
		replyError(code)
		return
	}
	// No refresh/client secret is ever sent to a native CLI consumer.
	raw, e := json.Marshal(struct {
		Type        string    `json:"type"`
		RequestID   uuid.UUID `json:"requestId"`
		AccessToken string    `json:"accessToken"`
		ExpiresAt   int64     `json:"expiresAt"`
	}{"credential", request.RequestID, c.AccessToken, c.ExpiresAt})
	if e != nil || len(raw) > 1<<20 {
		replyError("unavailable")
		return
	}
	defer clear(raw)
	raw = append(raw, '\n')
	_, _ = conn.Write(raw)
}
func (b *Broker) Close(ctx context.Context) error {
	b.closeOnce.Do(func() { b.cancel(); _ = b.listener.Close() })
	select {
	case <-b.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	done := make(chan struct{})
	go func() { b.workers.Wait(); close(done) }()
	select {
	case <-done:
		_ = os.RemoveAll(b.dir)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
