package clitemplate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ulikunitz/xz"
)

const maxBundleSize int64 = 4 << 30

// extractTree retains the actual vendor tree. Root confines all file writes;
// links may only reference paths within the same tree.
func extractTree(archive, format, dest string) error {
	root, e := os.OpenRoot(dest)
	if e != nil {
		return e
	}
	defer root.Close()
	seen := map[string]bool{}
	directories := map[string]bool{}
	var total int64
	entries := 0
	links := []string{}
	add := func(name string, mode os.FileMode, size int64, link string, r io.Reader) error {
		entries++
		if entries > 100000 {
			return errors.New("安装包条目过多")
		}
		clean := path.Clean(name)
		if clean == "." {
			return nil
		}
		if strings.ContainsAny(name, "\\\x00") || path.IsAbs(name) || clean == ".." || strings.HasPrefix(clean, "../") {
			return errors.New("安装包路径越界")
		}
		// Archive entries must not traverse a previously installed symlink.
		// Lexical target checks alone do not model symlink/.. resolution.
		parent := path.Dir(clean)
		for parent != "." {
			fi, err := root.Lstat(parent)
			if err == nil && fi.Mode()&os.ModeSymlink != 0 {
				return errors.New("安装包父目录是链接")
			}
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			parent = path.Dir(parent)
		}
		if seen[clean] {
			if mode.IsDir() && directories[clean] {
				return nil
			}
			return errors.New("安装包重复路径")
		}
		seen[clean] = true
		if mode.IsDir() {
			directories[clean] = true
			return root.MkdirAll(clean, 0700)
		}
		if e := root.MkdirAll(path.Dir(clean), 0700); e != nil {
			return e
		}
		if mode&os.ModeSymlink != 0 {
			target := path.Clean(path.Join(path.Dir(clean), link))
			if path.IsAbs(link) || strings.ContainsAny(link, "\\\x00") || target == ".." || strings.HasPrefix(target, "../") {
				return errors.New("安装包链接越界")
			}
			links = append(links, clean)
			return root.Symlink(filepath.FromSlash(link), filepath.FromSlash(clean))
		}
		if !mode.IsRegular() || size < 0 || size > maxBinarySize {
			return errors.New("安装包文件类型或大小无效")
		}
		total += size
		if total > maxBundleSize {
			return errors.New("安装包解压大小超限")
		}
		f, e := root.OpenFile(clean, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600|mode.Perm()&0111)
		if e != nil {
			return e
		}
		_, e = io.CopyN(f, r, size)
		ce := f.Close()
		if e != nil {
			return e
		}
		return ce
	}
	drainTar := func(tr *tar.Reader) error {
		for {
			h, e := tr.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				return e
			}
			mode := h.FileInfo().Mode()
			if h.Typeflag == tar.TypeLink {
				return errors.New("安装包不支持硬链接")
			}
			if e = add(h.Name, mode, h.Size, h.Linkname, tr); e != nil {
				return e
			}
		}
		return nil
	}
	switch format {
	case "zip":
		z, e := zip.OpenReader(archive)
		if e != nil {
			return e
		}
		defer z.Close()
		for _, entry := range z.File {
			rd, e := entry.Open()
			if e != nil {
				return e
			}
			link := ""
			var reader io.Reader = rd
			if entry.Mode()&os.ModeSymlink != 0 {
				b, e := io.ReadAll(io.LimitReader(rd, 4097))
				if e != nil || len(b) > 4096 {
					rd.Close()
					return errors.New("安装包链接无效")
				}
				link = string(b)
			}
			e = add(entry.Name, entry.Mode(), int64(entry.UncompressedSize64), link, reader)
			rd.Close()
			if e != nil {
				return e
			}
		}
	case "tar.gz":
		f, e := os.Open(archive)
		if e != nil {
			return e
		}
		defer f.Close()
		gz, e := gzip.NewReader(f)
		if e != nil {
			return e
		}
		defer gz.Close()
		if e = drainTar(tar.NewReader(gz)); e != nil {
			return e
		}
	case "tar.xz":
		f, e := os.Open(archive)
		if e != nil {
			return e
		}
		defer f.Close()
		xzr, e := xz.NewReader(f)
		if e != nil {
			return e
		}
		if e = drainTar(tar.NewReader(xzr)); e != nil {
			return e
		}
	default:
		return errors.New("安装包格式无效")
	}
	base, err := filepath.EvalSymlinks(dest)
	if err != nil {
		return err
	}
	for _, name := range links {
		if err := validateLinkWithinRoot(base, name); err != nil {
			return err
		}
	}

	return nil
}

type bundleReady struct {
	SHA256  string `json:"sha256"`
	Version string `json:"version"`
	Path    string `json:"path"`
}

func verifyNative(ctx context.Context, t *Template, stage string, a Asset) error {
	home, e := os.MkdirTemp(stage, ".verify-home-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(home)
	probe, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probe, filepath.Join(stage, "runtime", filepath.FromSlash(a.Path)), "--version")
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "LANG=C"}
	b, e := cmd.CombinedOutput()
	defer clear(b)
	if e != nil || !strings.Contains(string(b), t.Version) {
		return errors.New("工具完整运行依赖或固定版本验证失败")
	}
	return nil
}
func (i *Installer) installTree(ctx context.Context, t *Template, a Asset, archive, stage string) error {
	runtimeDir := filepath.Join(stage, "runtime")
	if e := os.MkdirAll(runtimeDir, 0700); e != nil {
		return e
	}
	if e := extractTree(archive, a.Format, runtimeDir); e != nil {
		return e
	}
	if e := verifyNative(ctx, t, stage, a); e != nil {
		return e
	}
	// The final native path is stable after the staging rename.
	final := filepath.Join(i.toolsDir, t.Name, t.Version)
	if e := writeDBLauncher(t, stage, filepath.Join(final, "runtime", filepath.FromSlash(a.Path))); e != nil {
		return e
	}
	b, _ := json.Marshal(bundleReady{a.SHA256, t.Version, a.Path})
	return os.WriteFile(filepath.Join(stage, ".bundle-ready.json"), b, 0600)
}
func (i *Installer) treeInstalled(t *Template, a Asset) bool {
	final := filepath.Join(i.toolsDir, t.Name, t.Version)
	b, e := os.ReadFile(filepath.Join(final, ".bundle-ready.json"))
	if e != nil {
		return false
	}
	var ready bundleReady
	if json.Unmarshal(b, &ready) != nil || ready.SHA256 != a.SHA256 || ready.Version != t.Version || ready.Path != a.Path {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if verifyNative(ctx, t, final, a) != nil {
		return false
	}
	return writeDBLauncher(t, final, filepath.Join(final, "runtime", filepath.FromSlash(a.Path))) == nil
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func writeDBLauncher(t *Template, stage, native string) error {
	executable, e := os.Executable()
	if e != nil {
		return e
	}
	script := "#!/bin/sh\nexec " + shellQuote(executable) + " internal-db-cli " + shellQuote(t.Name) + " " + shellQuote(native) + " \"$@\"\n"
	file := filepath.Join(stage, "bin", t.Binary)
	if e = os.MkdirAll(filepath.Dir(file), 0700); e != nil {
		return e
	}
	temp, e := os.CreateTemp(filepath.Dir(file), ".wrapper-")
	if e != nil {
		return e
	}
	defer os.Remove(temp.Name())
	if _, e = temp.WriteString(script); e == nil {
		e = temp.Chmod(0700)
	}
	ce := temp.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(temp.Name(), file)
}

// Resolve the real component chain, including symlinks preceding '..'.
// A vendor link may have a missing optional leaf, but its path may never
// traverse outside the canonical extraction root, even transiently.
func validateLinkWithinRoot(base, name string) error {
	pending := strings.Split(filepath.FromSlash(name), string(filepath.Separator))
	current := base
	hops := 0
	for len(pending) > 0 {
		part := pending[0]
		pending = pending[1:]
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			current = filepath.Dir(current)
		} else {
			candidate := filepath.Join(current, part)
			info, err := os.Lstat(candidate)
			if err != nil && !os.IsNotExist(err) && !errors.Is(err, syscall.ENOTDIR) {
				return errors.New("安装包链接目标无效")
			}
			if err == nil && info.Mode()&os.ModeSymlink != 0 {
				hops++
				if hops > 64 {
					return errors.New("安装包链接循环或过深")
				}
				target, err := os.Readlink(candidate)
				if err != nil || filepath.IsAbs(target) {
					return errors.New("安装包链接目标无效")
				}
				pending = append(strings.Split(target, string(filepath.Separator)), pending...)
				continue
			}
			current = candidate
		}
		relative, err := filepath.Rel(base, current)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("安装包链接实际目标越界")
		}
	}
	return nil
}
