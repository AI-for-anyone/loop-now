// loopctl-dist cross-compiles loopctl for the platforms in Platforms (arm64 and amd64 on macOS and Linux, and amd64
// on Windows), and generates SHA256SUMS and manifest.json.
//
//	go run ./cmd/loopctl-dist -version <version> -out <dir>
//
// The output is written to <dir>/<version>/. The server is built with the same version number and reads the
// manifest.json there.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Platforms are the platforms that are built.
var Platforms = []struct{ OS, Arch string }{
	{"darwin", "arm64"}, {"darwin", "amd64"}, {"linux", "amd64"}, {"linux", "arm64"}, {"windows", "amd64"},
}

// Manifest is the content of manifest.json.
type Manifest struct {
	SchemaVersion int        `json:"schemaVersion"`
	Program       string     `json:"program"`
	Version       string     `json:"version"`
	GoVersion     string     `json:"goVersion"`
	Artifacts     []Artifact `json:"artifacts"`
}

// Artifact is the binary for one platform.
type Artifact struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	File   string `json:"file"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

var versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)

func main() {
	version := flag.String("version", "", "Version number; defaults to the output of git describe --tags --always --dirty")
	out := flag.String("out", "", "Output directory; the artifacts are written to <out>/<version>/")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "loopctl-dist: -out is required.")
		os.Exit(2)
	}
	ctx := context.Background()
	root, err := moduleRoot(ctx)
	if err != nil {
		fail(err)
	}
	if *version == "" {
		b, err := exec.CommandContext(ctx, "git", "-C", root, "describe", "--tags", "--always", "--dirty").Output()
		if err != nil {
			fail(fmt.Errorf("determine the version with git describe (set it with -version instead): %w", err))
		}
		*version = strings.TrimSpace(string(b))
	}
	m, dir, err := Build(ctx, root, *version, *out)
	if err != nil {
		fail(err)
	}
	fmt.Printf("loopctl %s: wrote the artifacts for %d platforms to %s\n", m.Version, len(m.Artifacts), dir)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "loopctl-dist:", err)
	os.Exit(1)
}

func moduleRoot(ctx context.Context) (string, error) {
	b, err := exec.CommandContext(ctx, "go", "env", "GOMOD").Output()
	if err != nil {
		return "", err
	}
	mod := strings.TrimSpace(string(b))
	if mod == "" || mod == os.DevNull {
		return "", errors.New("the current directory is not inside a Go module")
	}
	return filepath.Dir(mod), nil
}

// Build builds all platforms, writes the binaries, SHA256SUMS and manifest.json, and returns the manifest and the
// output directory.
func Build(ctx context.Context, root, version, out string) (*Manifest, string, error) {
	if !versionPattern.MatchString(version) {
		return nil, "", fmt.Errorf("version %q may contain only letters, digits and . _ + -", version)
	}
	dir := filepath.Join(out, version)
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return nil, "", fmt.Errorf("%s already exists and is not empty; choose another output directory or clean it first", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", err
	}
	goVersion, err := exec.CommandContext(ctx, "go", "env", "GOVERSION").Output()
	if err != nil {
		return nil, "", err
	}
	m := &Manifest{SchemaVersion: 1, Program: "loopctl", Version: version, GoVersion: strings.TrimSpace(string(goVersion))}
	for _, p := range Platforms {
		file := "loopctl-" + p.OS + "-" + p.Arch
		if p.OS == "windows" {
			file += ".exe"
		}
		cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-buildvcs=false",
			"-ldflags", "-s -w -X loop-now/internal/buildinfo.version="+version,
			"-o", filepath.Join(dir, file), "./cmd/loopctl")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+p.OS, "GOARCH="+p.Arch)
		if b, err := cmd.CombinedOutput(); err != nil {
			return nil, "", fmt.Errorf("build %s/%s: %v\n%s", p.OS, p.Arch, err, b)
		}
		size, sum, err := digest(filepath.Join(dir, file))
		if err != nil {
			return nil, "", err
		}
		m.Artifacts = append(m.Artifacts, Artifact{OS: p.OS, Arch: p.Arch, File: file, Size: size, SHA256: sum})
	}
	sort.Slice(m.Artifacts, func(i, j int) bool { return m.Artifacts[i].File < m.Artifacts[j].File })
	var sums bytes.Buffer
	for _, a := range m.Artifacts {
		fmt.Fprintf(&sums, "%s  %s\n", a.SHA256, a.File) // same output format as shasum -a 256
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), sums.Bytes(), 0o644); err != nil {
		return nil, "", err
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), append(b, '\n'), 0o644); err != nil {
		return nil, "", err
	}
	return m, dir, nil
}

func digest(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}
