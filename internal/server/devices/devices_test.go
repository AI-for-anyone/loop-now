package devices_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/devices"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/sqlitetest"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newService(t *testing.T) (*devices.Service, *clock, *store.Store) {
	t.Helper()
	st := sqlitetest.NewStore(t)
	c := &clock{t: time.Date(2026, 11, 28, 9, 0, 0, 0, time.UTC)}
	return &devices.Service{Store: st, Now: c.now}, c, st
}

func addr(s string) *string { return &s }

const iPhoneSafari = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1"

func TestPairingCodeIsOneTimeAndExpires(t *testing.T) {
	s, c, _ := newService(t)
	ctx := context.Background()
	p, err := s.NewPairing()
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Code) < 22 || strings.ContainsAny(p.Code, "#?/&=+ ") {
		t.Fatalf("the code should be long and safe in a URL fragment: %q", p.Code)
	}
	if !p.ExpiresAt.Equal(c.now().Add(10 * time.Minute)) {
		t.Fatalf("a code lasts 10 minutes: %v", p.ExpiresAt)
	}
	d, token, err := s.Pair(ctx, p.Code, "", iPhoneSafari, addr("203.0.113.7"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "iPhone · Safari" || d.LastAddress == nil || *d.LastAddress != "203.0.113.7" {
		t.Fatalf("device: %+v", d)
	}
	if prefix, id, _, ok := auth.ParseToken(token); !ok || prefix != auth.DevicePrefix || id != d.ID {
		t.Fatalf("the session token is an lnd_ token of the device: %q", token)
	}
	if _, _, err := s.Pair(ctx, p.Code, "", iPhoneSafari, nil); !apperr.Is(err, apperr.CodePairingCodeInvalid) {
		t.Fatalf("a used code fails: %v", err)
	}
	late, err := s.NewPairing()
	if err != nil {
		t.Fatal(err)
	}
	c.add(10*time.Minute + time.Second)
	if _, _, err := s.Pair(ctx, late.Code, "", iPhoneSafari, nil); !apperr.Is(err, apperr.CodePairingCodeInvalid) {
		t.Fatalf("an expired code fails: %v", err)
	}
	if _, _, err := s.Pair(ctx, "not-a-code", "", iPhoneSafari, nil); !apperr.Is(err, apperr.CodePairingCodeInvalid) {
		t.Fatalf("a wrong code fails: %v", err)
	}
}

func TestPairingRateLimit(t *testing.T) {
	s, c, _ := newService(t)
	ctx := context.Background()
	p, err := s.NewPairing()
	if err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		if _, _, err := s.Pair(ctx, "wrong", "", "", nil); !apperr.Is(err, apperr.CodePairingCodeInvalid) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, _, err := s.Pair(ctx, p.Code, "", "", nil); !apperr.Is(err, apperr.CodeRateLimited) {
		t.Fatalf("after 10 wrong codes in a minute even the right one waits: %v", err)
	}
	c.add(time.Minute + time.Second)
	if _, _, err := s.Pair(ctx, p.Code, "", "", nil); err != nil {
		t.Fatalf("a minute later the right code works: %v", err)
	}
}

func TestPairingName(t *testing.T) {
	s, _, _ := newService(t)
	ctx := context.Background()
	p, _ := s.NewPairing()
	d, _, err := s.Pair(ctx, p.Code, "  Kitchen iPad  ", iPhoneSafari, nil)
	if err != nil || d.Name != "Kitchen iPad" {
		t.Fatalf("a given name is used, trimmed: %+v %v", d, err)
	}
	for _, bad := range []string{"   ", strings.Repeat("x", 65), "a\nb"} {
		p, _ := s.NewPairing()
		if _, _, err := s.Pair(ctx, p.Code, bad, "", nil); !apperr.Is(err, apperr.CodeValidationFailed) {
			t.Errorf("name %q: %v", bad, err)
		}
		// A bad name does not use the code up.
		if _, _, err := s.Pair(ctx, p.Code, "", "", nil); err != nil {
			t.Errorf("name %q used the code up: %v", bad, err)
		}
	}
}

func TestNameFromUserAgent(t *testing.T) {
	cases := map[string]string{
		iPhoneSafari: "iPhone · Safari",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/131.0.6778.73 Mobile/15E148 Safari/604.1": "iPhone · Chrome",
		"Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1":                 "iPad · Safari",
		"Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36":                          "Android · Chrome",
		"Mozilla/5.0 (Linux; Android 14; SM-S921B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/26.0 Chrome/122.0.0.0 Mobile Safari/537.36":     "Android · Samsung Internet",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36":                          "Mac · Chrome",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 14.7; rv:133.0) Gecko/20100101 Firefox/133.0":                                                            "Mac · Firefox",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0":                  "Windows · Edge",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 OPR/115.0.0.0":                            "Linux · Opera",
		"Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36":                                 "ChromeOS · Chrome",
		"curl/8.7.1": "Device",
		"":           "Device",
	}
	for ua, want := range cases {
		if got := devices.NameFromUserAgent(ua); got != want {
			t.Errorf("%q: got %q, want %q", ua, got, want)
		}
	}
}

func TestAuthenticateTouchesAndExpires(t *testing.T) {
	s, c, _ := newService(t)
	ctx := context.Background()
	p, _ := s.NewPairing()
	d, token, err := s.Pair(ctx, p.Code, "", iPhoneSafari, addr("203.0.113.7"))
	if err != nil {
		t.Fatal(err)
	}
	c.add(2 * time.Minute)
	got, err := s.Authenticate(ctx, token, addr("198.51.100.2"))
	if err != nil || got.ID != d.ID {
		t.Fatalf("authenticate: %+v %v", got, err)
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 1 || !list[0].LastSeenAt.Equal(c.now()) || *list[0].LastAddress != "198.51.100.2" {
		t.Fatalf("a request records when and where the device was last seen: %+v %v", list, err)
	}
	if !devices.ExpiresAt(list[0]).Equal(c.now().Add(30 * 24 * time.Hour)) {
		t.Fatalf("expires 30 days after it was last seen: %v", devices.ExpiresAt(list[0]))
	}
	_, id, _, _ := auth.ParseToken(token)
	for _, bad := range []string{"", "lnd_x", strings.Replace(token, token[len(token)-4:], "AAAA", 1), "lne_" + token[4:]} {
		if _, err := s.Authenticate(ctx, bad, nil); !apperr.Is(err, apperr.CodeUnauthenticated) {
			t.Errorf("token %q: %v", bad, err)
		}
	}
	c.add(30*24*time.Hour + time.Second)
	if _, err := s.Authenticate(ctx, token, nil); !apperr.Is(err, apperr.CodeUnauthenticated) {
		t.Fatalf("a session idle for 30 days has expired: %v", err)
	}
	if list, err := s.List(ctx); err != nil || len(list) != 0 {
		t.Fatalf("an expired device is no longer listed: %+v %v", list, err)
	}
	if err := s.Remove(ctx, id); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("and is gone: %v", err)
	}
}

func TestRemoveCancelsTrackedRequests(t *testing.T) {
	s, _, _ := newService(t)
	ctx := context.Background()
	p, _ := s.NewPairing()
	d, token, err := s.Pair(ctx, p.Code, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := s.NewPairing()
	d2, _, err := s.Pair(ctx, other.Code, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	untrack := s.Track(d.ID, cancel)
	defer untrack()
	otherCtx, otherCancel := context.WithCancel(ctx)
	defer otherCancel()
	defer s.Track(d2.ID, otherCancel)()
	if err := s.Remove(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reqCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("removing a device cancels its requests in flight")
	}
	if otherCtx.Err() != nil {
		t.Fatal("other devices keep their requests")
	}
	if _, err := s.Authenticate(ctx, token, nil); !apperr.Is(err, apperr.CodeUnauthenticated) {
		t.Fatalf("a removed device is refused: %v", err)
	}
	if _, err := s.Rename(ctx, d2.ID, "Phone"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rename(ctx, d2.ID, " "); !apperr.Is(err, apperr.CodeValidationFailed) {
		t.Fatalf("an empty name is refused: %v", err)
	}
	if _, err := s.Rename(ctx, uuid.New(), "Phone"); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("an unknown device: %v", err)
	}
}

func TestPairingStatusNamesTheDeviceThatUsedThisCode(t *testing.T) {
	s, c, _ := newService(t)
	ctx := context.Background()
	mine, _ := s.NewPairing()
	other, _ := s.NewPairing()
	if mine.ID == uuid.Nil || mine.ID == other.ID {
		t.Fatalf("pairing ids: %v %v", mine.ID, other.ID)
	}
	st, err := s.PairingStatus(ctx, mine.ID)
	if err != nil || st.State != devices.PairingOpen || st.Device != nil || !st.ExpiresAt.Equal(mine.ExpiresAt) {
		t.Fatalf("open: %+v %v", st, err)
	}
	// Another device pairs with the other code first: this code is still open.
	if _, _, err := s.Pair(ctx, other.Code, "Kitchen iPad", "", nil); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.PairingStatus(ctx, mine.ID); st.State != devices.PairingOpen {
		t.Fatalf("someone else's pairing changed this code: %+v", st)
	}
	d, _, err := s.Pair(ctx, mine.Code, "", iPhoneSafari, nil)
	if err != nil {
		t.Fatal(err)
	}
	st, err = s.PairingStatus(ctx, mine.ID)
	if err != nil || st.State != devices.PairingUsed || st.Device == nil || st.Device.ID != d.ID || st.Device.Name != "iPhone · Safari" {
		t.Fatalf("used: %+v %v", st, err)
	}
	// Renamed since: the status shows the device as it is now.
	if _, err := s.Rename(ctx, d.ID, "My phone"); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.PairingStatus(ctx, mine.ID); st.Device == nil || st.Device.Name != "My phone" {
		t.Fatalf("after rename: %+v", st)
	}
	// Removed since: still named as it was.
	if err := s.Remove(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if st, err := s.PairingStatus(ctx, mine.ID); err != nil || st.Device == nil || st.Device.ID != d.ID {
		t.Fatalf("after removal: %+v %v", st, err)
	}
	unused, _ := s.NewPairing()
	c.add(10*time.Minute + time.Second)
	if st, err := s.PairingStatus(ctx, unused.ID); err != nil || st.State != devices.PairingExpired || st.Device != nil {
		t.Fatalf("expired: %+v %v", st, err)
	}
	c.add(10 * time.Minute)
	if _, err := s.PairingStatus(ctx, unused.ID); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("forgotten 10 minutes after it expired: %v", err)
	}
	if _, err := s.PairingStatus(ctx, uuid.Must(uuid.NewV7())); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestAnEvictedCodeReadsAsExpired(t *testing.T) {
	s, c, _ := newService(t)
	first, _ := s.NewPairing()
	for range 20 {
		c.add(time.Second)
		_, _ = s.NewPairing()
	}
	if st, err := s.PairingStatus(context.Background(), first.ID); err != nil || st.State != devices.PairingExpired {
		t.Fatalf("evicted: %+v %v", st, err)
	}
	if _, _, err := s.Pair(context.Background(), first.Code, "", "", nil); !apperr.Is(err, apperr.CodePairingCodeInvalid) {
		t.Fatalf("an evicted code works: %v", err)
	}
}
