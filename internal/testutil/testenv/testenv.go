// Package testenv 为测试读取本机开发配置：环境变量优先，未设置时读取仓库根目录的 .env.local。
package testenv

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	once   sync.Once
	values map[string]string
)

// Get 返回环境变量的值；进程环境中没有时，再查 .env.local。
func Get(name string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	once.Do(load)
	return values[name]
}

func load() {
	values = map[string]string{}
	root := RepoRoot()
	if root == "" {
		return
	}
	f, err := os.Open(filepath.Join(root, ".env.local"))
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			values[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
}

// RepoRoot 从当前目录向上查找 go.mod 所在目录。
func RepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
