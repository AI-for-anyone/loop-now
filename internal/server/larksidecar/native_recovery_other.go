//go:build !darwin

package larksidecar

func nativeBootID() (string, error) { return "", ErrNativeRecoveryUnknown }
func readNativeProcess(int) (NativeProcessIdentity, error) {
	return NativeProcessIdentity{}, ErrNativeRecoveryUnknown
}
