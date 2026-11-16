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

// Install states, matching CliTemplate.install.status in the OpenAPI spec.
const (
	StateNotInstalled        = "not_installed"
	StateInstalling          = "installing"
	StateInstalled           = "installed"
	StateFailed              = "failed"
	StateUnsupportedPlatform = "unsupported_platform"
)

// maxBinarySize keeps a broken install package from extracting an oversized file.
const maxBinarySize = 1 << 30

// Installer installs the pinned version a template specifies into <work root>/tools/<name>/<version>/bin.
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

// NewInstaller creates an installer. When client is nil the default HTTP client is used.
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

// BinDir returns the bin directory of a template version.
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

// Status returns the install state and the failure reason.
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

// Ensure makes sure the template version is installed and returns its bin directory. Concurrent calls for the same
// version download and install it only once.
func (i *Installer) Ensure(ctx context.Context, t *Template) (string, error) {
	dir, _, err := i.EnsureReport(ctx, t)
	return dir, err
}

// EnsureReport is like Ensure, and also reports whether this call actually attempted an install:
// false when it was already installed or the call waited for someone else's install in progress; true when it
// downloaded and installed (whether it succeeded or failed) or when there is no install package for the current
// platform.
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

// RestoreFailures restores the install failures recorded by the previous run (keyed by name@version); it only
// applies to versions not installed on this machine and with no install currently in progress.
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

// Key returns the key of a template version (name@version), the same key RestoreFailures uses.
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
			return nil // already installed by another call
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
