// Package tunnel runs "access from phones": with the switch on and a signed-in Loop Now account, it registers this
// computer at the relay once, brings its Cloudflare Tunnel up, and runs cloudflared, which keeps the tunnel open from
// this computer and forwards https://<hostname> to the remote door. When cloudflared exits or loses Cloudflare for
// too long, it brings the tunnel up again (the relay recreates a recycled tunnel under the same hostname) and
// restarts it, waiting longer after each quick failure.
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/cloudflared"
	"loop-now/internal/server/relay"
	"loop-now/internal/server/remoteaccess"
)

// The states of the tunnel (the Tunnel schema's state).
const (
	StateOff         = "off"
	StateStarting    = "starting"
	StateDownloading = "downloading"
	StateConnecting  = "connecting"
	StateOnline      = "online"
	StateFailed      = "failed"
)

// Why the tunnel failed (the Tunnel schema's errorCode).
const (
	ErrSignedOut              = "signed_out"
	ErrHostLimitReached       = "host_limit_reached"
	ErrRelayUnavailable       = "relay_unavailable"
	ErrCloudflaredUnavailable = "cloudflared_unavailable"
	ErrCloudflaredFailed      = "cloudflared_failed"
	ErrRemoteDoorFailed       = "remote_door_failed"
)

// State mirrors the Tunnel schema.
type State struct {
	Enabled     bool                `json:"enabled"`
	State       string              `json:"state"`
	Hostname    *string             `json:"hostname"`
	URL         *string             `json:"url"`
	Error       *string             `json:"error"`
	ErrorCode   *string             `json:"errorCode"`
	RetryAt     *time.Time          `json:"retryAt"`
	Download    *Download           `json:"download"`
	Cloudflared *cloudflared.Binary `json:"cloudflared"`
}

// Download mirrors the TunnelDownload schema.
type Download struct {
	Version       string `json:"version"`
	ReceivedBytes int64  `json:"receivedBytes"`
	TotalBytes    *int64 `json:"totalBytes"`
}

// Door is the remote door the tunnel forwards to (remoteaccess.Door).
type Door interface {
	State() remoteaccess.State
	Apply(ctx context.Context, enabled bool, port *int) (remoteaccess.State, error)
}

// Installer finds or downloads cloudflared (cloudflared.Installer).
type Installer interface {
	Ensure(ctx context.Context, progress cloudflared.Progress) (cloudflared.Binary, error)
}

// Defaults of the Manager's timings.
const (
	DefaultBackoffBase     = time.Second
	DefaultBackoffMax      = time.Minute
	DefaultStableUptime    = 30 * time.Second
	DefaultReadyInterval   = 5 * time.Second
	DefaultNotReadyRestart = 2 * time.Minute
	// rejectionsBeforeRestart registration refusals (the tunnel was recycled or its token is stale) make cloudflared
	// restart with a fresh bring-up; a new tunnel may be refused a few times while Cloudflare spreads its token.
	rejectionsBeforeRestart = 4
	stopGrace               = 5 * time.Second
	// installRetryMin is the shortest wait before downloading cloudflared again after a failure.
	installRetryMin = 30 * time.Second
)

// Manager owns the tunnel.
type Manager struct {
	Account   *relay.Account
	Client    *relay.Client
	Door      Door
	Installer Installer
	// HostName names this computer in the relay's host list.
	HostName func() string
	Log      *slog.Logger
	// Timings; zero means the defaults above.
	BackoffBase, BackoffMax, StableUptime, ReadyInterval, NotReadyRestart time.Duration

	mu     sync.Mutex
	run    *run
	status status
	closed bool
}

// status is what the loop last reported; State adds the switch and the hostname.
type status struct {
	state       string
	err, code   string
	retryAt     *time.Time
	download    *Download
	cloudflared *cloudflared.Binary
}

type run struct {
	cancel context.CancelFunc
	done   chan struct{}
	// port is the remote door's port the tunnel was last brought up for.
	port int
	// restart asks the running cloudflared to stop so the loop brings the tunnel up again at once.
	restart chan struct{}
}

func (m *Manager) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.New(slog.DiscardHandler)
}

func orDefault(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// Start brings the tunnel back at server start when the switch is on.
func (m *Manager) Start() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Account.TunnelEnabled() {
		m.startLocked()
	}
}

// State reports the switch and what the tunnel does now.
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stateLocked()
}

func (m *Manager) stateLocked() State {
	st := State{Enabled: m.Account.TunnelEnabled(), State: StateOff, Cloudflared: m.status.cloudflared}
	if _, hostname := m.Account.Host(); hostname != "" {
		u := "https://" + hostname
		st.Hostname, st.URL = &hostname, &u
	}
	if m.run == nil {
		return st
	}
	st.State = m.status.state
	if m.status.err != "" {
		e, c := m.status.err, m.status.code
		st.Error, st.ErrorCode = &e, &c
	}
	st.RetryAt, st.Download = m.status.retryAt, m.status.download
	return st
}

// PublicOrigin is the tunnel's origin, https://<hostname>, once this computer has an address; empty before.
func (m *Manager) PublicOrigin() string {
	if _, hostname := m.Account.Host(); hostname != "" {
		return "https://" + hostname
	}
	return ""
}

// OnlineURL is the tunnel's address while it is online.
func (m *Manager) OnlineURL() (string, bool) {
	st := m.State()
	if st.State == StateOnline && st.URL != nil {
		return *st.URL, true
	}
	return "", false
}

func signedOut() error {
	return apperr.Conflict(apperr.CodeRelaySignedOut, "Sign in to a Loop Now account first.")
}

// SetEnabled turns access from phones on or off. On turns the remote door on (keeping its port setting) and starts
// the tunnel in the background; off stops cloudflared.
func (m *Manager) SetEnabled(ctx context.Context, on bool) (State, error) {
	if !on {
		m.stop(false)
		return m.State(), nil
	}
	if m.Account.Credential() == "" {
		return State{}, signedOut()
	}
	if door := m.Door.State(); !door.Enabled {
		if _, err := m.Door.Apply(ctx, true, door.Port); err != nil {
			return State{}, err
		}
	}
	if err := m.Account.SetTunnelEnabled(true); err != nil {
		return State{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.run == nil {
		m.startLocked()
	}
	return m.stateLocked(), nil
}

// stop turns the switch off unless keep, and stops the loop and cloudflared, waiting for them.
func (m *Manager) stop(keep bool) {
	if !keep {
		if err := m.Account.SetTunnelEnabled(false); err != nil {
			m.log().Error("could not save the switch for access from phones", "err", err)
		}
	}
	m.mu.Lock()
	r := m.run
	m.run = nil
	m.mu.Unlock()
	if r != nil {
		r.cancel()
		<-r.done
	}
}

// Release turns access from phones off, stops cloudflared and asks the relay to delete the tunnel, its DNS record
// and the address, then forgets the address.
func (m *Manager) Release(ctx context.Context) error {
	m.stop(false)
	id, _ := m.Account.Host()
	if id == "" {
		return nil
	}
	cred := m.Account.Credential()
	if cred == "" {
		return apperr.Conflict(apperr.CodeRelaySignedOut, "Sign in to the Loop Now account this address belongs to, then release it.")
	}
	err := m.Client.Release(ctx, cred, id)
	switch {
	case err == nil || relay.Code(err) == relay.CodeNotFound:
		m.log().Info("released the tunnel address", "hostId", id)
		return m.Account.SetHost("", "")
	case relay.Code(err) == relay.CodeUnauthorized:
		m.Account.Forget(cred)
		return apperr.Conflict(apperr.CodeRelaySignedOut, "The Loop Now account signed this computer out. Sign in again, then release the address.")
	case ctx.Err() != nil:
		return ctx.Err()
	default:
		return &apperr.Error{Status: http.StatusBadGateway, Code: apperr.CodeRelayFailed, Message: "Could not release the address: " + err.Error()}
	}
}

// SignOut stops the tunnel and signs out of the Loop Now account.
func (m *Manager) SignOut(ctx context.Context) {
	m.stop(false)
	m.Account.SignOut(ctx)
}

// AccountChanged follows the account: after a sign-in with the switch on the tunnel starts; after the relay refused
// the credential it waits for a sign-in.
func (m *Manager) AccountChanged() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || !m.Account.TunnelEnabled() {
		return
	}
	if m.Account.Credential() != "" && (m.run == nil || m.status.code == ErrSignedOut) {
		if m.run != nil {
			m.run.cancel()
			m.run = nil
		}
		m.startLocked()
	}
}

// DoorChanged follows the remote door after its setting changed: turning it off turns access from phones off; a new
// port brings the tunnel up again for it.
func (m *Manager) DoorChanged(door remoteaccess.State) {
	if !door.Enabled {
		if m.Account.TunnelEnabled() {
			m.stop(false)
		}
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.run != nil && door.State == remoteaccess.StateListening && portOf(door) != m.run.port {
		select {
		case m.run.restart <- struct{}{}:
		default:
		}
	}
}

// Close stops cloudflared for good, for server shutdown; the switch stays as it is.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	m.stop(true)
}

func (m *Manager) startLocked() {
	if m.closed {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &run{cancel: cancel, done: make(chan struct{}), restart: make(chan struct{}, 1)}
	m.run = r
	m.status = status{state: StateStarting, cloudflared: m.status.cloudflared}
	go m.loop(ctx, r)
}

// set reports the loop's progress, unless the loop is no longer the current one.
func (m *Manager) set(r *run, f func(s *status)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.run != r {
		return
	}
	f(&m.status)
}

// failure is why an attempt ended.
type failure struct {
	code, msg string
	// retry is false when only the person can fix it; after is a wait the relay asked for.
	retry bool
	after time.Duration
	// stable is set when cloudflared ran long enough that the next try starts again from the shortest wait.
	stable bool
	// now retries at once (the tunnel must be brought up again, for a new port or after a refusal).
	now bool
}

func (m *Manager) loop(ctx context.Context, r *run) {
	defer close(r.done)
	failures := 0
	for {
		f := m.attempt(ctx, r)
		if ctx.Err() != nil {
			return
		}
		if f.stable {
			failures = 0
		}
		m.log().Warn("the tunnel failed", "code", f.code, "err", f.msg, "retry", f.retry)
		if !f.retry {
			m.set(r, func(s *status) { *s = status{state: StateFailed, err: f.msg, code: f.code, cloudflared: s.cloudflared} })
			select {
			case <-ctx.Done():
				return
			case <-r.restart:
				continue
			}
		}
		var delay time.Duration
		if !f.now {
			failures++
			delay = orDefault(m.BackoffBase, DefaultBackoffBase) << min(failures-1, 16)
			delay = min(delay, orDefault(m.BackoffMax, DefaultBackoffMax))
			if f.code == ErrCloudflaredUnavailable {
				delay = max(delay, installRetryMin)
			}
			delay = max(delay, f.after)
		}
		at := time.Now().Add(delay).UTC()
		m.set(r, func(s *status) {
			*s = status{state: StateFailed, err: f.msg, code: f.code, retryAt: &at, cloudflared: s.cloudflared}
		})
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-r.restart:
			t.Stop()
		case <-t.C:
		}
	}
}

// relayFailure turns a relay error into why the attempt ended; a refused credential signs this computer out.
func (m *Manager) relayFailure(err error, cred, what string) failure {
	var re *relay.Error
	switch {
	case relay.Code(err) == relay.CodeUnauthorized:
		m.Account.Forget(cred)
		return failure{code: ErrSignedOut, msg: "The Loop Now account signed this computer out. Sign in again."}
	case relay.Code(err) == relay.CodeHostLimitReached:
		limit := ""
		if errors.As(err, &re) && re.Limit > 0 {
			limit = fmt.Sprintf(" (%d)", re.Limit)
		}
		return failure{code: ErrHostLimitReached, msg: "This Loop Now account already has as many computers as it may" + limit + ". Release the address on one of them, then turn access from phones on again."}
	case errors.As(err, &re):
		return failure{code: ErrRelayUnavailable, msg: "Could not " + what + ": " + err.Error(), retry: true, after: re.RetryAfter}
	default:
		return failure{code: ErrRelayUnavailable, msg: "Could not " + what + ": " + err.Error(), retry: true}
	}
}

func portOf(door remoteaccess.State) int {
	if door.State != remoteaccess.StateListening || door.URL == nil {
		return 0
	}
	var port int
	if _, err := fmt.Sscanf(*door.URL, "http://"+remoteaccess.Host+":%d", &port); err != nil {
		return 0
	}
	return port
}

// attempt brings the tunnel up and runs cloudflared until it exits, the loop stops, or a restart is asked for.
func (m *Manager) attempt(ctx context.Context, r *run) failure {
	m.set(r, func(s *status) { *s = status{state: StateStarting, cloudflared: s.cloudflared} })
	door := m.Door.State()
	port := portOf(door)
	if port == 0 {
		msg := "The remote door is not listening."
		if door.Error != nil && *door.Error != "" {
			msg = "The remote door could not listen: " + *door.Error
		}
		return failure{code: ErrRemoteDoorFailed, msg: msg, retry: true}
	}
	m.mu.Lock()
	r.port = port
	m.mu.Unlock()
	cred := m.Account.Credential()
	if cred == "" {
		return failure{code: ErrSignedOut, msg: "Sign in to a Loop Now account to turn access from phones on."}
	}
	var token string
	for tries := 0; ; tries++ {
		hostID, _ := m.Account.Host()
		if hostID == "" {
			name := ""
			if m.HostName != nil {
				name = m.HostName()
			}
			h, err := m.Client.Register(ctx, cred, m.Account.ClientID(), name)
			if err != nil {
				return m.relayFailure(err, cred, "register this computer at the relay")
			}
			if err := m.Account.SetHost(h.ID, h.Hostname); err != nil {
				return failure{code: ErrRelayUnavailable, msg: "Could not keep the address in the data folder: " + err.Error(), retry: true}
			}
			m.log().Info("registered this computer at the relay", "hostId", h.ID, "hostname", h.Hostname)
			hostID = h.ID
		}
		h, tok, err := m.Client.BringUp(ctx, cred, hostID, port)
		if relay.Code(err) == relay.CodeNotFound && tries == 0 {
			// Released elsewhere, or registered with another account: register again (a new address).
			m.log().Info("the relay no longer knows this computer's host; registering again", "hostId", hostID)
			if err := m.Account.SetHost("", ""); err != nil {
				return failure{code: ErrRelayUnavailable, msg: err.Error(), retry: true}
			}
			continue
		}
		if err != nil {
			return m.relayFailure(err, cred, "bring the tunnel up at the relay")
		}
		if h.Hostname != "" {
			_ = m.Account.SetHost(h.ID, h.Hostname)
		}
		token = tok
		break
	}

	bin, err := m.Installer.Ensure(ctx, func(received, total int64) {
		m.set(r, func(s *status) {
			d := &Download{Version: cloudflared.Version, ReceivedBytes: received}
			if total >= 0 {
				d.TotalBytes = &total
			}
			s.state, s.download = StateDownloading, d
		})
	})
	if err != nil {
		if ctx.Err() != nil {
			return failure{}
		}
		return failure{code: ErrCloudflaredUnavailable, msg: err.Error(), retry: !errors.Is(err, cloudflared.ErrUnsupported)}
	}
	m.set(r, func(s *status) { *s = status{state: StateConnecting, cloudflared: &bin} })
	return m.runCloudflared(ctx, r, bin, token)
}
