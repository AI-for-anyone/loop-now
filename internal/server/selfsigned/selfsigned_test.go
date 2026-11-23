package selfsigned

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestLoad: the first call generates a certificate and the second reuses it; it is replaced when expired or missing a
// host; the generated certificate passes Go client verification as its own CA.
func TestLoad(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	now := time.Now()
	cert, certPath, generated, err := Load(dir, []string{"127.0.0.1"}, now)
	if err != nil || !generated || certPath != filepath.Join(dir, CertFile) {
		t.Fatalf("first call should generate: generated=%v path=%s err=%v", generated, certPath, err)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(filepath.Join(dir, KeyFile)); info.Mode().Perm() != 0o600 {
			t.Fatalf("private key mode should be 0600, got %o", info.Mode().Perm())
		}
	}
	again, _, generated, err := Load(dir, []string{"127.0.0.1"}, now)
	if err != nil || generated {
		t.Fatalf("second call should reuse: generated=%v err=%v", generated, err)
	}
	if string(again.Certificate[0]) != string(cert.Certificate[0]) {
		t.Fatal("second call should read back the same certificate")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"localhost", "127.0.0.1", "::1"} {
		if err := leaf.VerifyHostname(h); err != nil {
			t.Fatalf("certificate should cover %s: %v", h, err)
		}
	}

	// A new host is required: replaced.
	_, _, generated, err = Load(dir, []string{"192.168.1.9", "my-mac.local"}, now)
	if err != nil || !generated {
		t.Fatalf("should regenerate when a host is missing: generated=%v err=%v", generated, err)
	}
	// Close to expiry: replaced.
	_, _, generated, err = Load(dir, nil, now.Add(validity-24*time.Hour))
	if err != nil || !generated {
		t.Fatalf("should regenerate when close to expiry: generated=%v err=%v", generated, err)
	}
	// Corrupt file: replaced.
	if err := os.WriteFile(filepath.Join(dir, KeyFile), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	cert, certPath, generated, err = Load(dir, nil, now)
	if err != nil || !generated {
		t.Fatalf("should regenerate when the private key is corrupt: generated=%v err=%v", generated, err)
	}

	// A client using cert.pem as its CA can connect to a TLS port served with this certificate.
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	defer srv.Close()
	pemBytes, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pemBytes) {
		t.Fatal("cert.pem should be addable to a trust pool")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}}
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	for _, host := range []string{"127.0.0.1", "localhost"} {
		resp, err := client.Get("https://" + net.JoinHostPort(host, port) + "/")
		if err != nil {
			t.Fatalf("connecting as %s should pass certificate verification: %v", host, err)
		}
		resp.Body.Close()
	}
	if _, err := (&http.Client{}).Get(srv.URL); err == nil {
		t.Fatal("a client without the CA should not trust the self-signed certificate")
	}
}

func TestWanted(t *testing.T) {
	got := wanted([]string{"", "0.0.0.0", "::", "[::1]", "LocalHost", "10.0.0.2", "10.0.0.2"})
	want := []string{"localhost", "127.0.0.1", "::1", "10.0.0.2"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
