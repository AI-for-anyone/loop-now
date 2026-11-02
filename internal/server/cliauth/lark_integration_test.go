package cliauth

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/clitemplate"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/larkauth"
	"loop-now/internal/server/store"
	"testing"
	"time"
)

type larkLoginFunc func(context.Context, larkauth.Application, func(larkauth.Authorization) error) (clicredential.Lark, error)

func (f larkLoginFunc) Login(ctx context.Context, a larkauth.Application, on func(larkauth.Authorization) error) (clicredential.Lark, error) {
	return f(ctx, a, on)
}
func larkCredential(a larkauth.Application) clicredential.Lark {
	return clicredential.Lark{Type: "lark_user", AppSecret: a.AppSecret, AccessToken: "synthetic-lark-access", RefreshToken: "synthetic-lark-refresh", ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), RefreshExpiresAt: time.Now().Add(24 * time.Hour).UnixMilli(), Identity: cm.LarkIdentity{Brand: a.Brand, AppID: a.AppID, OpenID: "ou-fixture", DisplayName: "已核实用户", VerifiedAt: time.Now()}, RuntimeVariantVersion: clicredential.LarkUserVariant}
}
func larkSource(t *testing.T, f googleFixture) store.ProjectCredentialRecord {
	t.Helper()
	catalog, e := clitemplate.Load("")
	if e != nil {
		t.Fatal(e)
	}
	template, _ := catalog.Get("lark-cli")
	payload, fields, e := template.Render(map[string]string{"appId": "app-fixture", "appSecret": "fixture-app-secret", "brand": "feishu"})
	if e != nil {
		t.Fatal(e)
	}
	plain, e := json.Marshal(payload)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(plain)
	r, e := f.st.CreateProjectCredential(context.Background(), f.project, "cli", "lark-cli", "原应用", store.ProjectCredentialWrite{TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1", FieldNames: fields, EnvNames: payload.EnvNames(), FilePaths: payload.FilePaths()}, Secret: plain})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func larkStart(source store.ProjectCredentialRecord) cm.CLIAuthStart {
	id := source.View.ID
	rev := source.View.Revision
	return cm.CLIAuthStart{OperationID: uuid.New(), CLI: "lark-cli", AuthType: "lark_user", SourceAccountID: &id, ExpectedSourceRevision: &rev}
}
func waitLark(t *testing.T, s *Service, project, id uuid.UUID, status string) cm.CLIAuthState {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		v, e := s.Get(context.Background(), project, id)
		if e != nil {
			t.Fatal(e)
		}
		if v.Operation.Status == status {
			return v
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("Lark terminal deadline")
	return cm.CLIAuthState{}
}
