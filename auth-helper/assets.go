package authhelper

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"loop-now/internal/privatefs"
)

//go:embed *.mjs google_auth.py google_runtime.py google_verify.py
var assets embed.FS

// Digest is the content address of the embedded assets (the first 12 hex
// digits of their SHA-256) and the name of the directory Install publishes, so
// server builds with different assets never share or overwrite helper files.
var Digest = sync.OnceValue(func() string {
	files, err := assets.ReadDir(".")
	if err != nil {
		panic("cannot read embedded auth helper assets")
	}
	h := sha256.New()
	for _, file := range files {
		content, err := assets.ReadFile(file.Name())
		if err != nil {
			panic("cannot read embedded auth helper asset")
		}
		fmt.Fprintf(h, "%s\x00%d\x00", file.Name(), len(content))
		h.Write(content)
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
})

// Install materializes the binary's fixed helper and platform extension assets
// in <parent>/<Digest()>. parent must be an absolute canonical path inside the
// caller-owned work root. Existing ancestors are never chmod'd; the parent, its
// own parent and the digest directory must be private (0700). Symlinks anywhere
// in the directory path or asset targets are rejected. An existing digest
// directory is only verified, never rewritten; a missing one is staged beside it
// and published by a single rename, so it is complete whenever it exists.
// Unrelated files and other digest directories are never touched.
// Caller must not launch either entry if Install returns an error.
func Install(parent string) (helperEntry, extensionEntry string, err error) {
	if !filepath.IsAbs(parent) || filepath.Clean(parent) != parent || parent == string(filepath.Separator) || strings.ContainsRune(parent, 0) {
		return "", "", errors.New("auth helper install root must be an absolute canonical path")
	}
	directory, err := openAssetDirectory(parent)
	if err != nil {
		return "", "", err
	}
	defer directory.Close()
	name := Digest()
	if err := installDigest(directory, name); err != nil {
		return "", "", err
	}
	root := filepath.Join(parent, name)
	return filepath.Join(root, "main.mjs"), filepath.Join(root, "runtime-refresh.mjs"), nil
}

func installDigest(parent *os.Root, name string) error {
	if _, err := parent.Lstat(name); err == nil {
		return verifyAssets(parent, name)
	} else if !os.IsNotExist(err) {
		return errors.New("cannot inspect auth helper installation directory")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return errors.New("cannot create auth helper asset stage")
	}
	stage := ".stage-" + hex.EncodeToString(random[:])
	if err := parent.Mkdir(stage, 0700); err != nil {
		return errors.New("cannot create auth helper asset stage")
	}
	defer parent.RemoveAll(stage) // exact owned stage only; gone once published
	directory, err := openChild(parent, stage, true)
	if err != nil {
		return err
	}
	files, err := assets.ReadDir(".")
	if err != nil {
		directory.Close()
		return errors.New("cannot read embedded auth helper assets")
	}
	for _, file := range files {
		content, err := assets.ReadFile(file.Name())
		if err == nil {
			err = writeAsset(directory, file.Name(), content)
		}
		if err != nil {
			directory.Close()
			return errors.New("cannot write auth helper asset")
		}
	}
	directory.Close()
	// Rename never replaces a non-empty directory: when an identical binary
	// published first, verify its copy instead.
	if err := parent.Rename(stage, name); err != nil {
		if _, statErr := parent.Lstat(name); statErr == nil {
			return verifyAssets(parent, name)
		}
		return errors.New("cannot publish auth helper assets")
	}
	return nil
}

func writeAsset(directory *os.Root, name string, content []byte) error {
	file, err := directory.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err := file.Write(content); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func verifyAssets(parent *os.Root, name string) error {
	directory, err := openChild(parent, name, true)
	if err != nil {
		return err
	}
	defer directory.Close()
	files, err := assets.ReadDir(".")
	if err != nil {
		return errors.New("cannot read embedded auth helper assets")
	}
	for _, file := range files {
		expected, err := assets.ReadFile(file.Name())
		if err != nil {
			return errors.New("cannot read embedded auth helper asset")
		}
		f, err := directory.OpenFile(file.Name(), os.O_RDONLY|privatefs.NoFollow, 0)
		if err != nil {
			return fmt.Errorf("auth helper asset %s is missing or not a regular file; remove %s to reinstall", file.Name(), name)
		}
		info, err := f.Stat()
		var actual bytes.Buffer
		if err == nil && info.Mode().IsRegular() && info.Size() == int64(len(expected)) {
			_, err = actual.ReadFrom(f)
		}
		f.Close()
		if err != nil || !info.Mode().IsRegular() || !bytes.Equal(actual.Bytes(), expected) {
			return fmt.Errorf("auth helper asset %s differs from this build; remove %s to reinstall", file.Name(), name)
		}
	}
	return nil
}

func openAssetDirectory(path string) (*os.Root, error) {
	// Windows 上从卷根（如 C:\）开始逐级打开；Unix 上卷名为空，就是 /。
	top := filepath.VolumeName(path) + string(filepath.Separator)
	directory, err := os.OpenRoot(top)
	if err != nil {
		return nil, errors.New("cannot open auth helper installation filesystem")
	}
	parts := strings.Split(strings.TrimPrefix(path, top), string(filepath.Separator))
	for index, name := range parts {
		if err := directory.Mkdir(name, 0700); err != nil && !os.IsExist(err) {
			directory.Close()
			return nil, errors.New("cannot create auth helper installation directory")
		}
		next, err := openChild(directory, name, index >= len(parts)-2)
		directory.Close()
		if err != nil {
			return nil, err
		}
		directory = next
	}
	return directory, nil
}

// openChild opens the directory name inside directory, rejecting symlinks and,
// when private is set, any mode other than 0700. It checks the opened identity,
// so a symlink/directory replacement between inspection and opening cannot
// redirect subsequent asset writes or reads.
func openChild(directory *os.Root, name string, private bool) (*os.Root, error) {
	info, err := directory.Lstat(name)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("auth helper installation path must contain only directories")
	}
	if private && !privatefs.IsPrivate(filepath.Join(directory.Name(), name), info) {
		return nil, errors.New("auth helper installation directories must be private")
	}
	next, err := directory.OpenRoot(name)
	if err != nil {
		return nil, errors.New("cannot open auth helper installation directory")
	}
	opened, openErr := next.Stat(".")
	after, afterErr := directory.Lstat(name)
	if openErr != nil || afterErr != nil || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, opened) || !os.SameFile(info, after) {
		next.Close()
		return nil, errors.New("auth helper installation directory changed while opening")
	}
	return next, nil
}
