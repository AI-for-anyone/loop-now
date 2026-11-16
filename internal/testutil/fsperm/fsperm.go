// Package fsperm is for tests: it opens a directory or file up to everyone, so a test can check that the code under
// test really tightens it again.
// On Windows the temporary directory lives under the user directory and is already accessible only to the current
// user; without opening it up first, there is no telling whether the code tightened it or it was inherited from the
// parent.
package fsperm

import "testing"

// Open makes path accessible to everyone: on Unix 0777 for a directory and 0666 for a file; on Windows it adds an
// ACE that allows Everyone full access, inheritable on a directory, so anything created in it later is also
// accessible to everyone.
func Open(t testing.TB, path string) {
	t.Helper()
	if err := open(path); err != nil {
		t.Fatalf("open up the permissions of %s: %v", path, err)
	}
}
