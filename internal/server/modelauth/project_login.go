package modelauth

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/store"
	"time"
)

func projectOperationView(o cm.Operation) cm.Operation {
	if terminal(o.Status) || o.AuthorizationURL != nil && !ValidAuthorizationURL(*o.AuthorizationURL) {
		o.AuthorizationURL = nil
	}
	if o.ErrorCode != nil {
		safe := operationView(store.ModelAuthOperation{Status: o.Status, ErrorCode: o.ErrorCode})
		o.ErrorMessage = safe.ErrorMessage
	}
	return o
}
func (p *ProjectService) loginView(ctx context.Context, o cm.Operation) (cm.LoginState, error) {
	out := cm.LoginState{Operation: projectOperationView(o)}
	id := o.AccountID
	if id == nil {
		id = o.TargetAccountID
	}
	if id != nil {
		account, e := p.Get(ctx, o.ProjectID, *id)
		if e != nil {
			return out, e
		}
		out.Account = &account
	}
	return out, nil
}
func (p *ProjectService) Start(ctx context.Context, project uuid.UUID, in cm.LoginStart) (cm.LoginState, error) {
	if in.Provider != "openai-codex" {
		return cm.LoginState{}, apperr.Invalid("provider", "仅支持Codex浏览器登录")
	}
	if in.AccountID == nil {
		if in.Name == nil || in.ExpectedRevision != nil {
			return cm.LoginState{}, apperr.Invalid("name", "新建登录须有名称且无目标版本")
		}
		if e := validateAccountName(*in.Name); e != nil {
			return cm.LoginState{}, e
		}
	} else if *in.AccountID == uuid.Nil || in.Name != nil || in.ExpectedRevision == nil || *in.ExpectedRevision < 1 {
		return cm.LoginState{}, apperr.Invalid("accountId", "重登需要账号ID与预期版本")
	}
	s := p.Auth
	if s.Helper == nil {
		return cm.LoginState{}, ErrHelperUnavailable
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return cm.LoginState{}, ErrHelperUnavailable
	}
	op, created, e := s.Store.NewProjectModelAuthOperation(ctx, project, in, time.Now().UTC().Add(s.Options.LoginTimeout))
	if e != nil {
		s.mu.Unlock()
		return cm.LoginState{}, e
	}
	if created {
		loginCtx, cancel := context.WithDeadline(context.Background(), op.ExpiresAt)
		work := &loginWorker{projectID: project, accountID: op.TargetAccountID, cancel: cancel, done: make(chan struct{})}
		s.operations[op.ID] = work
		s.workers.Add(1)
		go p.login(loginCtx, op, work)
	}
	s.mu.Unlock()
	return p.loginView(ctx, op)
}
func (p *ProjectService) login(ctx context.Context, op cm.Operation, work *loginWorker) {
	s := p.Auth
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
		next, e := s.Store.SetProjectModelAuthURL(record, op.ProjectID, op.ID, raw)
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
	if e == nil && ctx.Err() == nil && credential.Kind() == "oauth" {
		plain, encodeErr := credential.Encode()
		if encodeErr == nil {
			record, stop := context.WithTimeout(context.Background(), s.Options.RecordTimeout)
			stage = "credential_commit"
			_, saved, commitErr := s.Store.CompleteProjectModelAuthOperation(record, op.ProjectID, op.ID, store.ProjectCredentialWrite{AuthMethod: "oauth", AccountLabel: nil, Secret: plain})
			clear(plain)
			stop()
			if commitErr == nil {
				if saved != nil {
					p.changed(context.Background(), op.ProjectID)
				}
				return
			}
			e = commitErr
		} else {
			stage = "credential_encode"
			e = encodeErr
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
		diagnostic := providerDiagnostic{}
		var failure *helperFailure
		if errors.As(e, &failure) {
			diagnostic = failure.provider
		}
		s.Log.Warn("项目模型登录未完成", "projectId", op.ProjectID, "operationId", op.ID, "code", code, "stage", stage, "providerStage", diagnostic.Stage, "category", diagnostic.Category, "httpStatus", diagnostic.HTTPStatus)
	}
	// A failed commit's deadline must never poison the independent terminal write.
	failure, stop := context.WithTimeout(context.Background(), s.Options.RecordTimeout)
	defer stop()
	if _, err := s.Store.EndProjectModelAuthOperation(failure, op.ProjectID, op.ID, status, code); err != nil && s.Log != nil {
		s.Log.Warn("保存项目登录状态失败", "projectId", op.ProjectID, "operationId", op.ID, "code", "auth_unavailable")
	}
}
func (p *ProjectService) GetLogin(ctx context.Context, project, id uuid.UUID) (cm.LoginState, error) {
	o, e := p.Auth.Store.GetProjectModelAuthOperation(ctx, project, id)
	if e != nil {
		return cm.LoginState{}, e
	}
	if !terminal(o.Status) && !o.ExpiresAt.After(time.Now()) {
		o, e = p.Auth.Store.EndProjectModelAuthOperation(ctx, project, id, "expired", "auth_timeout")
		if e != nil {
			return cm.LoginState{}, e
		}
		p.Auth.cancelProjectLoginAndDrain(ctx, id)
	}
	return p.loginView(ctx, o)
}
func (p *ProjectService) ListLogins(ctx context.Context, project uuid.UUID, provider string, account *uuid.UUID) (cm.Operations, error) {
	rows, e := p.Auth.Store.ListProjectModelAuthOperations(ctx, project, provider, account)
	out := cm.Operations{Operations: []cm.Operation{}}
	if e != nil {
		return out, e
	}
	for _, row := range rows {
		out.Operations = append(out.Operations, projectOperationView(row))
	}
	return out, nil
}
func (p *ProjectService) CancelLogin(ctx context.Context, project, id uuid.UUID) (cm.LoginState, error) {
	if _, e := p.Auth.Store.GetProjectModelAuthOperation(ctx, project, id); e != nil {
		return cm.LoginState{}, e
	}
	o, e := p.Auth.Store.EndProjectModelAuthOperation(ctx, project, id, "cancelled", "")
	if e != nil {
		return cm.LoginState{}, e
	}
	p.Auth.cancelProjectLoginAndDrain(ctx, id)
	return p.loginView(ctx, o)
}

// A completed cancellation must release this operation's callback resources
// before the caller starts another login. Never cancel independent refreshes.
func (s *Service) cancelProjectLoginAndDrain(ctx context.Context, id uuid.UUID) {
	s.mu.Lock()
	w := s.operations[id]
	s.mu.Unlock()
	if w == nil {
		return
	}
	w.cancel()
	drain, stop := context.WithTimeout(context.WithoutCancel(ctx), s.Options.LoginDrainTimeout)
	defer stop()
	select {
	case <-w.done:
	case <-drain.Done():
		if s.Log != nil {
			s.Log.Warn("项目登录资源清理超时", "operationId", id, "code", "auth_cleanup_timeout")
		}
	}
}
func (s *Service) CancelProjectLogins(ctx context.Context, project uuid.UUID, account *uuid.UUID) error {
	s.mu.Lock()
	workers := []*loginWorker{}
	for _, w := range s.operations {
		if w.projectID == project && (account == nil || w.accountID != nil && *w.accountID == *account) {
			workers = append(workers, w)
		}
	}
	s.mu.Unlock()
	for _, w := range workers {
		w.cancel()
	}
	drain, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.Options.LoginDrainTimeout)
	defer cancel()
	for _, w := range workers {
		select {
		case <-w.done:
		case <-drain.Done():
			return drain.Err()
		}
	}
	return nil
}
