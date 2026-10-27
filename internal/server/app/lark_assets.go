package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/larksidecar"
)

const larkSourceCommit = "f065bf5b645af381f9b7475ce721451e6ca36a23"
const larkSourceSHA = "997aaddb0e74cc17903ee463c9bd6554ee671d7bdd5e0aa58343e74219ee46dd"

// Controlled build-014; official upstream source remains unmodified. Updating
// this pin requires a newly reviewed platform bundle, never a runtime download.
const larkManifestSHA = "941039beb0ec81ac39f90373400d83f5722684460f3455400961bb5df466fcc1"

type larkAssetFile struct{ File, SHA256, Protocol, EntryFile string }
type larkAssetManifest struct {
	ArtifactVersion, SourceCommit, SourceSHA256, Platform, BinarySHA256 string
	OfficialRelease, SourceModified                                     *bool
	BuildTags                                                           []string
	SandboxProbe, Launcher                                              larkAssetFile
}

func canonicalSHA(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && s == strings.ToLower(s)
}

// Install only the platform-built, fixed-source bundle. No URL, host CLI,
// keychain or build tool is consulted. The manifest is read once at startup;
// the returned paths/hashes stay in server memory, outside Agent control.
func prepareLarkAssets(ctx context.Context, bundle, workRoot, manifestSHA string) (larksidecar.LaunchConfig, error) {
	var zero larksidecar.LaunchConfig
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return zero, errors.New("Lark用户CLI平台未验证")
	}
	if ctx == nil || !filepath.IsAbs(bundle) || !filepath.IsAbs(workRoot) {
		return zero, errors.New("Lark用户CLI安装参数无效")
	}
	info, e := os.Lstat(bundle)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return zero, errors.New("Lark用户CLI发行包不可用")
	}
	root, e := os.OpenRoot(bundle)
	if e != nil {
		return zero, e
	}
	defer root.Close()
	manifestInfo, e := root.Lstat("manifest.json")
	if e != nil || !manifestInfo.Mode().IsRegular() || manifestInfo.Mode()&os.ModeSymlink != 0 || manifestInfo.Size() > 1<<20 {
		return zero, errors.New("Lark用户CLI清单无效")
	}
	f, e := openLarkRegular(root, "manifest.json")
	if e != nil {
		return zero, e
	}
	raw, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	ce := f.Close()
	if e != nil || ce != nil || len(raw) > 1<<20 {
		return zero, errors.New("Lark用户CLI清单无效")
	}
	manifestDigest := sha256.Sum256(raw)
	if !canonicalSHA(manifestSHA) || hex.EncodeToString(manifestDigest[:]) != manifestSHA {
		return zero, errors.New("Lark用户CLI发行清单摘要不匹配")
	}
	var m larkAssetManifest
	if json.Unmarshal(raw, &m) != nil || m.ArtifactVersion != clicredential.LarkUserVariant || m.SourceCommit != larkSourceCommit || m.SourceSHA256 != larkSourceSHA || m.Platform != runtime.GOOS+"-"+runtime.GOARCH || m.OfficialRelease == nil || *m.OfficialRelease || m.SourceModified == nil || *m.SourceModified || len(m.BuildTags) != 1 || m.BuildTags[0] != "authsidecar" ||
		!canonicalSHA(m.BinarySHA256) || !canonicalSHA(m.Launcher.SHA256) || !canonicalSHA(m.SandboxProbe.SHA256) || m.Launcher.File != "lark-cli-launcher" || m.Launcher.EntryFile != "bin/lark-cli" || m.Launcher.Protocol != "loop-now-lark-exec-v1" || m.SandboxProbe.File != "lark-cli-sandbox-probe" || m.SandboxProbe.Protocol != "loop-now-lark-sandbox-probe-v2" {
		return zero, errors.New("Lark用户CLI清单不匹配")
	}
	files := map[string]string{"lark-cli": m.BinarySHA256, "lark-cli-launcher": m.Launcher.SHA256, "bin/lark-cli": m.Launcher.SHA256, "lark-cli-sandbox-probe": m.SandboxProbe.SHA256}
	digest := sha256.Sum256(raw)
	work, e := os.OpenRoot(workRoot)
	if e != nil {
		return zero, e
	}
	defer work.Close()
	base := filepath.Join("tools", "lark-cli-user", clicredential.LarkUserVariant)
	if e = work.MkdirAll(base, 0700); e != nil {
		return zero, e
	}
	installed, e := work.OpenRoot(base)
	if e != nil {
		return zero, e
	}
	defer installed.Close()
	name := hex.EncodeToString(digest[:])
	dest := filepath.Join(workRoot, base, name)
	verified := func(path string) bool {
		r, e := installed.OpenRoot(path)
		if e != nil {
			return false
		}
		defer r.Close()
		f, e := openLarkRegular(r, "manifest.json")
		if e != nil {
			return false
		}
		b, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
		f.Close()
		if e != nil || len(b) > 1<<20 || sha256.Sum256(b) != digest {
			return false
		}
		for name, want := range files {
			if e = copyLarkAsset(ctx, r, name, want, nil); e != nil {
				return false
			}
		}
		return true
	}
	if fi, statErr := installed.Lstat(name); statErr == nil {
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return zero, errors.New("Lark用户CLI安装路径无效")
		}
		if !verified(name) {
			return zero, errors.New("Lark用户CLI已安装内容校验失败")
		}
	} else {
		if !os.IsNotExist(statErr) {
			return zero, statErr
		}
		stage := ".stage-" + uuid.NewString()
		e := installed.Mkdir(stage, 0700)
		if e != nil {
			return zero, e
		}
		defer installed.RemoveAll(stage)
		stageRoot, e := installed.OpenRoot(stage)
		if e != nil {
			return zero, e
		}
		defer stageRoot.Close()
		if e = stageRoot.Mkdir("bin", 0700); e != nil {
			return zero, e
		}
		for name, want := range files {
			if e = copyLarkAsset(ctx, root, name, want, stageRoot); e != nil {
				return zero, e
			}
		}
		licenseFile, e := openLarkRegular(root, "LICENSE")
		if e != nil {
			return zero, errors.New("Lark用户CLI许可证缺失")
		}
		license, e := io.ReadAll(io.LimitReader(licenseFile, (1<<20)+1))
		licenseFile.Close()
		if e != nil || len(license) == 0 || len(license) > 1<<20 {
			return zero, errors.New("Lark用户CLI许可证缺失")
		}
		if e = stageRoot.WriteFile("LICENSE", license, 0600); e != nil {
			return zero, e
		}
		if e = stageRoot.WriteFile("manifest.json", raw, 0600); e != nil {
			return zero, e
		}
		if e = installed.Rename(stage, name); e != nil && !verified(name) {
			return zero, errors.New("Lark用户CLI安装发布失败")
		}
	}
	host, e := os.UserHomeDir()
	if e != nil {
		return zero, e
	}
	return larksidecar.LaunchConfig{Binary: filepath.Join(dest, "lark-cli"), BinarySHA256: m.BinarySHA256, ProbeBinary: filepath.Join(dest, "lark-cli-sandbox-probe"), ProbeSHA256: m.SandboxProbe.SHA256, LauncherBinary: filepath.Join(dest, "bin", "lark-cli"), LauncherSHA256: m.Launcher.SHA256, HostHome: host}, nil
}

func copyLarkAsset(ctx context.Context, root *os.Root, name, want string, dest *os.Root) error {
	fi, e := root.Lstat(name)
	if e != nil || !fi.Mode().IsRegular() || fi.Mode()&os.ModeSymlink != 0 || fi.Size() > 512<<20 || fi.Mode().Perm()&0111 == 0 {
		return errors.New("Lark用户CLI文件无效")
	}
	if strings.Contains(name, "/") {
		parent, e := root.Lstat(filepath.Dir(name))
		if e != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
			return errors.New("Lark用户CLI目录无效")
		}
	}
	in, e := openLarkRegular(root, name)
	if e != nil {
		return e
	}
	defer in.Close()
	var out *os.File
	if dest != nil {
		out, e = dest.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if e != nil {
			return e
		}
		defer out.Close()
	}
	h := sha256.New()
	buf := make([]byte, 64<<10)
	var total int64
	for {
		if e = ctx.Err(); e != nil {
			return e
		}
		n, readErr := in.Read(buf)
		if n > 0 {
			total += int64(n)
			if total > 512<<20 {
				return errors.New("Lark用户CLI文件超限")
			}
			h.Write(buf[:n])
			if out != nil {
				if _, e = out.Write(buf[:n]); e != nil {
					return e
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if hex.EncodeToString(h.Sum(nil)) != want {
		return errors.New("Lark用户CLI文件摘要不匹配")
	}
	if out != nil {
		if e = out.Sync(); e != nil {
			return e
		}
		return out.Close()
	}
	return nil
}

func openLarkRegular(root *os.Root, name string) (*os.File, error) {
	f, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	fi, e := f.Stat()
	if e != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("Lark用户CLI须为普通文件")
	}
	return f, nil
}
