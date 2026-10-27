package larksidecar

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const ExecURLEnv = "LOOP_NOW_LARK_EXEC_URL"
const ExecKeyEnv = "LOOP_NOW_LARK_EXEC_KEY"
const execPath = "/__loop_now/lark/exec/v1"
const execRequestLimit = 1 << 20
const execFrameLimit = 32 << 10
const execTimeout = 10 * time.Minute
const execCleanupTimeout = 5 * time.Second

type execRequest struct {
	Args []string `json:"args"`
}
type execFrame struct {
	Type     string `json:"type"`
	Data     []byte `json:"data,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`
	Error    string `json:"error,omitempty"`
}

type frameOutput struct {
	mu        sync.Mutex
	w         http.ResponseWriter
	enc       *json.Encoder
	remaining int64
	cancel    context.CancelFunc
	err       error
}
type frameStream struct {
	out    *frameOutput
	stream string
}

func (s frameStream) Write(p []byte) (int, error) {
	s.out.mu.Lock()
	defer s.out.mu.Unlock()
	if s.out.err != nil {
		return 0, s.out.err
	}
	if int64(len(p)) > s.out.remaining {
		s.out.err = errors.New("lark output limit")
		s.out.cancel()
		return 0, s.out.err
	}
	written := 0
	for len(p) > 0 {
		n := min(len(p), 8<<10)
		_ = http.NewResponseController(s.out.w).SetWriteDeadline(time.Now().Add(5 * time.Second))
		if e := s.out.enc.Encode(execFrame{Type: s.stream, Data: p[:n]}); e != nil {
			s.out.err = errors.New("lark output unavailable")
			s.out.cancel()
			return written, s.out.err
		}
		if e := http.NewResponseController(s.out.w).Flush(); e != nil {
			s.out.err = e
			s.out.cancel()
			return written, e
		}
		s.out.remaining -= int64(n)
		written += n
		p = p[n:]
	}
	return written, nil
}

func (s *ManagedScope) execute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.URL.ForceQuery || len(r.Header.Values("Authorization")) != 1 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.execKey)) != 1 {
		fail(w, 403)
		return
	}
	// Count from accepted preparation, including bounded body decoding and OS
	// admission. It remains active through output drain and actual child Wait.
	if s.begin() != nil {
		fail(w, 409)
		return
	}
	completionConfirmed := true // no process has been started yet
	defer func() {
		if completionConfirmed {
			s.end()
		}
	}()
	ctx, cancel := context.WithTimeout(r.Context(), execTimeout)
	defer cancel()
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second))
	raw, e := io.ReadAll(io.LimitReader(r.Body, execRequestLimit+1))
	if e != nil || len(raw) > execRequestLimit || !uniqueJSONKeys(raw) {
		fail(w, 400)
		return
	}
	var input execRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&input) != nil || dec.Decode(new(any)) != io.EOF || len(input.Args) == 0 || len(input.Args) > 4096 {
		fail(w, 400)
		return
	}
	// Command takes only this server-held captured configuration; no identity,
	// env, binary, hash or root is decoded from the request.
	cmd, e := s.broker.Command(ctx, s.cfg, input.Args...)
	if e != nil {
		fail(w, 503)
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 200 * time.Millisecond
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		e := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if e == syscall.ESRCH {
			return nil
		}
		return e
	}
	// No inherited stdin/exec capability: finite @file was already snapshotted by
	// Command. Native env is exclusively the Command clean environment.
	cmd.Stdin = nil
	w.Header().Set("Content-Type", "application/x-ndjson")
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Second))
	w.WriteHeader(200)
	if http.NewResponseController(w).Flush() != nil {
		return
	}
	out := &frameOutput{w: w, enc: json.NewEncoder(w), remaining: min(s.broker.limits.ResponseBytes, execOutputLimit), cancel: cancel}
	cmd.Stdout = frameStream{out: out, stream: "stdout"}
	cmd.Stderr = frameStream{out: out, stream: "stderr"}
	code := 2
	category := "lark_execution_rejected"
	if e = cmd.Start(); e == nil {
		completionConfirmed = false
		identity, identityErr := readNativeProcess(cmd.Process.Pid)
		s.manager.mu.Lock()
		s.started++
		if identityErr != nil {
			s.identityUnknown = true
		} else {
			if s.processes == nil {
				s.processes = make(map[int]NativeProcessIdentity)
			}
			s.processes[identity.PID] = identity
		}
		s.manager.mu.Unlock()
		e = cmd.Wait() // Never decrement consumer based on request count or Pi idle.
		completionConfirmed = confirmProcessGroupExit(cmd.Process.Pid)
		if completionConfirmed {
			s.manager.mu.Lock()
			s.confirmed++
			if identityErr == nil {
				s.lastNative = &identity
				delete(s.processes, identity.PID)
			}
			s.manager.mu.Unlock()
		}
		code = 0
		category = ""
		if e != nil {
			code = 2
			category = "lark_execution_failed"
			if exit, ok := e.(*exec.ExitError); ok {
				if exit.ExitCode() >= 0 {
					code = exit.ExitCode()
				}
			}
		}
	}
	out.mu.Lock()
	defer out.mu.Unlock()
	if ctx.Err() != nil || out.err != nil || !completionConfirmed {
		code = 2
		category = "lark_execution_failed"
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Second))
	_ = out.enc.Encode(execFrame{Type: "exit", ExitCode: &code, Error: category})
	_ = http.NewResponseController(w).Flush()
}

// A CLI must not leave an inherited child outside the native lifetime. Always
// terminate its owned group after Wait, including normal parent exit. Unknown
// completion keeps the consumer registered, rather than manufacturing idle.
func confirmProcessGroupExit(pid int) bool {
	e := syscall.Kill(-pid, syscall.SIGKILL)
	if e == syscall.ESRCH {
		return true
	}
	if e != nil {
		return false
	}
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		if syscall.Kill(-pid, 0) == syscall.ESRCH {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}
