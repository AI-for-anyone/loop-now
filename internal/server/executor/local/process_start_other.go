//go:build !darwin && !linux && !windows

package local

import "errors"

// processStart is not available on other platforms: pi.pid records no start time, and reclaiming leftovers falls back
// to comparing command lines.
func processStart(int) (string, error) { return "", errors.New("process start time is not supported") }
