package service

import (
	"bytes"
	"context"
	"regexp"
	"time"

	"loop-now/internal/execenv"
)

var node24Version = regexp.MustCompile(`^v24\.[0-9]+\.[0-9]+$`)

func (s *Service) checkNodeRuntime(parent context.Context) bool {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	// This dependency check has no CLI credential, user script, inherited Node
	// options or CommandFactory. Unsupported Node does not stop other features.
	out := runProcess(ctx, CommandSpec{CLI: "node", Phase: "runtime", Executable: s.Options.NodeBin, Args: []string{"--version"}, Home: s.Options.WorkRoot, Environment: append(execenv.Base(s.Options.WorkRoot, ""), "HOME="+s.Options.WorkRoot, "PATH="+execenv.SystemPath(), "LANG=C")}, time.Second, nil)
	defer clear(out.Stdout)
	defer clear(out.Stderr)
	return out.ExitCode == 0 && !out.Overflow && !out.Cancelled && node24Version.Match(bytes.TrimSpace(out.Stdout))
}
