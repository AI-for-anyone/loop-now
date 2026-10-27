package source

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"loop-now/internal/server/clitemplate"
)

func fixture(t *testing.T) (string, *Reader) {
	t.Helper()
	home := t.TempDir()
	r, e := New(home, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { r.Close() })
	return home, r
}
func put(t *testing.T, home, name, value string) {
	t.Helper()
	p := filepath.Join(home, name)
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(value), 0600); e != nil {
		t.Fatal(e)
	}
}
func candidate(t *testing.T, r *Reader, cli string) Candidate {
	t.Helper()
	all, e := r.Discover(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range all {
		if c.CLI == cli {
			return c
		}
	}
	t.Fatal("candidate absent")
	return Candidate{}
}
func selection(t *testing.T, r *Reader, c Candidate) Selection {
	t.Helper()
	s, e := r.Select(c.ID)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func expectCode(t *testing.T, e error, code string) {
	t.Helper()
	f, ok := e.(Failure)
	if !ok || f.Code != code {
		t.Fatalf("expected safe code %s (error withheld)", code)
	}
}
func TestPGFirstMatchAndEscapes(t *testing.T) {
	for _, tc := range []struct {
		raw    string
		target []string
		want   string
	}{
		{"*:5432:*:user:first\nhost:5432:db:user:later\n", []string{"host", "5432", "db", "user"}, "first"},
		{"h\\:ost:5432:d\\:b:u\\\\ser:p\\:a\\\\ss\n", []string{"h:ost", "5432", "d:b", "u\\ser"}, "p:a\\ss"},
		{"\\*:5432:db:user:literal\n*:5432:db:user:wild\n", []string{"host", "5432", "db", "user"}, "wild"},
	} {
		got, e := pgPassword([]byte(tc.raw), tc.target)
		if e != nil || got != tc.want {
			t.Fatal("official first match/escape mismatch")
		}
	}
	_, e := pgPassword([]byte("*:*:*:*:secret"), []string{"host", "5432", "", "user"})
	expectCode(t, e, "missing_field")
}
func TestPGSelectedExtractionAndSupplements(t *testing.T) {
	home, r := fixture(t)
	secret := " SELECTED:a\\b "
	other := "NONSELECTED_PASSWORD_771"
	put(t, home, ".pg_service.conf", "[alpha]\nhost=offline.invalid\nport=5432\nuser=worker\nsslmode=disable\n[beta]\nhost=other.invalid\nport=5432\nuser=other\ndbname=other\nsslmode=disable\npassword="+other+"\n")
	put(t, home, ".pgpass", "offline.invalid:5432:chosen:worker: SELECTED\\:a\\\\b \n*:*:*:*:wrong_later_password\n")
	c := candidate(t, r, "psql")
	if c.Reason != "" || strings.Join(c.Missing, ",") != "database" {
		t.Fatal("missing database projection wrong")
	}
	encoded, _ := json.Marshal(c)
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), home) || strings.Contains(fmt.Sprintf("%+v", r), other) {
		t.Fatal("discovery retained/exposed a secret or path")
	}
	s := selection(t, r, c)
	called := 0
	var retained map[string]string
	e := r.Extract(context.Background(), s, Supplement{Database: "chosen"}, func(f map[string]string) error {
		called++
		if f["password"] != secret || f["database"] != "chosen" || f["sslMode"] != "disable" {
			t.Fatal("selected full mapping wrong")
		}
		retained = f
		return nil
	})
	if e != nil || called != 1 || len(retained) != 0 {
		t.Fatal("private callback/cleanup failed")
	}
	e = r.Extract(context.Background(), s, Supplement{Database: "chosen", SSLMode: "disable"}, func(map[string]string) error { t.Fatal("existing field overwritten"); return nil })
	expectCode(t, e, "supplement_override")
	// Unselected inline secrets were not decoded into a private credential map.
	raw, _ := os.ReadFile(filepath.Join(home, ".pg_service.conf"))
	g, e := parseINI(raw, false, true, "alpha")
	clear(raw)
	if e != nil || g["beta"].values["password"] != "" || !g["beta"].secrets["password"] {
		t.Fatal("unselected secret decoded")
	}
}
func TestSourceBindingPermissionsAndNoFollow(t *testing.T) {
	home, r := fixture(t)
	put(t, home, ".pg_service.conf", "[alpha]\nhost=offline.invalid\nport=5432\nuser=worker\ndbname=db\nsslmode=disable\n")
	put(t, home, ".pgpass", "*:*:*:*:SYNTHETIC_PASSWORD\n")
	c := candidate(t, r, "psql")
	s := selection(t, r, c)
	_, other := fixture(t)
	expectCode(t, other.Recheck(context.Background(), s), "candidate_not_found")
	if _, e := r.Select(filepath.Join(home, ".pgpass")); e == nil {
		t.Fatal("accepted caller path")
	}
	put(t, home, ".pgpass", "*:*:*:*:CHANGED_PASSWORD\n")
	expectCode(t, r.Recheck(context.Background(), s), "source_changed")
	s = selection(t, r, candidate(t, r, "psql"))
	if e := os.Chmod(filepath.Join(home, ".pgpass"), 0644); e != nil {
		t.Fatal(e)
	}
	// Rediscover binds the changed mode; extraction still enforces libpq privacy.
	s = selection(t, r, candidate(t, r, "psql"))
	e := r.Extract(context.Background(), s, Supplement{}, func(map[string]string) error { t.Fatal("insecure pgpass consumed"); return nil })
	expectCode(t, e, "source_unreadable")
	outside := t.TempDir()
	put(t, outside, "outside-pass", "*:*:*:*:ESCAPE_SECRET\n")
	os.Remove(filepath.Join(home, ".pgpass"))
	if e := os.Symlink(filepath.Join(outside, "outside-pass"), filepath.Join(home, ".pgpass")); e != nil {
		t.Fatal(e)
	}
	c = candidate(t, r, "psql")
	if c.Reason == "" {
		t.Fatal("escape symlink allowed")
	}
}
func TestUnknownDynamicAndIncompleteSources(t *testing.T) {
	for _, tc := range []struct{ cli, file, body string }{
		{"psql", ".pg_service.conf", "[x]\nhost=a,b\nport=5432\nuser=u\nsslmode=disable\n"},
		{"psql", ".pg_service.conf", "[x]\nhost=/tmp/socket\nport=5432\nuser=u\nsslmode=disable\n"},
		{"psql", ".pg_service.conf", "[x]\nhost=h\nport=5432\nuser=u\nsslmode=prefer\n"},
		{"psql", ".pg_service.conf", "[x]\nhost=h\nport=5432\nuser=u\ncredential_process=do-not-execute\n"},
		{"mysql", ".my.cnf", "!include /arbitrary/secret\n[client]\nhost=h\n"},
		{"mysql", ".my.cnf", "[client]\nhost=h\nport=3306\nuser=u\npassword=p\nssl-mode=PREFERRED\n"},
		{"mysql", ".my.cnf", "[client]\nhost=h\nport=3306\nuser=u\npassword=p\nssl-mode=DISABLED\ninit-command=BUSINESS_WRITE\n"},
	} {
		t.Run(tc.cli+fmt.Sprint(len(tc.body)), func(t *testing.T) {
			home, r := fixture(t)
			put(t, home, tc.file, tc.body)
			put(t, home, ".pgpass", "*:*:*:*:dummy")
			c := candidate(t, r, tc.cli)
			if c.Reason == "" {
				t.Fatal("unmappable source allowed")
			}
			if _, e := r.Select(c.ID); e == nil {
				t.Fatal("unsupported selection allowed")
			}
		})
	}
}
func TestMySQLOptionFileMappingAndSafeErrors(t *testing.T) {
	home, r := fixture(t)
	put(t, home, ".my.cnf", "[client]\nhost=offline.invalid\nport=3306\nuser=worker\npassword=\" a:b\\\\c\\\"# \"\n[mysql]\ndatabase=business\nssl-mode=DISABLED\n")
	c := candidate(t, r, "mysql")
	if c.Reason != "" {
		t.Fatal("static native config rejected")
	}
	s := selection(t, r, c)
	e := r.Extract(context.Background(), s, Supplement{}, func(f map[string]string) error {
		if f["password"] != " a:b\\c\"# " || f["database"] != "business" {
			t.Fatal("native quoted config lost data")
		}
		return fmt.Errorf("SYNTHETIC_CALLBACK_SECRET")
	})
	if e == nil || strings.Contains(e.Error(), "SYNTHETIC_CALLBACK_SECRET") {
		t.Fatal("callback diagnostics leaked")
	}
}
func TestTemplateFingerprintAndConcurrentReadback(t *testing.T) {
	home := t.TempDir()
	cat, e := clitemplate.Load("")
	if e != nil {
		t.Fatal(e)
	}
	r, e := New(home, cat)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	put(t, home, ".pg_service.conf", "[x]\nhost=h\nport=5432\nuser=u\ndbname=db\nsslmode=disable\npassword=synthetic\n")
	c := candidate(t, r, "psql")
	s := selection(t, r, c)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := r.Recheck(context.Background(), s); e != nil {
				t.Error("concurrent source check failed")
			}
		}()
	}
	wg.Wait()
	tmpl, _ := cat.Get("psql")
	tmpl.Credential.FormatVersion = "changed"
	expectCode(t, r.Recheck(context.Background(), s), "template_mismatch")
}
func syntheticTLS(t *testing.T) (string, string) {
	t.Helper()
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic"}, NotBefore: time.Unix(0, 0), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, e := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	pk, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}))
}
func TestTLSDependenciesFrozenAndComplete(t *testing.T) {
	home, r := fixture(t)
	cert, key := syntheticTLS(t)
	put(t, home, "ca.pem", cert)
	put(t, home, "client.pem", cert)
	put(t, home, "key.pem", key)
	put(t, home, ".pg_service.conf", "[x]\nhost=offline.invalid\nport=5432\nuser=u\ndbname=db\nsslmode=verify-full\npassword=synthetic\nsslrootcert="+filepath.Join(home, "ca.pem")+"\nsslcert="+filepath.Join(home, "client.pem")+"\nsslkey="+filepath.Join(home, "key.pem")+"\n")
	s := selection(t, r, candidate(t, r, "psql"))
	e := r.Extract(context.Background(), s, Supplement{}, func(f map[string]string) error {
		if f["caCert"] != cert || f["clientCert"] != cert || f["clientKey"] != key {
			t.Fatal("TLS mapping incomplete")
		}
		return nil
	})
	if e != nil {
		t.Fatal("complete TLS mapping rejected")
	}
	put(t, home, "key.pem", key+"\n")
	expectCode(t, r.Recheck(context.Background(), s), "source_changed")
}
func TestGoogleCompleteSAAndPersonalSeparation(t *testing.T) {
	home, r := fixture(t)
	_, key := syntheticTLS(t)
	sa := map[string]string{"type": "service_account", "project_id": "synthetic-project", "private_key_id": "synthetic-key", "private_key": key, "client_email": "robot@synthetic-project.iam.gserviceaccount.com", "client_id": "123456", "token_uri": "https://oauth2.googleapis.com/token"}
	raw, e := json.Marshal(sa)
	if e != nil {
		t.Fatal(e)
	}
	name := ".config/gcloud/application_default_credentials.json"
	put(t, home, name, string(raw))
	clear(raw)
	c := candidate(t, r, "gcloud")
	if c.Reason != "" {
		t.Fatal("full SA candidate unsupported")
	}
	s := selection(t, r, c)
	e = r.Extract(context.Background(), s, Supplement{}, func(f map[string]string) error {
		if !strings.Contains(f["serviceAccountJson"], "service_account") {
			t.Fatal("SA mapping absent")
		}
		return nil
	})
	if e != nil {
		t.Fatal("formal SA template rejected synthetic key")
	}
	put(t, home, name, `{"type":"authorized_user","refresh_token":"DO_NOT_COPY_PERSONAL"}`)
	c = candidate(t, r, "gcloud")
	if c.Reason != "requires_platform_login" {
		t.Fatal("personal refresh import allowed")
	}
	encoded, _ := json.Marshal(c)
	if strings.Contains(string(encoded), "DO_NOT_COPY_PERSONAL") {
		t.Fatal("personal secret leaked")
	}
	for _, kind := range []string{"external_account", "impersonated_service_account", "UNKNOWN_SECRET_LABEL"} {
		put(t, home, name, `{"type":"`+kind+`","credential_source":{"executable":{"command":"DO_NOT_EXECUTE"}}}`)
		c = candidate(t, r, "gcloud")
		if c.Reason != "unsupported" || strings.Contains(c.Label, kind) {
			t.Fatal("dynamic or unknown identity exposed/accepted")
		}
	}
}

func TestPrivateOriginStableAndBound(t *testing.T) {
	home, r := fixture(t)
	put(t, home, ".pg_service.conf", "[x]\nhost=h\nport=5432\nuser=u\ndbname=db\nsslmode=disable\npassword=synthetic\n")
	s := selection(t, r, candidate(t, r, "psql"))
	var first []byte
	if e := s.WithOrigin(func(b []byte) error { first = append([]byte{}, b...); return nil }); e != nil {
		t.Fatal(e)
	}
	defer clear(first)
	var retained []byte
	if e := s.WithOrigin(func(b []byte) error {
		if string(b) != string(first) {
			t.Fatal("origin not stable")
		}
		retained = b
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	for _, b := range retained {
		if b != 0 {
			t.Fatal("origin callback buffer not cleared")
		}
	}
	put(t, home, ".pg_service.conf", "[x]\nhost=h\nport=5432\nuser=u\ndbname=db\nsslmode=disable\npassword=changed\n")
	next := selection(t, r, candidate(t, r, "psql"))
	if e := next.WithOrigin(func(b []byte) error {
		if string(b) != string(first) {
			t.Fatal("origin incorrectly includes source version")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(first), home) || strings.Contains(string(first), "synthetic") {
		t.Fatal("origin contains path/secret instead of root identity")
	}
	expectCode(t, (Selection{}).WithOrigin(func([]byte) error { return nil }), "candidate_not_found")
}
func TestMissingTLSAndPublicCASupplement(t *testing.T) {
	home, r := fixture(t)
	cert, _ := syntheticTLS(t)
	put(t, home, ".my.cnf", "[client]\nhost=offline.invalid\nport=3306\nuser=u\npassword=synthetic\ndatabase=db\n")
	s := selection(t, r, candidate(t, r, "mysql"))
	called := 0
	e := r.Extract(context.Background(), s, Supplement{SSLMode: "VERIFY_IDENTITY", CACert: cert}, func(f map[string]string) error {
		called++
		if f["caCert"] != cert || f["sslMode"] != "VERIFY_IDENTITY" {
			t.Fatal("public supplement not mapped")
		}
		return nil
	})
	if e != nil || called != 1 {
		t.Fatal("valid public missing supplement rejected")
	}
	e = r.Extract(context.Background(), s, Supplement{SSLMode: "VERIFY_IDENTITY", CACert: "invalid CA"}, func(map[string]string) error { t.Fatal("invalid CA consumed"); return nil })
	expectCode(t, e, "invalid_credential")
}
func TestBadSAAndEncryptedClientKeyRejected(t *testing.T) {
	home, r := fixture(t)
	put(t, home, ".config/gcloud/application_default_credentials.json", `{"type":"service_account","project_id":"synthetic","private_key_id":"id","private_key":"INVALID_PRIVATE_KEY","client_email":"robot@synthetic.iam.gserviceaccount.com","client_id":"123","token_uri":"https://oauth2.googleapis.com/token"}`)
	s := selection(t, r, candidate(t, r, "gcloud"))
	e := r.Extract(context.Background(), s, Supplement{}, func(map[string]string) error { t.Fatal("invalid key consumed"); return nil })
	expectCode(t, e, "invalid_credential")
	cert, _ := syntheticTLS(t)
	put(t, home, "cert.pem", cert)
	put(t, home, "key.pem", "-----BEGIN ENCRYPTED PRIVATE KEY-----\nYWJj\n-----END ENCRYPTED PRIVATE KEY-----\n")
	put(t, home, ".pg_service.conf", "[x]\nhost=h\nport=5432\nuser=u\ndbname=db\nsslmode=require\npassword=synthetic\nsslcert="+filepath.Join(home, "cert.pem")+"\nsslkey="+filepath.Join(home, "key.pem")+"\n")
	s = selection(t, r, candidate(t, r, "psql"))
	e = r.Extract(context.Background(), s, Supplement{}, func(map[string]string) error { t.Fatal("encrypted key silently downgraded"); return nil })
	expectCode(t, e, "invalid_credential")
}
func TestPostExtractionRecheckAndCtimeBinding(t *testing.T) {
	home, r := fixture(t)
	body := "[x]\nhost=h\nport=5432\nuser=u\ndbname=db\nsslmode=disable\npassword=synthetic\n"
	put(t, home, ".pg_service.conf", body)
	s := selection(t, r, candidate(t, r, "psql"))
	info, e := os.Stat(filepath.Join(home, ".pg_service.conf"))
	if e != nil {
		t.Fatal(e)
	}
	// Linux stamps ctime from the coarse clock (one tick is several ms); rewrite
	// after a tick so only ctime, not timing luck, tells the two writes apart.
	time.Sleep(50 * time.Millisecond)
	put(t, home, ".pg_service.conf", body)
	if e = os.Chtimes(filepath.Join(home, ".pg_service.conf"), info.ModTime(), info.ModTime()); e != nil {
		t.Fatal(e)
	}
	expectCode(t, r.Recheck(context.Background(), s), "source_changed")
	s = selection(t, r, candidate(t, r, "psql"))
	e = r.Extract(context.Background(), s, Supplement{}, func(map[string]string) error {
		put(t, home, ".pg_service.conf", body+"# changed after extraction\n")
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	// Server MUST call Recheck before commit; extraction cannot lock host writes.
	expectCode(t, r.Recheck(context.Background(), s), "source_changed")
}
func TestMaskedOutputAndBoundedCapture(t *testing.T) {
	if !maskedLoginOutput([]byte("[x]\npassword = *****\n")) || maskedLoginOutput([]byte("[x]\npassword = RAW_SYNTHETIC_SECRET\n")) {
		t.Fatal("masked native shape not enforced")
	}
	var b privateBuffer
	if _, e := b.Write([]byte("PRIVATE_SENTINEL")); e != nil {
		t.Fatal(e)
	}
	defer clear(b.Bytes())
	if strings.Contains(fmt.Sprintf("%+v", &b), "PRIVATE_SENTINEL") {
		t.Fatal("private capture formatter leaked")
	}
	if _, e := b.Write(make([]byte, maxFile)); e == nil {
		t.Fatal("native output not bounded")
	}
}
func TestGoogleStoresAndToolUnavailableAreExplicit(t *testing.T) {
	home, r := fixture(t)
	put(t, home, ".config/gcloud/credentials.db", "not-a-real-store")
	put(t, home, ".config/gcloud/access_tokens.db", "not-a-real-cache")
	put(t, home, ".mylogin.cnf", "not-a-real-login-file")
	all, e := r.Discover(context.Background())
	if e != nil || len(all) != 3 {
		t.Fatal("unsupported sources omitted")
	}
	reasons := map[string]bool{}
	for _, c := range all {
		reasons[c.Reason] = true
		if _, e := r.Select(c.ID); e == nil {
			t.Fatal("unsupported source selected")
		}
	}
	for _, code := range []string{"unsupported_cli_store", "cached_token_not_credential", "tool_unavailable"} {
		if !reasons[code] {
			t.Fatal("source state not distinguished")
		}
	}
}

func TestSectionCannotBecomeNativeFlag(t *testing.T) {
	if safeName("--help") || safeName("--debug") || safeName("../escape") || safeName("bad\nname") {
		t.Fatal("unsafe section accepted")
	}
	if _, e := parseINI([]byte("[--help]\nhost=offline.invalid\n"), true, false); e == nil {
		t.Fatal("native argument injection source accepted")
	}
}
