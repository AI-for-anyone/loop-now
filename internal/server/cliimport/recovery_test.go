package cliimport

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"loop-now/internal/server/cliimport/source"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/store"
)

func TestCLIImportLarkCanonicalDedup(t *testing.T) {
	e := newImportEnv(t)
	ctx := context.Background()
	dir := filepath.Join(e.home, ".lark-cli")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	body := `{"strictMode":"off","currentApp":"synthetic","apps":[{"name":"synthetic","appId":"cli_synthetic","appSecret":"synthetic-lark-secret","brand":"feishu","defaultAs":"bot","users":[]}]}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	s := e.service(t, Options{}, nil)
	scan, err := s.Scan(ctx, e.project)
	if err != nil {
		t.Fatal(err)
	}
	first := selected(scan)
	if len(first.CandidateIDs) != 1 {
		t.Fatal("real Lark source not available")
	}
	if _, err = s.Start(ctx, e.project, first); err != nil {
		t.Fatal(err)
	}
	saved := awaitImport(t, s, e.project, first.OperationID)
	if saved.Status != "succeeded" {
		t.Fatal("first Lark save", saved.ErrorCode)
	}
	row, _, err := e.st.GetProjectCLIVersion(ctx, *saved.Items[0].AccountID, *saved.Items[0].VersionID)
	if err != nil {
		t.Fatal(err)
	}
	original := append([]byte(nil), row.Secret...)
	var stored clitemplate.Payload
	if err = json.Unmarshal(row.Secret, &stored); err != nil {
		t.Fatal(err)
	}
	tLark, _ := e.templates.Get("lark-cli")
	fresh, _, err := tLark.Render(map[string]string{"appId": "cli_synthetic", "appSecret": "synthetic-lark-secret", "brand": "feishu"})
	if err != nil {
		t.Fatal(err)
	}
	key := func(p clitemplate.Payload) []byte {
		for _, f := range p.Files {
			if strings.HasSuffix(f.Path, "master.key.file") {
				return f.Content
			}
		}
		return nil
	}
	if len(key(stored)) != 32 || len(key(fresh)) != 32 || bytes.Equal(key(stored), key(fresh)) {
		t.Fatal("fixture failed to exercise random Lark renderer material")
	}
	scan2, err := s.Scan(ctx, e.project)
	if err != nil {
		t.Fatal(err)
	}
	second := selected(scan2)
	if _, err = s.Start(ctx, e.project, second); err != nil {
		t.Fatal(err)
	}
	again := awaitImport(t, s, e.project, second.OperationID)
	if again.Status != "succeeded" || again.Items[0].Status != "already_imported" || *again.Items[0].VersionID != *saved.Items[0].VersionID || *again.CredentialsRevision != *saved.CredentialsRevision {
		t.Fatal("same Lark logical credential drifted across scans")
	}
	current, _, err := e.st.GetProjectCLIVersion(ctx, *saved.Items[0].AccountID, *saved.Items[0].VersionID)
	if err != nil || !bytes.Equal(original, current.Secret) {
		t.Fatal("already-imported rewrote original payload")
	}
	if a, r := e.counts(t); a != 1 || r != 1 {
		t.Fatal("Lark duplicate account")
	}
	for _, f := range fresh.Files {
		clear(f.Content)
	}
	for _, f := range stored.Files {
		clear(f.Content)
	}
}

// This separately tests the unresolved-pending branch against real PG; the
// actual acknowledgement-loss tests prove COMMIT transport ambiguity itself.
func TestCLIImportUnknownPendingDeadlineReconciliation(t *testing.T) {
	e := newImportEnv(t)
	ctx := context.Background()
	s := e.service(t, Options{}, nil)
	deadline := time.Now().Add(100 * time.Millisecond)
	h := sha256.Sum256([]byte("pending-outcome"))
	candidate := uuid.New()
	id := uuid.New()
	in := store.CLIImportBegin{ID: id, ProjectID: e.project, ScanID: uuid.New(), InstanceID: s.o.InstanceID, RequestFingerprint: h[:], ExpectedRevision: 1, ProvenanceKeyID: s.o.Hasher.KeyID(), Deadline: deadline, Items: []store.CLIImportItem{{CandidateID: candidate, CLI: "gh", Label: "合成未决"}}}
	if _, _, err := e.st.BeginCLIImport(ctx, in); err != nil {
		t.Fatal(err)
	}
	if early, err := e.st.ExpireCLIImport(ctx, e.project, id); err != nil || early.Status != "pending" {
		t.Fatal("expiry CAS ignored DB deadline", err)
	}
	work, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	done := make(chan Operation, 1)
	go func() {
		op, err := s.resolveUnknown(work, e.project, id)
		if err != nil {
			t.Error(err)
		}
		done <- op
	}()
	for n := 0; n < 2; n++ {
		op, err := s.Get(ctx, e.project, id)
		if err != nil || op.Status != "pending" {
			t.Fatal("GET changed pending outcome", err)
		}
	}
	select {
	case op := <-done:
		if op.Status != "expired" || *op.ErrorCode != "timeout" {
			t.Fatal("pending unknown did not expire")
		}
	case <-time.After(time.Second):
		t.Fatal("unknown reconciliation hung")
	}
	if e.extracts.Load() != 0 || e.prepared.Load() != 0 {
		t.Fatal("unknown reconciliation replayed source work")
	}
}

func TestCLIImportPublicCAOnlyBeforeExtraction(t *testing.T) {
	e := newImportEnv(t)
	e.files(t, false)
	ctx := context.Background()
	raw, err := os.ReadFile(filepath.Join(e.home, ".pg_service.conf"))
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.ReplaceAll(raw, []byte("sslmode=disable\n"), nil)
	if err = os.WriteFile(filepath.Join(e.home, ".pg_service.conf"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	private := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("synthetic-private-block")}))
	s := e.service(t, Options{}, nil)
	scan, err := s.Scan(ctx, e.project)
	if err != nil {
		t.Fatal(err)
	}
	var candidate Candidate
	for _, v := range scan.Candidates {
		if v.CLI == "psql" {
			candidate = v
		}
	}
	if candidate.CandidateID == uuid.Nil {
		t.Fatal("PG missing-TLS candidate absent")
	}
	tls := "verify-full"
	badFirst := "-----BEGIN CERTIFICATE-----\n!!!invalid-base64!!!\n-----END CERTIFICATE-----\n" + ca
	nested := "-----BEGIN CERTIFICATE-----\n" + ca + "-----END CERTIFICATE-----\n"
	for _, bad := range []string{ca + private, private, ca + "trailing garbage", "prefix garbage" + ca, badFirst, nested} {
		in := StartInput{OperationID: uuid.New(), ScanID: scan.ScanID, CandidateIDs: []uuid.UUID{candidate.CandidateID}, ExpectedCredentialsRevision: scan.CredentialsRevision, Supplements: []Supplement{{CandidateID: candidate.CandidateID, SSLMode: &tls, CACert: &bad}}}
		if _, err = s.Start(ctx, e.project, in); err == nil {
			t.Fatal("secret/non-CA supplement accepted")
		}
	}
	if e.extracts.Load() != 0 || e.prepared.Load() != 0 {
		t.Fatal("invalid CA touched secrets")
	}
	if a, r := e.counts(t); a != 0 || r != 0 {
		t.Fatal("invalid CA saved")
	}
	good := StartInput{OperationID: uuid.New(), ScanID: scan.ScanID, CandidateIDs: []uuid.UUID{candidate.CandidateID}, ExpectedCredentialsRevision: scan.CredentialsRevision, Supplements: []Supplement{{CandidateID: candidate.CandidateID, SSLMode: &tls, CACert: &ca}}}
	if _, err = s.Start(ctx, e.project, good); err != nil {
		t.Fatal(err)
	}
	if op := awaitImport(t, s, e.project, good.OperationID); op.Status != "succeeded" {
		t.Fatal("allowed absent CA/TLS did not validate")
	}
	view, err := safeCandidate(source.Candidate{ID: uuid.NewString(), CLI: "gh", SourceKind: "gh_hosts_plaintext", Label: "safe", Reason: "keyring_version_unbound"})
	if err != nil || view.ReasonCode == nil || *view.ReasonCode != "keyring_version_unavailable" {
		t.Fatal("keyring reason lost")
	}
}

type serviceAckConn struct {
	net.Conn
	arm      *atomic.Bool
	drop     atomic.Bool
	observed *atomic.Bool
	deny     *atomic.Bool
}

func (c *serviceAckConn) Write(b []byte) (int, error) {
	if c.deny != nil && c.deny.Load() {
		c.Close()
		return 0, errors.New("synthetic TEST connection outage")
	}
	if len(b) > 5 && b[0] == 'Q' && strings.EqualFold(strings.TrimSpace(string(bytes.TrimRight(b[5:], "\x00"))), "commit") && c.arm.CompareAndSwap(true, false) {
		c.drop.Store(true)
	}
	return c.Conn.Write(b)
}

func TestCLIImportUnknownExpiryRetriesAfterDBOutage(t *testing.T) {
	e := newImportEnv(t)
	e.files(t, false)
	ctx := context.Background()
	arm, observed, deny := &atomic.Bool{}, &atomic.Bool{}, &atomic.Bool{}
	defer deny.Store(false)
	original := e.st.Pool
	config := original.Config().Copy()
	if config.ConnConfig.TLSConfig != nil {
		t.Skip("ack fixture requires local TEST non-TLS socket")
	}
	config.ConnConfig.Config.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &serviceAckConn{Conn: c, arm: arm, observed: observed, deny: deny}, nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	e.st.Pool = pool
	t.Cleanup(original.Close)
	s := e.service(t, Options{OperationTimeout: 150 * time.Millisecond, RecordTimeout: 30 * time.Millisecond}, nil)
	scan, err := s.Scan(ctx, e.project)
	if err != nil {
		t.Fatal(err)
	}
	in := selected(scan)
	arm.Store(true)
	if op, err := s.Start(ctx, e.project, in); err != nil || op.Status != "pending" || !observed.Load() {
		t.Fatal("unknown acceptance setup failed", err)
	}
	deny.Store(true)
	// Force the original deadline writeback to fail, then verify it has actually
	// released its job. Only the independent DB expiry reconciler can close it.
	ended := false
	limit := time.Now().Add(2 * time.Second)
	for time.Now().Before(limit) {
		s.mu.Lock()
		_, exists := s.jobs[in.OperationID]
		s.mu.Unlock()
		if !exists {
			ended = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !ended {
		t.Fatal("original deadline owner did not end boundedly")
	}
	deny.Store(false)
	op := awaitImport(t, s, e.project, in.OperationID)
	if op.Status != "expired" || *op.ErrorCode != "timeout" || e.extracts.Load() != 0 || e.prepared.Load() != 0 {
		t.Fatal("DB recovery did not expire without secret replay")
	}
	closeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err = s.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	done := s.maintenanceDone
	s.mu.Unlock()
	select {
	case <-done:
	default:
		t.Fatal("expiry reconciliation survived Close")
	}
	t.Log("actual acceptance COMMIT ack dropped; first deadline DB write failed; DB-only maintenance retried after restore; Close stopped it")
}
func (c *serviceAckConn) Read(b []byte) (int, error) {
	if !c.drop.Swap(false) {
		return c.Conn.Read(b)
	}
	for {
		head := make([]byte, 5)
		if _, err := io.ReadFull(c.Conn, head); err != nil {
			return 0, err
		}
		n := int(binary.BigEndian.Uint32(head[1:])) - 4
		if n < 0 || n > 1<<20 {
			return 0, io.ErrUnexpectedEOF
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(c.Conn, body); err != nil {
			return 0, err
		}
		if head[0] == 'C' && string(body) == "COMMIT\x00" {
			c.observed.Store(true)
			c.Close()
			return 0, io.EOF
		}
		if head[0] == 'E' {
			return 0, io.ErrUnexpectedEOF
		}
	}
}
func TestCLIImportBeginUnknownExpiresWithoutExtraction(t *testing.T) {
	e := newImportEnv(t)
	e.files(t, false)
	ctx := context.Background()
	arm, observed := &atomic.Bool{}, &atomic.Bool{}
	original := e.st.Pool
	config := original.Config().Copy()
	if config.ConnConfig.TLSConfig != nil {
		t.Skip("ACK fault fixture requires exclusively local TEST non-TLS socket")
	}
	config.ConnConfig.Config.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &serviceAckConn{Conn: c, arm: arm, observed: observed}, nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	e.st.Pool = pool
	t.Cleanup(original.Close)
	s := e.service(t, Options{OperationTimeout: 300 * time.Millisecond}, nil)
	scan, err := s.Scan(ctx, e.project)
	if err != nil {
		t.Fatal(err)
	}
	in := selected(scan)
	arm.Store(true)
	op, err := s.Start(ctx, e.project, in)
	if err != nil || op.Status != "pending" || !observed.Load() {
		t.Fatal("unknown acceptance was not read back", err)
	}
	// GETs remain side-effect-free. The original background owner only expires
	// under DB locks at deadline; it never launches secret extraction.
	expired := awaitImport(t, s, e.project, in.OperationID)
	if expired.Status != "expired" || *expired.ErrorCode != "timeout" || e.extracts.Load() != 0 || e.prepared.Load() != 0 {
		t.Fatal("unknown acceptance replayed or remained active")
	}
	if a, r := e.counts(t); a != 0 || r != 0 {
		t.Fatal("unknown acceptance created credentials")
	}
	if again, err := s.Start(ctx, e.project, in); err != nil || again.Status != "expired" {
		t.Fatal("expired unknown operation was replayed", err)
	}
	t.Log("actual Begin COMMIT acknowledgement lost; independent GET pending; no extraction; deadline CAS expired")
}
