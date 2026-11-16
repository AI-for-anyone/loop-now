// Package buildinfo provides the version number shared by the server and loopctl, used for the Loop-Now-Version
// response header and the client's version comparison.
//
// Release builds inject it with ldflags:
//
//	go build -ldflags "-X loop-now/internal/buildinfo.version=<version>" ./cmd/...
//
// Without injection, it is built from the VCS information Go writes into the binary as
// dev-<first 12 characters of the revision>, with -dirty added when the work tree has uncommitted changes; when
// there is no VCS information either, it is dev.
package buildinfo

import (
	"runtime/debug"
	"sync"
)

// version is injected with ldflags; it is not exported and can only be read through Version.
var version string

var fallback = sync.OnceValue(func() string {
	bi, ok := debug.ReadBuildInfo()
	return resolve("", bi, ok)
})

// Version returns the version of this program.
func Version() string {
	if version != "" {
		return version
	}
	return fallback()
}

// resolve computes the version from the injected value and the build information.
func resolve(injected string, bi *debug.BuildInfo, ok bool) string {
	if injected != "" {
		return injected
	}
	if !ok || bi == nil {
		return "dev"
	}
	var rev string
	var modified bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	v := "dev-" + rev
	if modified {
		v += "-dirty"
	}
	return v
}
