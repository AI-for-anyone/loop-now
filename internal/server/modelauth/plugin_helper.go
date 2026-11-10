package modelauth

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	authhelper "loop-now/auth-helper"
	"loop-now/internal/privatefs"
	"loop-now/internal/procgroup"
	cm "loop-now/internal/server/credentialmodel"
)

// 插件提供方的订阅登录（plugins/SPEC.md §13）：一次登录一个辅助进程（auth-helper/provider-main.mjs），HOME 是这次登录的
// 新状态目录；授权地址、设备码与要人回答的问题经 PluginLoginUI 交给控制台，回答经 replies 交还辅助进程。

var (
	// ErrExtensionLoad 表示插件扩展加载失败或没有注册这个提供方的 OAuth 登录。
	ErrExtensionLoad = errors.New("插件扩展加载失败")
	// ErrInvalidAuthorizationURL 表示扩展给出的授权地址或设备码地址不合规。
	ErrInvalidAuthorizationURL = errors.New("授权地址无效")
)

// ValidPluginURL 是插件提供方授权地址与设备码地址的通用检查：https、没有用户名密码与 fragment、
// 查询里不带授权码或令牌、不超过 4096 字节。Codex 仍用 ValidAuthorizationURL 的严格白名单。
func ValidPluginURL(raw string) bool {
	if len(raw) > 4096 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return false
	}
	for k := range q {
		switch strings.ToLower(k) {
		case "code", "access_token", "refresh_token", "id_token", "token", "client_secret", "password":
			return false
		}
	}
	return true
}

// PluginLoginRequest 是一次插件提供方登录。
type PluginLoginRequest struct {
	OperationID uuid.UUID
	Provider    string
	// Extensions 是插件 Pi 扩展的绝对路径；Home 是这次登录的状态目录（辅助进程的 HOME）。
	Extensions []string
	Home       string
}

// PluginLoginUI 接收登录过程中要给人看的东西；返回错误时登录结束（视为取消）。
type PluginLoginUI interface {
	AuthURL(url string) error
	DeviceCode(code, uri string, expiresIn time.Duration) error
	Prompt(cm.LoginPrompt)
	PromptClosed(id string)
}

// PluginLoginHelper 运行一次插件提供方登录，成功时返回 login 的返回值（Pi 认证条目，JSON 对象）。
type PluginLoginHelper interface {
	Login(ctx context.Context, req PluginLoginRequest, ui PluginLoginUI, replies <-chan cm.LoginInput) ([]byte, error)
}

// PluginHelperOptions 是辅助进程的参数。
type PluginHelperOptions struct {
	NodeBin, Entry, WorkRoot, BasePath, PiPackageRoot, PiVersion string
	Timeout, StopGrace                                           time.Duration
}

// PluginHelper 用 auth-helper/provider-main.mjs 运行插件提供方登录。
type PluginHelper struct{ Options PluginHelperOptions }

type pluginFrame struct {
	Type              string                 `json:"type"`
	ID                string                 `json:"id"`
	Status            string                 `json:"status"`
	Code              string                 `json:"code"`
	AuthEntry         json.RawMessage        `json:"authEntry"`
	PromptID          string                 `json:"promptId"`
	Kind              string                 `json:"kind"`
	Message           string                 `json:"message"`
	Options           []cm.LoginPromptOption `json:"options"`
	ProtocolVersion   int                    `json:"protocolVersion"`
	ExpectedPiVersion string                 `json:"expectedPiVersion"`
	Event             struct {
		Type             string `json:"type"`
		URL              string `json:"url"`
		UserCode         string `json:"userCode"`
		VerificationURI  string `json:"verificationUri"`
		ExpiresInSeconds int64  `json:"expiresInSeconds"`
	} `json:"event"`
}

func (h *PluginHelper) Login(ctx context.Context, req PluginLoginRequest, ui PluginLoginUI, replies <-chan cm.LoginInput) ([]byte, error) {
	failed := func(stage string) error { return &helperFailure{cause: ErrHelperUnavailable, stage: stage} }
	if ctx.Err() != nil {
		return nil, ErrAuthCancelled
	}
	// 这次操作自己的临时目录与 PI_CODING_AGENT_DIR，不进状态目录。
	root, err := os.MkdirTemp(h.Options.WorkRoot, "provider-login-")
	if err != nil {
		return nil, failed("operation_directory")
	}
	defer os.RemoveAll(root)
	tmp, agentDir := filepath.Join(root, "tmp"), filepath.Join(root, "agent")
	for _, d := range []string{root, tmp, agentDir} {
		if err := os.MkdirAll(d, 0o700); err != nil || privatefs.MakePrivate(d) != nil {
			return nil, failed("operation_permissions")
		}
	}
	childIn, parentWrite, err := os.Pipe()
	if err != nil {
		return nil, failed("ipc_input_pipe")
	}
	defer childIn.Close()
	defer parentWrite.Close()
	parentRead, childOut, err := os.Pipe()
	if err != nil {
		return nil, failed("ipc_output_pipe")
	}
	defer parentRead.Close()
	defer childOut.Close()
	processCtx, kill := context.WithCancel(context.Background())
	defer kill()
	cmd, err := authhelper.ProviderCommand(processCtx, authhelper.ProviderProcessOptions{NodeBin: h.Options.NodeBin, Entry: h.Options.Entry, Home: req.Home, Temp: tmp, AgentDir: agentDir,
		BasePath: h.Options.BasePath, Input: childIn, Output: childOut})
	if err != nil {
		return nil, failed("helper_command")
	}
	expectedPiVersion := h.Options.PiVersion
	if expectedPiVersion == "" {
		expectedPiVersion = "1.4.0"
	}
	cmd.Env = append(cmd.Env, "LOOP_NOW_AUTH_PI_VERSION="+expectedPiVersion)
	cmd.Cancel = procgroup.Cancel(cmd)
	if err := procgroup.Start(cmd); err != nil {
		return nil, failed("helper_start")
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
	frames := make(chan pluginFrame, 8)
	go func() {
		defer close(frames)
		scan := bufio.NewScanner(parentRead)
		scan.Buffer(make([]byte, 4096), MaxFrame+1)
		for scan.Scan() {
			line := scan.Bytes()
			var f pluginFrame
			err := json.Unmarshal(line, &f)
			clear(line)
			if err != nil {
				return
			}
			select {
			case frames <- f:
			case <-processCtx.Done():
				return
			}
		}
	}()
	write := func(v any) error {
		data, err := json.Marshal(v)
		if err != nil || len(data) > MaxFrame {
			return ErrHelperUnavailable
		}
		defer clear(data)
		if err := parentWrite.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return ErrHelperUnavailable
		}
		if _, err := parentWrite.Write(append(data, '\n')); err != nil {
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
		return nil, ErrAuthExpired
	}
	deadline := time.NewTimer(timeout + 2*time.Second)
	defer deadline.Stop()
	id := req.OperationID.String()
	cancelled := ctx.Done()
	var grace <-chan time.Time
	sent := false
	for {
		select {
		case <-cancelled:
			cancelled = nil
			_ = write(map[string]any{"type": "abort", "id": id})
			stop := h.Options.StopGrace
			if stop <= 0 {
				stop = 2 * time.Second
			}
			grace = time.After(stop)
		case <-grace:
			return nil, ErrAuthCancelled
		case <-deadline.C:
			return nil, ErrAuthExpired
		case in := <-replies:
			if sent {
				err := write(map[string]any{"type": "prompt_reply", "id": id, "promptId": in.PromptID, "value": in.Value})
				in.Value = ""
				if err != nil {
					return nil, failed("ipc_reply_write")
				}
			}
		case f, ok := <-frames:
			if !ok {
				return nil, failed("ipc_eof")
			}
			if f.Type == "ready" {
				if sent || f.ProtocolVersion != 1 || f.ExpectedPiVersion != expectedPiVersion {
					return nil, failed("ready_protocol")
				}
				if err := write(map[string]any{"type": "login", "id": id, "provider": req.Provider, "extensions": req.Extensions, "piPackageRoot": h.Options.PiPackageRoot,
					"timeoutMs": max(int64(1), timeout.Milliseconds())}); err != nil {
					return nil, failed("ipc_request_write")
				}
				sent = true
				continue
			}
			if !sent || f.ID != id {
				return nil, failed("ipc_operation_identity")
			}
			switch f.Type {
			case "notify":
				switch f.Event.Type {
				case "auth_url":
					if !ValidPluginURL(f.Event.URL) {
						return nil, ErrInvalidAuthorizationURL
					}
					if ui.AuthURL(f.Event.URL) != nil {
						return nil, ErrAuthCancelled
					}
				case "device_code":
					if !ValidPluginURL(f.Event.VerificationURI) || f.Event.UserCode == "" || len(f.Event.UserCode) > 256 {
						return nil, ErrInvalidAuthorizationURL
					}
					if ui.DeviceCode(f.Event.UserCode, f.Event.VerificationURI, time.Duration(f.Event.ExpiresInSeconds)*time.Second) != nil {
						return nil, ErrAuthCancelled
					}
				}
			case "prompt":
				if f.Kind != "select" && f.Kind != "text" && f.Kind != "manual_code" {
					return nil, failed("prompt_kind")
				}
				options := f.Options
				if options == nil {
					options = []cm.LoginPromptOption{}
				}
				ui.Prompt(cm.LoginPrompt{ID: f.PromptID, Kind: f.Kind, Message: f.Message, Options: options})
			case "prompt_closed":
				ui.PromptClosed(f.PromptID)
			case "result":
				switch f.Status {
				case "succeeded":
					if len(f.AuthEntry) == 0 || ctx.Err() != nil {
						clear(f.AuthEntry)
						return nil, ErrAuthCancelled
					}
					return f.AuthEntry, nil
				case "cancelled":
					return nil, ErrAuthCancelled
				case "expired":
					return nil, ErrAuthExpired
				}
				switch f.Code {
				case "auth_port_busy":
					return nil, ErrCallbackPortBusy
				case "extension_load_failed":
					return nil, ErrExtensionLoad
				case "invalid_authorization_url":
					return nil, ErrInvalidAuthorizationURL
				}
				return nil, &helperFailure{cause: errors.New("auth_failed"), stage: "helper_provider_failed"}
			}
		}
	}
}
