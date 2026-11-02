package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/clitemplate"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/store"
)

func githubWrite(e projectMigrationEnv, user, login, value string, scopes []string) store.ProjectCredentialWrite {
	identity := &cm.GitHubIdentity{Host: "github.com", UserID: user, Login: login, GrantedScopes: scopes, VerifiedAt: time.Now().UTC()}
	plain, _ := json.Marshal(clitemplate.Payload{Env: map[string]string{"GH_TOKEN": value}, Files: []clitemplate.PayloadFile{}})
	return store.ProjectCredentialWrite{AuthMethod: "oauth", AccountLabel: &login, GitHubIdentity: identity, TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1", FieldNames: []string{"token"}, EnvNames: []string{"GH_TOKEN"}}, Secret: plain}
}

func githubOperation(t *testing.T, e projectMigrationEnv, target *cm.Account, expires time.Time) cm.CLIAuthOperation {
	t.Helper()
	in := cm.CLIAuthStart{OperationID: uuid.New(), CLI: "gh", AuthType: "github_user"}
	if target != nil {
		in.AccountID, in.ExpectedRevision = &target.ID, &target.Revision
	}
	op, created, err := e.s.NewProjectCLIAuthOperation(context.Background(), e.project, in, expires)
	if err != nil || !created {
		t.Fatal("operation create failed", err)
	}
	return op
}
