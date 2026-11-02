package cliauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/store"
)

type googleFixture struct {
	st             *store.Store
	project, agent uuid.UUID
}

func syntheticGoogle() clicredential.Google {
	return clicredential.Google{Type: "authorized_user", Account: "synthetic@example.invalid", ClientID: "fixture-client", ClientSecret: "fixture-secret", RefreshToken: "fixture-refresh", AccessToken: "fixture-access", ExpiresAt: time.Now().Add(-time.Hour).UnixMilli(), Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"}}
}
func (f googleFixture) write(t *testing.T, c clicredential.Google) store.ProjectCredentialWrite {
	t.Helper()
	plain, e := c.Encode()
	if e != nil {
		t.Fatal(e)
	}
	label := c.Account
	return store.ProjectCredentialWrite{AuthMethod: "oauth", AccountLabel: &label, TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1"}, Secret: plain}
}

type googleRefreshFunc func(context.Context, clicredential.Google) (clicredential.Google, error)

func (fn googleRefreshFunc) Refresh(ctx context.Context, c clicredential.Google) (clicredential.Google, error) {
	return fn(ctx, c)
}

type googleLoginFunc func(context.Context, uuid.UUID, func(string) error) (clicredential.Google, error)

func (fn googleLoginFunc) Login(ctx context.Context, id uuid.UUID, url func(string) error) (clicredential.Google, error) {
	return fn(ctx, id, url)
}

func ptrWrite(w store.ProjectCredentialWrite) *store.ProjectCredentialWrite { return &w }

type commitFaultStore struct {
	RefreshStore
	after bool
}

func (s commitFaultStore) CommitGoogleCLIRefresh(ctx context.Context, b clicredential.Binding, g int64, lease uuid.UUID, secret []byte) error {
	if s.after {
		if e := s.RefreshStore.CommitGoogleCLIRefresh(ctx, b, g, lease, secret); e != nil {
			return e
		}
	}
	return errors.New("synthetic commit acknowledgement unavailable")
}
