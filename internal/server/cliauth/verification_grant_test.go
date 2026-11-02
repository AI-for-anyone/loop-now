package cliauth

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/cliverify"
	"loop-now/internal/server/store"
	"strings"
	"testing"
	"time"
)

func managementOperation(t *testing.T, f googleFixture, account, version uuid.UUID) clicredential.VerificationGrantBinding {
	t.Helper()
	id := uuid.New()
	_, _, e := f.st.BeginCLIVerification(context.Background(), f.project, account, cliverify.Start{OperationID: id, ExpectedVersionID: version}, store.VerificationMetadata{TemplateSource: "builtin", CredentialFormat: "1", TemplateFingerprint: strings.Repeat("a", 64), AdapterRevision: "fixture", ProbeKind: "google_crm_read"}, "console", nil, time.Now().Add(time.Minute))
	if e != nil {
		t.Fatal(e)
	}
	return clicredential.VerificationGrantBinding{ProjectID: f.project, AccountID: account, VersionID: version, OperationID: id}
}

type managementCommitFault struct {
	RefreshStore
	ManagementRefreshStore
	after bool
}

func (s managementCommitFault) CommitGoogleCLIVerificationRefresh(ctx context.Context, b clicredential.VerificationGrantBinding, g int64, lease uuid.UUID, secret []byte) error {
	if s.after {
		if e := s.ManagementRefreshStore.CommitGoogleCLIVerificationRefresh(ctx, b, g, lease, secret); e != nil {
			return e
		}
	}
	return errors.New("synthetic commit acknowledgement unavailable")
}
