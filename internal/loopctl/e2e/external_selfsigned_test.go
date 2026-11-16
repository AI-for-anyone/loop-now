package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"loop-now/internal/server/app"
	"loop-now/internal/server/config"
	"loop-now/internal/server/selfsigned"
)

// The desktop setup: no certificate or private key is given, only a self-signed certificate directory
// (LOOP_NOW_EXTERNAL_TLS_SELF_SIGNED); the server generates them itself and writes the https address of the
// external port into the ready file; an external identity can then use loopctl through the TLS external port with
// the cert.pem in that directory as --ca-file.
func TestExternalIdentitySelfSigned(t *testing.T) {
	w := newWorld(t, true)
	tlsDir := filepath.Join(t.TempDir(), "tls")
	readyFile := filepath.Join(t.TempDir(), "run", "server-ready.json")
	s := w.start(func(c *config.Config) {
		c.ExternalTLSCert, c.ExternalTLSKey = "", ""
		c.ExternalTLSSelfSigned = tlsDir
		c.ReadyFile = readyFile
	})
	defer s.stop()

	// Serve writes the ready file; wait for it to appear.
	var ready app.Ready
	deadline := time.Now().Add(10 * time.Second)
	for {
		if b, err := os.ReadFile(readyFile); err == nil {
			if err := json.Unmarshal(b, &ready); err != nil {
				t.Fatalf("ready file is not JSON: %v", err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server did not write the ready file")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if ready.ExternalURL != s.ext {
		t.Fatalf("external address in the ready file is %q, want %q", ready.ExternalURL, s.ext)
	}
	caFile := filepath.Join(tlsDir, selfsigned.CertFile)
	if _, err := os.Stat(caFile); err != nil {
		t.Fatalf("server should generate a certificate in the directory: %v", err)
	}

	_, _, secret := s.issueToken("", "desktop-user", nil)
	ext := newLoopctl(t, nil)
	ext.stdin = secret + "\n"
	// config set does not connect to the server and has no serverVersion, so only check the exit code.
	if r := ext.run("config", "set", "default", "--server", ready.ExternalURL, "--ca-file", caFile, "--token-stdin"); r.Code != 0 {
		t.Fatalf("%s", r.Stdout)
	}
	ext.stdin = ""
	if d := ext.run("whoami").ok(t); d["kind"] != "external" || d["address"] != "@desktop-user" {
		t.Fatalf("%v", d)
	}
	// Without the CA, verification fails instead of being let through.
	bare := newLoopctl(t, map[string]string{"LOOPCTL_URL": ready.ExternalURL, "LOOPCTL_TOKEN": secret})
	bare.run("whoami").fails(t, "tls_verify_failed", 3)

	// A restart keeps the same certificate, so loopctl's config does not need to change.
	s.stop()
	s = w.start(func(c *config.Config) {
		c.ExternalTLSCert, c.ExternalTLSKey = "", ""
		c.ExternalTLSSelfSigned = tlsDir
	})
	if d := ext.run("whoami").ok(t); d["address"] != "@desktop-user" {
		t.Fatalf("%v", d)
	}
}
