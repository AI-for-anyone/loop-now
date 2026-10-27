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

// Source 是安装来源（接口里的 PluginSource）。market 来源给出 Market 与 ID（Version 可选），
// 由 resolveMarket 换成条目的 git 或 npm 来源。
type Source struct {
	Kind     string  `json:"kind"`
	Location string  `json:"location"`
	Ref      *string `json:"ref"`
	Market   string  `json:"market,omitempty"`
	ID       string  `json:"id,omitempty"`
	Version  string  `json:"version,omitempty"`
}

// ResolvedSource 是安装时记录的精确来源（接口里的 PluginResolvedSource），原样保存在 plugins.source。
type ResolvedSource struct {
	Kind       string  `json:"kind"`
	Location   string  `json:"location"`
	Ref        *string `json:"ref"`
	Commit     *string `json:"commit"`
	NpmVersion *string `json:"npmVersion"`
	Market     *string `json:"market"` // 从市场安装时的市场地址
}

// 来源种类。
const (
	SourceBuiltin = "builtin"
	SourceLocal   = "local"
	SourceGit     = "git"
	SourceNPM     = "npm"
	SourceMarket  = "market"
)

// maxPackageBytes 是一个插件包解开后的总大小上限（不含依赖安装）。
const maxPackageBytes = 512 << 20

var commitPattern = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

func sourceFailed(format string, args ...any) error {
	return &apperr.Error{Status: 422, Code: apperr.CodePluginSourceFailed, Message: fmt.Sprintf(format, args...)}
}

// fetch 把来源取到 dest（local 来源不复制，返回原目录），返回插件目录与精确来源。
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

// extractTarball 解开 npm 包（gzip 的 tar，顶层目录 package/），只接受普通文件与目录。
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

// installDeps 为 git 与 npm 来源安装运行依赖：有锁文件用 npm ci，没有用 npm install，都不运行安装脚本、不装开发依赖，
// 也不装 peerDependencies（Pi 包按约定把 Pi 自己的包声明为 peer，由运行它的 Pi 提供，与 Pi 的安装器一致）。
// 没有 dependencies 时什么也不做。
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

// npm 是与 node 同目录的 npm；没有配置 node 时为空。
func (s *Service) npm() string {
	if s.NodeBin == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(s.NodeBin), "npm")
}

// run 执行一个外部命令，返回标准输出；失败时错误带标准错误的末尾。
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

// digest 返回插件内容的 sha256：按相对路径排序的每个文件的路径与内容摘要；不含 node_modules 与 .git。
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

// copyTree 复制目录（普通文件与目录；符号链接跳过）。
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
