package cliimport

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/cliimport/source"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/store"
)

type testHasher struct{ key, id string }

func (h testHasher) KeyID() string { return h.id }
func (h testHasher) mac(parts ...string) []byte {
	m := hmac.New(sha256.New, []byte(h.key))
	for _, p := range parts {
		b, _ := json.Marshal(p)
		m.Write(b)
	}
	return m.Sum(nil)
}
func (h testHasher) Origin(project uuid.UUID, b []byte) ([]byte, error) {
	return h.mac("origin", project.String(), string(b)), nil
}
func (h testHasher) Content(project uuid.UUID, cli, format string, b []byte) ([]byte, error) {
	return h.mac("content", project.String(), cli, format, string(b)), nil
}

type controlledReader struct {
	Reader
	extractions *atomic.Int64
	// prepared counts consumer callbacks that produced a credential; failAt
	// fails the consumer right after its nth credential was prepared.
	prepared, failAt *atomic.Int64
	closed           *atomic.Int64
	entered          chan struct{}
	gate             <-chan struct{}
	after            func()
}

func (r *controlledReader) Extract(ctx context.Context, sel source.Selection, sup source.Supplement, f func(map[string]string) error) error {
	r.extractions.Add(1)
	if r.entered != nil {
		select {
		case r.entered <- struct{}{}:
		default:
		}
	}
	if r.gate != nil {
		select {
		case <-r.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	err := r.Reader.Extract(ctx, sel, sup, func(fields map[string]string) error {
		if e := f(fields); e != nil {
			return e
		}
		if n := r.prepared.Add(1); r.failAt.Load() == n {
			return errors.New("synthetic credential preparation failure")
		}
		return nil
	})
	if r.after != nil {
		r.after()
	}
	return err
}
func (r *controlledReader) Close() error { r.closed.Add(1); return r.Reader.Close() }

type importEnv struct {
	st                                 *store.Store
	templates                          *clitemplate.Catalog
	home                               string
	project                            uuid.UUID
	extracts, closes, prepared, failAt atomic.Int64
}

func (e *importEnv) files(t *testing.T, two bool) {
	t.Helper()
	config := "[alpha]\nhost=127.0.0.1\nport=5432\nuser=userA\ndbname=dbA\nsslmode=disable\n"
	pass := "127.0.0.1:5432:dbA:userA:synthetic-password-A\n"
	if two {
		config += "[beta]\nhost=127.0.0.1\nport=5432\nuser=userB\ndbname=dbB\nsslmode=disable\n"
		pass += "127.0.0.1:5432:dbB:userB:synthetic-password-B\n"
	}
	for n, c := range map[string]string{".pg_service.conf": config, ".pgpass": pass} {
		if err := os.WriteFile(filepath.Join(e.home, n), []byte(c), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
func (e *importEnv) service(t *testing.T, o Options, configure func(*controlledReader)) *Service {
	t.Helper()
	if o.InstanceID == uuid.Nil {
		o.InstanceID = uuid.New()
	}
	if o.Hasher == nil {
		o.Hasher = testHasher{"synthetic-hmac", "key-one"}
	}
	o.ReaderFactory = func(ctx context.Context) (Reader, error) {
		r, err := source.New(e.home, e.templates)
		if err != nil {
			return nil, err
		}
		wrapped := &controlledReader{Reader: r, extractions: &e.extracts, prepared: &e.prepared, failAt: &e.failAt, closed: &e.closes}
		if configure != nil {
			configure(wrapped)
		}
		return wrapped, nil
	}
	s, err := NewService(e.st, e.templates, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), settle)
		defer cancel()
		if err := s.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return s
}
func selected(scan Scan) StartInput {
	in := StartInput{OperationID: uuid.New(), ScanID: scan.ScanID, ExpectedCredentialsRevision: scan.CredentialsRevision}
	for _, c := range scan.Candidates {
		if c.Status == "may_import" {
			in.CandidateIDs = append(in.CandidateIDs, c.CandidateID)
		}
	}
	return in
}

// settle bounds waits for work the test has already set going. Cancelled work
// may still spend two RecordTimeouts (5s each) on DB writes, so the bound only
// turns a real hang into a failure; it is never what makes a test pass.
const settle = 30 * time.Second

// waitEntered waits for the controlled Reader to be entered, failing instead
// of blocking until the package test timeout when it never is.
func waitEntered(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(settle):
		t.Fatal("controlled Reader was not entered")
	}
}
func awaitImport(t *testing.T, s *Service, p, id uuid.UUID) Operation {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(settle)
	for time.Now().Before(deadline) {
		op, err := s.Get(ctx, p, id)
		if err != nil {
			t.Fatal(err)
		}
		if op.Terminal() {
			return op
		}
		time.Sleep(time.Millisecond * 5)
	}
	t.Fatal("operation did not finish")
	return Operation{}
}
func (e *importEnv) counts(t *testing.T) (int, int) {
	t.Helper()
	var a, r int
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM project_credential_accounts WHERE project_id=$1),(SELECT count(*) FROM cli_credential_import_receipts WHERE project_id=$1)`, e.project).Scan(&a, &r); err != nil {
		t.Fatal(err)
	}
	return a, r
}

func TestCLIImportInputAndNaming(t *testing.T) {
	id := uuid.NewString()
	good := `{"operationId":"` + id + `","scanId":"` + uuid.NewString() + `","candidateIds":["` + uuid.NewString() + `"],"expectedCredentialsRevision":1}`
	var in StartInput
	if err := json.Unmarshal([]byte(good), &in); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(good, id, uuid.Nil.String(), 1), strings.Replace(good, `"candidateIds":[`, `"candidateIds":null,"unused":[`, 1), strings.Replace(good, `"expectedCredentialsRevision":1`, `"expectedCredentialsRevision":1,"expectedCredentialsRevision":2`, 1), strings.Replace(good, `"expectedCredentialsRevision":1`, `"expectedCredentialsRevision":1,"password":"secret"`, 1)} {
		if json.Unmarshal([]byte(bad), &in) == nil {
			t.Fatal("unsafe JSON accepted")
		}
	}
	for _, c := range []Candidate{{CLI: "psql", SourceKind: "pg_service", Label: strings.Repeat("界", 300)}, {CLI: "mysql", SourceKind: "mysql_login_path", Label: "bad\nlabel"}, {CLI: "gh", SourceKind: "gh_hosts_plaintext", Label: ""}} {
		n := accountName(c)
		if len([]rune(n)) > 200 || strings.TrimSpace(n) != n || strings.Contains(n, "\n") {
			t.Fatal("invalid generated name")
		}
	}
}

type publicationGateReader struct {
	Reader
	ready, cancelled chan struct{}
	release          <-chan struct{}
}

func (r *publicationGateReader) Discover(ctx context.Context) ([]source.Candidate, error) {
	items, err := r.Reader.Discover(ctx)
	if err != nil {
		return nil, err
	}
	close(r.ready)
	// Freeze after actual successful discovery, before parent Scan publication.
	<-ctx.Done()
	close(r.cancelled)
	<-r.release
	return items, nil
}
