package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"loop-now/internal/server/cliverify"
	"os"
	"os/exec"
	"strings"
	"time"
)

const guardFrameLimit = 256 << 10
const guardResultLimit = 4 << 10

type guardCommand struct {
	Protocol    int               `json:"protocol"`
	Type        string            `json:"type"`
	Executable  string            `json:"executable"`
	CWD         string            `json:"cwd"`
	Home        string            `json:"home"`
	Args        []string          `json:"args"`
	Env         map[string]string `json:"env"`
	Deadline    int64             `json:"deadlineUnixMs"`
	Grace       int64             `json:"termGraceMs"`
	InstanceID  string            `json:"instanceId"`
	OperationID string            `json:"operationId"`
	LeasePath   string            `json:"leasePath"`
}
type guardResult struct {
	Protocol int     `json:"protocol"`
	Type     string  `json:"type"`
	Reason   string  `json:"reason"`
	ExitCode *int    `json:"exitCode"`
	Signal   *string `json:"signal"`
}

func (s *Service) execute(ctx context.Context, spec CommandSpec, op cliverify.Operation, lease *os.File) privateOutput {
	fail := privateOutput{ExitCode: -1}
	if lease == nil {
		return fail
	}
	native := exec.Command(spec.Executable, spec.Args...)
	if s.Options.CommandFactory != nil {
		var e error
		native, e = s.Options.CommandFactory(ctx, spec)
		if e != nil || native == nil {
			return fail
		}
	}
	if len(native.ExtraFiles) != 0 {
		return fail
	}
	env := native.Env
	if env == nil {
		env = spec.Environment
	}
	values := map[string]string{}
	for _, value := range env {
		k, v, ok := strings.Cut(value, "=")
		if !ok || k == "" {
			return fail
		}
		values[k] = v
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(s.Options.ProbeTimeout)
	}
	frame := guardCommand{1, "command", native.Path, spec.Home, spec.Home, append([]string{}, native.Args[1:]...), values, deadline.UnixMilli(), s.Options.StopGrace.Milliseconds(), s.InstanceID.String(), op.ID.String(), lease.Name()}
	raw, e := json.Marshal(frame)
	if e != nil || len(raw)+1 > guardFrameLimit {
		clear(raw)
		return fail
	}
	raw = append(raw, '\n')
	defer clear(raw)
	controlR, controlW, e := os.Pipe()
	if e != nil {
		return fail
	}
	defer controlR.Close()
	defer controlW.Close()
	resultR, resultW, e := os.Pipe()
	if e != nil {
		return fail
	}
	defer resultR.Close()
	defer resultW.Close()
	guard := exec.Command(s.Options.NodeBin, s.Options.GuardEntry)
	guard.Env = []string{"HOME=" + spec.Home, "TMPDIR=" + spec.Home, "PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}
	guard.ExtraFiles = []*os.File{controlR, resultW, lease}
	sent := make(chan struct{})
	go func() { defer close(sent); _, _ = controlW.Write(raw) }()
	safe := make(chan []byte, 1)
	go func() { data, _ := io.ReadAll(io.LimitReader(resultR, guardResultLimit+1)); safe <- data }()
	output := runProcess(ctx, spec, s.Options.StopGrace+time.Second, func(context.Context, CommandSpec) (*exec.Cmd, error) { return guard, nil })
	// Close parent copies only after guard Wait, never signal EOF during a normal
	// command. Closing these also releases readers if guard failed to spawn.
	_ = controlW.Close()
	_ = controlR.Close()
	_ = resultW.Close()
	<-sent
	data := <-safe
	defer clear(data)
	var result guardResult
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	valid := len(data) <= guardResultLimit && dec.Decode(&result) == nil && dec.Decode(new(any)) == io.EOF && result.Protocol == 1 && result.Type == "result"
	if valid && result.Signal != nil {
		switch *result.Signal {
		case "SIGTERM", "SIGKILL", "SIGINT", "other":
		default:
			valid = false
		}
	}
	if !valid {
		output.ExitCode = -1
		return output
	}
	switch result.Reason {
	case "exit":
		if result.ExitCode == nil || *result.ExitCode < 0 || *result.ExitCode > 255 || *result.ExitCode != output.ExitCode {
			output.ExitCode = -1
		}
	case "output_limit":
		output.Overflow = true
	case "deadline":
		output.TimedOut = true
	case "parent_eof", "cancelled":
		output.Cancelled = true
	case "invalid_request", "spawn_failed":
		output.ExitCode = -1
	default:
		output.ExitCode = -1
	}
	return output
}
