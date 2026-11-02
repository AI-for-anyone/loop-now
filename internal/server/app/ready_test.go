package app_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"loop-now/internal/server/app"
	"loop-now/internal/server/config"
)

// 就绪文件整份写出（目录不存在时建好），再写一次是整体替换；目录里不留临时文件。
func TestWriteReadyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shell", "ready.json")
	for _, url := range []string{"http://127.0.0.1:1", "http://127.0.0.1:2"} {
		if err := app.WriteReadyFile(path, app.Ready{URL: url, PID: 42, Version: "v"}); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		var r app.Ready
		if err != nil || json.Unmarshal(b, &r) != nil || r != (app.Ready{URL: url, PID: 42, Version: "v"}) {
			t.Fatalf("就绪文件内容不对：%s %v", b, err)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("留下了临时文件：%v", entries)
	}
}

// 控制台目录必须有 index.html；没有配置目录、二进制也没有内嵌时没有控制台。
func TestConsoleFiles(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := app.ConsoleFiles(dir); err == nil || !strings.Contains(err.Error(), config.EnvWebDir) {
		t.Fatalf("没有 index.html 的目录应拒绝：%v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if fsys, source, err := app.ConsoleFiles(dir); err != nil || fsys == nil || source != dir {
		t.Fatalf("%v %q %v", fsys, source, err)
	}
	if fsys, _, err := app.ConsoleFiles(""); err != nil || fsys != nil {
		t.Fatalf("不带 embedweb 构建时不应有控制台：%v %v", fsys, err)
	}
}

// 真实的服务端：就绪文件里的地址就是控制台端口，那里同时提供控制台与 API；停止后就绪文件被删除。
func TestConsoleAndReadyFileOnRealServer(t *testing.T) {
	w := newWorld(t)
	web := t.TempDir()
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("<title>loop now console</title>"), 0o600); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(t.TempDir(), "ready.json")
	s := w.start(func(c *config.Config) { c.WebDir, c.ReadyFile = web, ready })
	var r app.Ready
	for i := 0; i < 200; i++ {
		if b, err := os.ReadFile(ready); err == nil && json.Unmarshal(b, &r) == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if r.URL != "http://"+s.a.Addr() || r.PID != os.Getpid() {
		t.Fatalf("就绪文件不对：%+v", r)
	}
	for path, want := range map[string]string{"/": "loop now console", "/agents/x": "loop now console", "/api/v1/context": `"project"`} {
		resp, err := http.Get(r.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(string(b), want) {
			t.Errorf("%s：%d %.80s", path, resp.StatusCode, b)
		}
	}
	s.stop()
	if _, err := os.Stat(ready); !os.IsNotExist(err) {
		t.Fatalf("停止后就绪文件应删除：%v", err)
	}
}
