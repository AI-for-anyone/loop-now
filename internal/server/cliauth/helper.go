package cliauth

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"loop-now/internal/server/clicredential"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"
)

type HelperRuntime struct{ SDKRoot, PythonRoot, Script string }
type HelperOptions struct {
	WorkRoot  string
	Runtime   func(context.Context) (HelperRuntime, error)
	StopGrace time.Duration
	// Command is an explicit fixture injection, never a runtime environment flag.
	Command func(context.Context, string, HelperRuntime) *exec.Cmd
}
type ProcessHelper struct{ Options HelperOptions }
type countDiscard struct{ count atomic.Int64 }

func (w *countDiscard) Write(b []byte) (int, error) { w.count.Add(int64(len(b))); return len(b), nil }
func shellQuote(s string) string {
	var b bytes.Buffer
	b.WriteByte('\'')
	for _, c := range s {
		if c == '\'' {
			b.WriteString("'\\''")
		} else {
			b.WriteRune(c)
		}
	}
	b.WriteByte('\'')
	return b.String()
}
func privatePythonCommand(r HelperRuntime, args ...string) *exec.Cmd {
	fw := r.PythonRoot
	script := "export DYLD_FRAMEWORK_PATH=" + shellQuote(filepath.Dir(fw)) + " DYLD_LIBRARY_PATH=" + shellQuote(filepath.Join(fw, "Versions/3.13/lib")) + " PYTHONHOME=" + shellQuote(filepath.Join(fw, "Versions/3.13")) + "; exec " + shellQuote(filepath.Join(fw, "Versions/3.13/bin/python3.13")) + " -S " + shellQuote(r.Script)
	for _, arg := range args {
		script += " " + shellQuote(arg)
	}
	return exec.Command("/bin/sh", "-c", script)
}
func (h *ProcessHelper) Login(ctx context.Context, id uuid.UUID, onURL func(string) error) (clicredential.Google, error) {
	return h.invoke(ctx, id, "login", nil, onURL)
}
func (h *ProcessHelper) Refresh(ctx context.Context, old clicredential.Google) (clicredential.Google, error) {
	return h.invoke(ctx, uuid.New(), "refresh", &old, nil)
}
func (h *ProcessHelper) invoke(ctx context.Context, id uuid.UUID, operation string, old *clicredential.Google, onURL func(string) error) (clicredential.Google, error) {
	fail := func(code string) (clicredential.Google, error) { return clicredential.Google{}, &SafeError{Code: code} }
	if h.Options.Runtime == nil {
		return fail("helper_unavailable")
	}
	runtime, e := h.Options.Runtime(ctx)
	if e != nil {
		return fail("helper_unavailable")
	}
	if ctx.Err() != nil {
		return fail("cancelled")
	}
	for _, p := range []string{runtime.SDKRoot, runtime.PythonRoot, runtime.Script, h.Options.WorkRoot} {
		if !filepath.IsAbs(p) {
			return fail("helper_unavailable")
		}
	}
	if e = os.MkdirAll(h.Options.WorkRoot, 0700); e != nil {
		return fail("helper_unavailable")
	}
	dir, e := os.MkdirTemp(h.Options.WorkRoot, "google-"+id.String()+"-")
	if e != nil {
		return fail("helper_unavailable")
	}
	defer os.RemoveAll(dir)
	config := filepath.Join(dir, "config")
	if e = os.Mkdir(config, 0700); e != nil {
		return fail("helper_unavailable")
	}
	inputR, inputW, e := os.Pipe()
	if e != nil {
		return fail("helper_unavailable")
	}
	defer inputR.Close()
	defer inputW.Close()
	outputR, outputW, e := os.Pipe()
	if e != nil {
		return fail("helper_unavailable")
	}
	defer outputR.Close()
	defer outputW.Close()
	cmd := privatePythonCommand(runtime)
	if h.Options.Command != nil {
		cmd = h.Options.Command(ctx, dir, runtime)
		if cmd == nil {
			return fail("helper_unavailable")
		}
	}
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + dir, "CLOUDSDK_CONFIG=" + config, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "PYTHONNOUSERSITE=1", "CLOUDSDK_CORE_DISABLE_USAGE_REPORTING=true", "CLOUDSDK_COMPONENT_MANAGER_DISABLE_UPDATE_CHECK=true"}
	cmd.ExtraFiles = []*os.File{inputR, outputW}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, stderr := &countDiscard{}, &countDiscard{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if ctx.Err() != nil {
		return fail("cancelled")
	}
	if e = cmd.Start(); e != nil {
		return fail("helper_unavailable")
	}
	inputR.Close()
	outputW.Close()
	// Cancel/deadline always reap the group; SDK server.timeout is insufficient.
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	cleaned := false
	stop := func() {
		if cleaned {
			return
		}
		cleaned = true
		select {
		case <-wait:
			return
		default:
		}
		syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		grace := h.Options.StopGrace
		if grace <= 0 {
			grace = time.Second
		}
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case <-wait:
		case <-timer.C:
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-wait
		}
	}
	defer stop()
	var credential json.RawMessage = []byte("null")
	var expected *string
	if old != nil {
		credential, e = old.Encode()
		if e != nil {
			return fail("invalid_response")
		}
		defer clear(credential)
		v := old.Account
		expected = &v
	}
	request := struct {
		Protocol        int             `json:"protocol"`
		Operation       string          `json:"operation"`
		SDKRoot         string          `json:"sdkRoot"`
		CallbackPort    int             `json:"callbackPort"`
		ExpectedAccount *string         `json:"expectedAccount"`
		Credential      json.RawMessage `json:"credential"`
	}{1, operation, runtime.SDKRoot, 8085, expected, credential}
	raw, e := json.Marshal(request)
	if e != nil {
		return fail("invalid_response")
	}
	raw = append(raw, '\n')
	written := make(chan error, 1)
	// The writer owns and erases its buffer, including cancellation while a
	// pipe write is blocked. Never erase bytes concurrently with Write.
	go func() { _, e := inputW.Write(raw); clear(raw); inputW.Close(); written <- e }()
	select {
	case e = <-written:
		if e != nil {
			return fail("helper_unavailable")
		}
	case <-ctx.Done():
		inputW.Close()
		<-written
		return fail("timeout")
	}
	type message struct {
		Type         string          `json:"type"`
		Stage        string          `json:"stage"`
		HTTPStatus   *int            `json:"httpStatus"`
		URL          string          `json:"url"`
		CallbackPort *int            `json:"callbackPort"`
		Credential   json.RawMessage `json:"credential"`
		Code         string          `json:"code"`
	}
	type frame struct {
		message message
		err     error
	}
	events := make(chan frame, 1)
	readCtx, cancelRead := context.WithCancel(context.Background())
	defer cancelRead()
	go func() {
		scanner := bufio.NewScanner(outputR)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			var msg message
			d := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
			d.DisallowUnknownFields()
			if d.Decode(&msg) != nil || d.Decode(new(any)) != io.EOF {
				select {
				case events <- frame{err: errors.New("invalid private frame")}:
				case <-readCtx.Done():
				}
				return
			}
			select {
			case events <- frame{message: msg}:
			case <-readCtx.Done():
				return
			}
		}
		select {
		case events <- frame{err: io.EOF}:
		case <-readCtx.Done():
		}
	}()
	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return fail("cancelled")
			}
			return fail("timeout")
		case received := <-events:
			if received.err != nil {
				return fail("helper_unavailable")
			}
			msg := received.message
			switch msg.Type {
			case "stage":
				switch msg.Stage {
				case "provider_loaded", "callback_ready", "exchange_started", "exchange_http", "credential_received", "credential_validated", "result_queued":
				default:
					return fail("invalid_response")
				}
				if msg.HTTPStatus != nil && (*msg.HTTPStatus < 100 || *msg.HTTPStatus > 599) {
					return fail("invalid_response")
				}
			case "authorization_url":
				if operation != "login" || onURL == nil || msg.CallbackPort == nil || *msg.CallbackPort != 8085 || !ValidAuthorizationURL(msg.URL) {
					return fail("invalid_response")
				}
				if e = onURL(msg.URL); e != nil {
					return fail("cancelled")
				}
			case "result":
				next, e := clicredential.DecodeGoogle(msg.Credential)
				clear(msg.Credential)
				if e != nil {
					return fail("invalid_response")
				}
				if old != nil && (next.Account != old.Account || next.ClientID != old.ClientID) {
					return fail("account_mismatch")
				}
				return next, nil
			case "error":
				switch msg.Code {
				case "callback_unavailable", "cancelled", "timeout", "network", "http", "invalid_response", "account_mismatch", "reauth_required", "helper_unavailable":
					return fail(msg.Code)
				default:
					return fail("helper_unavailable")
				}
			default:
				return fail("invalid_response")
			}
		}
	}
}
