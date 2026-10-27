package clitemplate

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type googleReady struct {
	SHA256       string `json:"sha256"`
	PythonSHA256 string `json:"pythonSha256"`
	Version      string `json:"version"`
}

// GoogleRuntimePaths returns only the platform's fixed private installation.
func (i *Installer) GoogleRuntimePaths(t *Template) (sdk, framework string) {
	root := filepath.Join(i.toolsDir, t.Name, t.Version, "runtime")
	return filepath.Join(root, "google-cloud-sdk"), filepath.Join(root, "Python.framework")
}
func googleDependency(a Asset) (Asset, error) {
	if len(a.Dependencies) != 1 || a.Dependencies[0].Layout != "python_framework" || a.Dependencies[0].Format != "tar.gz" || a.Dependencies[0].Path != "python-3.13.7-macos11.pkg" {
		return Asset{}, errors.New("Google私有Python安装清单无效")
	}
	return a.Dependencies[0], nil
}
func privatePythonShell(framework string) string {
	return "export DYLD_FRAMEWORK_PATH=" + shellQuote(filepath.Dir(framework)) + " DYLD_LIBRARY_PATH=" + shellQuote(filepath.Join(framework, "Versions/3.13/lib")) + " PYTHONHOME=" + shellQuote(filepath.Join(framework, "Versions/3.13")) + "; exec " + shellQuote(filepath.Join(framework, "Versions/3.13/bin/python3.13")) + " -S "
}
func verifyGoogle(ctx context.Context, t *Template, root string) error {
	home, e := os.MkdirTemp(root, ".verify-home-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(home)
	sdk := filepath.Join(root, "runtime/google-cloud-sdk")
	framework := filepath.Join(root, "runtime/Python.framework")
	// Fixed libraries are checked without site initialization or network calls.
	code := "import sys,ssl,sqlite3,ctypes,lzma,bz2; assert sys.version_info[:3]==(3,13,7); sys.path[:0]=" + pythonList(filepath.Join(sdk, "lib"), filepath.Join(sdk, "lib/third_party")) + "; import google.auth,rsa; assert google.auth.__version__=='2.50.0'"
	probe, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	env := []string{"HOME=" + home, "CLOUDSDK_CONFIG=" + home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "PYTHONNOUSERSITE=1", "CLOUDSDK_CORE_DISABLE_USAGE_REPORTING=true", "CLOUDSDK_COMPONENT_MANAGER_DISABLE_UPDATE_CHECK=true"}
	for _, args := range []string{"-c " + shellQuote(code), shellQuote(filepath.Join(sdk, "lib/gcloud.py")) + " --version"} {
		cmd := exec.CommandContext(probe, "/bin/sh", "-c", privatePythonShell(framework)+args)
		cmd.Env = env
		cmd.Dir = home
		out, err := cmd.CombinedOutput()
		ok := err == nil
		if strings.Contains(args, "--version") {
			ok = ok && strings.Contains(string(out), "Google Cloud SDK "+t.Version)
		}
		clear(out)
		if !ok {
			return errors.New("Google SDK或私有Python依赖版本验证失败")
		}
	}
	return nil
}
func pythonList(paths ...string) string { b, _ := json.Marshal(paths); return string(b) }

func (i *Installer) installGoogle(ctx context.Context, t *Template, a Asset, archive, stage string) error {
	dep, e := googleDependency(a)
	if e != nil {
		return e
	}
	root := filepath.Join(stage, "runtime")
	if e = os.MkdirAll(root, 0700); e != nil {
		return e
	}
	if e = extractTree(archive, a.Format, root); e != nil {
		return e
	}
	work, e := os.MkdirTemp(stage, ".python-package-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(work)
	pkg, e := os.CreateTemp(work, "download-")
	if e != nil {
		return e
	}
	e = download(ctx, i.client, dep.URL, pkg, dep.SHA256)
	ce := pkg.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	unpack := filepath.Join(work, "unpack")
	if e = os.Mkdir(unpack, 0700); e != nil {
		return e
	}
	if e = extractTree(pkg.Name(), dep.Format, unpack); e != nil {
		return e
	}
	expanded := filepath.Join(work, "expanded")
	cmd := exec.CommandContext(ctx, "/usr/sbin/pkgutil", "--expand-full", filepath.Join(unpack, dep.Path), expanded)
	cmd.Env = []string{"HOME=" + work, "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	cmd.Dir = work
	out, err := cmd.CombinedOutput()
	clear(out)
	if err != nil {
		return errors.New("私有Python包解包失败")
	}
	// expand-full extracts payload only; no installer scripts are executed.
	source := filepath.Join(expanded, "Python_Framework.pkg/Payload")
	if e = copyPrivateTree(source, filepath.Join(root, "Python.framework")); e != nil {
		return e
	}
	if e = verifyGoogle(ctx, t, stage); e != nil {
		return e
	}
	if e = i.writeGoogleLauncher(t, stage); e != nil {
		return e
	}
	b, _ := json.Marshal(googleReady{a.SHA256, dep.SHA256, t.Version})
	return os.WriteFile(filepath.Join(stage, ".google-ready.json"), b, 0600)
}
func copyPrivateTree(source, dest string) error {
	info, e := os.Lstat(source)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("私有Python运行树缺失")
	}
	links := []string{}
	e = filepath.WalkDir(source, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, e := filepath.Rel(source, p)
		if e != nil {
			return e
		}
		target := filepath.Join(dest, rel)
		info, e := d.Info()
		if e != nil {
			return e
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, e := os.Readlink(p)
			if e != nil || filepath.IsAbs(link) {
				return errors.New("私有Python链接无效")
			}
			links = append(links, rel)
			return os.Symlink(link, target)
		}
		if !info.Mode().IsRegular() {
			return errors.New("私有Python文件类型无效")
		}
		in, e := os.Open(p)
		if e != nil {
			return e
		}
		defer in.Close()
		out, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600|info.Mode().Perm()&0111)
		if e != nil {
			return e
		}
		_, e = io.Copy(out, in)
		ce := out.Close()
		if e != nil {
			return e
		}
		return ce
	})
	if e != nil {
		return e
	}
	base, e := filepath.EvalSymlinks(dest)
	if e != nil {
		return e
	}
	for _, link := range links {
		if e = validateLinkWithinRoot(base, link); e != nil {
			return e
		}
	}
	return nil
}
func (i *Installer) googleInstalled(t *Template, a Asset) bool {
	dep, e := googleDependency(a)
	if e != nil {
		return false
	}
	root := filepath.Join(i.toolsDir, t.Name, t.Version)
	b, e := os.ReadFile(filepath.Join(root, ".google-ready.json"))
	if e != nil {
		return false
	}
	var ready googleReady
	if json.Unmarshal(b, &ready) != nil || ready != (googleReady{a.SHA256, dep.SHA256, t.Version}) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return verifyGoogle(ctx, t, root) == nil && i.writeGoogleLauncher(t, root) == nil
}
func (i *Installer) writeGoogleLauncher(t *Template, stage string) error {
	executable, e := os.Executable()
	if e != nil {
		return e
	}
	script := "#!/bin/sh\nexec " + shellQuote(executable) + " internal-gcloud-cli " + shellQuote(filepath.Join(i.toolsDir, t.Name, t.Version, "runtime")) + " \"$@\"\n"
	return os.WriteFile(filepath.Join(stage, "bin", t.Binary), []byte(script), 0700)
}
