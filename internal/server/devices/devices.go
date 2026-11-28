// Package devices pairs phones and other devices with this server and keeps their sessions at the remote door.
//
// A person at this computer makes a one-time pairing code (10 minutes); the device sends it to the remote door and
// gets a session token (`lnd_…`, carried in a cookie). Only hashes are kept: of open codes in memory, of session secrets
// in the database. A session not used for 30 days expires. Removing a device cancels its requests in flight, so its
// open event streams close at once.
package devices

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
)

const (
	// PairingTTL is how long a pairing code works.
	PairingTTL = 10 * time.Minute
	// IdleTimeout is how long a session lasts without being used.
	IdleTimeout = 30 * 24 * time.Hour
	// failureLimit wrong codes within failureWindow make every attempt wait until the window has passed. The count is
	// for the whole remote door: behind a tunnel every request comes from the same address.
	failureLimit  = 10
	failureWindow = time.Minute
	// maxOpenCodes caps the codes waiting to be used; the oldest goes first.
	maxOpenCodes = 20
	codeBytes    = 16
	// MaxNameLength is the longest device name, in characters.
	MaxNameLength = 64
)

// The session cookie: __Host- over HTTPS (Secure, no Domain, so a sibling subdomain cannot set or shadow it), the plain
// name over HTTP (the remote door opened on this computer). The browser keeps it for 400 days, the longest browsers
// allow; the server ends the session after 30 idle days.
const (
	CookieName       = "loop_now_device"
	SecureCookieName = "__Host-loop_now_device"
	CookieMaxAge     = 400 * 24 * 60 * 60
)

// Cookie returns the session cookie carrying token.
func Cookie(token string, secure bool) *http.Cookie {
	name := CookieName
	if secure {
		name = SecureCookieName
	}
	return &http.Cookie{Name: name, Value: token, Path: "/", MaxAge: CookieMaxAge, HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode}
}

// Service holds the open pairing codes, the failed attempts and the requests in flight per device.
type Service struct {
	Store *store.Store
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu       sync.Mutex
	codes    map[[sha256.Size]byte]*pairing // hash of an open code -> its pairing
	pairings map[uuid.UUID]*pairing         // every code made in the last PairingTTL after its expiry, open or not
	failures []time.Time
	live     map[uuid.UUID]map[*tracked]struct{}
}

// pairing is what the server remembers of a code: not the code, only when it expires and who used it.
type pairing struct {
	id        uuid.UUID
	expiresAt time.Time
	// usedBy is the device that paired with it; evicted marks a code pushed out by newer ones (it reads as expired).
	usedBy  *store.DeviceSession
	evicted bool
}

type tracked struct{ cancel context.CancelFunc }

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Pairing is a new one-time code; Code is shown only now. ID names it for PairingStatus.
type Pairing struct {
	ID        uuid.UUID
	Code      string
	ExpiresAt time.Time
}

// The states of a pairing code.
const (
	PairingOpen    = "open"
	PairingUsed    = "used"
	PairingExpired = "expired"
)

// PairingStatus is whether a code was used, and by which device.
type PairingStatus struct {
	ID        uuid.UUID
	State     string
	ExpiresAt time.Time
	// Device paired with the code (state used).
	Device *store.DeviceSession
}

// NewPairing makes a one-time pairing code.
func (s *Service) NewPairing() (Pairing, error) {
	raw := make([]byte, codeBytes)
	if _, err := rand.Read(raw); err != nil {
		return Pairing{}, err
	}
	code := base64.RawURLEncoding.EncodeToString(raw)
	id, err := uuid.NewV7()
	if err != nil {
		return Pairing{}, err
	}
	now := s.now()
	p := Pairing{ID: id, Code: code, ExpiresAt: now.Add(PairingTTL)}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneCodes(now)
	if s.codes == nil {
		s.codes = map[[sha256.Size]byte]*pairing{}
		s.pairings = map[uuid.UUID]*pairing{}
	}
	for len(s.codes) >= maxOpenCodes {
		var oldest [sha256.Size]byte
		var at time.Time
		for h, pr := range s.codes {
			if at.IsZero() || pr.expiresAt.Before(at) {
				oldest, at = h, pr.expiresAt
			}
		}
		s.codes[oldest].evicted = true
		delete(s.codes, oldest)
	}
	pr := &pairing{id: id, expiresAt: p.ExpiresAt}
	s.codes[sha256.Sum256([]byte(code))] = pr
	s.pairings[id] = pr
	return p, nil
}

// pruneCodes forgets expired codes, and their records PairingTTL after they expired.
func (s *Service) pruneCodes(now time.Time) {
	for h, pr := range s.codes {
		if !now.Before(pr.expiresAt) {
			delete(s.codes, h)
		}
	}
	for id, pr := range s.pairings {
		if !now.Before(pr.expiresAt.Add(PairingTTL)) {
			delete(s.pairings, id)
		}
	}
}

// PairingStatus reports whether a code is open, used (and by which device, as it is now when it is still paired) or
// expired; NotFound once the server no longer remembers it (PairingTTL after it expired, or after a restart).
func (s *Service) PairingStatus(ctx context.Context, id uuid.UUID) (PairingStatus, error) {
	now := s.now()
	s.mu.Lock()
	s.pruneCodes(now)
	pr := s.pairings[id]
	var st PairingStatus
	if pr != nil {
		st = PairingStatus{ID: pr.id, ExpiresAt: pr.expiresAt, State: PairingOpen}
		switch {
		case pr.usedBy != nil:
			d := *pr.usedBy
			st.State, st.Device = PairingUsed, &d
		case pr.evicted || !now.Before(pr.expiresAt):
			st.State = PairingExpired
		}
	}
	s.mu.Unlock()
	if pr == nil {
		return PairingStatus{}, apperr.NotFound("Pairing code")
	}
	if st.Device != nil {
		if d, _, err := s.Store.LookupDeviceSession(ctx, st.Device.ID); err == nil {
			st.Device = &d
		} else if !store.IsNotFound(err) {
			return PairingStatus{}, err
		}
	}
	return st, nil
}

// takeCode uses a code up and returns its pairing; it fails when the code is wrong, has expired or was used, and
// while too many wrong codes came in the last minute.
func (s *Service) takeCode(code string) (*pairing, error) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.failures[:0]
	for _, at := range s.failures {
		if now.Sub(at) < failureWindow {
			kept = append(kept, at)
		}
	}
	s.failures = kept
	if len(s.failures) >= failureLimit {
		return nil, &apperr.Error{Status: http.StatusTooManyRequests, Code: apperr.CodeRateLimited, Message: "Too many wrong pairing codes. Try again in a minute."}
	}
	s.pruneCodes(now)
	h := sha256.Sum256([]byte(code))
	pr, ok := s.codes[h]
	if !ok {
		s.failures = append(s.failures, now)
		return nil, &apperr.Error{Status: http.StatusForbidden, Code: apperr.CodePairingCodeInvalid,
			Message: "The pairing code is wrong, has expired or was already used. Make a new one in Settings on the computer."}
	}
	delete(s.codes, h)
	return pr, nil
}

// CleanName trims a device name and checks it: 1 to 64 characters, no control characters.
func CleanName(field, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", apperr.Invalid(field, "Required: a name for the device.")
	}
	if utf8.RuneCountInString(name) > MaxNameLength {
		return "", apperr.Invalid(field, "At most 64 characters.")
	}
	if strings.IndexFunc(name, unicode.IsControl) >= 0 || !utf8.ValidString(name) {
		return "", apperr.Invalid(field, "Must not contain control characters.")
	}
	return name, nil
}

// Pair uses a pairing code up and starts a session for the device. name, when not empty, names the device; otherwise
// the name comes from userAgent. It returns the device and its session token, which is given out only now.
func (s *Service) Pair(ctx context.Context, code, name, userAgent string, address *string) (store.DeviceSession, string, error) {
	if name != "" {
		var err error
		if name, err = CleanName("name", name); err != nil {
			return store.DeviceSession{}, "", err
		}
	} else {
		name = NameFromUserAgent(userAgent)
	}
	pr, err := s.takeCode(code)
	if err != nil {
		return store.DeviceSession{}, "", err
	}
	tok, err := auth.NewToken(auth.DevicePrefix)
	if err != nil {
		return store.DeviceSession{}, "", err
	}
	now := s.now()
	d, err := s.Store.InsertDeviceSession(ctx, store.DeviceSession{ID: tok.ID, Name: name, CreatedAt: now, LastSeenAt: now, LastAddress: address}, tok.Hash)
	if err != nil {
		return store.DeviceSession{}, "", err
	}
	s.mu.Lock()
	used := d
	pr.usedBy = &used
	s.mu.Unlock()
	return d, tok.Plain, nil
}

func unauthenticated() error {
	return apperr.Unauthenticated("This device is not paired, or it was removed or not used for 30 days. Pair it again from Settings on the computer.")
}

// TokenID returns the device a session token names, without checking it; ok is false when it is not a session token.
func TokenID(token string) (uuid.UUID, bool) {
	prefix, id, _, ok := auth.ParseToken(token)
	if !ok || prefix != auth.DevicePrefix {
		return uuid.Nil, false
	}
	return id, true
}

// Authenticate checks a session token and records the request (time and address, at most once a minute).
func (s *Service) Authenticate(ctx context.Context, token string, address *string) (store.DeviceSession, error) {
	prefix, id, secret, ok := auth.ParseToken(token)
	if !ok || prefix != auth.DevicePrefix {
		return store.DeviceSession{}, unauthenticated()
	}
	d, hash, err := s.Store.LookupDeviceSession(ctx, id)
	if err != nil {
		if store.IsNotFound(err) {
			return store.DeviceSession{}, unauthenticated()
		}
		return store.DeviceSession{}, err
	}
	if !auth.SecretMatches(hash, secret) {
		return store.DeviceSession{}, unauthenticated()
	}
	now := s.now()
	if !now.Before(ExpiresAt(d)) {
		return store.DeviceSession{}, unauthenticated()
	}
	if wrote, err := s.Store.TouchDeviceSession(ctx, d.ID, now, address); err != nil {
		return store.DeviceSession{}, err
	} else if wrote {
		d.LastSeenAt, d.LastAddress = now, address
	}
	return d, nil
}

// ExpiresAt is when a session expires if it is not used again.
func ExpiresAt(d store.DeviceSession) time.Time { return d.LastSeenAt.Add(IdleTimeout) }

// List lists the devices whose session is live, the most recently seen first; expired ones are deleted.
func (s *Service) List(ctx context.Context) ([]store.DeviceSession, error) {
	return s.Store.ListDeviceSessions(ctx, s.now().Add(-IdleTimeout))
}

// Rename changes a device's name.
func (s *Service) Rename(ctx context.Context, id uuid.UUID, name string) (store.DeviceSession, error) {
	name, err := CleanName("name", name)
	if err != nil {
		return store.DeviceSession{}, err
	}
	return s.Store.RenameDeviceSession(ctx, id, name)
}

// Track registers the cancel function of a request of device id, which Remove calls; call the returned function when
// the request ends. Register before checking the session, so a removal can never slip between the check and the
// registration.
func (s *Service) Track(id uuid.UUID, cancel context.CancelFunc) (untrack func()) {
	t := &tracked{cancel: cancel}
	s.mu.Lock()
	if s.live == nil {
		s.live = map[uuid.UUID]map[*tracked]struct{}{}
	}
	if s.live[id] == nil {
		s.live[id] = map[*tracked]struct{}{}
	}
	s.live[id][t] = struct{}{}
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.live[id], t)
		if len(s.live[id]) == 0 {
			delete(s.live, id)
		}
		s.mu.Unlock()
	}
}

// Remove ends a device's session: it deletes the session and cancels the device's requests in flight.
func (s *Service) Remove(ctx context.Context, id uuid.UUID) error {
	if err := s.Store.DeleteDeviceSession(ctx, id); err != nil {
		return err
	}
	s.mu.Lock()
	var cancels []context.CancelFunc
	for t := range s.live[id] {
		cancels = append(cancels, t.cancel)
	}
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	return nil
}

// NameFromUserAgent makes a device name such as "iPhone · Safari" from a browser's User-Agent; "Device" when it says
// nothing useful.
func NameFromUserAgent(ua string) string {
	has := func(s string) bool { return strings.Contains(ua, s) }
	var device string
	switch {
	case has("iPhone"):
		device = "iPhone"
	case has("iPad"):
		device = "iPad"
	case has("Android"):
		device = "Android"
	case has("CrOS"):
		device = "ChromeOS"
	case has("Macintosh"):
		device = "Mac"
	case has("Windows"):
		device = "Windows"
	case has("Linux"):
		device = "Linux"
	default:
		return "Device"
	}
	var browser string
	switch {
	case has("Edg/"), has("EdgiOS/"), has("EdgA/"):
		browser = "Edge"
	case has("OPR/"), has("OPiOS/"):
		browser = "Opera"
	case has("SamsungBrowser/"):
		browser = "Samsung Internet"
	case has("Firefox/"), has("FxiOS/"):
		browser = "Firefox"
	case has("Chrome/"), has("CriOS/"):
		browser = "Chrome"
	case has("Safari/"):
		browser = "Safari"
	}
	if browser == "" {
		return device
	}
	return device + " · " + browser
}
