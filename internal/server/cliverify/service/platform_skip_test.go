package service

import (
	"runtime"
	"testing"
)

// skipWithoutCLITemplates skips verification tests that need to install CLI templates: the templates have no Windows
// install packages yet, so verification would end with verification_unsupported.
func skipWithoutCLITemplates(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the CLI templates have no Windows install packages yet")
	}
}
