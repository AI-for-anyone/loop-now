//go:build windows

package local

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// linkDir creates a directory junction on Windows: it needs no privilege to create symlinks, and the state directory
// and the Agent directory are both under the work root, on the same volume.
func linkDir(target, link string) error {
	if strings.ContainsAny(target+link, "\"&|<>^%") {
		return fmt.Errorf("the path contains characters that cannot be passed to mklink")
	}
	cmd := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	if out, err := exec.Command(cmd, "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		return fmt.Errorf("create the directory junction: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// linkFile creates a hard link on Windows: no privilege is needed on the same volume; Pi writes auth.json in place, so
// writes are visible through all links.
func linkFile(target, link string) error { return os.Link(target, link) }

// isLink reports whether path is the link itself (a symlink or a directory junction).
func isLink(path string, info fs.FileInfo) bool {
	if info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
		return true
	}
	_, err := os.Readlink(path)
	return err == nil
}
