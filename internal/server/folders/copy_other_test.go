//go:build !darwin

package folders

import "testing"

func forceCopyFallback(*testing.T) {}

func cloneSupported(*testing.T, string) bool { return false }
