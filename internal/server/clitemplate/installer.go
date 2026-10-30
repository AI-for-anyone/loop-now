package clitemplate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 安装状态，与 OpenAPI 的 CliTemplate.install.status 一致。
const (
	StateNotInstalled        = "not_installed"
	StateInstalling          = "installing"
	StateInstalled           = "installed"
	StateFailed              = "failed"
	StateUnsupportedPlatform = "unsupported_platform"
)

// maxBinarySize 防止异常安装包解出过大的文件。
const maxBinarySize = 1 << 30

// Installer 把模板规定的固定版本安装到 <工作根目录>/tools/<名称>/<版本>/bin。
type Installer struct {
	toolsDir string
	client   *http.Client
	timeout  time.Duration

	mu       sync.Mutex
	inflight map[string]*installCall
	failures map[string]string
	closed   bool
}

type installCall struct {
	done chan struct{}
	err  error
}

// NewInstaller 创建安装器。client 为 nil 时使用默认 HTTP 客户端。
func NewInstaller(workRoot string, timeout time.Duration, client *http.Client) *Installer {
	if client == nil {
		client = &http.Client{}
	}
	return &Installer{
		toolsDir: filepath.Join(workRoot, "tools"),
		client:   client,
		timeout:  timeout,
		inflight: map[string]*installCall{},
		failures: map[string]string{},
	}
}

func key(t *Template) string { return t.Name + "@" + t.Version }

// BinDir 返回模板版本的 bin 目录。
func (i *Installer) BinDir(t *Template) string {
	return filepath.Join(i.toolsDir, t.Name, t.Version, "bin")
}

func (i *Installer) installed(t *Template) bool {
	if a, ok := t.Asset(); ok {
		if a.Layout == "tree" {
			return i.treeInstalled(t, a)
		}
		if a.Layout == "google_sdk" {
			return i.googleInstalled(t, a)
		}
	}
	fi, err := os.Stat(filepath.Join(i.BinDir(t), t.Binary))
	return err == nil && fi.Mode().IsRegular()
}

// Status 返回安装状态与失败原因。
func (i *Installer) Status(t *Template) (string, string) {
	if _, ok := t.Asset(); !ok {
		return StateUnsupportedPlatform, "模板没有当前平台（" + Platform() + "）的安装包；环境里已有的同名命令不受影响，可直接使用"
	}
	if i.installed(t) {
		return StateInstalled, ""
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if _, ok := i.inflight[key(t)]; ok {
		return StateInstalling, ""
	}
	if msg, ok := i.failures[key(t)]; ok {
		return StateFailed, msg
	}
	return StateNotInstalled, ""
}

// Ensure 确保模板版本已安装，返回 bin 目录。同一版本并发调用只下载安装一次。
func (i *Installer) Ensure(ctx context.Context, t *Template) (string, error) {
	dir, _, err := i.EnsureReport(ctx, t)
	return dir, err
}

// EnsureReport 与 Ensure 相同，另外报告这次调用是否实际进行了安装尝试：
// 已安装或等待别人进行中的安装时为 false；下载安装（无论成败）或没有当前平台的安装包时为 true。
func (i *Installer) EnsureReport(ctx context.Context, t *Template) (dir string, attempted bool, err error) {
	asset, supported := t.Asset()
	k := key(t)
	i.mu.Lock()
	if i.closed {
		i.mu.Unlock()
		return "", false, errors.New("CLI安装器已停止")
	}
	if c, ok := i.inflight[k]; ok {
		i.mu.Unlock()
		select {
		case <-c.done:
			if c.err != nil {
				return "", false, c.err
			}
			return i.BinDir(t), false, nil
		case <-ctx.Done():
			return "", false, ctx.Err()
		}
	}
	c := &installCall{done: make(chan struct{})}
	i.inflight[k] = c
	i.mu.Unlock()

	// Register admission before even cached dependency verification, but do
	// not hold the mutex across that bounded subprocess/filesystem work.
	// Close(ctx) can therefore honor its own deadline while this call drains.
	attempted = !i.installed(t)
	if !supported {
		attempted = true
		c.err = fmt.Errorf("%s 模板没有当前平台（%s）的安装包；可直接使用环境中已安装的同名命令", t.Name, Platform())
	} else if attempted {
		installCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), i.timeout)
		c.err = i.install(installCtx, t, asset)
		cancel()
	}

	i.mu.Lock()
	delete(i.inflight, k)
	if c.err != nil {
		i.failures[k] = c.err.Error()
	} else {
		delete(i.failures, k)
	}
	i.mu.Unlock()
	close(c.done)
	if c.err != nil {
		return "", attempted, c.err
	}
	return i.BinDir(t), attempted, nil
}

// RestoreFailures 恢复上次运行记下的安装失败（键为 名称@版本），只对本机尚未安装、当前没有进行中安装的版本生效。
func (i *Installer) RestoreFailures(failures map[string]string, templates []*Template) {
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, t := range templates {
		k := key(t)
		msg, ok := failures[k]
		if !ok || i.installed(t) {
			continue
		}
		if _, busy := i.inflight[k]; busy {
			continue
		}
		if _, has := i.failures[k]; !has {
			i.failures[k] = msg
		}
	}
}

// Key 返回模板版本的键（名称@版本），与 RestoreFailures 的键一致。
func Key(t *Template) string { return key(t) }

func (i *Installer) install(ctx context.Context, t *Template, a Asset) error {
	tmpDir := filepath.Join(i.toolsDir, ".tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return err
	}
	archive, err := os.CreateTemp(tmpDir, t.Name+"-*.download")
	if err != nil {
		return err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()

	if err := download(ctx, i.client, a.URL, archive, a.SHA256); err != nil {
		return fmt.Errorf("下载 %s %s 失败：%w", t.Name, t.Version, err)
	}

	stage := filepath.Join(tmpDir, fmt.Sprintf("%s-%s-%s", t.Name, t.Version, randHex()))
	defer os.RemoveAll(stage)
	binPath := filepath.Join(stage, "bin", t.Binary)
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		return err
	}
	if a.Layout == "google_sdk" {
		err = i.installGoogle(ctx, t, a, archive.Name(), stage)
	} else if a.Layout == "tree" {
		err = i.installTree(ctx, t, a, archive.Name(), stage)
	} else {
		switch a.Format {
		case "zip":
			err = extractZip(archive.Name(), a.Path, binPath)
		case "tar.gz":
			err = extractTarGz(archive.Name(), a.Path, binPath)
		}
	}
	if err != nil {
		return fmt.Errorf("解压 %s 失败：%w", t.Name, err)
	}
	final := filepath.Join(i.toolsDir, t.Name, t.Version)
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	if err := os.Rename(stage, final); err != nil {
		if i.installed(t) {
			return nil // 已被其他调用装好
		}
		return err
	}
	return nil
}

func download(ctx context.Context, client *http.Client, url string, dst *os.File, wantSHA string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(dst, h), resp.Body); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSHA {
		return fmt.Errorf("摘要不符：期望 %s，实际 %s", wantSHA, got)
	}
	return nil
}

func normalize(name string) string { return strings.TrimPrefix(path.Clean(name), "./") }

func writeBinary(dst string, r io.Reader) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, maxBinarySize+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxBinarySize {
		err = errors.New("二进制文件过大")
	}
	return err
}

func extractZip(archive, want, dst string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if normalize(f.Name) != want {
			continue
		}
		if !f.Mode().IsRegular() {
			return fmt.Errorf("%s 不是普通文件", want)
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		return writeBinary(dst, rc)
	}
	return fmt.Errorf("安装包中找不到 %s", want)
}

func extractTarGz(archive, want, dst string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("安装包中找不到 %s", want)
		}
		if err != nil {
			return err
		}
		if normalize(h.Name) != want {
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return fmt.Errorf("%s 不是普通文件", want)
		}
		return writeBinary(dst, tr)
	}
}

func randHex() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Close closes admission under the same mutex as in-flight registration, then
// drains published calls. It does not cancel/delete an installation shared by
// consumers. A timeout is explicitly incomplete cleanup, never success.
func (i *Installer) Close(ctx context.Context) error {
	i.mu.Lock()
	i.closed = true
	calls := make([]*installCall, 0, len(i.inflight))
	for _, call := range i.inflight {
		calls = append(calls, call)
	}
	i.mu.Unlock()
	for _, call := range calls {
		select {
		case <-call.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
