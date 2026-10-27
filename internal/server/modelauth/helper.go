package modelauth

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	authhelper "loop-now/auth-helper"
	"loop-now/internal/server/modelcredential"
)

var (
	ErrHelperUnavailable = errors.New("认证辅助组件不可用")
	ErrAuthCancelled     = errors.New("认证操作已取消")
	ErrAuthExpired       = errors.New("认证操作已到期")
	ErrRefreshUncertain  = errors.New("凭证轮换结果不确定，不能重试旧令牌")
	ErrCallbackPortBusy  = errors.New("认证回调端口已被占用")
)

const MaxFrame = 1024 * 1024

func ValidAuthorizationURL(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Host != "auth.openai.com" || u.Path != "/oauth/authorize" || u.User != nil || u.Fragment != "" {
		return false
	}
	allowed := map[string]bool{"response_type": true, "client_id": true, "redirect_uri": true, "scope": true, "code_challenge": true, "code_challenge_method": true, "state": true, "id_token_add_organizations": true, "codex_cli_simplified_flow": true, "originator": true}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil || q.Get("state") == "" || q.Get("code_challenge") == "" {
		return false
	}
	if redirect := q.Get("redirect_uri"); redirect != "" && redirect != "http://localhost:1455/auth/callback" {
		return false
	}
	for k, values := range q {
		if !allowed[k] || len(values) != 1 {
			return false
		}
	}
	return true
}

type HelperOptions struct {
	NodeBin, Entry, WorkRoot, BasePath, PiPackageRoot string
	PiVersion                                         string
	Timeout, StopGrace                                time.Duration
}
type Helper struct{ Options HelperOptions }

func (h *Helper) Refresh(ctx context.Context, c modelcredential.Credential) (modelcredential.Credential, error) {
	return h.run(ctx, uuid.NewString(), "refresh", c, nil)
}
func (h *Helper) Login(ctx context.Context, id uuid.UUID, onURL func(string) error) (modelcredential.Credential, error) {
	return h.run(ctx, id.String(), "login", modelcredential.Credential{}, onURL)
}

type helperFrame struct {
	Diagnostic *struct {
		Stage      string `json:"stage"`
		Category   string `json:"category"`
		HTTPStatus int    `json:"httpStatus"`
	} `json:"diagnostic"`
	Stage      string          `json:"stage"`
	Category   string          `json:"category"`
	HTTPStatus int             `json:"httpStatus"`
	Type       string          `json:"type"`
	ID         string          `json:"id"`
	Status     string          `json:"status"`
	Code       string          `json:"code"`
	Credential json.RawMessage `json:"credential"`
	Event      struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"event"`
	ProtocolVersion   int    `json:"protocolVersion"`
	ExpectedPiVersion string `json:"expectedPiVersion"`
}

func (h *Helper) run(ctx context.Context, id, kind string, c modelcredential.Credential, onURL func(string) error) (modelcredential.Credential, error) {
	var diagnostic providerDiagnostic
	failed := func(stage string) error {
		return &helperFailure{cause: ErrHelperUnavailable, stage: stage, provider: diagnostic}
	}
	if ctx.Err() != nil {
		return modelcredential.Credential{}, ErrAuthCancelled
	}
	root, e := os.MkdirTemp(h.Options.WorkRoot, "auth-operation-")
	if e != nil {
		return modelcredential.Credential{}, failed("operation_directory")
	}
	defer os.RemoveAll(root)
	if e = os.Chmod(root, 0700); e != nil {
		return modelcredential.Credential{}, failed("operation_permissions")
	}
	childIn, parentWrite, e := os.Pipe()
	if e != nil {
		return modelcredential.Credential{}, failed("ipc_input_pipe")
	}
	defer childIn.Close()
	defer parentWrite.Close()
	parentRead, childOut, e := os.Pipe()
	if e != nil {
		return modelcredential.Credential{}, failed("ipc_output_pipe")
	}
	defer parentRead.Close()
	defer childOut.Close()
	processCtx, kill := context.WithCancel(context.Background())
	defer kill()
	cmd, e := authhelper.Command(processCtx, authhelper.ProcessOptions{NodeBin: h.Options.NodeBin, Entry: h.Options.Entry, Home: root, BasePath: h.Options.BasePath, Input: childIn, Output: childOut})
	if e != nil {
		return modelcredential.Credential{}, failed("helper_command")
	}
	expectedPiVersion := h.Options.PiVersion
	if expectedPiVersion == "" {
		expectedPiVersion = "1.4.0"
	}
	cmd.Env = append(cmd.Env, "LOOP_NOW_AUTH_PI_VERSION="+expectedPiVersion)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	if e = cmd.Start(); e != nil {
		return modelcredential.Credential{}, failed("helper_start")
	}
	childIn.Close()
	childOut.Close()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	defer func() {
		kill()
		parentRead.Close()
		parentWrite.Close()
		select {
		case <-exited:
		case <-time.After(2 * time.Second):
		}
	}()
	frames := make(chan helperFrame, 8)
	readStage := make(chan string, 1)
	go func() {
		stage := "ipc_eof"
		defer func() { readStage <- stage; close(frames) }()
		scan := bufio.NewScanner(parentRead)
		scan.Buffer(make([]byte, 4096), MaxFrame+1)
		for scan.Scan() {
			line := scan.Bytes()
			if len(line) > MaxFrame {
				clear(line)
				stage = "ipc_frame_limit"
				return
			}
			var f helperFrame
			if json.Unmarshal(line, &f) != nil {
				clear(line)
				stage = "ipc_frame_json"
				return
			}
			clear(line)
			select {
			case frames <- f:
			case <-processCtx.Done():
				stage = "ipc_stopped"
				return
			}
		}
		if err := scan.Err(); err != nil {
			stage = "ipc_read"
			if errors.Is(err, bufio.ErrTooLong) {
				stage = "ipc_frame_limit"
			}
		}
	}()
	write := func(v any) error {
		data, e := json.Marshal(v)
		if e != nil || len(data) > MaxFrame {
			return ErrHelperUnavailable
		}
		defer clear(data)
		data = append(data, '\n')
		if e = parentWrite.SetWriteDeadline(time.Now().Add(5 * time.Second)); e != nil {
			return ErrHelperUnavailable
		}
		_, e = parentWrite.Write(data)
		if e != nil {
			return ErrHelperUnavailable
		}
		return nil
	}
	timeout := h.Options.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < timeout {
		timeout = time.Until(deadline)
	}
	if timeout <= 0 {
		return modelcredential.Credential{}, ErrAuthExpired
	}
	deadline := time.NewTimer(timeout + 2*time.Second)
	defer deadline.Stop()
	cancelled := ctx.Done()
	var grace <-chan time.Time
	var graceTimer *time.Timer
	defer func() {
		if graceTimer != nil {
			graceTimer.Stop()
		}
	}()
	sent := false
	for {
		select {
		case <-cancelled:
			cancelled = nil
			_ = write(map[string]any{"type": "abort", "id": id})
			stopGrace := h.Options.StopGrace
			if stopGrace <= 0 {
				stopGrace = 2 * time.Second
			}
			graceTimer = time.NewTimer(stopGrace)
			grace = graceTimer.C
		case <-grace:
			if kind == "refresh" {
				return modelcredential.Credential{}, ErrRefreshUncertain
			}
			return modelcredential.Credential{}, ErrAuthCancelled
		case <-deadline.C:
			if kind == "refresh" {
				return modelcredential.Credential{}, ErrRefreshUncertain
			}
			return modelcredential.Credential{}, ErrAuthExpired
		case f, ok := <-frames:
			if !ok {
				if kind == "refresh" && sent {
					return modelcredential.Credential{}, ErrRefreshUncertain
				}
				return modelcredential.Credential{}, failed(<-readStage)
			}
			if f.Type == "ready" {
				if sent || f.ProtocolVersion != 1 || f.ExpectedPiVersion != expectedPiVersion {
					return modelcredential.Credential{}, failed("ready_protocol")
				}
				frame := map[string]any{"type": kind, "id": id, "provider": "openai-codex", "timeoutMs": max(int64(1), timeout.Milliseconds()), "piPackageRoot": h.Options.PiPackageRoot}
				var secret []byte
				if kind == "refresh" {
					secret, e = c.Encode()
					if e != nil {
						return modelcredential.Credential{}, failed("refresh_input_credential")
					}
					frame["credential"] = json.RawMessage(secret)
				}
				e = write(frame)
				clear(secret)
				if e != nil {
					return modelcredential.Credential{}, failed("ipc_request_write")
				}
				sent = true
				continue
			}
			if !sent || f.ID != id {
				return modelcredential.Credential{}, failed("ipc_operation_identity")
			}
			if f.Type == "diagnostic" {
				if kind == "login" {
					diagnostic.observe(f)
				}
				continue
			}
			if f.Type == "notify" && f.Event.Type == "auth_url" {
				if kind != "login" || !ValidAuthorizationURL(f.Event.URL) {
					return modelcredential.Credential{}, failed("authorization_url")
				}
				if onURL != nil {
					if e = onURL(f.Event.URL); e != nil {
						return modelcredential.Credential{}, ErrAuthCancelled
					}
				}
			}
			if f.Type != "result" {
				continue
			} // browser callback resolves the helper's private manual prompt.
			if kind == "login" && f.Diagnostic != nil {
				diagnostic.observe(helperFrame{Stage: f.Diagnostic.Stage, Category: f.Diagnostic.Category, HTTPStatus: f.Diagnostic.HTTPStatus})
			}
			if f.Status == "succeeded" {
				next, e := modelcredential.Decode(f.Credential)
				clear(f.Credential)
				if e != nil {
					return modelcredential.Credential{}, failed("result_credential_decode")
				}
				if kind == "login" && ctx.Err() != nil {
					return modelcredential.Credential{}, ErrAuthCancelled
				}
				if kind == "refresh" && !c.SameAccount(next) {
					return modelcredential.Credential{}, ErrRefreshUncertain
				}
				return next, nil
			}
			switch f.Code {
			case "callback_port_in_use":
				return modelcredential.Credential{}, ErrCallbackPortBusy
			}
			if kind == "refresh" {
				return modelcredential.Credential{}, ErrRefreshUncertain
			}
			switch f.Status {
			case "cancelled":
				return modelcredential.Credential{}, ErrAuthCancelled
			case "expired":
				return modelcredential.Credential{}, ErrAuthExpired
			}
			return modelcredential.Credential{}, failed(helperResultStage(f.Code))
		}
	}
}

// ValidateEntry refuses a runtime-controlled helper path. Deployment config is
// resolved once by app startup; Agent repositories never provide this component.
func ValidateEntry(path string) bool { return filepath.IsAbs(path) && !strings.ContainsRune(path, 0) }

var _ io.Closer = (*os.File)(nil)
