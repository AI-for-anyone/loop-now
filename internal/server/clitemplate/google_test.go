package clitemplate

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
)

func TestGoogleServiceAccountStrictRendering(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	base := map[string]any{"type": "service_account", "project_id": "synthetic-project", "private_key_id": "synthetic-id", "private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), "client_email": "fixture@synthetic-project.iam.gserviceaccount.com", "client_id": "synthetic-client", "token_uri": "https://oauth2.googleapis.com/token"}
	encode := func(c map[string]any) string {
		b, e := json.Marshal(c)
		if e != nil {
			t.Fatal(e)
		}
		return string(b)
	}
	fields := map[string]string{"serviceAccountJson": encode(base)}
	files, err := renderGoogleSA(fields)
	if err != nil || len(files) != 1 || files[0].Path != ".loop-now/gcloud/service-account.json" {
		t.Fatal("valid SA rendering rejected")
	}
	// A service-account project identifies the key; it is not an implicit target
	// project. Only the independently recorded defaultProject creates that file.
	fields["defaultProject"] = "explicit-target"
	files, err = renderGoogleSA(fields)
	if err != nil || len(files) != 2 || string(files[1].Content) != "explicit-target" {
		t.Fatal("explicit project lost")
	}
	for _, c := range []struct {
		name, field string
		value       any
	}{
		{"wrong-type", "type", "authorized_user"}, {"null-key", "private_key", nil}, {"wrong-key-type", "private_key", 42},
		{"malformed-key", "private_key", "synthetic-invalid-key"}, {"host-token", "token_uri", "http://127.0.0.1/token"},
		{"issuer", "auth_uri", "https://example.invalid/auth"}, {"cert-source", "auth_provider_x509_cert_url", "https://example.invalid/certs"},
		{"external-account", "credential_source", map[string]any{"file": "/host/auth"}}, {"scope-authority", "universe_domain", "example.invalid"},
		{"email", "client_email", "fixture@example.invalid"}, {"empty-project", "project_id", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			bad := map[string]any{}
			for k, v := range base {
				bad[k] = v
			}
			bad[c.field] = c.value
			if _, e := renderGoogleSA(map[string]string{"serviceAccountJson": encode(bad)}); e == nil || strings.Contains(e.Error(), "synthetic-invalid-key") || strings.Contains(e.Error(), "/host/auth") {
				t.Fatal("unsafe SA accepted or secret reflected")
			}
		})
	}
	for _, project := range []string{"../host", "a\nb", " a", "a b"} {
		if _, e := renderGoogleSA(map[string]string{"serviceAccountJson": encode(base), "defaultProject": project}); e == nil {
			t.Fatal("unsafe target project accepted")
		}
	}
}
