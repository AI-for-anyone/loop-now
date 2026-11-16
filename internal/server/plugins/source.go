package plugins

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"loop-now/internal/pluginmanifest"
	"loop-now/internal/server/apperr"
)

// Source is an install source (the API's PluginSource). A market source gives Market and ID (Version is optional)
// and is turned into the entry's git or npm source by resolveMarket.
type Source struct {
	Kind     string  `json:"kind"`
	Location string  `json:"location"`
	Ref      *string `json:"ref"`
	Market   string  `json:"market,omitempty"`
	ID       string  `json:"id,omitempty"`
	Version  string  `json:"version,omitempty"`
}

// ResolvedSource is the exact source recorded at install time (the API's PluginResolvedSource), stored as-is in
// plugins.source.
type ResolvedSource struct {
	Kind       string  `json:"kind"`
	Location   string  `json:"location"`
	Ref        *string `json:"ref"`
	Commit     *string `json:"commit"`
	NpmVersion *string `json:"npmVersion"`
	Market     *string `json:"market"` // the market address when installed from a market
}

// Source kinds.
const (
	SourceBuiltin = "builtin"
	SourceLocal   = "local"
	SourceGit     = "git"
	SourceNPM     = "npm"
	SourceMarket  = "market"
)

// maxPackageBytes is the limit on the total unpacked size of one plugin package (not counting installed
// dependencies).
const maxPackageBytes = 512 << 20

var commitPattern = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

func sourceFailed(format string, args ...any) error {
	return &apperr.Error{Status: 422, Code: apperr.CodePluginSourceFailed, Message: fmt.Sprintf(format, args...)}
}

// fetch fetches the source into dest (a local source is not copied; its original directory is returned) and returns
// the plugin directory and the exact source.
func (s *Service) fetch(ctx context.Context, src Source, dest string) (string, ResolvedSource, error) {
	out := ResolvedSource{Kind: src.Kind, Location: src.Location, Ref: src.Ref}
	switch src.Kind {
	case SourceBuiltin:
		if s.OfficialDir == "" || !pluginmanifest.ValidID(src.Location) {
			return "", out, apperr.NotFound("官方插件")
		}
		from := filepath.Join(s.OfficialDir, src.Location)
		if info, err := os.Stat(from); err != nil || !info.IsDir() {
			return "", out, apperr.NotFound("官方插件")
		}
		if err := copyTree(from, dest); err != nil {
			return "", out, err
		}
		return dest, out, nil
	case SourceLocal:
		if !filepath.IsAbs(src.Location) {
			return "", out, apperr.Invalid("source.location", "本地目录须是服务端本机的绝对路径")
		}
		dir := filepath.Clean(src.Location)
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return "", out, sourceFailed("本地目录 %s 不存在", dir)
		}
		out.Location = dir
		return dir, out, nil
	case SourceGit:
		if err := s.gitFetch(ctx, src, dest, &out); err != nil {
			return "", out, err
		}
		return dest, out, nil
	case SourceNPM:
		if err := s.npmFetch(ctx, src.Location, dest); err != nil {
			return "", out, err
		}
		return dest, out, nil
	}
	return "", out, apperr.Invalid("source.kind", "只能是 builtin、local、git 或 npm")
}

func (s *Service) gitFetch(ctx context.Context, src Source, dest string, out *ResolvedSource) error {
	if strings.HasPrefix(src.Location, "-") {
		return apperr.Invalid("source.location", "git 地址无效")
	}
	ref := ""
	if src.Ref != nil {
		ref = *src.Ref
	}
	if strings.HasPrefix(ref, "-") {
		return apperr.Invalid("source.ref", "ref 无效")
	}
	args := []string{"clone", "--quiet"}
	if ref == "" {
		args = append(args, "--depth", "1")
	} else if !commitPattern.MatchString(ref) {
		args = append(args, "--depth", "1", "--branch", ref)
	}
	args = append(args, "--", src.Location, dest)
	if _, err := s.run(ctx, "", s.GitBin, args...); err != nil {
		return sourceFailed("git 克隆失败：%v", err)
	}
	if ref != "" && commitPattern.MatchString(ref) {
		if _, err := s.run(ctx, dest, s.GitBin, "checkout", "--quiet", ref); err != nil {
			return sourceFailed("git 检出 %s 失败：%v", ref, err)
		}
	}
	commit, err := s.run(ctx, dest, s.GitBin, "rev-parse", "HEAD")
	if err != nil {
		return sourceFailed("读取 git 提交失败：%v", err)
	}
	c := strings.TrimSpace(commit)
	out.Commit = &c
	return os.RemoveAll(filepath.Join(dest, ".git"))
}

func (s *Service) npmFetch(ctx context.Context, spec, dest string) error {
	if spec == "" || strings.HasPrefix(spec, "-") {
		return apperr.Invalid("source.location", "npm 包规格无效")
	}
	pack := dest + ".pack"
	if err := os.MkdirAll(pack, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(pack)
	raw, err := s.run(ctx, pack, s.npm(), "pack", spec, "--pack-destination", pack, "--json", "--ignore-scripts")
	if err != nil {
		return sourceFailed("npm pack 失败：%v", err)
	}
	var files []struct {
		Filename string `json:"filename"`
	}
	if json.Unmarshal([]byte(raw), &files) != nil || len(files) != 1 {
		return sourceFailed("npm pack 的输出无法解析")
	}
	f, err := os.Open(filepath.Join(pack, filepath.Base(files[0].Filename)))
	if err != nil {
		return sourceFailed("找不到 npm pack 生成的文件：%v", err)
	}
	defer f.Close()
	return extractTarball(f, dest)
}

// extractTarball unpacks an npm package (a gzipped tar with the top-level directory package/), accepting only regular
// files and directories.
func extractTarball(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return sourceFailed("npm 包不是 gzip：%v", err)
	}
	tr := tar.NewReader(gz)
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return sourceFailed("npm 包无法解开：%v", err)
		}
		name := filepath.Clean(filepath.FromSlash(h.Name))
		if i := strings.IndexRune(name, filepath.Separator); i >= 0 {
			name = name[i+1:]
		} else {
			continue
		}
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return sourceFailed("npm 包含有越界路径 %s", h.Name)
		}
		target := filepath.Join(dest, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			total += h.Size
			if total > maxPackageBytes {
				return sourceFailed("npm 包解开后超过 %d MiB", maxPackageBytes>>20)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			mode := os.FileMode(0o600)
			if h.Mode&0o111 != 0 {
				mode = 0o700
			}
			w, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			_, err = io.CopyN(w, tr, h.Size)
			if cerr := w.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		}
	}
}

// installDeps installs runtime dependencies for git and npm sources: npm ci when there is a lock file, npm install
// otherwise. Neither runs install scripts or installs dev dependencies, and neither installs peerDependencies (by
// convention a Pi package declares Pi's own packages as peers, provided by the Pi that runs it; this matches Pi's
// installer). It does nothing when there are no dependencies.
func (s *Service) installDeps(ctx context.Context, dir string) error {
	b, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return err
	}
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if json.Unmarshal(b, &pkg) != nil || len(pkg.Dependencies) == 0 {
		return nil
	}
	flags := []string{"--ignore-scripts", "--omit=dev", "--omit=peer", "--no-audit", "--no-fund"}
	args := append([]string{"install"}, flags...)
	for _, lock := range []string{"package-lock.json", "npm-shrinkwrap.json"} {
		if _, err := os.Stat(filepath.Join(dir, lock)); err == nil {
			args = append([]string{"ci"}, flags...)
			break
		}
	}
	if _, err := s.run(ctx, dir, s.npm(), args...); err != nil {
		return sourceFailed("安装依赖失败（npm %s）：%v", args[0], err)
	}
	return nil
}

// npm is the npm in the same directory as node; empty when node is not configured.
func (s *Service) npm() string {
	if s.NodeBin == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(s.NodeBin), "npm")
}

// run runs an external command and returns its standard output; on failure the error carries the tail of standard
// error.
func (s *Service) run(ctx context.Context, dir, bin string, args ...string) (string, error) {
	if bin == "" {
		return "", errors.New("服务端没有配置需要的可执行文件（git 或 node）")
	}
	ctx, cancel := context.WithTimeout(ctx, s.FetchTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	path := os.Getenv("PATH")
	if s.NodeBin != "" {
		path = filepath.Dir(s.NodeBin) + string(os.PathListSeparator) + path
	}
	cmd.Env = append(os.Environ(), "PATH="+path, "GIT_TERMINAL_PROMPT=0", "npm_config_update_notifier=false")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		tail := strings.TrimSpace(stderr.String())
		if len(tail) > 500 {
			tail = "…" + tail[len(tail)-500:]
		}
		return "", fmt.Errorf("%v：%s", err, tail)
	}
	return stdout.String(), nil
}

// digest returns the sha256 of the plugin content: the path and content digest of every file, sorted by relative
// path; node_modules and .git are excluded.
func digest(dir string) (string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git") && path != dir {
			return filepath.SkipDir
		}
		if d.Type().IsRegular() {
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	slices.Sort(files)
	h := sha256.New()
	for _, rel := range files {
		b, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(b)
		fmt.Fprintf(h, "%s\x00%x\n", filepath.ToSlash(rel), sum)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// copyTree copies a directory (regular files and directories; symlinks are skipped).
func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o700)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, b, info.Mode().Perm()|0o600)
		}
		return nil
	})
}
