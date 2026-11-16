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
	codex := o.Provider == "openai-codex"
	valid := ValidAuthorizationURL
	if !codex {
		valid = ValidPluginURL
	}
	if terminal(o.Status) || o.AuthorizationURL != nil && !valid(*o.AuthorizationURL) {
		o.AuthorizationURL = nil
	}
	if terminal(o.Status) {
		o.DeviceCode, o.Prompt = nil, nil
	}
	if o.ErrorCode != nil {
		safe := operationView(store.ModelAuthOperation{Status: o.Status, ErrorCode: o.ErrorCode})
		o.ErrorMessage = safe.ErrorMessage
		if !codex {
			if m, ok := pluginErrorMessages[*o.ErrorCode]; ok {
				o.ErrorMessage = &m
			}
		}
	}
	return o
}

// pluginErrorMessages are the error messages for plugin provider logins (without the extension's original text).
var pluginErrorMessages = map[string]string{
	"auth_failed":               "Subscription authorization did not complete.",
	"auth_port_busy":            "The local port the login callback needs is already in use. Finish your other logins and try again.",
	"extension_load_failed":     "Could not load the plugin extension, or the extension does not register a login for this provider.",
	"plugin_unavailable":        "The plugin was uninstalled or turned off.",
	"invalid_authorization_url": "The authorization URL given by the plugin is not allowed.",
}

func (p *ProjectService) loginView(ctx context.Context, o cm.Operation) (cm.LoginState, error) {
	out := cm.LoginState{Operation: projectOperationView(p.withPrompt(o))}
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
	plugin := ""
	if in.Provider != "openai-codex" {
		var ok bool
		if plugin, ok = p.pluginProvider(in.Provider); !ok {
			return cm.LoginState{}, apperr.Invalid("provider", "Subscription login supports only openai-codex and plugin providers.")
		}
	}
	if in.AccountID == nil {
		if in.Name == nil || in.ExpectedRevision != nil {
			return cm.LoginState{}, apperr.Invalid("name", "A login for a new account requires name and must not have expectedRevision.")
		}
		if e := validateAccountName(*in.Name); e != nil {
			return cm.LoginState{}, e
		}
	} else if *in.AccountID == uuid.Nil || in.Name != nil || in.ExpectedRevision == nil || *in.ExpectedRevision < 1 {
		return cm.LoginState{}, apperr.Invalid("accountId", "Logging in again requires a valid accountId and an expectedRevision of at least 1, and no name.")
	}
	s := p.Auth
	if plugin != "" {
		if e := p.checkPluginLogin(ctx, project, plugin); e != nil {
			return cm.LoginState{}, e
		}
	} else if s.Helper == nil {
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
		if plugin != "" {
			go p.pluginLogin(loginCtx, op, work, plugin)
		} else {
			go p.login(loginCtx, op, work)
		}
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
		s.Log.Warn("project model login did not complete", "projectId", op.ProjectID, "operationId", op.ID, "code", code, "stage", stage, "providerStage", diagnostic.Stage, "category", diagnostic.Category, "httpStatus", diagnostic.HTTPStatus)
	}
	// A failed commit's deadline must never poison the independent terminal write.
	failure, stop := context.WithTimeout(context.Background(), s.Options.RecordTimeout)
	defer stop()
	if _, err := s.Store.EndProjectModelAuthOperation(failure, op.ProjectID, op.ID, status, code); err != nil && s.Log != nil {
		s.Log.Warn("failed to save the project login status", "projectId", op.ProjectID, "operationId", op.ID, "code", "auth_unavailable")
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
		out.Operations = append(out.Operations, projectOperationView(p.withPrompt(row)))
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
			s.Log.Warn("project login resource cleanup timed out", "operationId", id, "code", "auth_cleanup_timeout")
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
