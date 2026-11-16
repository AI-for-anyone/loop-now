package modelauth

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/providerstate"
	"loop-now/internal/server/store"
)

// Project logins for plugin providers (plugins/SPEC.md §13). The account is the project model account; each successful
// login creates one model auth version whose "credential" records only the plugin (modelcredential.PluginState); the
// tokens and the extension's state live in the state directory named after the version ID.

// PluginLogins is what plugin-provider logins need, shared by project accounts and platform Agents' own logins.
// Without it only Codex logins are supported.
type PluginLogins struct {
	Helper PluginLoginHelper
	States *providerstate.Manager
	// Extensions returns the absolute paths of the Pi extensions of the plugin's current version; returns not_found
	// when the plugin does not exist.
	Extensions func(ctx context.Context, plugin string) ([]string, error)

	mu  sync.Mutex
	ops map[uuid.UUID]*pluginLoginState
}

type pluginLoginState struct {
	mu      sync.Mutex
	prompt  *cm.LoginPrompt
	replies chan cm.LoginInput
	// device is the device code of a platform Agent login. That operation table has no device-code columns, so it
	// lives in memory only (project logins keep theirs in the database).
	device *cm.DeviceCode
}

// answer hands the person's answer to the helper process. Only the pending question can be answered, and a select
// answer must be one of its options.
func (st *pluginLoginState) answer(in cm.LoginInput) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	q := st.prompt
	if q == nil || q.ID != in.PromptID {
		return apperr.Invalid("promptId", "没有这个待回答的问题，请刷新后重试")
	}
	if q.Kind == "select" && !slices.ContainsFunc(q.Options, func(o cm.LoginPromptOption) bool { return o.ID == in.Value }) {
		return apperr.Invalid("value", "请从选项中选择")
	}
	select {
	case st.replies <- in:
		st.prompt = nil
		return nil
	default:
		return apperr.Invalid("promptId", "回答正在处理，请稍后重试")
	}
}

func (st *pluginLoginState) setPrompt(q cm.LoginPrompt) {
	st.mu.Lock()
	st.prompt = &q
	st.mu.Unlock()
}

func (st *pluginLoginState) closePrompt(id string) {
	st.mu.Lock()
	if st.prompt != nil && st.prompt.ID == id {
		st.prompt = nil
	}
	st.mu.Unlock()
}

// register records a plugin login in progress; unregister drops it when the login ends.
func (l *PluginLogins) register(id uuid.UUID) *pluginLoginState {
	state := &pluginLoginState{replies: make(chan cm.LoginInput, 4)}
	l.mu.Lock()
	if l.ops == nil {
		l.ops = map[uuid.UUID]*pluginLoginState{}
	}
	l.ops[id] = state
	l.mu.Unlock()
	return state
}

func (l *PluginLogins) unregister(id uuid.UUID) {
	l.mu.Lock()
	delete(l.ops, id)
	l.mu.Unlock()
}

func (l *PluginLogins) state(id uuid.UUID) *pluginLoginState {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ops[id]
}

// pluginLoginFailure maps a failed plugin-provider login to the operation's terminal status and error code.
func pluginLoginFailure(ctx context.Context, err error) (status, code string) {
	status, code = "failed", "auth_failed"
	switch {
	case errors.Is(err, errPluginUnavailable):
		code = "plugin_unavailable"
	case errors.Is(err, ErrExtensionLoad):
		code = "extension_load_failed"
	case errors.Is(err, ErrInvalidAuthorizationURL):
		code = "invalid_authorization_url"
	case errors.Is(err, ErrCallbackPortBusy):
		code = "auth_port_busy"
	case errors.Is(err, ErrAuthExpired) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		status, code = "expired", "auth_timeout"
	case errors.Is(err, ErrAuthCancelled) || ctx.Err() != nil:
		status, code = "cancelled", ""
	case errors.Is(err, ErrHelperUnavailable):
		code = "helper_unavailable"
	}
	return status, code
}

// pluginProvider returns the plugin that registered provider; ok is false when it is not a plugin provider with OAuth.
func (s *Service) pluginProvider(provider string) (string, bool) {
	if s.Models == nil {
		return "", false
	}
	v, ok := s.Models.Provider(provider)
	if !ok || v.Source != "plugin" || v.PluginID == nil || !slices.Contains(v.AuthMethods, "oauth") {
		return "", false
	}
	return *v.PluginID, true
}

func (p *ProjectService) pluginProvider(provider string) (string, bool) {
	return p.Auth.pluginProvider(provider)
}

func (p *ProjectService) pluginState(id uuid.UUID) *pluginLoginState {
	return p.Plugins.state(id)
}

// withPrompt attaches the question waiting for a person's answer to an in-progress plugin login operation.
func (p *ProjectService) withPrompt(o cm.Operation) cm.Operation {
	if terminal(o.Status) {
		return o
	}
	if st := p.pluginState(o.ID); st != nil {
		st.mu.Lock()
		if st.prompt != nil {
			q := *st.prompt
			o.Prompt = &q
		}
		st.mu.Unlock()
	}
	return o
}

// checkPluginLogin checks, before a plugin provider login starts, that the plugin can be used: the login components are
// installed and the plugin is enabled in this project.
func (p *ProjectService) checkPluginLogin(ctx context.Context, project uuid.UUID, plugin string) error {
	if p.Plugins == nil || p.Plugins.Helper == nil || p.Plugins.States == nil || p.Plugins.Extensions == nil {
		return ErrHelperUnavailable
	}
	on, err := p.Auth.Store.PluginEnabledIn(ctx, plugin, project)
	if err != nil {
		return err
	}
	if !on {
		return apperr.Conflict(apperr.CodePluginNotEnabled, "插件 "+plugin+" 没有在这个项目里启用，先启用再登录")
	}
	return nil
}

type pluginUI struct {
	p     *ProjectService
	op    cm.Operation
	state *pluginLoginState
}

func (u pluginUI) record() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), u.p.Auth.Options.RecordTimeout)
}

func (u pluginUI) AuthURL(url string) error {
	ctx, stop := u.record()
	defer stop()
	next, err := u.p.Auth.Store.SetProjectModelAuthURL(ctx, u.op.ProjectID, u.op.ID, url)
	if err != nil {
		return err
	}
	if terminal(next.Status) {
		return ErrAuthCancelled
	}
	return nil
}

func (u pluginUI) DeviceCode(code, uri string, expiresIn time.Duration) error {
	ctx, stop := u.record()
	defer stop()
	var expires *time.Time
	if expiresIn > 0 {
		t := time.Now().UTC().Add(expiresIn)
		expires = &t
	}
	next, err := u.p.Auth.Store.SetProjectModelAuthDeviceCode(ctx, u.op.ProjectID, u.op.ID, code, uri, expires)
	if err != nil {
		return err
	}
	if terminal(next.Status) {
		return ErrAuthCancelled
	}
	return nil
}

func (u pluginUI) Prompt(q cm.LoginPrompt) { u.state.setPrompt(q) }

func (u pluginUI) PromptClosed(id string) { u.state.closePrompt(id) }

// pluginLogin runs one plugin provider login: new state directory -> helper process login -> write auth.json and rename
// to the final directory -> commit the account and version.
func (p *ProjectService) pluginLogin(ctx context.Context, op cm.Operation, work *loginWorker, plugin string) {
	s := p.Auth
	defer s.workers.Done()
	defer work.cancel()
	state := p.Plugins.register(op.ID)
	defer func() {
		p.Plugins.unregister(op.ID)
		s.mu.Lock()
		delete(s.operations, op.ID)
		s.mu.Unlock()
		close(work.done)
	}()
	version := uuid.Must(uuid.NewV7())
	stage := "plugin_extensions"
	var err error
	var extensions []string
	if extensions, err = p.Plugins.Extensions(ctx, plugin); err == nil {
		stage = "state_directory"
		var home string
		if home, err = p.Plugins.States.Begin(version); err == nil {
			stage = "helper_login"
			var entry []byte
			entry, err = p.Plugins.Helper.Login(ctx, PluginLoginRequest{OperationID: op.ID, Provider: op.Provider, Extensions: extensions, Home: home}, pluginUI{p: p, op: op, state: state}, state.replies)
			if err == nil && ctx.Err() == nil {
				stage = "credential_commit"
				err = p.publishPluginLogin(op, plugin, version, entry)
				clear(entry)
				if err == nil || errors.Is(err, providerstate.ErrNotSaved) {
					return
				}
			} else if err == nil {
				err = ErrAuthCancelled
			}
		}
	} else if store.IsNotFound(err) {
		err = errPluginUnavailable
	}
	p.Plugins.States.Discard(version)
	if errors.Is(err, ErrAuthCancelled) && ctx.Err() == nil {
		err = ErrAuthCancelled
	}
	status, code := pluginLoginFailure(ctx, err)
	if s.Log != nil {
		s.Log.Warn("插件提供方登录未完成", "projectId", op.ProjectID, "operationId", op.ID, "provider", op.Provider, "plugin", plugin, "code", code, "stage", stage, "detail", helperDiagnostic(err))
	}
	record, stop := context.WithTimeout(context.Background(), s.Options.RecordTimeout)
	defer stop()
	if _, err := s.Store.EndProjectModelAuthOperation(record, op.ProjectID, op.ID, status, code); err != nil && s.Log != nil {
		s.Log.Warn("保存项目登录状态失败", "projectId", op.ProjectID, "operationId", op.ID, "code", "auth_unavailable")
	}
}

var errPluginUnavailable = errors.New("插件不可用")

// publishPluginLogin turns the state directory of a successful login into a final version, under the same lock as the
// database commit.
func (p *ProjectService) publishPluginLogin(op cm.Operation, plugin string, version uuid.UUID, entry []byte) error {
	s := p.Auth
	credential, err := modelcredential.PluginStateOf(plugin)
	if err != nil {
		return err
	}
	plain, err := credential.Encode()
	if err != nil {
		return err
	}
	defer clear(plain)
	saved := false
	err = p.Plugins.States.Publish(version, op.Provider, entry, func() error {
		record, stop := context.WithTimeout(context.Background(), s.Options.RecordTimeout)
		defer stop()
		_, rec, err := s.Store.CompleteProjectModelAuthOperation(record, op.ProjectID, op.ID, store.ProjectCredentialWrite{VersionID: version, AuthMethod: "oauth", Secret: plain})
		if err != nil {
			return err
		}
		if rec == nil {
			return providerstate.ErrNotSaved
		}
		saved = true
		return nil
	})
	if err == nil && saved {
		p.changed(context.Background(), op.ProjectID)
	}
	return err
}

// Input hands a person's answer to a login question to the helper process. Only the question currently being waited on
// can be answered; the answer is passed on only in memory and is never saved or logged.
func (p *ProjectService) Input(ctx context.Context, project, id uuid.UUID, in cm.LoginInput) (cm.LoginState, error) {
	o, err := p.Auth.Store.GetProjectModelAuthOperation(ctx, project, id)
	if err != nil {
		return cm.LoginState{}, err
	}
	if len(in.Value) > 4096 {
		return cm.LoginState{}, apperr.Invalid("value", "回答不能超过 4096 字节")
	}
	st := p.pluginState(id)
	if terminal(o.Status) || st == nil {
		return cm.LoginState{}, apperr.Invalid("promptId", "这次登录没有在等回答")
	}
	if err := st.answer(in); err != nil {
		return cm.LoginState{}, err
	}
	return p.loginView(ctx, o)
}
