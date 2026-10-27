package modelauth

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/store"
)

type LoginHelper interface {
	Login(context.Context, uuid.UUID, func(string) error) (modelcredential.Credential, error)
}
type KeyInput struct {
	Provider              string `json:"provider"`
	Key                   string `json:"key"`
	ExpectedConfigVersion *int64 `json:"expectedConfigVersion,omitempty"`
}
type KeyController interface {
	PutKey(context.Context, uuid.UUID, KeyInput) (Metadata, error)
	Clear(context.Context, uuid.UUID) error
}
type ServiceOptions struct {
	LoginTimeout, RecordTimeout time.Duration
	LoginDrainTimeout           time.Duration
	MaxCredential               int64
}
type Service struct {
	Project     *ProjectService
	Store       *store.Store
	Helper      LoginHelper
	Coordinator *Coordinator
	Models      *modelcatalog.Catalog
	Options     ServiceOptions
	Log         *slog.Logger
	Changed     func(uuid.UUID)
	mu          sync.Mutex
	closed      bool
	operations  map[uuid.UUID]*loginWorker
	workers     sync.WaitGroup
}

type loginWorker struct {
	projectID uuid.UUID
	accountID *uuid.UUID
	agentID   uuid.UUID
	cancel    context.CancelFunc
	done      chan struct{}
}

func NewService(st *store.Store, h LoginHelper, c *Coordinator, models *modelcatalog.Catalog, o ServiceOptions, log *slog.Logger) *Service {
	if o.LoginTimeout <= 0 {
		o.LoginTimeout = 15 * time.Minute
	}
	if o.RecordTimeout <= 0 {
		o.RecordTimeout = 5 * time.Second
	}
	if o.MaxCredential <= 0 {
		o.MaxCredential = 4096
	}
	if o.LoginDrainTimeout <= 0 {
		grace := 2 * time.Second
		if helper, ok := h.(*Helper); ok && helper.Options.StopGrace > 0 {
			grace = helper.Options.StopGrace
		}
		// Abort pipe write <=5s, process reap <=2s, then one bounded terminal write.
		o.LoginDrainTimeout = grace + 7*time.Second + o.RecordTimeout + time.Second
	}
	return &Service{Store: st, Helper: h, Coordinator: c, Models: models, Options: o, Log: log, operations: map[uuid.UUID]*loginWorker{}}
}
func (s *Service) State(ctx context.Context, agentID uuid.UUID) (State, error) {
	if _, e := s.Store.GetAgent(ctx, agentID); e != nil {
		return State{}, e
	}
	operation, e := s.Store.LatestModelAuthOperation(ctx, agentID)
	if e != nil {
		return State{}, e
	}
	view := State{}
	if operation != nil {
		op, e := s.Get(ctx, agentID, operation.ID)
		if e != nil {
			return State{}, e
		}
		view.Operation = &op
	}
	// Read metadata after projecting the operation. A visible success must never
	// be paired with metadata from before that success's atomic pointer commit.
	meta, e := s.Metadata(ctx, agentID)
	if e != nil {
		return State{}, e
	}
	view.Credential = meta
	return view, nil
}
func (s *Service) Metadata(ctx context.Context, agentID uuid.UUID) (*Metadata, error) {
	if s.Project != nil {
		a, e := s.Store.GetAgent(ctx, agentID)
		if e != nil {
			return nil, e
		}
		if a.ProjectID != nil {
			return s.Project.CurrentModelMetadata(ctx, agentID)
		}
	}
	v, exists, e := s.Store.CurrentModelAuthVersion(ctx, agentID)
	if e != nil {
		return nil, e
	}
	if exists {
		if v == nil {
			return nil, nil
		}
		m := versionMetadata(*v)
		if _, e := modelcredential.Decode(v.Secret); e != nil {
			m.Status = "unavailable"
		}
		return &m, nil
	}
	// Compatibility inventory only. No conversion decision is inferred here.
	rows, e := s.Store.ListCredentials(ctx, agentID)
	if e != nil {
		return nil, e
	}
	for _, row := range rows {
		if row.Purpose == store.PurposeModel {
			method := "legacy"
			m := Metadata{Status: "set", UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339Nano), FieldNames: []string{}, EnvNames: row.EnvNames, FilePaths: []string{}, Warnings: []clitemplate.Warning{}, AuthMethod: &method}
			if m.EnvNames == nil {
				m.EnvNames = []string{}
			}
			return &m, nil
		}
	}
	return nil, nil
}
func versionMetadata(v store.ModelAuthVersion) Metadata {
	status := "set"
	if v.RefreshState == "unavailable" {
		status = "unavailable"
	}
	return Metadata{Status: status, UpdatedAt: v.UpdatedAt.UTC().Format(time.RFC3339Nano), FieldNames: []string{}, EnvNames: []string{}, FilePaths: []string{"auth.json"}, Warnings: []clitemplate.Warning{}, Provider: &v.Provider, AuthMethod: &v.AuthMethod, VersionID: &v.ID, AccountLabel: v.AccountLabel}
}
func (s *Service) PutKey(ctx context.Context, agentID uuid.UUID, input KeyInput) (Metadata, error) {
	if e := s.Store.RequirePrivateAgentCredentials(ctx, agentID); e != nil {
		return Metadata{}, e
	}
	p, ok := s.Models.Provider(input.Provider)
	if !ok {
		return Metadata{}, apperr.Invalid("provider", "未知提供方")
	}
	supported := false
	for _, method := range p.AuthMethods {
		if method == "api_key" {
			supported = true
		}
	}
	if !supported {
		return Metadata{}, apperr.Invalid("provider", "该提供方不支持API Key录入，请使用控制台登录")
	}
	credential, e := modelcredential.APIKey(input.Key)
	if e != nil {
		return Metadata{}, apperr.Invalid("key", "模型key不能为空或格式不符")
	}
	plain, e := credential.Encode()
	if e != nil {
		return Metadata{}, apperr.Invalid("key", "模型key格式无效")
	}
	defer clear(plain)
	if int64(len(plain)) > s.Options.MaxCredential {
		return Metadata{}, apperr.TooLarge("模型凭证超过上限")
	}
	v, e := s.Store.PutModelAuthKey(ctx, agentID, input.Provider, input.ExpectedConfigVersion, plain)
	if e != nil {
		return Metadata{}, e
	}
	if s.Changed != nil {
		s.Changed(agentID)
	}
	return versionMetadata(v), nil
}
func (s *Service) Clear(ctx context.Context, agentID uuid.UUID) error {
	e := s.Store.ClearModelAuthVersion(ctx, agentID)
	if e == nil && s.Changed != nil {
		s.Changed(agentID)
	}
	return e
}
func (s *Service) Start(ctx context.Context, agentID uuid.UUID, input LoginInput) (Operation, error) {
	if e := s.Store.RequirePrivateAgentCredentials(ctx, agentID); e != nil {
		return Operation{}, e
	}
	if input.Provider != "openai-codex" || input.ExpectedConfigVersion < 1 {
		return Operation{}, apperr.Invalid("provider", "只支持openai-codex控制台登录")
	}
	if s.Helper == nil {
		return Operation{}, ErrHelperUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Operation{}, ErrHelperUnavailable
	}
	op, e := s.Store.NewModelAuthOperation(ctx, agentID, input.Provider, input.ExpectedConfigVersion, time.Now().UTC().Add(s.Options.LoginTimeout))
	if errors.Is(e, store.ErrModelAuthBusy) {
		return Operation{}, apperr.Conflict("model_auth_busy", "该Agent已有登录操作，请先取消或等待")
	}
	if e != nil {
		return Operation{}, e
	}
	loginCtx, cancel := context.WithDeadline(context.Background(), op.ExpiresAt)
	work := &loginWorker{agentID: agentID, cancel: cancel, done: make(chan struct{})}
	s.operations[op.ID] = work
	s.workers.Add(1)
	go s.login(loginCtx, op, work)
	return operationView(op), nil
}
func (s *Service) login(ctx context.Context, op store.ModelAuthOperation, work *loginWorker) {
	defer s.workers.Done()
	defer work.cancel()
	defer func() { s.mu.Lock(); delete(s.operations, op.ID); s.mu.Unlock(); close(work.done) }()
	stage := "helper_login"
	credential, e := s.Helper.Login(ctx, op.ID, func(raw string) error {
		if !ValidAuthorizationURL(raw) {
			stage = "authorization_url"
			return ErrHelperUnavailable
		}
		record, stop := context.WithTimeout(context.Background(), s.Options.RecordTimeout)
		defer stop()
		next, e := s.Store.SetModelAuthOperationURL(record, op.ID, raw)
		if e != nil {
			stage = "operation_url_write"
			return e
		}
		if terminal(next.Status) {
			return ErrAuthCancelled
		}
		return nil
	})
	if e != nil && stage == "helper_login" {
		stage = helperDiagnostic(e)
	}
	record, stop := context.WithTimeout(context.Background(), s.Options.RecordTimeout)
	defer stop()
	if e == nil && ctx.Err() == nil && credential.Kind() == "oauth" {
		stage = "credential_encode"
		plain, err := credential.Encode()
		if err == nil {
			defer clear(plain)
			stage = "credential_commit"
			_, version, err := s.Store.CompleteModelAuthOperation(record, op.ID, nil, plain)
			if err == nil {
				if version != nil && s.Changed != nil {
					s.Changed(op.AgentID)
				}
				return
			}
		}
	}
	status, code := "failed", "auth_failed"
	switch {
	case errors.Is(e, ErrCallbackPortBusy):
		code = "auth_port_busy"
	case errors.Is(e, ErrAuthExpired) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		status, code = "expired", "auth_timeout"
	case errors.Is(e, ErrAuthCancelled) || ctx.Err() != nil:
		status, code = "cancelled", ""
	case errors.Is(e, ErrHelperUnavailable):
		code = "helper_unavailable"
	}
	if s.Log != nil {
		var failure *helperFailure
		diagnostic := providerDiagnostic{}
		if errors.As(e, &failure) {
			diagnostic = failure.provider
		}
		s.Log.Warn("模型登录未完成", "agentId", op.AgentID, "operationId", op.ID, "code", code, "stage", stage, "providerStage", diagnostic.Stage, "providerCategory", diagnostic.Category, "providerHTTPStatus", diagnostic.HTTPStatus)
	}
	stop()
	failureCtx, failureStop := context.WithTimeout(context.Background(), s.Options.RecordTimeout)
	defer failureStop()
	_, err := s.Store.EndModelAuthOperation(failureCtx, op.ID, status, code)
	if err != nil && s.Log != nil {
		s.Log.Warn("保存模型登录状态失败", "agentId", op.AgentID, "operationId", op.ID, "code", "auth_unavailable")
	}
}
func terminal(status string) bool { return status != "pending" && status != "awaiting_authorization" }
func (s *Service) Get(ctx context.Context, agentID, id uuid.UUID) (Operation, error) {
	a, e := s.Store.GetAgent(ctx, agentID)
	if e != nil {
		return Operation{}, e
	}
	op, e := s.Store.GetModelAuthOperation(ctx, agentID, id)
	if e != nil {
		return Operation{}, e
	}
	if !terminal(op.Status) {
		status, code := "", ""
		if !op.ExpiresAt.After(time.Now()) {
			status, code = "expired", "auth_timeout"
		} else if op.ConfigVersion != a.ConfigVersion || op.Provider != a.ModelProvider {
			status, code = "failed", "configuration_changed"
		}
		if status != "" {
			op, e = s.Store.EndModelAuthOperation(ctx, id, status, code)
			if e != nil {
				return Operation{}, e
			}
			s.cancel(id)
		}
	}
	return operationView(op), nil
}
func (s *Service) Cancel(ctx context.Context, agentID, id uuid.UUID) (Operation, error) {
	if _, e := s.Store.GetAgent(ctx, agentID); e != nil {
		return Operation{}, e
	}
	if _, e := s.Store.GetModelAuthOperation(ctx, agentID, id); e != nil {
		return Operation{}, e
	}
	op, e := s.Store.EndModelAuthOperation(ctx, id, "cancelled", "")
	if e == nil {
		s.cancel(id)
	}
	return operationView(op), e
}
func (s *Service) cancel(id uuid.UUID) {
	s.mu.Lock()
	work := s.operations[id]
	s.mu.Unlock()
	if work != nil {
		work.cancel()
	}
}
func operationView(op store.ModelAuthOperation) Operation {
	url := op.AuthorizationURL
	if terminal(op.Status) || url != nil && !ValidAuthorizationURL(*url) {
		url = nil
	}
	var message *string
	if op.ErrorCode != nil {
		safe := map[string]string{"auth_failed": "Codex授权未完成", "auth_port_busy": "认证回调端口1455已被占用，请结束自己的其他登录后重试", "auth_timeout": "登录操作已到期", "auth_unavailable": "模型账号认证不可用", "configuration_changed": "Agent配置已更改，请重新登录", "server_restarted": "服务已重启，请重新登录", "invalid_authorization_url": "授权地址无效", "helper_unavailable": "认证辅助组件不可用"}[*op.ErrorCode]
		if safe == "" {
			safe = "模型账号认证未完成"
		}
		message = &safe
	}
	return Operation{ID: op.ID, AgentID: op.AgentID, Provider: op.Provider, ConfigVersion: op.ConfigVersion, Status: op.Status, StartedAt: op.StartedAt, ExpiresAt: op.ExpiresAt, CompletedAt: op.CompletedAt, AuthorizationURL: url, ErrorCode: op.ErrorCode, ErrorMessage: message}
}
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	for _, work := range s.operations {
		work.cancel()
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	if s.Coordinator != nil {
		return s.Coordinator.Close(ctx)
	}
	return nil
}
