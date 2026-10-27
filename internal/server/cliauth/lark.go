package cliauth

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/clicredential"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/larkauth"
	"loop-now/internal/server/store"
	"time"
)

func (s *Service) startLark(ctx context.Context, project uuid.UUID, in cm.CLIAuthStart) (cm.CLIAuthState, error) {
	b, e := json.Marshal(in)
	var v cm.CLIAuthStart
	if e != nil || json.Unmarshal(b, &v) != nil {
		return cm.CLIAuthState{}, apperr.Invalid("body", "Lark登录字段无效")
	}
	// The different-user relogin policy remains a product decision. Do not
	// implement either an identity gate or replacement through this unfinished
	// branch. New-account login is independent and fully implemented below.
	if in.AccountID != nil {
		return cm.CLIAuthState{}, &apperr.Error{Status: 503, Code: apperr.CodeInternal, Message: "Lark重登接线尚未就绪"}
	}
	s.mu.Lock()
	if s.closed || s.Lark == nil {
		s.mu.Unlock()
		return cm.CLIAuthState{}, &apperr.Error{Status: 503, Code: apperr.CodeInternal, Message: "Lark认证组件暂不可用"}
	}
	op, created, e := s.Store.NewProjectCLIAuthOperation(ctx, project, in, time.Now().Add(s.Options.LoginTimeout))
	if e != nil {
		s.mu.Unlock()
		return cm.CLIAuthState{}, e
	}
	if created {
		workerCtx, cancel := context.WithDeadline(context.Background(), op.ExpiresAt)
		w := loginWorker{project: project, account: in.AccountID, cli: in.CLI, cancel: cancel, done: make(chan struct{})}
		s.workers[op.ID] = w
		go s.login(workerCtx, op, w)
	}
	s.mu.Unlock()
	return s.project(ctx, op)
}
func (s *Service) larkLogin(ctx context.Context, op cm.CLIAuthOperation) {
	stage := "lark_source"
	row, e := s.Store.GetProjectLarkAuthSource(ctx, op.ProjectID, op.ID)
	var app larkauth.Application
	if e == nil {
		app, e = larkauth.DecodeApplication(row.Secret)
	}
	var c clicredential.Lark
	if e == nil {
		stage = "lark_device_flow"
		c, e = s.Lark.Login(ctx, app, func(a larkauth.Authorization) error {
			if !larkauth.ValidAuthorizationURL(app.Brand, a.URL) {
				return &larkauth.SafeError{Code: "invalid_response"}
			}
			record, cancel := context.WithTimeout(ctx, s.Options.RecordTimeout)
			defer cancel()
			updated, e := s.Store.SetProjectLarkAuthorization(record, op.ProjectID, op.ID, a.URL, a.UserCode, a.ExpiresAt)
			if e == nil && updated.Status != "awaiting_authorization" {
				return context.Canceled
			}
			return e
		})
	}
	if e == nil && (ctx.Err() != nil || !c.Valid() || c.Identity.Brand != app.Brand || c.Identity.AppID != app.AppID || c.AppSecret != app.AppSecret) {
		e = &larkauth.SafeError{Code: "invalid_response"}
	}
	if e == nil {
		stage = "lark_commit"
		plain, err := c.Encode()
		if err != nil {
			e = err
		} else {
			record, cancel := context.WithTimeout(context.Background(), s.Options.RecordTimeout)
			label := c.Identity.OpenID
			_, _, e = s.Store.CompleteProjectCLIAuthOperation(record, op.ProjectID, op.ID, store.ProjectCredentialWrite{AuthMethod: "oauth", AccountLabel: &label, LarkIdentity: &c.Identity, TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1", FieldNames: []string{}, EnvNames: []string{}, FilePaths: []string{}}, Secret: plain})
			cancel()
			clear(plain)
		}
	}
	if e == nil {
		return
	}
	status, code, httpStatus := "failed", "helper_unavailable", 0
	var safe *larkauth.SafeError
	if errors.As(e, &safe) {
		switch safe.Code {
		case "invalid_response", "network_error", "oauth_failed":
			code = safe.Code
		case "reauth_required":
			code = "requires_login"
		case "auth_cancelled":
			status, code = "cancelled", "auth_cancelled"
		case "auth_timeout":
			status, code = "expired", "auth_timeout"
		}
		if safe.HTTPStatus >= 100 && safe.HTTPStatus <= 599 {
			httpStatus = safe.HTTPStatus
		}
	}
	if errors.Is(e, store.ErrCLIAuthExpired) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		status, code = "expired", "auth_timeout"
	} else if errors.Is(ctx.Err(), context.Canceled) {
		status, code = "cancelled", "auth_cancelled"
	}
	if s.Log != nil {
		s.Log.Warn("Lark CLI登录未完成", "stage", stage, "category", code, "httpStatus", httpStatus, "operationId", op.ID.String())
	}
	s.end(op, status, code)
}
