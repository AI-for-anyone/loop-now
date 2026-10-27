package cliauth

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/clicredential"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/githubauth"
	"loop-now/internal/server/larkauth"
	"loop-now/internal/server/store"
)

type ServiceOptions struct{ LoginTimeout, RecordTimeout time.Duration }
type GitHubLoginHelper interface {
	Login(context.Context, func(githubauth.Authorization) error) (githubauth.Credential, error)
}
type LarkLoginHelper interface {
	Login(context.Context, larkauth.Application, func(larkauth.Authorization) error) (clicredential.Lark, error)
}
type loginWorker struct {
	project uuid.UUID
	account *uuid.UUID
	cli     string
	cancel  context.CancelFunc
	done    chan struct{}
}
type Service struct {
	Store           *store.Store
	Helper          LoginHelper
	GitHub          GitHubLoginHelper
	Lark            LarkLoginHelper
	LarkCoordinator *larkauth.Coordinator
	Coordinator     *Coordinator
	Options         ServiceOptions
	Account         func(context.Context, uuid.UUID, uuid.UUID) (cm.Account, error)
	Log             *slog.Logger
	mu              sync.Mutex
	closed          bool
	workers         map[uuid.UUID]loginWorker
	port            chan struct{}
}

func NewService(st *store.Store, helper LoginHelper, coordinator *Coordinator, o ServiceOptions, log *slog.Logger) *Service {
	if o.LoginTimeout <= 0 {
		o.LoginTimeout = 5 * time.Minute
	}
	if o.RecordTimeout <= 0 {
		o.RecordTimeout = 5 * time.Second
	}
	return &Service{Store: st, Helper: helper, GitHub: githubauth.NewClient(nil), Lark: larkauth.NewClient(nil), Coordinator: coordinator, Options: o, Log: log, workers: map[uuid.UUID]loginWorker{}, port: make(chan struct{}, 1)}
}
func (s *Service) Start(ctx context.Context, project uuid.UUID, in cm.CLIAuthStart) (cm.CLIAuthState, error) {
	if in.CLI == "lark-cli" {
		return s.startLark(ctx, project, in)
	}
	if in.SourceAccountID != nil || in.ExpectedSourceRevision != nil {
		return cm.CLIAuthState{}, apperr.Invalid("body", "该CLI不接受应用来源字段")
	}
	if in.OperationID == uuid.Nil || !(in.CLI == "gcloud" && in.AuthType == "google_user" || in.CLI == "gh" && in.AuthType == "github_user") {
		return cm.CLIAuthState{}, apperr.Invalid("body", "CLI登录字段无效")
	}
	if in.AccountID == nil {
		if in.ExpectedRevision != nil || in.CLI == "gh" && in.Name != nil || in.CLI == "gcloud" && (in.Name == nil || strings.TrimSpace(*in.Name) != *in.Name || *in.Name == "" || !utf8.ValidString(*in.Name) || utf8.RuneCountInString(*in.Name) > 200 || strings.IndexFunc(*in.Name, unicode.IsControl) >= 0) {
			return cm.CLIAuthState{}, apperr.Invalid("name", "CLI登录名称无效")
		}
	} else if *in.AccountID == uuid.Nil || in.ExpectedRevision == nil || *in.ExpectedRevision < 1 || in.Name != nil {
		return cm.CLIAuthState{}, apperr.Invalid("accountId", "CLI登录目标无效")
	}
	s.mu.Lock()
	if s.closed || in.CLI == "gcloud" && s.Helper == nil || in.CLI == "gh" && s.GitHub == nil {
		s.mu.Unlock()
		return cm.CLIAuthState{}, &apperr.Error{Status: 503, Code: apperr.CodeInternal, Message: "认证辅助组件暂不可用"}
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
func (s *Service) project(ctx context.Context, op cm.CLIAuthOperation) (cm.CLIAuthState, error) {
	out := cm.CLIAuthState{Operation: op}
	if op.Status != "pending" && op.Status != "awaiting_authorization" {
		out.Operation.AuthorizationURL = nil
		out.Operation.UserCode = nil
	}
	if op.Status == "succeeded" && op.AccountID != nil && s.Account != nil {
		// Operation is read first; account projection cannot predate its commit.
		account, e := s.Account(ctx, op.ProjectID, *op.AccountID)
		if e != nil {
			return out, e
		}
		out.Account = &account
	}
	return out, nil
}
func (s *Service) Get(ctx context.Context, project, id uuid.UUID) (cm.CLIAuthState, error) {
	op, e := s.Store.GetProjectCLIAuthOperation(ctx, project, id)
	if e != nil {
		return cm.CLIAuthState{}, e
	}
	return s.project(ctx, op)
}
func (s *Service) List(ctx context.Context, project uuid.UUID, account *uuid.UUID, cli ...string) (cm.CLIAuthOperations, error) {
	ops, e := s.Store.ListProjectCLIAuthOperations(ctx, project, account, cli...)
	return cm.CLIAuthOperations{Operations: ops}, e
}
func (s *Service) Cancel(ctx context.Context, project, id uuid.UUID) (cm.CLIAuthState, error) {
	op, e := s.Store.EndProjectCLIAuthOperation(ctx, project, id, "cancelled", "auth_cancelled")
	if e != nil {
		return cm.CLIAuthState{}, e
	}
	// Cancellation becomes observable only after the transaction commits.
	s.mu.Lock()
	w, ok := s.workers[id]
	s.mu.Unlock()
	if ok {
		w.cancel()
		select {
		case <-w.done:
		case <-ctx.Done():
			return cm.CLIAuthState{}, ctx.Err()
		}
	}
	return s.project(ctx, op)
}
func (s *Service) login(ctx context.Context, op cm.CLIAuthOperation, w loginWorker) {
	defer func() { w.cancel(); s.mu.Lock(); delete(s.workers, op.ID); s.mu.Unlock(); close(w.done) }()
	if op.CLI == "lark-cli" {
		s.larkLogin(ctx, op)
		return
	}
	if op.CLI == "gh" {
		s.githubLogin(ctx, op)
		return
	}
	select {
	case s.port <- struct{}{}:
		defer func() { <-s.port }()
	default:
		s.end(op, "failed", "callback_unavailable")
		return
	}
	credential, e := s.Helper.Login(ctx, op.ID, func(url string) error {
		if !ValidAuthorizationURL(url) {
			return &SafeError{Code: "invalid_response"}
		}
		record, cancel := context.WithTimeout(ctx, s.Options.RecordTimeout)
		defer cancel()
		updated, e := s.Store.SetProjectCLIAuthURL(record, op.ProjectID, op.ID, url)
		if e == nil && updated.Status != "awaiting_authorization" {
			return &SafeError{Code: "cancelled"}
		}
		return e
	})
	if e == nil && !credential.Valid() {
		e = &SafeError{Code: "invalid_response"}
	}
	if e == nil {
		plain, err := credential.Encode()
		if err != nil {
			e = err
		} else {
			record, cancel := context.WithTimeout(context.Background(), s.Options.RecordTimeout)
			label := credential.Account
			_, _, e = s.Store.CompleteProjectCLIAuthOperation(record, op.ProjectID, op.ID, store.ProjectCredentialWrite{AuthMethod: "oauth", AccountLabel: &label, TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1"}, Secret: plain})
			cancel()
			clear(plain)
		}
	}
	if e != nil {
		code := "helper_unavailable"
		status := "failed"
		var safe *SafeError
		if errors.As(e, &safe) {
			switch safe.Code {
			case "callback_unavailable", "invalid_response", "account_mismatch":
				code = safe.Code
			case "network":
				code = "network_error"
			case "http":
				code = "oauth_failed"
			case "reauth_required":
				code = "requires_login"
			case "cancelled":
				status, code = "cancelled", "auth_cancelled"
			case "timeout":
				status, code = "expired", "auth_timeout"
			}
		}
		if errors.Is(e, store.ErrCLIAuthExpired) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status, code = "expired", "auth_timeout"
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			status, code = "cancelled", "auth_cancelled"
		}
		s.end(op, status, code)
	}
}
func (s *Service) end(op cm.CLIAuthOperation, status, code string) {
	// A failed Complete may have exhausted its own deadline; use a fresh bound.
	ctx, cancel := context.WithTimeout(context.Background(), s.Options.RecordTimeout)
	defer cancel()
	if _, e := s.Store.EndProjectCLIAuthOperation(ctx, op.ProjectID, op.ID, status, code); e != nil && s.Log != nil {
		s.Log.Warn("CLI登录终态回写失败", "stage", "login_terminal_record", "operationId", op.ID.String(), "cli", op.CLI)
	}
}

func (s *Service) githubLogin(ctx context.Context, op cm.CLIAuthOperation) {
	stage := "github_protocol"
	credential, err := s.GitHub.Login(ctx, func(a githubauth.Authorization) error {
		record, cancel := context.WithTimeout(ctx, s.Options.RecordTimeout)
		defer cancel()
		updated, err := s.Store.SetProjectGitHubAuthorization(record, op.ProjectID, op.ID, a.URL, a.UserCode, a.ExpiresAt)
		if err == nil && updated.Status != "awaiting_authorization" {
			return context.Canceled
		}
		return err
	})
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if err == nil {
		stage = "github_credential"
		plain, encodeErr := credential.Encode()
		if encodeErr != nil {
			err = encodeErr
		} else {
			stage = "github_commit"
			record, cancel := context.WithTimeout(context.Background(), s.Options.RecordTimeout)
			label := credential.Identity.Login
			_, _, err = s.Store.CompleteProjectCLIAuthOperation(record, op.ProjectID, op.ID, store.ProjectCredentialWrite{AuthMethod: "oauth", AccountLabel: &label, GitHubIdentity: &credential.Identity, TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1", FieldNames: []string{"token"}, EnvNames: []string{"GH_TOKEN"}, FilePaths: []string{}}, Secret: plain})
			cancel()
			clear(plain)
		}
	}
	if err == nil {
		return
	}
	status, code, httpStatus := "failed", "helper_unavailable", 0
	var safe *githubauth.SafeError
	if errors.As(err, &safe) {
		switch safe.Code {
		case "invalid_response", "network_error", "oauth_failed":
			code = safe.Code
		case "auth_timeout":
			status, code = "expired", "auth_timeout"
		case "auth_cancelled":
			status, code = "cancelled", "auth_cancelled"
		}
		if safe.HTTPStatus >= 100 && safe.HTTPStatus <= 599 {
			httpStatus = safe.HTTPStatus
		}
	}
	if errors.Is(err, store.ErrCLIAuthExpired) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		status, code = "expired", "auth_timeout"
	} else if errors.Is(ctx.Err(), context.Canceled) {
		status, code = "cancelled", "auth_cancelled"
	}
	if s.Log != nil {
		s.Log.Warn("GitHub CLI登录未完成", "stage", stage, "category", code, "httpStatus", httpStatus, "operationId", op.ID.String())
	}
	s.end(op, status, code)
}
func (s *Service) CancelProject(ctx context.Context, project uuid.UUID) error {
	return s.CancelLogins(ctx, project, nil)
}
func (s *Service) CancelLogins(ctx context.Context, project uuid.UUID, account *uuid.UUID) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.Options.RecordTimeout+3*time.Second)
	defer cancel()
	s.mu.Lock()
	wait := []chan struct{}{}
	for _, w := range s.workers {
		if w.project == project && (account == nil || w.account != nil && *w.account == *account) {
			w.cancel()
			wait = append(wait, w.done)
		}
	}
	s.mu.Unlock()
	for _, done := range wait {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	wait := []chan struct{}{}
	type pendingGitHub struct {
		id     uuid.UUID
		worker loginWorker
	}
	github := []pendingGitHub{}
	for id, w := range s.workers {
		if w.cli == "gh" || w.cli == "lark-cli" {
			github = append(github, pendingGitHub{id, w})
		} else {
			w.cancel()
		}
		wait = append(wait, w.done)
	}
	s.mu.Unlock()
	var recordErr error
	for _, pending := range github {
		record, cancel := context.WithTimeout(ctx, s.Options.RecordTimeout)
		_, err := s.Store.EndProjectCLIAuthOperation(record, pending.worker.project, pending.id, "failed", "server_restarted")
		cancel()
		if err != nil {
			recordErr = errors.Join(recordErr, err)
			if s.Log != nil {
				s.Log.Warn("GitHub登录关闭状态写入失败", "stage", "github_shutdown_record", "operationId", pending.id.String())
			}
		}
		// A late successful result now observes the committed terminal state.
		pending.worker.cancel()
	}
	for _, done := range wait {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if s.Coordinator != nil {
		recordErr = errors.Join(recordErr, s.Coordinator.Close(ctx))
	}
	if s.LarkCoordinator != nil {
		recordErr = errors.Join(recordErr, s.LarkCoordinator.Close(ctx))
	}
	return recordErr
}
