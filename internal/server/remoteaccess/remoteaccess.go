// Package remoteaccess runs the remote door: a second listener on this computer's loopback address for phones and
// other devices, which a tunnel forwards to. It is off by default; the setting is saved and applied at once. What the
// door lets in (device sessions) is decided by its handler, not here.
package remoteaccess

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"loop-now/internal/server/store"
)

// The states of the remote door.
const (
	StateOff       = "off"
	StateListening = "listening"
	StateFailed    = "failed"
)

// Host is the address the remote door listens on.
const Host = "127.0.0.1"

// State is the setting and what the listener does now; it mirrors the RemoteAccess schema.
type State struct {
	Enabled bool    `json:"enabled"`
	Port    *int    `json:"port"`
	State   string  `json:"state"`
	URL     *string `json:"url"`
	Error   *string `json:"error"`
}

// Door owns the remote listener.
type Door struct {
	Store *store.Store
	// Handler serves every request on the remote door.
	Handler http.Handler
	// BaseContext is the context of every request (cancelled when the server shuts down); nil means
	// context.Background.
	BaseContext context.Context
	Log         *slog.Logger

	mu       sync.Mutex
	settings store.RemoteAccessSettings
	srv      *http.Server
	ln       net.Listener
	err      string
	// closed is set by Close: a setting saved by a request still running during shutdown opens nothing.
	closed bool
}

// Start reads the saved setting and opens the door when it is on. A door that cannot listen is reported in State,
// not as an error.
func (d *Door) Start(ctx context.Context) error {
	settings, err := d.Store.GetRemoteAccessSettings(ctx)
	if err != nil {
		return fmt.Errorf("read the remote door setting: %w", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.settings = settings
	return d.applyLocked(ctx)
}

// Apply saves the setting and applies it: on listens (at port, or at a port the server picks and keeps), off closes
// the listener and every connection on it. Applying the setting the door already runs with keeps its connections.
func (d *Door) Apply(ctx context.Context, enabled bool, port *int) (State, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.Store.PutRemoteAccessSettings(ctx, enabled, port); err != nil {
		return State{}, err
	}
	d.settings.Enabled, d.settings.Port = enabled, port
	if err := d.applyLocked(ctx); err != nil {
		return State{}, err
	}
	return d.stateLocked(), nil
}

// State reports the setting and what the listener does now.
func (d *Door) State() State {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stateLocked()
}

// Close closes the listener and every connection on it for good, without changing the setting (for server shutdown).
func (d *Door) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	return d.closeLocked()
}

func (d *Door) stateLocked() State {
	s := State{Enabled: d.settings.Enabled, Port: d.settings.Port, State: StateOff}
	switch {
	case d.ln != nil:
		s.State = StateListening
		url := "http://" + d.ln.Addr().String()
		s.URL = &url
	case d.settings.Enabled:
		s.State = StateFailed
		msg := d.err
		s.Error = &msg
	}
	return s
}

func (d *Door) listeningPort() int {
	if d.ln == nil {
		return 0
	}
	return d.ln.Addr().(*net.TCPAddr).Port
}

func (d *Door) closeLocked() error {
	if d.srv == nil {
		return nil
	}
	// Close, not Shutdown: open event streams never end by themselves, and a door turned off must let nobody in.
	err := d.srv.Close()
	d.srv, d.ln = nil, nil
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

func (d *Door) applyLocked(ctx context.Context) error {
	s := d.settings
	if d.ln != nil && s.Enabled && (s.Port == nil || *s.Port == d.listeningPort()) {
		return nil
	}
	if err := d.closeLocked(); err != nil {
		d.Log.Warn("closing the remote door failed", "err", err)
	}
	d.err = ""
	if !s.Enabled {
		return nil
	}
	if d.closed {
		d.err = "The server is shutting down."
		return nil
	}
	var ln net.Listener
	var err error
	switch {
	case s.Port != nil:
		ln, err = net.Listen("tcp", net.JoinHostPort(Host, strconv.Itoa(*s.Port)))
	case s.PickedPort != nil:
		if ln, err = net.Listen("tcp", net.JoinHostPort(Host, strconv.Itoa(*s.PickedPort))); err != nil {
			d.Log.Info("the remote door's earlier port is taken; picking another", "port", *s.PickedPort, "err", err)
			ln, err = net.Listen("tcp", net.JoinHostPort(Host, "0"))
		}
	default:
		ln, err = net.Listen("tcp", net.JoinHostPort(Host, "0"))
	}
	if err != nil {
		d.err = err.Error()
		d.Log.Warn("the remote door could not listen", "err", err)
		return nil
	}
	if s.Port == nil {
		port := ln.Addr().(*net.TCPAddr).Port
		if s.PickedPort == nil || *s.PickedPort != port {
			if err := d.Store.SetRemoteAccessPickedPort(ctx, port); err != nil {
				_ = ln.Close()
				return err
			}
			d.settings.PickedPort = &port
		}
	}
	base := d.BaseContext
	if base == nil {
		base = context.Background()
	}
	srv := &http.Server{Handler: d.Handler, ReadHeaderTimeout: 10 * time.Second, BaseContext: func(net.Listener) context.Context { return base }}
	d.srv, d.ln = srv, ln
	d.Log.Info("serving the remote door", "addr", ln.Addr().String())
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			d.Log.Error("the remote door stopped serving", "err", err)
		}
	}()
	return nil
}
