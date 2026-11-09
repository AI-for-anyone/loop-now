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

// 桌面版的开法：不给证书与私钥，只给自签证书目录（LOOP_NOW_EXTERNAL_TLS_SELF_SIGNED），服务端自己生成并在就绪文件里
// 写出对外端口的 https 地址；外部身份用目录里的 cert.pem 当 --ca-file 就能经 TLS 对外端口用 loopctl。
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

	// 就绪文件由 Serve 写出，等它出现。
	var ready app.Ready
	deadline := time.Now().Add(10 * time.Second)
	for {
		if b, err := os.ReadFile(readyFile); err == nil {
			if err := json.Unmarshal(b, &ready); err != nil {
				t.Fatalf("就绪文件不是 JSON：%v", err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("服务端没有写出就绪文件")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if ready.ExternalURL != s.ext {
		t.Fatalf("就绪文件里的对外地址 %q 应为 %q", ready.ExternalURL, s.ext)
	}
	caFile := filepath.Join(tlsDir, selfsigned.CertFile)
	if _, err := os.Stat(caFile); err != nil {
		t.Fatalf("服务端应在目录里生成证书：%v", err)
	}

	_, _, secret := s.issueToken("", "desktop-user", nil)
	ext := newLoopctl(t, nil)
	ext.stdin = secret + "\n"
	// config set 不连服务端，没有 serverVersion，只看退出码。
	if r := ext.run("config", "set", "default", "--server", ready.ExternalURL, "--ca-file", caFile, "--token-stdin"); r.Code != 0 {
		t.Fatalf("%s", r.Stdout)
	}
	ext.stdin = ""
	if d := ext.run("whoami").ok(t); d["kind"] != "external" || d["address"] != "@desktop-user" {
		t.Fatalf("%v", d)
	}
	// 不带 CA 时校验失败，而不是被放行。
	bare := newLoopctl(t, map[string]string{"LOOPCTL_URL": ready.ExternalURL, "LOOPCTL_TOKEN": secret})
	bare.run("whoami").fails(t, "tls_verify_failed", 3)

	// 重启沿用同一张证书：loopctl 的配置不用改。
	s.stop()
	s = w.start(func(c *config.Config) {
		c.ExternalTLSCert, c.ExternalTLSKey = "", ""
		c.ExternalTLSSelfSigned = tlsDir
	})
	if d := ext.run("whoami").ok(t); d["address"] != "@desktop-user" {
		t.Fatalf("%v", d)
	}
}
