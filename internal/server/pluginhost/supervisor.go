package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/pluginmanifest"
	"loop-now/internal/server/store"
)

// Spec describes how to load one plugin that has a platform side; it comes from the install and grant service. The
// order of the specs is the order of loading and of synchronous calls.
type Spec struct {
	ID string
	// Events is the manifest's loopNow.events: both observe events and synchronous hooks are included.
	Events []string
	// Permissions is the manifest's loopNow.permissions; the plugin host's state_* requests need state.
	Permissions []string
	// FailClosed makes a timeout or error of an intercept hook count as blocking.
	FailClosed bool
	// Load is the payload of the load message, sent to the plugin host as-is.
	Load json.RawMessage
	// Prepare, when not nil, is called before loading and its return value replaces Load (for example, the plugin
	// token is only issued at that point, affecting just this one plugin).
	Prepare func(context.Context) (json.RawMessage, error)
	// Schedules is the manifest's schedule[]: after loading, schedule requests are sent according to cron.
	Schedules []pluginmanifest.Schedule
	// AccountKinds is the manifest's accounts[].kind; the plugin host's accounts_list can only read these kinds.
	AccountKinds []string
}

// Store is the persistence the plugin host supervisor needs, implemented by *store.Store.
type Store interface {
	PluginStateGet(ctx context.Context, plugin string, project *uuid.UUID, key string) (json.RawMessage, error)
	PluginStateSet(ctx context.Context, plugin string, project *uuid.UUID, key string, value json.RawMessage) error
	PluginStateDelete(ctx context.Context, plugin string, project *uuid.UUID, key string) error
	PluginStateList(ctx context.Context, plugin string, project *uuid.UUID, prefix string) ([]store.PluginStateEntry, error)
	PluginEnabledIn(ctx context.Context, plugin string, project uuid.UUID) (bool, error)
	PluginAccountsOfKind(ctx context.Context, project uuid.UUID, plugin, kind string) ([]store.PluginAccount, error)
	AppendPluginHostLog(ctx context.Context, plugin, level, line string) error
	PrunePluginHostLog(ctx context.Context, before time.Time) error
	PrunePluginEvents(ctx context.Context, before time.Time) error
	SetPluginStatus(ctx context.Context, plugin, status string, lastError *string) error
	ClaimPluginDelivery(ctx context.Context, plugin string, lease time.Duration) (*store.PluginDelivery, error)
	AckPluginDelivery(ctx context.Context, plugin, eventID string, duration time.Duration) error
	FailPluginDelivery(ctx context.Context, plugin, eventID, reason string, retryAt *time.Time) error
}

// Options are the parameters of the plugin host supervisor; zero durations take the default values.
type Options struct {
	// Command is the command that starts the plugin host: node and the platform's built-in host entry point.
	Command []string
	// Dir is the plugin host's working directory: <work root>/platform/plugins/.
	Dir   string
	Env   []string
	Store Store
	// Specs returns the plugins to load once the plugin host has started; it is called on every (re)start.
	Specs func(context.Context) ([]Spec, error)
	Log   *slog.Logger

	ReadyTimeout  time.Duration // waiting for ready, default 30 seconds
	LoadTimeout   time.Duration // load, unload, config_changed, default 30 seconds
	CallTimeout   time.Duration // synchronous hooks, default 15 seconds
	EventTimeout  time.Duration // ack of observe events, default 60 seconds
	ShutdownGrace time.Duration // wait for the host to exit on its own after shutdown, default 5 seconds
	RestartDelay  time.Duration // wait before restarting after a crash, default 1 second
	RestartLimit  int           // stop restarting after this many exits within the window, default 3
	RestartWindow time.Duration // default 5 minutes
	// Backoff is the redelivery interval after an observe event fails; once attempt len(Backoff)+1 still fails, it is
	// marked failed.
	Backoff        []time.Duration
	PollInterval   time.Duration // interval for checking due redeliveries without a notification, default 5 seconds
	LogRetention   time.Duration // how long plugin host logs are kept, default 7 days
	EventRetention time.Duration // how long observe events and delivery records are kept, default 30 days

	Now             func() time.Time // clock for scheduled jobs, default time.Now
	ScheduleTick    time.Duration    // interval for checking scheduled jobs, default 1 second
	ScheduleTimeout time.Duration    // how long one scheduled job waits for a response, default 10 minutes
}

func (o *Options) defaults() {
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&o.ReadyTimeout, 30*time.Second)
	def(&o.LoadTimeout, 30*time.Second)
	def(&o.CallTimeout, 15*time.Second)
	def(&o.EventTimeout, time.Minute)
	def(&o.ShutdownGrace, 5*time.Second)
	def(&o.RestartDelay, time.Second)
	def(&o.RestartWindow, 5*time.Minute)
	def(&o.PollInterval, 5*time.Second)
	def(&o.LogRetention, 7*24*time.Hour)
	def(&o.EventRetention, 30*24*time.Hour)
	def(&o.ScheduleTick, time.Second)
	def(&o.ScheduleTimeout, 10*time.Minute)
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.RestartLimit <= 0 {
		o.RestartLimit = 3
	}
	if o.Backoff == nil {
		o.Backoff = []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute}
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
}

// Plugin host states.
const (
	StateDisabled   = "disabled"   // disabled globally
	StateStarting   = "starting"   // launched, waiting for ready and loading
	StateRunning    = "running"    // running
	StateRestarting = "restarting" // waiting to restart after a crash
	StateStopped    = "stopped"    // exits within the window reached the limit; no more automatic restarts
	StateIdle       = "idle"       // the server is stopping or has not started yet
)

// Plugin statuses (plugins.status).
const (
	StatusInstalled   = "installed"
	StatusActive      = "active"
	StatusError       = "error"
	StatusHostStopped = "host_stopped"
)

// Crash records the plugin host's most recent unexpected exit and the requests it was handling at the time.
type Crash struct {
	At       time.Time  `json:"at"`
	Error    string     `json:"error"`
	InFlight []inflight `json:"inFlight"`
	Last     *inflight  `json:"last"`
	Stderr   []string   `json:"stderr"`
}

// HostStatus is the plugin host's current status, shown on the plugins page.
type HostStatus struct {
	State       string      `json:"state"`
	PID         int         `json:"pid,omitempty"`
	StartedAt   *time.Time  `json:"startedAt"`
	HostVersion string      `json:"hostVersion,omitempty"`
	RecentExits []time.Time `json:"recentExits"`
	LastCrash   *Crash      `json:"lastCrash"`
	Loaded      []string    `json:"loaded"`
}

// stderrTail is the number of last standard error lines kept in a crash record.
const stderrTail = 10

type loadedPlugin struct {
	spec   Spec
	cancel context.CancelFunc
	wake   chan struct{}
	done   chan struct{}
}

type logLine struct{ plugin, level, line string }

// Supervisor supervises one plugin host process.
type Supervisor struct {
	o Options

	mu          sync.Mutex
	state       string
	disabled    bool
	gaveUp      bool
	stopping    bool
	conn        *conn
	startedAt   time.Time
	hostVersion string
	exits       []time.Time
	lastCrash   *Crash
	stderr      []string
	known       []Spec
	loaded      map[string]*loadedPlugin

	signal     chan struct{}
	quit       chan struct{}
	runDone    chan struct{}
	logs       chan logLine
	logsClosed bool
	logDone    chan struct{}
}

// New creates a plugin host supervisor; the host is only launched once Start is called.
func New(o Options) *Supervisor {
	o.defaults()
	return &Supervisor{o: o, state: StateIdle, loaded: map[string]*loadedPlugin{}, signal: make(chan struct{}, 1),
		quit: make(chan struct{}), runDone: make(chan struct{}), logs: make(chan logLine, 1024), logDone: make(chan struct{})}
}

// Start starts supervising; when disabled is true (disabled globally) the plugin host is not launched until
// SetDisabled(false).
func (s *Supervisor) Start(disabled bool) {
	s.mu.Lock()
	s.disabled = disabled
	s.mu.Unlock()
	go s.writeLogs()
	go s.run()
}

// Stop makes the plugin host exit and stops supervising.
func (s *Supervisor) Stop(ctx context.Context) error {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return nil
	}
	s.stopping = true
	c := s.conn
	s.mu.Unlock()
	close(s.quit)
	if c != nil {
		c.stop(s.o.ShutdownGrace)
	}
	select {
	case <-s.runDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.mu.Lock()
	s.logsClosed = true
	close(s.logs)
	s.mu.Unlock()
	select {
	case <-s.logDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// SetDisabled turns the global disable on or off: disabling stops the plugin host; re-enabling resets the restart
// count and launches the host.
func (s *Supervisor) SetDisabled(disabled bool) {
	s.mu.Lock()
	s.disabled = disabled
	c := s.conn
	if !disabled {
		s.gaveUp, s.exits = false, nil
	}
	s.mu.Unlock()
	if disabled && c != nil {
		c.stop(s.o.ShutdownGrace)
	}
	s.poke()
}

// Restart restarts the plugin host on manual request: it resets the restart count, and first makes the host exit if
// it is running.
func (s *Supervisor) Restart() {
	s.mu.Lock()
	s.gaveUp, s.exits = false, nil
	c := s.conn
	s.mu.Unlock()
	if c != nil {
		c.stop(s.o.ShutdownGrace)
	}
	s.poke()
}

// Status returns the plugin host's current status.
func (s *Supervisor) Status() HostStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := HostStatus{State: s.state, HostVersion: s.hostVersion, RecentExits: slices.Clone(s.exits), LastCrash: s.lastCrash, Loaded: []string{}}
	if s.conn != nil {
		st.PID = s.conn.cmd.Process.Pid
		at := s.startedAt
		st.StartedAt = &at
	}
	for _, spec := range s.known {
		if s.loaded[spec.ID] != nil {
			st.Loaded = append(st.Loaded, spec.ID)
		}
	}
	return st
}

func (s *Supervisor) poke() {
	select {
	case s.signal <- struct{}{}:
	default:
	}
}

func (s *Supervisor) setState(state string) {
	s.mu.Lock()
	s.state = state
	s.mu.Unlock()
}

// run is the supervision loop: it launches the plugin host when needed, waits for it to exit, and then decides the
// next step by the restart policy.
func (s *Supervisor) run() {
	defer close(s.runDone)
	go s.prune()
	for {
		s.mu.Lock()
		switch {
		case s.stopping:
			s.state = StateIdle
			s.mu.Unlock()
			return
		case s.disabled:
			s.state = StateDisabled
		case s.gaveUp:
			s.state = StateStopped
		default:
			s.state = StateStarting
		}
		idle := s.state != StateStarting
		s.mu.Unlock()
		if idle {
			select {
			case <-s.signal:
			case <-s.quit:
			}
			continue
		}
		c, err := s.launch()
		if c != nil {
			<-c.done
			err = c.exitErr
		}
		s.afterExit(c, err)
		s.mu.Lock()
		restarting := s.state == StateRestarting
		s.mu.Unlock()
		if restarting {
			select {
			case <-time.After(s.o.RestartDelay):
			case <-s.quit:
			}
		}
	}
}

// launch launches the plugin host, waits for ready and loads the plugins; a nil return means the host did not come
// up, and err is the reason.
func (s *Supervisor) launch() (*conn, error) {
	s.mu.Lock()
	s.stderr = nil
	s.mu.Unlock()
	c, err := startConn(s.o.Command, s.o.Dir, s.o.Env, s.onMessage, s.onStderr)
	if err != nil {
		return nil, fmt.Errorf("start the plugin host: %w", err)
	}
	timer := time.NewTimer(s.o.ReadyTimeout)
	defer timer.Stop()
	select {
	case p := <-c.ready:
		if p.Protocol != ProtocolVersion {
			c.kill()
			return nil, fmt.Errorf("the plugin host uses protocol version %d, but the server only supports %d", p.Protocol, ProtocolVersion)
		}
		s.mu.Lock()
		s.conn, s.startedAt, s.hostVersion = c, time.Now(), p.HostVersion
		stopping := s.stopping || s.disabled
		s.mu.Unlock()
		if stopping {
			c.stop(s.o.ShutdownGrace)
			return c, nil
		}
	case <-timer.C:
		c.kill()
		return nil, fmt.Errorf("the plugin host was not ready within %s", s.o.ReadyTimeout)
	case <-c.done:
		return nil, fmt.Errorf("the plugin host exited before it was ready: %v", c.exitErr)
	case <-s.quit:
		c.stop(s.o.ShutdownGrace)
		return c, nil
	}
	s.o.Log.Info("plugin host ready", "pid", c.cmd.Process.Pid, "hostVersion", s.hostVersion)
	s.loadAll(c)
	s.setState(StateRunning)
	return c, nil
}

// afterExit stops delivery after the plugin host exits and, depending on whether the exit was unexpected, records a
// crash and decides whether to restart.
func (s *Supervisor) afterExit(c *conn, err error) {
	// Take the requests that were unfinished at exit first, then stop the delivery goroutines: stopping delivery
	// withdraws their pending requests.
	var pending []inflight
	var last *inflight
	if c != nil {
		pending, last = c.snapshot()
	}
	s.mu.Lock()
	loaded := s.loaded
	s.loaded = map[string]*loadedPlugin{}
	s.conn = nil
	s.mu.Unlock()
	for _, lp := range loaded {
		lp.cancel()
		<-lp.done
	}
	if c != nil && c.intentional.Load() {
		s.mu.Lock()
		disabled := s.disabled
		known := s.known
		s.mu.Unlock()
		if disabled {
			s.markAll(known, StatusInstalled)
		}
		return
	}
	now := time.Now()
	crash := &Crash{At: now, Error: fmt.Sprint(err), InFlight: pending, Last: last, Stderr: []string{}}
	if crash.InFlight == nil {
		crash.InFlight = []inflight{}
	}
	s.mu.Lock()
	crash.Stderr = append(crash.Stderr, s.stderr...)
	s.lastCrash = crash
	kept := s.exits[:0]
	for _, at := range s.exits {
		if now.Sub(at) < s.o.RestartWindow {
			kept = append(kept, at)
		}
	}
	s.exits = append(kept, now)
	gaveUp := len(s.exits) >= s.o.RestartLimit
	s.gaveUp = gaveUp
	if gaveUp {
		s.state = StateStopped
	} else {
		s.state = StateRestarting
	}
	known := s.known
	s.mu.Unlock()
	s.o.Log.Error("plugin host exited unexpectedly", "err", crash.Error, "inFlight", len(crash.InFlight), "willRestart", !gaveUp)
	s.enqueueLog("", "error", "The plugin host exited unexpectedly: "+crash.Error)
	if gaveUp {
		s.markAll(known, StatusHostStopped)
	}
}

func (s *Supervisor) markAll(specs []Spec, status string) {
	for _, spec := range specs {
		if err := s.o.Store.SetPluginStatus(context.Background(), spec.ID, status, nil); err != nil {
			s.o.Log.Warn("failed to update the plugin status", "plugin", spec.ID, "err", err)
		}
	}
}

// loadAll loads every plugin returned by Specs; a single plugin failing to load only puts that plugin into the error
// status.
func (s *Supervisor) loadAll(c *conn) {
	ctx := context.Background()
	specs, err := s.o.Specs(ctx)
	if err != nil {
		s.o.Log.Error("failed to read the plugins to load", "err", err)
		s.enqueueLog("", "error", "Could not read the plugins to load: "+err.Error())
		return
	}
	s.mu.Lock()
	s.known = specs
	s.mu.Unlock()
	for _, spec := range specs {
		s.loadOne(ctx, c, spec)
	}
}

func (s *Supervisor) loadOne(ctx context.Context, c *conn, spec Spec) {
	payload, err := spec.Load, error(nil)
	if spec.Prepare != nil {
		payload, err = spec.Prepare(ctx)
	}
	if err == nil {
		_, err = c.request(ctx, TypeLoad, spec.ID, payload, s.o.LoadTimeout, inflight{})
	}
	if err != nil {
		msg := err.Error()
		var pe *PluginError
		if errors.As(err, &pe) {
			msg = pe.Message
		}
		s.o.Log.Warn("plugin failed to load", "plugin", spec.ID, "err", msg)
		s.enqueueLog(spec.ID, "error", "Could not load the plugin: "+msg)
		if serr := s.o.Store.SetPluginStatus(ctx, spec.ID, StatusError, &msg); serr != nil {
			s.o.Log.Warn("failed to update the plugin status", "plugin", spec.ID, "err", serr)
		}
		return
	}
	lp := s.startDispatcher(c, spec)
	s.mu.Lock()
	s.loaded[spec.ID] = lp
	s.mu.Unlock()
	if serr := s.o.Store.SetPluginStatus(ctx, spec.ID, StatusActive, nil); serr != nil {
		s.o.Log.Warn("failed to update the plugin status", "plugin", spec.ID, "err", serr)
	}
}

// Reload unloads one plugin in the plugin host and loads it again from the latest Specs (a new version installed,
// a change in enabling); when the host is not running it takes effect on the next start.
func (s *Supervisor) Reload(ctx context.Context, id string) error {
	s.mu.Lock()
	c := s.conn
	s.mu.Unlock()
	if c == nil {
		return nil
	}
	if err := s.unloadOne(ctx, c, id); err != nil {
		s.o.Log.Warn("failed to unload the plugin", "plugin", id, "err", err)
	}
	specs, err := s.o.Specs(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.known = specs
	s.mu.Unlock()
	for _, spec := range specs {
		if spec.ID == id {
			s.loadOne(ctx, c, spec)
		}
	}
	return nil
}

// Unload unloads one plugin in the plugin host (the plugin was uninstalled or no longer has a platform side).
func (s *Supervisor) Unload(ctx context.Context, id string) error {
	s.mu.Lock()
	c := s.conn
	s.known = slices.DeleteFunc(slices.Clone(s.known), func(spec Spec) bool { return spec.ID == id })
	s.mu.Unlock()
	if c == nil {
		return nil
	}
	return s.unloadOne(ctx, c, id)
}

func (s *Supervisor) unloadOne(ctx context.Context, c *conn, id string) error {
	s.mu.Lock()
	lp := s.loaded[id]
	delete(s.loaded, id)
	s.mu.Unlock()
	if lp == nil {
		return nil
	}
	lp.cancel()
	<-lp.done
	_, err := c.request(ctx, TypeUnload, id, struct{}{}, s.o.LoadTimeout, inflight{})
	return err
}

// ConfigChanged hands the new config to a loaded plugin; it returns ErrNotLoaded when the plugin is not loaded.
func (s *Supervisor) ConfigChanged(ctx context.Context, id string, payload any) error {
	_, err := s.Request(ctx, TypeConfigChanged, id, payload, s.o.LoadTimeout)
	return err
}

// ErrNotLoaded means the plugin is not currently in the plugin host (the host is not running, is disabled, or the
// load failed).
var ErrNotLoaded = errors.New("the plugin is not loaded")

// Request sends a request to a loaded plugin and waits for the result (action, http, schedule and so on).
func (s *Supervisor) Request(ctx context.Context, typ, plugin string, payload any, timeout time.Duration) (json.RawMessage, error) {
	s.mu.Lock()
	c, lp := s.conn, s.loaded[plugin]
	s.mu.Unlock()
	if c == nil || lp == nil {
		return nil, ErrNotLoaded
	}
	return c.request(ctx, typ, plugin, payload, timeout, inflight{})
}

// onStderr records one line of the plugin host's standard error in the platform log and the host log; a line with
// an [<id>] prefix is attributed to that plugin.
func (s *Supervisor) onStderr(line string) {
	plugin, text := "", line
	if m := stderrPrefix.FindStringSubmatch(line); m != nil {
		plugin, text = m[1], m[3]
	}
	s.mu.Lock()
	s.stderr = append(s.stderr, line)
	if len(s.stderr) > stderrTail {
		s.stderr = s.stderr[len(s.stderr)-stderrTail:]
	}
	s.mu.Unlock()
	s.o.Log.Warn("plugin host output", "plugin", plugin, "line", text)
	s.enqueueLog(plugin, "warn", text)
}

// onMessage handles logs and requests from the plugin host; requests are handled in separate goroutines so they do
// not block reading responses.
func (s *Supervisor) onMessage(c *conn, m Message) {
	switch m.Type {
	case TypeLog:
		var p logPayload
		_ = json.Unmarshal(m.Payload, &p)
		level := p.Level
		switch level {
		case "debug", "info", "warn", "error":
		default:
			level = "info"
		}
		s.o.Log.Log(context.Background(), slogLevel(level), "plugin log", "plugin", m.Plugin, "message", p.Message)
		s.enqueueLog(m.Plugin, level, p.Message)
	case TypeStateGet, TypeStateSet, TypeStateDelete, TypeStateList:
		go func() {
			v, err := s.handleState(m)
			c.respond(m.ID, m.Plugin, v, err)
		}()
	case TypeAccountsList:
		go func() {
			v, err := s.handleAccounts(m)
			c.respond(m.ID, m.Plugin, v, err)
		}()
	default:
		s.o.Log.Warn("plugin host sent an unknown message", "type", m.Type, "plugin", m.Plugin)
		if m.ID != "" {
			c.respond(m.ID, m.Plugin, nil, fmt.Errorf("unsupported request %s", m.Type))
		}
	}
}

// handleAccounts answers accounts_list: a plugin in the load list (including one still loading or in its lifecycle
// start) whose manifest declares accounts.read and this account kind, and that is enabled in projectId, gets the
// non-deleted accounts of that kind in this project (in the order they were added; values include secret fields).
func (s *Supervisor) handleAccounts(m Message) (any, error) {
	s.mu.Lock()
	var spec *Spec
	for i := range s.known {
		if s.known[i].ID == m.Plugin {
			spec = &s.known[i]
		}
	}
	s.mu.Unlock()
	if spec == nil {
		return nil, errors.New("the plugin is not loaded")
	}
	if !slices.Contains(spec.Permissions, "accounts.read") {
		return nil, errors.New("the manifest does not declare the accounts.read permission")
	}
	var r struct {
		ProjectID string `json:"projectId"`
		Kind      string `json:"kind"`
	}
	if err := json.Unmarshal(m.Payload, &r); err != nil {
		return nil, errors.New("the request cannot be parsed")
	}
	if !slices.Contains(spec.AccountKinds, r.Kind) {
		return nil, fmt.Errorf("the manifest does not declare account kind %s", r.Kind)
	}
	project, err := uuid.Parse(r.ProjectID)
	if err != nil {
		return nil, errors.New("projectId is invalid")
	}
	ctx := context.Background()
	on, err := s.o.Store.PluginEnabledIn(ctx, m.Plugin, project)
	if err != nil {
		return nil, err
	}
	if !on {
		return nil, errors.New("the plugin is not enabled in this project")
	}
	accounts, err := s.o.Store.PluginAccountsOfKind(ctx, project, m.Plugin, r.Kind)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(accounts))
	for _, a := range accounts {
		values := a.Values
		if values == nil {
			values = map[string]any{}
		}
		out = append(out, map[string]any{"id": a.ID, "kind": a.Kind, "name": a.Name, "values": values})
	}
	return out, nil
}

// stateValueMax is the maximum size in bytes of one state value.
const stateValueMax = 1 << 20

func (s *Supervisor) handleState(m Message) (any, error) {
	s.mu.Lock()
	var spec *Spec
	for i := range s.known {
		if s.known[i].ID == m.Plugin {
			spec = &s.known[i]
		}
	}
	s.mu.Unlock()
	if spec == nil {
		return nil, fmt.Errorf("plugin %q is not loaded", m.Plugin)
	}
	if !slices.Contains(spec.Permissions, "state") {
		return nil, errors.New("the manifest does not declare the state permission")
	}
	var r stateRequest
	if err := json.Unmarshal(m.Payload, &r); err != nil {
		return nil, errors.New("the request cannot be parsed")
	}
	var project *uuid.UUID
	if r.ProjectID != nil {
		id, err := uuid.Parse(*r.ProjectID)
		if err != nil {
			return nil, errors.New("projectId must be a UUID")
		}
		project = &id
	}
	if m.Type != TypeStateList && (len(r.Key) == 0 || len(r.Key) > 512) {
		return nil, errors.New("key must be 1 to 512 bytes")
	}
	ctx := context.Background()
	switch m.Type {
	case TypeStateGet:
		v, err := s.o.Store.PluginStateGet(ctx, m.Plugin, project, r.Key)
		if v == nil {
			return json.RawMessage("null"), err
		}
		return v, err
	case TypeStateSet:
		if len(r.Value) == 0 {
			return nil, errors.New("value is missing")
		}
		if len(r.Value) > stateValueMax {
			return nil, fmt.Errorf("value must be at most %d bytes", stateValueMax)
		}
		return nil, s.o.Store.PluginStateSet(ctx, m.Plugin, project, r.Key, r.Value)
	case TypeStateDelete:
		return nil, s.o.Store.PluginStateDelete(ctx, m.Plugin, project, r.Key)
	default:
		return s.o.Store.PluginStateList(ctx, m.Plugin, project, r.Prefix)
	}
}

func slogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}

// enqueueLog hands one log line to the writer goroutine; when the queue is full the line is dropped so protocol
// reading is never blocked.
func (s *Supervisor) enqueueLog(plugin, level, line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logsClosed {
		return
	}
	select {
	case s.logs <- logLine{plugin, level, line}:
	default:
		s.o.Log.Warn("plugin host log queue is full; dropped a line", "plugin", plugin)
	}
}

func (s *Supervisor) writeLogs() {
	defer close(s.logDone)
	for l := range s.logs {
		if err := s.o.Store.AppendPluginHostLog(context.Background(), l.plugin, l.level, l.line); err != nil {
			s.o.Log.Warn("failed to write the plugin host log", "err", err)
		}
	}
}

// prune deletes, every hour, plugin host logs and observe events (including delivery records) older than their
// retention period.
func (s *Supervisor) prune() {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if err := s.o.Store.PrunePluginHostLog(context.Background(), time.Now().Add(-s.o.LogRetention)); err != nil {
			s.o.Log.Warn("failed to prune plugin host logs", "err", err)
		}
		if err := s.o.Store.PrunePluginEvents(context.Background(), time.Now().Add(-s.o.EventRetention)); err != nil {
			s.o.Log.Warn("failed to prune plugin events", "err", err)
		}
		select {
		case <-t.C:
		case <-s.quit:
			return
		}
	}
}
