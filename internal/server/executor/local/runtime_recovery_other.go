//go:build !darwin

package local

// No best-effort process-name/ps fallback on an unverified platform.
func recoveryBootID() (string, error) { return "", ErrRuntimeStopUnconfirmed }
func readRecoveryProcess(int) (RuntimeProcessIdentity, error) {
	return RuntimeProcessIdentity{}, ErrRuntimeStopUnconfirmed
}
