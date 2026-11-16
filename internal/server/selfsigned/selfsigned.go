// Package selfsigned maintains the self-signed TLS certificate for the external port: cert.pem and key.pem in a
// directory, regenerated when missing, unreadable, expired or not covering the required hosts.
// The certificate is also its own CA, so loopctl can verify it by using cert.pem as --ca-file. The desktop shell uses it
// to open the external port (LOOP_NOW_EXTERNAL_TLS_SELF_SIGNED).
package selfsigned

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	CertFile = "cert.pem"
	KeyFile  = "key.pem"
	// Valid for ten years: only non-browser clients such as loopctl verify it, so browser limits on certificate
	// lifetime do not apply.
	validity = 10 * 365 * 24 * time.Hour
)

// Load returns the usable certificate in the directory and the path of cert.pem, generating it first when needed
// (generated is then true).
// hosts are the host names or IPs the certificate must cover; loopback addresses are always included, and empty
// strings and wildcard addresses are ignored.
func Load(dir string, hosts []string, now time.Time) (cert tls.Certificate, certPath string, generated bool, err error) {
	certPath, keyPath := filepath.Join(dir, CertFile), filepath.Join(dir, KeyFile)
	want := wanted(hosts)
	// A certificate that cannot be read (missing, corrupt, not a matching pair) or is not usable is regenerated and
	// overwritten.
	if existing, loadErr := tls.LoadX509KeyPair(certPath, keyPath); loadErr == nil {
		if leaf, parseErr := x509.ParseCertificate(existing.Certificate[0]); parseErr == nil && usable(leaf, want, now) {
			existing.Leaf = leaf
			return existing, certPath, false, nil
		}
	}
	if err = generate(dir, certPath, keyPath, want, now); err != nil {
		return tls.Certificate{}, "", false, err
	}
	cert, err = tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, "", false, fmt.Errorf("读取刚生成的证书失败：%w", err)
	}
	return cert, certPath, true, nil
}

// wanted normalizes and deduplicates the hosts to cover: the loopback addresses are always among them.
func wanted(hosts []string) []string {
	out := []string{"localhost", "127.0.0.1", "::1"}
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(h), "["), "]"))
		if h == "" {
			continue
		}
		if ip := net.ParseIP(h); ip != nil && ip.IsUnspecified() {
			continue
		}
		if !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	return out
}

// usable reports whether the existing certificate can still be used: not expired (with a 30-day margin) and covering
// all required hosts.
func usable(leaf *x509.Certificate, want []string, now time.Time) bool {
	if now.Before(leaf.NotBefore) || now.Add(30*24*time.Hour).After(leaf.NotAfter) {
		return false
	}
	for _, h := range want {
		if leaf.VerifyHostname(h) != nil {
			return false
		}
	}
	return true
}

func generate(dir, certPath, keyPath string, hosts []string, now time.Time) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Loop Now external port"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(validity),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	// Write the private key first, then the certificate, each as a temp file that is renamed into place, so neither
	// file is ever half-written. The two renames are separate, though: a reader between them sees the new key with the
	// old certificate. Load treats a mismatched pair as unreadable and regenerates it.
	if err := writeAtomic(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return err
	}
	return writeAtomic(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), mode)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}
