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

// 插件提供方的项目登录（plugins/SPEC.md §13）。账号就是项目模型账号；一次成功登录产生一个模型认证版本，
// 它的“凭证”只记下插件（modelcredential.PluginState），令牌与扩展的状态在以版本 ID 命名的状态目录里。

// PluginLogins 是 ProjectService 支持插件提供方登录所需的东西；为空时只支持 Codex。
type PluginLogins struct {
	Helper PluginLoginHelper
	States *providerstate.Manager
	// Extensions 返回插件当前版本 Pi 扩展的绝对路径；插件不存在时返回 not_found。
	Extensions func(ctx context.Context, plugin string) ([]string, error)

	mu  sync.Mutex
	ops map[uuid.UUID]*pluginLoginState
}

type pluginLoginState struct {
	mu      sync.Mutex
	prompt  *cm.LoginPrompt
	replies chan cm.LoginInput
}

// pluginProvider 返回注册了 provider 的插件；不是带 OAuth 的插件提供方时 ok 为 false。
func (p *ProjectService) pluginProvider(provider string) (string, bool) {
	if p.Auth.Models == nil {
		return "", false
	}
	v, ok := p.Auth.Models.Provider(provider)
	if !ok || v.Source != "plugin" || v.PluginID == nil || !slices.Contains(v.AuthMethods, "oauth") {
		return "", false
	}
	return *v.PluginID, true
}

func (p *ProjectService) pluginState(id uuid.UUID) *pluginLoginState {
	if p.Plugins == nil {
		return nil
	}
	p.Plugins.mu.Lock()
	defer p.Plugins.mu.Unlock()
	return p.Plugins.ops[id]
}

// withPrompt 给进行中的插件登录操作带上等人回答的问题。
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

// checkPluginLogin 在开始插件提供方登录前检查插件可用：已装好登录组件、插件在这个项目里启用。
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

func (u pluginUI) Prompt(q cm.LoginPrompt) {
	u.state.mu.Lock()
	u.state.prompt = &q
	u.state.mu.Unlock()
}

func (u pluginUI) PromptClosed(id string) {
	u.state.mu.Lock()
	if u.state.prompt != nil && u.state.prompt.ID == id {
		u.state.prompt = nil
	}
	u.state.mu.Unlock()
}

// pluginLogin 运行一次插件提供方登录：新状态目录 → 辅助进程登录 → 写 auth.json 并改名为正式目录 → 提交账号与版本。
func (p *ProjectService) pluginLogin(ctx context.Context, op cm.Operation, work *loginWorker, plugin string) {
	s := p.Auth
	defer s.workers.Done()
	defer work.cancel()
	state := &pluginLoginState{replies: make(chan cm.LoginInput, 4)}
	p.Plugins.mu.Lock()
	if p.Plugins.ops == nil {
		p.Plugins.ops = map[uuid.UUID]*pluginLoginState{}
	}
	p.Plugins.ops[op.ID] = state
	p.Plugins.mu.Unlock()
	defer func() {
		p.Plugins.mu.Lock()
		delete(p.Plugins.ops, op.ID)
		p.Plugins.mu.Unlock()
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
	status, code := "failed", "auth_failed"
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

// publishPluginLogin 把登录成功的状态目录变成正式版本，与数据库提交在同一把锁下。
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

// Input 把人对登录中问题的回答交给辅助进程。只能回答当前正在等的那个问题；回答只在内存里转交，不保存、不记日志。
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
	st.mu.Lock()
	q := st.prompt
	if q == nil || q.ID != in.PromptID {
		st.mu.Unlock()
		return cm.LoginState{}, apperr.Invalid("promptId", "没有这个待回答的问题，请刷新后重试")
	}
	if q.Kind == "select" && !slices.ContainsFunc(q.Options, func(o cm.LoginPromptOption) bool { return o.ID == in.Value }) {
		st.mu.Unlock()
		return cm.LoginState{}, apperr.Invalid("value", "请从选项中选择")
	}
	select {
	case st.replies <- in:
		st.prompt = nil
	default:
		st.mu.Unlock()
		return cm.LoginState{}, apperr.Invalid("promptId", "回答正在处理，请稍后重试")
	}
	st.mu.Unlock()
	return p.loginView(ctx, o)
}
