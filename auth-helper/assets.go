package authhelper

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

//go:embed *.mjs google_auth.py google_runtime.py google_verify.py
var assets embed.FS

// Install materializes the binary's fixed helper and platform extension assets.
// root must be an absolute canonical path inside the caller-owned work root.
// Existing ancestors are never chmod'd; the immediate parent and root must be
// private (0700). Symlinks anywhere in the directory path or asset targets are
// rejected. Each file is replaced atomically; no unrelated files are removed.
// Caller must not launch either entry if Install returns an error.
func Install(root string) (helperEntry, extensionEntry string, err error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == string(filepath.Separator) || strings.ContainsRune(root, 0) {
		return "", "", errors.New("auth helper install root must be an absolute canonical path")
	}
	directory, err := openAssetDirectory(root)
	if err != nil {
		return "", "", err
	}
	defer directory.Close()
	files, err := assets.ReadDir(".")
	if err != nil {
		return "", "", errors.New("cannot read embedded auth helper assets")
	}
	// Validate every existing target before publishing any replacement.
	for _, file := range files {
		if info, err := directory.Lstat(file.Name()); err == nil {
			if !info.Mode().IsRegular() {
				return "", "", errors.New("auth helper asset target must be a regular file")
			}
		} else if !os.IsNotExist(err) {
			return "", "", errors.New("cannot inspect auth helper asset target")
		}
	}
	for _, file := range files {
		content, err := assets.ReadFile(file.Name())
		if err != nil {
			return "", "", errors.New("cannot read embedded auth helper asset")
		}
		if err := replaceAsset(directory, file.Name(), content); err != nil {
			return "", "", err
		}
	}
	return filepath.Join(root, "main.mjs"), filepath.Join(root, "runtime-refresh.mjs"), nil
}

func openAssetDirectory(path string) (*os.Root, error) {
	directory, err := os.OpenRoot(string(filepath.Separator))
	if err != nil {
		return nil, errors.New("cannot open auth helper installation filesystem")
	}
	parts := strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator))
	for index, name := range parts {
		if err := directory.Mkdir(name, 0700); err != nil && !os.IsExist(err) {
			directory.Close()
			return nil, errors.New("cannot create auth helper installation directory")
		}
		info, err := directory.Lstat(name)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			directory.Close()
			return nil, errors.New("auth helper installation path must contain only directories")
		}
		if index >= len(parts)-2 && info.Mode().Perm() != 0700 {
			directory.Close()
			return nil, errors.New("auth helper installation root and parent must be private")
		}
		next, err := directory.OpenRoot(name)
		if err != nil {
			directory.Close()
			return nil, errors.New("cannot open auth helper installation directory")
		}
		// Check the opened identity, so a symlink/directory replacement between
		// inspection and opening cannot redirect subsequent asset writes.
		opened, openErr := next.Stat(".")
		after, afterErr := directory.Lstat(name)
		if openErr != nil || afterErr != nil || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, opened) || !os.SameFile(info, after) {
			next.Close()
			directory.Close()
			return nil, errors.New("auth helper installation directory changed while opening")
		}
		directory.Close()
		directory = next
	}
	return directory, nil
}

func replaceAsset(directory *os.Root, name string, content []byte) error {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return errors.New("cannot create auth helper asset stage")
	}
	stage := ".auth-helper-" + hex.EncodeToString(random[:])
	file, err := directory.OpenFile(stage, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("cannot create auth helper asset stage")
	}
	defer directory.Remove(stage) // exact owned stage only, including failure paths
	if _, err := file.Write(content); err != nil {
		file.Close()
		return errors.New("cannot write auth helper asset")
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return errors.New("cannot sync auth helper asset")
	}
	if err := file.Close(); err != nil {
		return errors.New("cannot close auth helper asset")
	}
	// Rename replaces the directory entry itself; it never follows a target link.
	if info, err := directory.Lstat(name); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("auth helper asset target must be a regular file")
		}
	} else if !os.IsNotExist(err) {
		return errors.New("cannot inspect auth helper asset target")
	}
	if err := directory.Rename(stage, name); err != nil {
		return errors.New("cannot publish auth helper asset")
	}
	return nil
}
