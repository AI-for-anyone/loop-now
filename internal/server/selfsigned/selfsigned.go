// Package selfsigned 维护对外端口的自签 TLS 证书：目录里的 cert.pem 与 key.pem，没有、读不出、已过期或没覆盖所需主机时重新生成。
// 证书同时是自己的 CA，loopctl 把 cert.pem 当 --ca-file 即可校验。桌面壳用它开对外端口（LOOP_NOW_EXTERNAL_TLS_SELF_SIGNED）。
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
	// 有效期十年：只有 loopctl 这类非浏览器客户端校验它，不受浏览器对证书寿命的限制。
	validity = 10 * 365 * 24 * time.Hour
)

// Load 返回目录里可用的证书与 cert.pem 的路径，必要时先生成（generated 为 true）。
// hosts 是证书要覆盖的主机名或 IP，回环地址总是包含；空串与通配地址忽略。
func Load(dir string, hosts []string, now time.Time) (cert tls.Certificate, certPath string, generated bool, err error) {
	certPath, keyPath := filepath.Join(dir, CertFile), filepath.Join(dir, KeyFile)
	want := wanted(hosts)
	// 读不出（没有、损坏、不成对）或不能用的证书都重新生成、覆盖掉。
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

// wanted 规范化并去重要覆盖的主机：回环地址总在其中。
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

// usable 判断现有证书还能不能用：未过期（留 30 天余量）且覆盖全部所需主机。
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
	// 先写私钥再写证书，都是临时文件改名：读的一方要么看到成对的新文件，要么看到旧的。
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
