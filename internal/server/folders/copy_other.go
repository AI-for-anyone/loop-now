//go:build !darwin

package folders

// cloneTree always falls back to a plain copy on platforms without clonefile.
func cloneTree(src, dst string) error { return errCloneUnsupported }
