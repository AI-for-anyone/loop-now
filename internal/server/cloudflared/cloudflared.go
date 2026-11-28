// Package cloudflared finds the cloudflared program that keeps this computer's Cloudflare Tunnel open, or downloads a
// pinned release from Cloudflare's GitHub releases with its SHA-256 checked. In order: a configured path, one on
// PATH, the release downloaded earlier, a new download.
package cloudflared

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"sync"
	"time"
)

// Version is the release downloaded when no usable cloudflared is found.
const Version = "2026.11.2"

// MinVersion is the oldest cloudflared used from a configured path or PATH (older macOS builds lack the certificate
// authority Cloudflare's edge moved to; T3 Code requires the same).
const MinVersion = "2026.10.3"

// Asset is a release file: its URL, its SHA-256 (hex) and whether it is a .tgz holding the program.
type Asset struct {
	URL    string
	SHA256 string
	Tgz    bool
}

const releases = "https://github.com/cloudflare/cloudflared/releases/download/" + Version + "/"

// assets are the pinned release's files per GOOS/GOARCH, with the SHA-256 digests GitHub lists for them.
var assets = map[string]Asset{
	"darwin/arm64":  {URL: releases + "cloudflared-darwin-arm64.tgz", SHA256: "f6d58eeec91ad1ec5fd1f7e4a587303772929b2625be5d24068033851c36ff55", Tgz: true},
	"darwin/amd64":  {URL: releases + "cloudflared-darwin-amd64.tgz", SHA256: "6153d9ec629174882a3adbff2f7dc5a24b4f3bdd7ccc38eaa84fba26eb890311", Tgz: true},
	"linux/arm64":   {URL: releases + "cloudflared-linux-arm64", SHA256: "04a7b7f08bce85d56b26ffe1fdbfbb21a074495a363d16f0783c40e25b5c5281"},
	"linux/amd64":   {URL: releases + "cloudflared-linux-amd64", SHA256: "56f13335143ec0e22772963290683a84a04114a35ec64cbc794a4d39ff9bb8cf"},
	"windows/amd64": {URL: releases + "cloudflared-windows-amd64.exe", SHA256: "555b46c85582461d189cac30d14ad05480d612613801be9f110b8ce08c5b2113"},
}

// AssetFor returns the pinned release file for a platform.
func AssetFor(goos, goarch string) (Asset, bool) {
	a, ok := assets[goos+"/"+goarch]
	return a, ok
}

// Source is where a cloudflared came from (the Cloudflared schema's source).
type Source string

const (
	SourceConfigured Source = "configured"
	SourcePath       Source = "path"
	SourceDownloaded Source = "downloaded"
)

// Binary is a usable cloudflared; it mirrors the Cloudflared schema.
type Binary struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	Source  Source `json:"source"`
}

// ErrUnsupported means there is no pinned release for this platform and none was found; trying again does not help.
var ErrUnsupported = errors.New("unsupported platform")

// Progress reports a download: received bytes and the total (-1 when unknown).
type Progress func(received, total int64)

const (
	downloadTimeout   = 10 * time.Minute
	probeTimeout      = 10 * time.Second
	validationTimeout = 2 * time.Minute // a virus scanner may hold the first start of a new program
	maxDownload       = 256 << 20
)

// Installer finds or downloads cloudflared.
type Installer struct {
	// Dir holds downloaded releases: <Dir>/<version>/<goos>-<goarch>/cloudflared.
	Dir string
	// Configured is LOOP_NOW_CLOUDFLARED_PATH; when set, it is the only cloudflared used.
	Configured string
	// PathDirs lists the directories of PATH to search; nil searches none.
	PathDirs func(ctx context.Context) []string
	HTTP     *http.Client
	Log      *slog.Logger
	// GOOS and GOARCH pick the release; empty means this computer's. Asset replaces the pinned file (for tests).
	GOOS, GOARCH string
	Asset        *Asset

	mu     sync.Mutex
	probes map[probeKey]string
}

type probeKey struct {
	path  string
	size  int64
	mtime time.Time
}

func (i *Installer) log() *slog.Logger {
	if i.Log != nil {
		return i.Log
	}
	return slog.New(slog.DiscardHandler)
}

func (i *Installer) platform() (string, string) {
	goos, goarch := i.GOOS, i.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	return goos, goarch
}

func (i *Installer) exe() string {
	if goos, _ := i.platform(); goos == "windows" {
		return "cloudflared.exe"
	}
	return "cloudflared"
}

// ManagedPath is where the pinned release is installed.
func (i *Installer) ManagedPath() string {
	goos, goarch := i.platform()
	return filepath.Join(i.Dir, Version, goos+"-"+goarch, i.exe())
}

// Ensure returns a usable cloudflared, downloading the pinned release when none is found; progress (may be nil)
// hears about the download.
func (i *Installer) Ensure(ctx context.Context, progress Progress) (Binary, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.Configured != "" {
		v, err := i.probe(ctx, i.Configured, probeTimeout)
		if err != nil {
			return Binary{}, fmt.Errorf("LOOP_NOW_CLOUDFLARED_PATH is %s, which does not run as cloudflared: %w", i.Configured, err)
		}
		if compare(v, MinVersion) < 0 {
			return Binary{}, fmt.Errorf("LOOP_NOW_CLOUDFLARED_PATH is cloudflared %s; %s or newer is needed", v, MinVersion)
		}
		return Binary{Path: i.Configured, Version: v, Source: SourceConfigured}, nil
	}
	if b, ok := i.fromPath(ctx); ok {
		return b, nil
	}
	managed := i.ManagedPath()
	if v, err := i.probe(ctx, managed, probeTimeout); err == nil && v == Version {
		return Binary{Path: managed, Version: v, Source: SourceDownloaded}, nil
	}
	asset, ok := i.asset()
	if !ok {
		goos, goarch := i.platform()
		return Binary{}, fmt.Errorf("%w: Loop Now has no cloudflared release for %s/%s; install cloudflared %s or newer and put it on PATH, or set LOOP_NOW_CLOUDFLARED_PATH", ErrUnsupported, goos, goarch, MinVersion)
	}
	if err := i.download(ctx, asset, managed, progress); err != nil {
		return Binary{}, err
	}
	return Binary{Path: managed, Version: Version, Source: SourceDownloaded}, nil
}

func (i *Installer) asset() (Asset, bool) {
	if i.Asset != nil {
		return *i.Asset, true
	}
	return AssetFor(i.platform())
}

func (i *Installer) fromPath(ctx context.Context) (Binary, bool) {
	if i.PathDirs == nil {
		return Binary{}, false
	}
	seen := map[string]bool{}
	for _, dir := range i.PathDirs(ctx) {
		if dir == "" || !filepath.IsAbs(dir) || seen[dir] {
			continue
		}
		seen[dir] = true
		p := filepath.Join(dir, i.exe())
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		v, err := i.probe(ctx, p, probeTimeout)
		if err != nil {
			i.log().Info("skipping a cloudflared on PATH that does not run", "path", p, "err", err)
			continue
		}
		if compare(v, MinVersion) < 0 {
			i.log().Info("skipping an old cloudflared on PATH", "path", p, "version", v, "minVersion", MinVersion)
			continue
		}
		return Binary{Path: p, Version: v, Source: SourcePath}, true
	}
	return Binary{}, false
}

var versionPattern = regexp.MustCompile(`\b(\d{4})\.(\d{1,2})\.(\d+)\b`)

// ParseVersion finds the version in `cloudflared version` output, such as "2026.11.2".
func ParseVersion(out string) (string, bool) {
	m := versionPattern.FindStringSubmatch(out)
	if m == nil {
		return "", false
	}
	return m[1] + "." + strconv.Itoa(atoi(m[2])) + "." + strconv.Itoa(atoi(m[3])), true
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func compare(a, b string) int {
	pa, pb := versionPattern.FindStringSubmatch(a), versionPattern.FindStringSubmatch(b)
	if pa == nil || pb == nil {
		return -1
	}
	for k := 1; k <= 3; k++ {
		if d := atoi(pa[k]) - atoi(pb[k]); d != 0 {
			return d
		}
	}
	return 0
}

// probe runs `<path> version` and returns the version it reports; successful answers are kept per file revision.
func (i *Installer) probe(ctx context.Context, path string, timeout time.Duration) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a file", path)
	}
	key := probeKey{path: path, size: info.Size(), mtime: info.ModTime()}
	if v, ok := i.probes[key]; ok {
		return v, nil
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "version")
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("`%s version` did not answer within %v", path, timeout)
		}
		return "", fmt.Errorf("`%s version` failed: %w", path, err)
	}
	v, ok := ParseVersion(string(out))
	if !ok {
		return "", fmt.Errorf("`%s version` printed no version", path)
	}
	if i.probes == nil {
		i.probes = map[probeKey]string{}
	}
	i.probes[key] = v
	return v, nil
}

type counter struct {
	r        io.Reader
	n, total int64
	progress Progress
	last     time.Time
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.progress != nil && (err != nil || time.Since(c.last) > 200*time.Millisecond) {
		c.last = time.Now()
		c.progress(c.n, c.total)
	}
	return n, err
}

func (i *Installer) download(ctx context.Context, asset Asset, managed string, progress Progress) (err error) {
	dir := filepath.Dir(managed)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("could not make the folder for cloudflared %s: %w", dir, err)
	}
	tmpDir, err := os.MkdirTemp(dir, ".install-")
	if err != nil {
		return fmt.Errorf("could not make the folder for cloudflared %s: %w", dir, err)
	}
	defer os.RemoveAll(tmpDir)

	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	i.log().Info("downloading cloudflared", "version", Version, "url", asset.URL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return err
	}
	client := i.HTTP
	if client == nil {
		client = &http.Client{}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("could not download cloudflared %s from %s: %w", Version, asset.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("could not download cloudflared %s from %s: the server answered %s", Version, asset.URL, resp.Status)
	}
	if progress != nil {
		progress(0, resp.ContentLength)
	}
	archive := filepath.Join(tmpDir, "download")
	f, err := os.OpenFile(archive, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), &counter{r: io.LimitReader(resp.Body, maxDownload+1), total: resp.ContentLength, progress: progress})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("could not download cloudflared %s from %s: %w", Version, asset.URL, err)
	}
	if n > maxDownload {
		return fmt.Errorf("the cloudflared download from %s is larger than %d MiB; refused", asset.URL, maxDownload>>20)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != asset.SHA256 {
		return fmt.Errorf("the cloudflared download from %s does not match its pinned SHA-256 (got %s, want %s); refused", asset.URL, got, asset.SHA256)
	}
	program := filepath.Join(tmpDir, i.exe())
	if asset.Tgz {
		if err := extract(archive, program); err != nil {
			return fmt.Errorf("could not unpack the cloudflared download: %w", err)
		}
	} else if err := os.Rename(archive, program); err != nil {
		return err
	}
	if err := os.Chmod(program, 0o755); err != nil {
		return err
	}
	v, err := i.probe(ctx, program, validationTimeout)
	if err != nil {
		return fmt.Errorf("the downloaded cloudflared does not run: %w", err)
	}
	if v != Version {
		return fmt.Errorf("the downloaded cloudflared reports version %s, not %s; refused", v, Version)
	}
	if err := os.Rename(program, managed); err != nil {
		return fmt.Errorf("could not install cloudflared at %s: %w", managed, err)
	}
	i.log().Info("installed cloudflared", "version", Version, "path", managed)
	return nil
}

// extract writes the file named cloudflared in a .tgz to dst.
func extract(archive, dst string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return errors.New("the archive holds no cloudflared")
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || filepath.Base(h.Name) != "cloudflared" {
			continue
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, io.LimitReader(tr, maxDownload)); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	}
}
