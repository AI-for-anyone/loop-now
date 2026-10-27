package authhelper

import (
	"bytes"
	"os"
	"path/filepath"
)

// ValidateServiceTierEntry admits only the binary's own fixed adapter, never
// a project-supplied extension. Install materializes it with the other assets.
func ValidateServiceTierEntry(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	expected, err := assets.ReadFile("codex-service-tier.mjs")
	if err != nil || info.Size() != int64(len(expected)) {
		return false
	}
	actual, err := os.ReadFile(path)
	return err == nil && bytes.Equal(actual, expected)
}
