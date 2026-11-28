package relay_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"loop-now/internal/server/relay"
	"loop-now/internal/server/relay/relaytest"
)

func signedInClient(t *testing.T) (*relaytest.Fake, *relay.Client, string) {
	t.Helper()
	e := newAccount(t)
	e.signIn("acct-1")
	return e.fake, e.acc.Client, e.acc.Credential()
}

func TestRegisterBringUpAndRelease(t *testing.T) {
	fake, c, cred := signedInClient(t)
	ctx := context.Background()
	h, err := c.Register(ctx, cred, "client-0123456789abcdef", "Studio Mac")
	if err != nil {
		t.Fatal(err)
	}
	again, err := c.Register(ctx, cred, "client-0123456789abcdef", "Studio Mac")
	if err != nil || again.ID != h.ID || again.Hostname != h.Hostname {
		t.Fatalf("registering again: %+v %v", again, err)
	}
	up, token, err := c.BringUp(ctx, cred, h.ID, 53158)
	if err != nil || up.TunnelID == nil || token != relaytest.TunnelToken(*up.TunnelID) || up.RemotePort == nil || *up.RemotePort != 53158 {
		t.Fatalf("bring-up: %+v %q %v", up, token, err)
	}
	if err := c.Release(ctx, cred, h.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.BringUp(ctx, cred, h.ID, 53158); relay.Code(err) != relay.CodeNotFound {
		t.Fatalf("bring-up after release: %v", err)
	}
	if len(fake.Hosts()) != 0 {
		t.Fatal("the host is still there")
	}
}

func TestRelayErrorsCarryTheirFields(t *testing.T) {
	fake, c, cred := signedInClient(t)
	ctx := context.Background()
	fake.HostLimit = 1
	fake.AddHost("acct-1", "another-computer-0001")
	_, err := c.Register(ctx, cred, "client-0123456789abcdef", "")
	var re *relay.Error
	if !errors.As(err, &re) || re.Status != http.StatusForbidden || re.Code != relay.CodeHostLimitReached || re.Limit != 1 || relay.Retryable(err) {
		t.Fatalf("host limit: %#v", err)
	}
	fake.FailRetryAfter("POST /v1/hosts", 409, "busy", 3)
	_, err = c.Register(ctx, cred, "client-0123456789abcdef", "")
	if !errors.As(err, &re) || re.Code != relay.CodeBusy || re.RetryAfter != 3*time.Second || !relay.Retryable(err) {
		t.Fatalf("busy: %#v", err)
	}
	fake.Fail("POST /v1/hosts", 502, "upstream_error")
	if _, err = c.Register(ctx, cred, "client-0123456789abcdef", ""); !relay.Retryable(err) {
		t.Fatalf("upstream error is retryable: %v", err)
	}
	if _, err = c.Register(ctx, "lnh_unknown", "client-0123456789abcdef", ""); relay.Code(err) != relay.CodeUnauthorized || relay.Retryable(err) {
		t.Fatalf("unknown credential: %v", err)
	}
	fake.Close()
	_, err = c.Register(ctx, cred, "client-0123456789abcdef", "")
	if !errors.As(err, &re) || re.Status != 0 || !relay.Retryable(err) {
		t.Fatalf("relay down: %#v", err)
	}
}

func TestAReplayedCodeIsRefusedAndRevokesTheFirstCredential(t *testing.T) {
	e := newAccount(t)
	in, _ := e.acc.StartSignIn()
	back, _ := e.fake.Approve(in.URL, "acct-1")
	browse(t, back)
	cred := e.acc.Credential()
	// The fake relay follows the contract: the same code a second time fails and revokes what the first use issued.
	_, err := e.acc.Client.ExchangeCode(context.Background(), queryOf(t, back, "code"), "a-verifier-that-does-not-matter-here-000000", strings.SplitN(back, "?", 2)[0])
	if relay.Code(err) != relay.CodeInvalidGrant || !e.fake.Revoked(cred) {
		t.Fatalf("replay: %v, revoked %v", err, e.fake.Revoked(cred))
	}
}

func queryOf(t *testing.T, raw, key string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get(key)
}
