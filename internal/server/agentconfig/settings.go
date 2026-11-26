package agentconfig

import (
	"context"
	"os"
	"path/filepath"
	"slices"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/store"
)

var permissionModes = []string{agentruntime.ModeSupervised, agentruntime.ModeAcceptEdits, agentruntime.ModeAuto, agentruntime.ModeFull}

func validMode(mode string) bool { return slices.Contains(permissionModes, mode) }

// modeFor is the permission mode a new Agent on provider gets: the one asked for, else the default mode (DefaultMode)
// when the runtime offers it, else Supervised when it offers that, else Full access (Pi).
func (s *Service) modeFor(ctx context.Context, provider string, asked *string) (string, error) {
	if asked != nil {
		if !validMode(*asked) {
			return "", apperr.Invalid("permissionMode", "Must be supervised, accept_edits, auto or full.")
		}
		return *asked, nil
	}
	mode := store.PermissionModeFull
	if s.DefaultMode != nil {
		var err error
		if mode, err = s.DefaultMode(ctx); err != nil {
			return "", err
		}
	}
	offered := agentruntime.ForProvider(provider).Capabilities.PermissionModes
	switch {
	case slices.Contains(offered, mode):
		return mode, nil
	case slices.Contains(offered, agentruntime.ModeSupervised):
		return agentruntime.ModeSupervised, nil
	}
	return agentruntime.ModeFull, nil
}

// validateSettings checks an Agent's working folder and permission mode against its runtime: the folder must be an
// existing folder given as an absolute path, on a runtime that can work in a folder the person picks; a mode other than
// Full access needs a runtime that can ask (a mode it lacks then works as Supervised). Pi can do neither.
func validateSettings(provider string, workingDirectory *string, mode string) error {
	rt := agentruntime.ForProvider(provider)
	if workingDirectory != nil {
		dir := *workingDirectory
		if !rt.Capabilities.WorkingDirectory {
			return apperr.Invalid("workingDirectory", rt.Name+" cannot work in a folder you pick. Choose another runtime, or clear the working folder.")
		}
		if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
			return apperr.Invalid("workingDirectory", "Must be an absolute path.")
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return apperr.Invalid("workingDirectory", "Must be an existing folder on this computer.")
		}
	}
	if !validMode(mode) {
		return apperr.Invalid("permissionMode", "Must be supervised, accept_edits, auto or full.")
	}
	offered := rt.Capabilities.PermissionModes
	if mode != agentruntime.ModeFull && !slices.Contains(offered, mode) && !slices.Contains(offered, agentruntime.ModeSupervised) {
		return apperr.Invalid("permissionMode", rt.Name+" runs with Full access only. Choose another runtime, or Full access.")
	}
	return nil
}
