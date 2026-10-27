package source

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
)

func TestSupplementableFieldsIndependentOfRequiredMissing(t *testing.T) {
	cert, _ := syntheticTLS(t)
	for _, cli := range []string{"psql", "mysql"} {
		t.Run(cli, func(t *testing.T) {
			for _, tc := range []struct {
				name             string
				mode             string
				hasCA            bool
				missing, allowed []string
			}{
				{"tls_unselected", "", false, []string{"sslMode"}, []string{"sslMode", "caCert"}},
				{"verify_missing_ca", "verify", false, []string{"caCert"}, []string{"caCert"}},
				{"verify_existing_ca", "verify", true, []string{}, []string{}},
				{"tls_unselected_existing_ca", "", true, []string{"sslMode"}, []string{"sslMode"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					home, r := fixture(t)
					put(t, home, "ca.pem", cert)
					file := ".pg_service.conf"
					body := "[x]\nhost=offline.invalid\nport=5432\nuser=u\ndbname=db\npassword=synthetic\n"
					tlsKey, caKey, verify := "sslmode", "sslrootcert", "verify-full"
					if cli == "mysql" {
						file = ".my.cnf"
						body = "[client]\nhost=offline.invalid\nport=3306\nuser=u\ndatabase=db\npassword=synthetic\n"
						tlsKey, caKey, verify = "ssl-mode", "ssl-ca", "VERIFY_IDENTITY"
					}
					if tc.mode != "" {
						body += tlsKey + "=" + verify + "\n"
					}
					if tc.hasCA {
						body += caKey + "=" + filepath.Join(home, "ca.pem") + "\n"
					}
					put(t, home, file, body)
					c := candidate(t, r, cli)
					if c.Reason != "" || !slices.Equal(c.Missing, tc.missing) || !slices.Equal(c.SupplementableFields, tc.allowed) {
						t.Fatal("required vs supplementable projection mismatch")
					}
					s := selection(t, r, c)
					sup := Supplement{}
					if tc.mode == "" {
						sup.SSLMode = verify
					}
					if !tc.hasCA {
						sup.CACert = cert
					}
					if e := r.Extract(context.Background(), s, sup, func(f map[string]string) error {
						if f["sslMode"] != verify || f["caCert"] != cert {
							t.Fatal("supplemented verification TLS mapping lost CA")
						}
						return nil
					}); e != nil {
						t.Fatal("allowed missing nonsecret combination rejected", e)
					}
					if tc.hasCA {
						bad := sup
						bad.CACert = cert
						e := r.Extract(context.Background(), s, bad, func(map[string]string) error { t.Fatal("existing CA overwritten"); return nil })
						expectCode(t, e, "supplement_override")
					}
					if tc.mode != "" {
						bad := sup
						bad.SSLMode = verify
						e := r.Extract(context.Background(), s, bad, func(map[string]string) error { t.Fatal("existing TLS overwritten"); return nil })
						expectCode(t, e, "supplement_override")
					}
					if len(c.SupplementableFields) > 0 {
						c.SupplementableFields[0] = "password"
						if slices.Contains(s.entry.candidate.SupplementableFields, "password") {
							t.Fatal("ordinary projection mutated private candidate policy")
						}
					}
				})
			}
		})
	}
}
func TestSupplementableFieldsDoNotGrantSecretsOrSkipFinalTLSValidation(t *testing.T) {
	cert, _ := syntheticTLS(t)
	home, r := fixture(t)
	put(t, home, ".my.cnf", "[client]\nhost=offline.invalid\nport=3306\nuser=u\npassword=synthetic\ndatabase=db\nssl-mode=DISABLED\n")
	c := candidate(t, r, "mysql")
	if len(c.Missing) != 0 || !slices.Equal(c.SupplementableFields, []string{"caCert"}) {
		t.Fatal("source-absent availability confused with required fields")
	}
	e := r.Extract(context.Background(), selection(t, r, c), Supplement{CACert: cert}, func(map[string]string) error { t.Fatal("invalid TLS/CA combination bypassed renderer"); return nil })
	expectCode(t, e, "invalid_credential")
	put(t, home, ".my.cnf", "[client]\nhost=offline.invalid\nport=3306\nuser=u\n")
	c = candidate(t, r, "mysql")
	if c.Reason != "credential_unavailable" || len(c.SupplementableFields) != 0 {
		t.Fatal("missing secret incorrectly enabled supplements")
	}
	put(t, home, ghHostsFile, "github.com:\n  user: alice\n  oauth_token: synthetic\n")
	c = candidate(t, r, "gh")
	if len(c.SupplementableFields) != 0 {
		t.Fatal("non-DB gained supplementation")
	}
}
