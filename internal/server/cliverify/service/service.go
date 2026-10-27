// Package service runs the fixed, persisted CLI verification operations.
package service

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"log/slog"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/cliauth"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/cliverify"
	"loop-now/internal/server/store"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Options struct {
	Concurrency                                            int
	WorkRoot, GoogleEntry, NodeBin, GuardEntry             string
	InstallTimeout, ProbeTimeout, RecordTimeout, StopGrace time.Duration
	CommandFactory                                         CommandFactory // Explicit Go-only offline fixture, never HTTP/configurable.
}
type job struct {
	project, account uuid.UUID
	cancel           context.CancelFunc
	done             chan struct{}
}
type Service struct {
	InstanceID uuid.UUID
	Store      *store.Store
	Templates  *clitemplate.Catalog
	Installer  *clitemplate.Installer
	Google     *cliauth.Coordinator
	Options    Options
	registry   *Registry
	log        *slog.Logger
	mu         sync.Mutex
	jobs       map[uuid.UUID]*job
	closed     bool
	nodeReady  bool
	stop       context.CancelFunc
	scanDone   chan struct{}
	slots      chan struct{}
}

func New(ctx context.Context, st *store.Store, templates *clitemplate.Catalog, installer *clitemplate.Installer, google *cliauth.Coordinator, o Options, log *slog.Logger) (*Service, error) {
	if st == nil || templates == nil || installer == nil || !filepath.IsAbs(o.WorkRoot) || !filepath.IsAbs(o.NodeBin) || !filepath.IsAbs(o.GuardEntry) {
		return nil, errors.New("CLI验证依赖无效")
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 4
	}
	if o.InstallTimeout <= 0 {
		o.InstallTimeout = 5 * time.Minute
	}
	if o.ProbeTimeout <= 0 {
		o.ProbeTimeout = 30 * time.Second
	}
	if o.RecordTimeout <= 0 {
		o.RecordTimeout = 5 * time.Second
	}
	if o.StopGrace <= 0 {
		o.StopGrace = 2 * time.Second
	}
	if o.StopGrace > 5*time.Second {
		o.StopGrace = 5 * time.Second
	}
	if o.StopGrace < time.Millisecond {
		o.StopGrace = time.Millisecond
	}
	canonical, e := filepath.EvalSymlinks(o.WorkRoot)
	if e != nil {
		return nil, e
	}
	o.WorkRoot = canonical
	registry, e := NewRegistry()
	if e != nil {
		return nil, e
	}
	if e = st.RecoverCLIVerifications(ctx); e != nil {
		return nil, e
	}
	base, stop := context.WithCancel(context.Background())
	s := &Service{Store: st, Templates: templates, Installer: installer, Google: google, Options: o, registry: registry, log: log, jobs: map[uuid.UUID]*job{}, stop: stop, scanDone: make(chan struct{}), slots: make(chan struct{}, o.Concurrency)}
	s.InstanceID = uuid.New()
	if st.InstanceID != nil && *st.InstanceID != uuid.Nil {
		s.InstanceID = *st.InstanceID
	}
	if e = s.privateRoots(); e != nil {
		stop()
		return nil, e
	}
	if e = s.recoverHomes(); e != nil {
		stop()
		return nil, e
	}
	s.nodeReady = s.checkNodeRuntime(ctx)
	st.VerificationInvalidated = s.invalidate
	go s.scan(base)
	return s, nil
}
func manager(ctx context.Context) (string, *uuid.UUID, error) {
	p, ok := auth.From(ctx)
	if !ok || (p.Kind != auth.KindConsole && p.Kind != auth.KindExternal) {
		return "", nil, apperr.Forbidden("该身份不能管理凭证验证")
	}
	if p.Kind == auth.KindExternal {
		if p.ID == uuid.Nil {
			return "", nil, apperr.Forbidden("外部身份无效")
		}
		return string(p.Kind), &p.ID, nil
	}
	return string(p.Kind), nil, nil
}
func (s *Service) Start(ctx context.Context, p, a uuid.UUID, in cliverify.Start) (cliverify.Operation, error) {
	kind, actor, e := manager(ctx)
	if e != nil {
		return cliverify.Operation{}, e
	}
	if e = in.Validate(); e != nil {
		return cliverify.Operation{}, e
	}
	// Preserve idempotent recovery even if the current version/template changed.
	old, e := s.Store.GetCLIVerification(ctx, p, a, in.OperationID)
	if e == nil {
		if old.VersionID != in.ExpectedVersionID {
			er := apperr.Conflict(apperr.CodeVerificationOperationConflict, "验证操作目标不匹配")
			er.Details = cliverify.ConflictDetails{ProjectID: p, AccountID: a, OperationID: old.ID, VersionID: old.VersionID}
			return old, er
		}
		return old, nil
	}
	if !apperr.Is(e, apperr.CodeNotFound) {
		return old, e
	}
	record, e := s.Store.GetProjectCredential(ctx, p, a)
	if e != nil {
		return old, e
	}
	if record.View.DeletedAt != nil {
		return old, apperr.NotFound("凭证账号")
	}
	if record.View.Purpose == store.PurposeCLI && (record.View.CurrentVersionID == nil || *record.View.CurrentVersionID != in.ExpectedVersionID) {
		err := apperr.PreconditionFailed("CLI凭证版本已更改，请重新读取")
		err.Details = cliverify.VersionConflictDetails{ProjectID: p, AccountID: a, ExpectedVersionID: in.ExpectedVersionID, CurrentVersionID: record.View.CurrentVersionID}
		return old, err
	}
	d, e := s.registry.lookup(s.Templates, record)
	if e != nil {
		return old, e
	}
	if !s.nodeReady {
		e := unsupported(*record.View.CLI, record.TemplateSource, "adapter_unavailable")
		if safe, ok := apperr.As(e); ok {
			safe.Message = "验证运行环境需Node24"
		}
		return old, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return old, &apperr.Error{Status: 503, Code: apperr.CodeInternal, Message: "CLI验证服务已停止"}
	}
	expires := time.Now().Add(s.Options.InstallTimeout + s.Options.ProbeTimeout + 2*s.Options.RecordTimeout)
	op, created, e := s.Store.BeginCLIVerification(ctx, p, a, in, store.VerificationMetadata{TemplateSource: "builtin", CredentialFormat: d.Template.CredentialFormat(), TemplateFingerprint: d.Fingerprint, AdapterRevision: d.Revision, ProbeKind: d.Kind}, kind, actor, expires)
	if e != nil || !created {
		return op, e
	}
	run, cancel := context.WithDeadline(context.Background(), op.ExpiresAt)
	j := &job{project: p, account: a, cancel: cancel, done: make(chan struct{})}
	s.jobs[op.ID] = j
	go func() {
		defer close(j.done)
		defer cancel()
		defer func() { s.mu.Lock(); delete(s.jobs, op.ID); s.mu.Unlock() }()
		s.run(run, op, d)
	}()
	return op, nil
}
func (s *Service) State(ctx context.Context, p, a uuid.UUID) (cliverify.State, error) {
	if _, _, e := manager(ctx); e != nil {
		return cliverify.State{}, e
	}
	return s.Store.CLIVerificationState(ctx, p, a)
}
func (s *Service) Get(ctx context.Context, p, a, id uuid.UUID) (cliverify.Operation, error) {
	if _, _, e := manager(ctx); e != nil {
		return cliverify.Operation{}, e
	}
	return s.Store.GetCLIVerification(ctx, p, a, id)
}
func (s *Service) Cancel(ctx context.Context, p, a, id uuid.UUID) (cliverify.Operation, error) {
	if _, _, e := manager(ctx); e != nil {
		return cliverify.Operation{}, e
	}
	op, e := s.Store.CancelCLIVerification(ctx, p, a, id)
	if e != nil {
		return op, e
	}
	s.mu.Lock()
	j := s.jobs[id]
	if j != nil {
		j.cancel()
	}
	s.mu.Unlock()
	if j != nil {
		timer := time.NewTimer(s.Options.StopGrace + time.Second)
		defer timer.Stop()
		select {
		case <-j.done:
		case <-timer.C:
		case <-ctx.Done():
		}
	}
	return op, nil
}
func (s *Service) invalidate(p uuid.UUID, a *uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.project == p && (a == nil || *a == j.account) {
			j.cancel()
		}
	}
}
func (s *Service) scan(ctx context.Context) {
	defer close(s.scanDone)
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			c, cancel := context.WithTimeout(ctx, s.Options.RecordTimeout)
			e := s.Store.ExpireCLIVerifications(c)
			cancel()
			if e != nil {
				s.safeLog("expiry_record_failed")
				continue
			}
			s.mu.Lock()
			jobs := make(map[uuid.UUID]*job, len(s.jobs))
			for id, j := range s.jobs {
				jobs[id] = j
			}
			s.mu.Unlock()
			for id, j := range jobs {
				c, cancel := context.WithTimeout(ctx, s.Options.RecordTimeout)
				op, e := s.Store.GetCLIVerification(c, j.project, j.account, id)
				cancel()
				if e == nil && op.Status != "pending" && op.Status != "running" {
					j.cancel()
				}
			}
		}
	}
}
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.stop()
	jobs := make([]*job, 0, len(s.jobs))
	for _, j := range s.jobs {
		j.cancel()
		jobs = append(jobs, j)
	}
	s.mu.Unlock()
	for _, j := range jobs {
		select {
		case <-j.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	select {
	case <-s.scanDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) safeLog(code string) {
	if s.log != nil {
		s.log.Warn("CLI验证状态维护失败", "errorCode", code)
	}
}
func (s *Service) record(op cliverify.Operation, result cliverify.Result) {
	ctx, cancel := context.WithTimeout(context.Background(), s.Options.RecordTimeout)
	defer cancel()
	if _, _, e := s.Store.FinishCLIVerification(ctx, op.ProjectID, op.AccountID, op.ID, result); e != nil {
		// An unknown commit is read back, never followed by another external probe or
		// a replacement failure that could overwrite a committed successful result.
		confirm, stop := context.WithTimeout(context.Background(), s.Options.RecordTimeout)
		defer stop()
		read, e := s.Store.GetCLIVerification(confirm, op.ProjectID, op.AccountID, op.ID)
		if e != nil || read.Status == "pending" || read.Status == "running" {
			s.safeLog("result_record_unknown")
		}
	}
}
func (s *Service) run(ctx context.Context, op cliverify.Operation, d descriptor) {
	result := cliverify.Result{Outcome: "failed"}
	code := "probe_failed"
	result.ErrorCode = &code
	defer func() {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = "timeout"
			result = cliverify.Result{Outcome: "failed", ErrorCode: &code}
		}
		s.record(op, result)
	}()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return
	}
	claimed, ok, e := s.Store.ClaimCLIVerification(ctx, op.ProjectID, op.AccountID, op.ID)
	if e != nil || !ok {
		return
	}
	op = claimed
	home, lease, e := s.operationHome(op.ID)
	if e != nil {
		code = "configuration_invalid"
		return
	}
	defer s.releaseHome(home, lease)
	bin, e := s.ensureTool(ctx, d.Template)
	if e != nil {
		code = "tool_prepare_failed"
		return
	}
	if ctx.Err() != nil {
		return
	}
	env := baseEnvironment(home, d.Template)
	if op.CLI == "gcloud" {
		if e = os.MkdirAll(filepath.Join(home, ".loop-now/gcloud/config"), 0700); e != nil {
			code = "configuration_invalid"
			return
		}
	}
	executable := filepath.Join(bin, d.Template.Binary)
	versionSpec := CommandSpec{CLI: op.CLI, Phase: "version", Executable: executable, Args: []string{"--version"}, Home: home, Environment: env}
	if op.CLI == "gcloud" {
		versionSpec = s.googleVersionSpec(home, env, d.Template)
	}
	vctx, vcancel := context.WithTimeout(ctx, 10*time.Second)
	vout := s.execute(vctx, versionSpec, op, lease)
	vcancel()
	actual, ok := actualVersion(op.CLI, vout.Stdout)
	clear(vout.Stdout)
	clear(vout.Stderr)
	if !ok || actual != d.Template.Version || vout.ExitCode != 0 || vout.Overflow || vout.Cancelled {
		if s.log != nil {
			s.log.Warn("CLI验证工具版本校验失败", "cli", op.CLI, "exitCode", vout.ExitCode, "versionMatched", ok, "fixedVersionMatched", actual == d.Template.Version, "cancelled", vout.Cancelled, "outputOverflow", vout.Overflow)
		}
		code = "tool_unsupported"
		return
	}
	if e = s.Store.CLIVerificationPhase(ctx, op.ProjectID, op.AccountID, op.ID, "resolving_credentials", &actual); e != nil {
		return
	}
	// Revalidate target after installation. Never substitute the account current
	// pointer for the operation's fixed version.
	row, source, e := s.Store.GetProjectCLIVersion(ctx, op.AccountID, op.VersionID)
	if e != nil || source != "builtin" || row.TemplateVersion != op.CredentialFormat {
		code = "target_changed"
		return
	}
	probeCtx, stop := context.WithTimeout(ctx, s.Options.ProbeTimeout)
	defer stop()
	if op.CLI == "gcloud" {
		result = s.googleProbe(probeCtx, op, d, home, env, row, lease)
		return
	}
	request, extra, e := s.materialize(probeCtx, op, home, row)
	if e != nil {
		code = "configuration_invalid"
		return
	}
	env = append(env, extra...)
	request.Executable = executable
	plan, e := fixedPlan(request)
	if e != nil {
		code = "configuration_invalid"
		return
	}
	if e = s.Store.CLIVerificationPhase(ctx, op.ProjectID, op.AccountID, op.ID, "probing", nil); e != nil {
		return
	}
	output := s.execute(probeCtx, CommandSpec{CLI: op.CLI, Phase: "probe", Executable: plan.Executable, Args: plan.Args, Home: home, Environment: env}, op, lease)
	result = parseOutput(request, output, probeCtx)
	clear(output.Stdout)
	clear(output.Stderr)
}

func (s *Service) ensureTool(ctx context.Context, t *clitemplate.Template) (string, error) {
	type prepared struct {
		bin string
		err error
	}
	done := make(chan prepared, 1)
	go func() { bin, e := s.Installer.Ensure(ctx, t); done <- prepared{bin, e} }()
	select {
	case r := <-done:
		return r.bin, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
