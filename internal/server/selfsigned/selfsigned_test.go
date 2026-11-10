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

// 第一次生成，第二次沿用同一张；过期或缺少主机时换新；生成的证书能作为自己的 CA 通过 Go 客户端校验。
func TestLoad(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	now := time.Now()
	cert, certPath, generated, err := Load(dir, []string{"127.0.0.1"}, now)
	if err != nil || !generated || certPath != filepath.Join(dir, CertFile) {
		t.Fatalf("首次应生成：generated=%v path=%s err=%v", generated, certPath, err)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(filepath.Join(dir, KeyFile)); info.Mode().Perm() != 0o600 {
			t.Fatalf("私钥权限应为 0600，得到 %o", info.Mode().Perm())
		}
	}
	again, _, generated, err := Load(dir, []string{"127.0.0.1"}, now)
	if err != nil || generated {
		t.Fatalf("第二次应沿用：generated=%v err=%v", generated, err)
	}
	if string(again.Certificate[0]) != string(cert.Certificate[0]) {
		t.Fatal("第二次应读回同一张证书")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"localhost", "127.0.0.1", "::1"} {
		if err := leaf.VerifyHostname(h); err != nil {
			t.Fatalf("证书应覆盖 %s：%v", h, err)
		}
	}

	// 要求新的主机：换新。
	_, _, generated, err = Load(dir, []string{"192.168.1.9", "my-mac.local"}, now)
	if err != nil || !generated {
		t.Fatalf("缺少主机时应重新生成：generated=%v err=%v", generated, err)
	}
	// 临近过期：换新。
	_, _, generated, err = Load(dir, nil, now.Add(validity-24*time.Hour))
	if err != nil || !generated {
		t.Fatalf("临近过期时应重新生成：generated=%v err=%v", generated, err)
	}
	// 损坏的文件：换新。
	if err := os.WriteFile(filepath.Join(dir, KeyFile), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	cert, certPath, generated, err = Load(dir, nil, now)
	if err != nil || !generated {
		t.Fatalf("私钥损坏时应重新生成：generated=%v err=%v", generated, err)
	}

	// 用 cert.pem 当 CA 的客户端能连上以这张证书服务的 TLS 端口。
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
		t.Fatal("cert.pem 应能加入信任池")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}}
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	for _, host := range []string{"127.0.0.1", "localhost"} {
		resp, err := client.Get("https://" + net.JoinHostPort(host, port) + "/")
		if err != nil {
			t.Fatalf("以 %s 连接应通过证书校验：%v", host, err)
		}
		resp.Body.Close()
	}
	if _, err := (&http.Client{}).Get(srv.URL); err == nil {
		t.Fatal("不带 CA 的客户端不应信任自签证书")
	}
}

func TestWanted(t *testing.T) {
	got := wanted([]string{"", "0.0.0.0", "::", "[::1]", "LocalHost", "10.0.0.2", "10.0.0.2"})
	want := []string{"localhost", "127.0.0.1", "::1", "10.0.0.2"}
	if len(got) != len(want) {
		t.Fatalf("得到 %v，想要 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("得到 %v，想要 %v", got, want)
		}
	}
}
