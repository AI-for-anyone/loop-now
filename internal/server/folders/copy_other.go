//go:build !darwin

package folders

// cloneTree 在没有 clonefile 的平台上总是退回普通复制。
func cloneTree(src, dst string) error { return errCloneUnsupported }
