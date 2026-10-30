package cliimport

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/cliimport/source"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/store"
)

type Reader interface {
	Discover(context.Context) ([]source.Candidate, error)
	Select(string) (source.Selection, error)
	Recheck(context.Context, source.Selection) error
	Extract(context.Context, source.Selection, source.Supplement, func(map[string]string) error) error
	Close() error
}
type Hasher interface {
	KeyID() string
	Origin(uuid.UUID, []byte) ([]byte, error)
	Content(uuid.UUID, string, string, []byte) ([]byte, error)
}
type Options struct {
	InstanceID                               uuid.UUID
	ReaderFactory                            func(context.Context) (Reader, error)
	Hasher                                   Hasher
	ScanTTL, OperationTimeout, RecordTimeout time.Duration
	MaxBatch, MaxActive, MaxScansPerProject  int
	MaxCredential                            int64
	Changed                                  func(context.Context, uuid.UUID)
}
type scanEntry struct {
	view   Scan
	reader Reader
	leases int
	timer  *time.Timer
}
type discovery struct {
	project uuid.UUID
	cancel  context.CancelFunc
	done    chan struct{}
}
type job struct {
	project  uuid.UUID
	cancel   context.CancelFunc
	done     chan struct{}
	accepted chan struct{}
}
type Service struct {
	st                 *store.Store
	catalog            *clitemplate.Catalog
	o                  Options
	mu                 sync.Mutex
	scans              map[uuid.UUID]*scanEntry
	jobs               map[uuid.UUID]*job
	discoveries        map[uuid.UUID]*discovery
	closed             bool
	maintenanceStarted bool
	maintenanceDone    chan struct{}
	life               context.Context
	stop               context.CancelFunc
	// withDeadline derives an accepted operation's work context. Tests replace
	// it so the deadline arrives on their signal instead of the wall clock.
	withDeadline func(context.Context, time.Time) (context.Context, context.CancelFunc)
}

func NewService(st *store.Store, catalog *clitemplate.Catalog, o Options) (*Service, error) {
	if st == nil || catalog == nil || o.InstanceID == uuid.Nil || o.ReaderFactory == nil || o.Hasher == nil || o.Hasher.KeyID() == "" || len(o.Hasher.KeyID()) > 200 {
		return nil, errors.New("invalid trusted import dependencies")
	}
	if o.ScanTTL <= 0 {
		o.ScanTTL = 10 * time.Minute
	}
	if o.MaxCredential <= 0 {
		o.MaxCredential = modelcredential.MaxBytes
	}
	if o.OperationTimeout <= 0 {
		o.OperationTimeout = 2 * time.Minute
	}
	if o.RecordTimeout <= 0 {
		o.RecordTimeout = 5 * time.Second
	}
	if o.MaxBatch <= 0 {
		o.MaxBatch = 20
	}
	if o.MaxBatch > 20 {
		return nil, errors.New("invalid import batch limit")
	}
	if o.MaxActive <= 0 {
		o.MaxActive = 4
	}
	if o.MaxScansPerProject <= 0 {
		o.MaxScansPerProject = 4
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{st: st, catalog: catalog, o: o, scans: map[uuid.UUID]*scanEntry{}, jobs: map[uuid.UUID]*job{}, discoveries: map[uuid.UUID]*discovery{}, life: ctx, stop: cancel, withDeadline: context.WithDeadline}, nil
}

var safeKind = regexp.MustCompile(`^[a-z0-9_]{1,80}$`)
var tools = []string{"gh", "lark-cli", "gcloud", "psql", "mysql"}

func safeLabel(label, cli, kind string) string {
	if !utf8.ValidString(label) || strings.TrimSpace(label) != label || strings.IndexFunc(label, unicode.IsControl) >= 0 || label == "" {
		label = cli + " · " + kind
	}
	r := []rune(label)
	if len(r) > 200 {
		label = string(r[:200])
	}
	return strings.TrimSpace(label)
}
func accountName(c Candidate) string {
	label := safeLabel(c.Label, c.CLI, c.SourceKind)
	if label == c.CLI+" · "+c.SourceKind {
		return label
	}
	suffix := " (" + c.CLI + ")"
	r := []rune(label)
	limit := 200 - len([]rune(suffix))
	if len(r) > limit {
		label = strings.TrimSpace(string(r[:limit]))
	}
	return label + suffix
}
func reason(code string) *string {
	if code == "" {
		return nil
	}
	return &code
}
func (s *Service) recordContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), s.o.RecordTimeout)
}
func copyScan(v Scan) Scan {
	v.Candidates = append([]Candidate{}, v.Candidates...)
	for i := range v.Candidates {
		c := &v.Candidates[i]
		c.MissingFields = append([]string{}, c.MissingFields...)
		c.SupplementableFields = append([]string{}, c.SupplementableFields...)
		if c.ReasonCode != nil {
			x := *c.ReasonCode
			c.ReasonCode = &x
		}
	}
	v.Sources = append([]SourceSummary{}, v.Sources...)
	for i := range v.Sources {
		if v.Sources[i].ReasonCode != nil {
			x := *v.Sources[i].ReasonCode
			v.Sources[i].ReasonCode = &x
		}
	}
	return v
}
func safeCandidate(c source.Candidate) (Candidate, error) {
	id, e := uuid.Parse(c.ID)
	if e != nil || id == uuid.Nil || !slices.Contains(tools, c.CLI) || !safeKind.MatchString(c.SourceKind) {
		return Candidate{}, errors.New("invalid source metadata")
	}
	out := Candidate{CandidateID: id, CLI: c.CLI, SourceKind: c.SourceKind, Label: safeLabel(c.Label, c.CLI, c.SourceKind), Status: "may_import", MissingFields: []string{}, SupplementableFields: []string{}}
	for _, f := range c.Missing {
		if slices.Contains([]string{"host", "port", "user", "database", "sslMode", "caCert", "clientCert", "clientKey", "password", "token", "appId", "appSecret", "brand", "serviceAccountJson"}, f) {
			out.MissingFields = append(out.MissingFields, f)
		}
	}
	for _, f := range c.SupplementableFields {
		if slices.Contains([]string{"database", "sslMode", "caCert"}, f) {
			out.SupplementableFields = append(out.SupplementableFields, f)
		}
	}
	switch c.Reason {
	case "":
		if len(c.Missing) > 0 {
			out.Status = "needs_fields"
		}
	case "requires_platform_login":
		out.Status = c.Reason
		out.ReasonCode = reason(c.Reason)
	case "source_unreadable":
		out.Status = "unreadable"
		out.ReasonCode = reason(c.Reason)
	case "tool_unavailable", "template_mismatch", "unsupported_cli_store", "cached_token_not_credential", "keyring_version_unavailable":
		out.Status = "unsupported"
		out.ReasonCode = reason(c.Reason)
	case "keyring_version_unbound":
		out.Status = "unsupported"
		out.ReasonCode = reason("keyring_version_unavailable")
	default:
		out.Status = "unsupported"
		out.ReasonCode = reason("unsupported")
	}
	return out, nil
}
func (s *Service) gcLocked(now time.Time) {
	for id, e := range s.scans {
		if !now.Before(e.view.ExpiresAt) && e.leases == 0 {
			if e.timer != nil {
				e.timer.Stop()
			}
			e.reader.Close()
			delete(s.scans, id)
		}
	}
}
func (s *Service) Scan(ctx context.Context, project uuid.UUID) (Scan, error) {
	if project == uuid.Nil {
		return Scan{}, apperr.Invalid("projectId", "UUID不能为零")
	}
	_, rev, err := s.st.ListProjectCredentials(ctx, project, "cli", "")
	if err != nil {
		return Scan{}, err
	}
	s.mu.Lock()
	s.gcLocked(time.Now())
	count := 0
	for _, v := range s.scans {
		if v.view.ProjectID == project {
			count++
		}
	}
	for _, v := range s.discoveries {
		if v.project == project {
			count++
		}
	}
	if s.closed {
		s.mu.Unlock()
		return Scan{}, errors.New("import service closed")
	}
	if count >= s.o.MaxScansPerProject {
		s.mu.Unlock()
		return Scan{}, apperr.Conflict("import_in_progress", "扫描数量达到上限")
	}
	work, cancel := context.WithTimeout(ctx, s.o.OperationTimeout)
	discoveryID := uuid.New()
	d := &discovery{project: project, cancel: cancel, done: make(chan struct{})}
	s.discoveries[discoveryID] = d
	s.mu.Unlock()
	stop := context.AfterFunc(s.life, cancel)
	defer func() {
		stop()
		cancel()
		s.mu.Lock()
		delete(s.discoveries, discoveryID)
		s.mu.Unlock()
		close(d.done)
	}()
	r, err := s.o.ReaderFactory(work)
	if err != nil {
		return Scan{}, apperr.Invalid("source", "本机来源不可读")
	}
	keep := false
	defer func() {
		if !keep {
			r.Close()
		}
	}()
	raw, err := r.Discover(work)
	if err != nil {
		return Scan{}, apperr.Invalid("source", "本机来源不可读")
	}
	view := Scan{ScanID: uuid.New(), ProjectID: project, ExpiresAt: time.Now().Add(s.o.ScanTTL), CredentialsRevision: rev, Candidates: []Candidate{}, Sources: []SourceSummary{}}
	for _, c := range raw {
		v, e := safeCandidate(c)
		if e != nil {
			return Scan{}, e
		}
		if v.Status == "may_import" || v.Status == "needs_fields" {
			sel, e := r.Select(c.ID)
			if e != nil {
				return Scan{}, apperr.Invalid("source", "候选不可读")
			}
			var mac []byte
			e = sel.WithOrigin(func(b []byte) error { var err error; mac, err = s.o.Hasher.Origin(project, b); return err })
			if e != nil || len(mac) != 32 {
				return Scan{}, errors.New("import provenance unavailable")
			}
			v.PreviouslyImported, e = s.st.CLIImportOriginSeen(work, project, s.o.Hasher.KeyID(), mac)
			clear(mac)
			if e != nil {
				return Scan{}, e
			}
		}
		view.Candidates = append(view.Candidates, v)
	}
	for _, cli := range tools {
		summary := SourceSummary{CLI: cli, Status: "no_source"}
		for _, c := range view.Candidates {
			if c.CLI == cli {
				if c.Status == "may_import" || c.Status == "needs_fields" {
					summary.Status = "candidates"
					summary.ReasonCode = nil
					break
				}
				summary.Status = "unsupported"
				summary.ReasonCode = reason("unsupported")
				if c.Status == "unreadable" {
					summary.Status = "unreadable"
					summary.ReasonCode = reason("source_unreadable")
				}
			}
		}
		view.Sources = append(view.Sources, summary)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := work.Err(); err != nil {
		return Scan{}, err
	}
	s.gcLocked(time.Now())
	count = 0
	for _, e := range s.scans {
		if e.view.ProjectID == project {
			count++
		}
	}
	if s.closed || count >= s.o.MaxScansPerProject {
		return Scan{}, apperr.Conflict("import_in_progress", "扫描数量达到上限")
	}
	entry := &scanEntry{view: copyScan(view), reader: r}
	entry.timer = time.AfterFunc(time.Until(view.ExpiresAt), func() { s.mu.Lock(); defer s.mu.Unlock(); s.gcLocked(time.Now()) })
	s.scans[view.ScanID] = entry
	keep = true
	return copyScan(view), nil
}
func fingerprint(in StartInput) []byte {
	in.CandidateIDs = append([]uuid.UUID(nil), in.CandidateIDs...)
	sort.Slice(in.CandidateIDs, func(i, j int) bool { return in.CandidateIDs[i].String() < in.CandidateIDs[j].String() })
	in.Supplements = append([]Supplement(nil), in.Supplements...)
	sort.Slice(in.Supplements, func(i, j int) bool {
		return in.Supplements[i].CandidateID.String() < in.Supplements[j].CandidateID.String()
	})
	if len(in.Supplements) == 0 {
		in.Supplements = nil
	}
	b, _ := json.Marshal(in)
	h := sha256.Sum256(b)
	clear(b)
	return h[:]
}
func (s *Service) Start(ctx context.Context, project uuid.UUID, in StartInput) (Operation, error) {
	if project == uuid.Nil {
		return Operation{}, apperr.Invalid("projectId", "UUID不能为零")
	}
	if err := in.Validate(); err != nil {
		return Operation{}, err
	}
	if len(in.CandidateIDs) > s.o.MaxBatch {
		return Operation{}, apperr.Invalid("candidateIds", "批次超限")
	}
	hash := fingerprint(in)
	defer clear(hash)
	if old, err := s.st.FindCLIImport(ctx, project, in.OperationID, hash); err == nil {
		return old, nil
	} else if !store.IsNotFound(err) {
		return Operation{}, err
	}
	s.mu.Lock()
	if existing, exists := s.jobs[in.OperationID]; exists {
		s.mu.Unlock()
		select {
		case <-existing.accepted:
		case <-ctx.Done():
			return Operation{}, ctx.Err()
		}
		return s.st.FindCLIImport(ctx, project, in.OperationID, hash)
	}
	s.gcLocked(time.Now())
	e := s.scans[in.ScanID]
	if s.closed {
		s.mu.Unlock()
		return Operation{}, errors.New("import service closed")
	}
	if e == nil || e.view.ProjectID != project || !time.Now().Before(e.view.ExpiresAt) {
		s.mu.Unlock()
		return Operation{}, apperr.Conflict("import_scan_expired", "请重新扫描")
	}
	if len(s.jobs) >= s.o.MaxActive {
		s.mu.Unlock()
		return Operation{}, apperr.Conflict("import_in_progress", "导入数量达到上限")
	}
	candidates := map[uuid.UUID]Candidate{}
	for _, v := range e.view.Candidates {
		candidates[v.CandidateID] = v
	}
	selected := []Candidate{}
	safeItems := []store.CLIImportItem{}
	for _, id := range in.CandidateIDs {
		v, ok := candidates[id]
		if !ok {
			s.mu.Unlock()
			return Operation{}, apperr.NotFound("候选")
		}
		if v.Status != "may_import" && v.Status != "needs_fields" {
			s.mu.Unlock()
			return Operation{}, apperr.Invalid("candidateIds", "候选不支持导入")
		}
		selected = append(selected, v)
		safeItems = append(safeItems, store.CLIImportItem{CandidateID: id, CLI: v.CLI, Label: v.Label, Status: "pending"})
	}
	for _, sup := range in.Supplements {
		v := candidates[sup.CandidateID]
		for key, p := range map[string]*string{"database": sup.Database, "sslMode": sup.SSLMode, "caCert": sup.CACert} {
			if p != nil && !slices.Contains(v.SupplementableFields, key) {
				s.mu.Unlock()
				return Operation{}, apperr.Invalid("supplements", "不能补充源已有或不允许的字段")
			}
		}
	}
	// Reserve active capacity and Reader lease across durable acceptance. The HTTP
	// request can end; accepted work belongs to service lifetime/deadline.
	deadline := time.Now().Add(s.o.OperationTimeout)
	work, cancel := s.withDeadline(s.life, deadline)
	j := &job{project: project, cancel: cancel, done: make(chan struct{}), accepted: make(chan struct{})}
	s.jobs[in.OperationID] = j
	s.startMaintenanceLocked()
	e.leases++
	s.mu.Unlock()
	release := func() {
		cancel()
		s.mu.Lock()
		delete(s.jobs, in.OperationID)
		e.leases--
		s.gcLocked(time.Now())
		s.mu.Unlock()
		close(j.done)
	}
	accept, acceptCancel := context.WithTimeout(work, s.o.RecordTimeout)
	op, created, err := s.st.BeginCLIImport(accept, store.CLIImportBegin{ID: in.OperationID, ProjectID: project, ScanID: in.ScanID, InstanceID: s.o.InstanceID, RequestFingerprint: hash, ExpectedRevision: in.ExpectedCredentialsRevision, ProvenanceKeyID: s.o.Hasher.KeyID(), Deadline: deadline, Items: safeItems})
	acceptCancel()
	if store.IsCLIImportCommitUnknown(err) {
		record, end := s.recordContext()
		confirmed, readErr := s.st.FindCLIImport(record, project, in.OperationID, hash)
		end()
		if readErr == nil {
			// Unknown acceptance never starts extraction. Preserve the original
			// receipt and reconcile only its deadline/terminal, without replay.
			close(j.accepted)
			if confirmed.Terminal() {
				release()
			} else {
				go func() { defer release(); _, _ = s.resolveUnknown(work, project, in.OperationID) }()
			}
			return confirmed, nil
		} else {
			close(j.accepted)
			go func() { defer release(); _, _ = s.resolveUnknown(work, project, in.OperationID) }()
			return Operation{}, err
		}
	}
	close(j.accepted)
	if err != nil {
		release()
		return Operation{}, err
	}
	if !created {
		release()
		return op, nil
	}
	// Deep copy supplement strings so callers cannot mutate a running job.
	b, _ := json.Marshal(in)
	var copyIn StartInput
	_ = json.Unmarshal(b, &copyIn)
	clear(b)
	go func() { defer release(); s.run(work, project, copyIn, e.reader, selected) }()
	return op, nil
}
func (s *Service) Get(ctx context.Context, project, id uuid.UUID) (Operation, error) {
	return s.st.GetCLIImport(ctx, project, id)
}
func (s *Service) Cancel(ctx context.Context, project, id uuid.UUID) (Operation, error) {
	if project == uuid.Nil || id == uuid.Nil {
		return Operation{}, apperr.Invalid("operationId", "UUID不能为零")
	}
	record, end := s.recordContext()
	defer end()
	op, err := s.st.CancelCLIImport(record, project, id)
	if err != nil {
		return op, err
	}
	s.mu.Lock()
	j := s.jobs[id]
	s.mu.Unlock()
	if j != nil && op.Status != "succeeded" {
		j.cancel()
		select {
		case <-j.done:
		case <-record.Done():
			return op, record.Err()
		}
	}
	return op, nil
}
func (s *Service) Recover(ctx context.Context) error {
	return s.st.RecoverCLIImports(ctx, s.o.InstanceID)
}

// CancelProject is a trusted post-COMMIT hook, not an HTTP action. The deleting
// transaction must first call Store.TerminateProjectCLIImportsTx. Never call on rollback.
// It only cancels this project's contexts/leases; it does not mutate DB history.
func (s *Service) CancelProject(ctx context.Context, project uuid.UUID) error {
	if project == uuid.Nil {
		return apperr.Invalid("projectId", "UUID不能为零")
	}
	s.mu.Lock()
	jobs := []*job{}
	discoveries := []*discovery{}
	for _, j := range s.jobs {
		if j.project == project {
			j.cancel()
			jobs = append(jobs, j)
		}
	}
	for _, d := range s.discoveries {
		if d.project == project {
			d.cancel()
			discoveries = append(discoveries, d)
		}
	}
	for id, e := range s.scans {
		if e.view.ProjectID == project {
			e.view.ExpiresAt = time.Now()
			if e.timer != nil {
				e.timer.Stop()
			}
			if e.leases == 0 {
				e.reader.Close()
				delete(s.scans, id)
			}
		}
	}
	s.mu.Unlock()
	for _, d := range discoveries {
		select {
		case <-d.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for _, j := range jobs {
		select {
		case <-j.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.stop()
	jobs := []*job{}
	for _, j := range s.jobs {
		j.cancel()
		jobs = append(jobs, j)
	}
	discoveries := []*discovery{}
	for _, d := range s.discoveries {
		d.cancel()
		discoveries = append(discoveries, d)
	}
	maintenance := s.maintenanceDone
	s.mu.Unlock()
	for _, d := range discoveries {
		select {
		case <-d.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for _, j := range jobs {
		select {
		case <-j.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if maintenance != nil {
		select {
		case <-maintenance:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, e := range s.scans {
		if e.leases == 0 {
			if e.timer != nil {
				e.timer.Stop()
			}
			e.reader.Close()
			delete(s.scans, id)
		}
	}
	return nil
}

// Started by an explicit Start, never by construction or GET. No Reader,
// extraction, sealing, or new work is reachable from this expiry reconciler.
func (s *Service) startMaintenanceLocked() {
	if s.maintenanceStarted {
		return
	}
	s.maintenanceStarted = true
	s.maintenanceDone = make(chan struct{})
	go func() {
		defer close(s.maintenanceDone)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-s.life.Done():
				return
			case <-ticker.C:
				c, cancel := context.WithTimeout(s.life, s.o.RecordTimeout)
				_ = s.st.ExpireDueCLIImports(c, s.o.InstanceID)
				cancel()
			}
		}
	}()
}
func (s *Service) run(ctx context.Context, project uuid.UUID, in StartInput, r Reader, candidates []Candidate) {
	failure := func(code string, id uuid.UUID) {
		record, end := s.recordContext()
		defer end()
		status := "failed"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status, code = "expired", "timeout"
		} else if ctx.Err() != nil {
			status, code = "cancelled", "cancelled"
		}
		_, _ = s.st.FinishCLIImport(record, project, in.OperationID, status, code, id)
	}
	if err := s.st.SetCLIImportPhase(ctx, project, in.OperationID, s.o.InstanceID, "extracting"); err != nil {
		failure("internal_error", uuid.Nil)
		return
	}
	prepared := []store.CLIImportPreparedItem{}
	selections := []source.Selection{}
	defer func() {
		for _, p := range prepared {
			clear(p.OriginMAC)
			clear(p.ContentMAC)
			clear(p.Secret)
		}
	}()
	supplements := map[uuid.UUID]Supplement{}
	for _, v := range in.Supplements {
		supplements[v.CandidateID] = v
	}
	for _, c := range candidates {
		if ctx.Err() != nil {
			failure("cancelled", c.CandidateID)
			return
		}
		sel, err := r.Select(c.CandidateID.String())
		if err != nil {
			failure(safeFailure(err), c.CandidateID)
			return
		}
		selections = append(selections, sel)
		p := store.CLIImportPreparedItem{CandidateID: c.CandidateID, AccountID: uuid.New(), VersionID: uuid.New(), CLI: c.CLI, Name: accountName(c), TemplateSource: "builtin", AuthMethod: "static", ProvenanceKeyID: s.o.Hasher.KeyID()}
		if c.CLI == "gcloud" {
			p.AuthMethod = "service_account"
		}
		err = sel.WithOrigin(func(b []byte) error { var e error; p.OriginMAC, e = s.o.Hasher.Origin(project, b); return e })
		if err != nil {
			failure("internal_error", c.CandidateID)
			return
		}
		sup := supplements[c.CandidateID]
		value := func(p *string) string {
			if p == nil {
				return ""
			}
			return *p
		}
		err = r.Extract(ctx, sel, source.Supplement{Database: value(sup.Database), SSLMode: value(sup.SSLMode), CACert: value(sup.CACert)}, func(fields map[string]string) error {
			t, ok := s.catalog.Get(c.CLI)
			if !ok {
				return errors.New("template unavailable")
			}
			payload, names, e := t.Render(fields)
			defer func() {
				for _, f := range payload.Files {
					clear(f.Content)
				}
				clear(payload.Env)
			}()
			if e != nil {
				return e
			}
			plain, e := json.Marshal(payload)
			defer clear(plain)
			if e != nil {
				return e
			}
			if int64(len(plain)) > s.o.MaxCredential {
				return errors.New("credential too large")
			}
			p.CredentialFormat = t.CredentialFormat()
			p.Meta = store.CredentialMeta{TemplateVersion: p.CredentialFormat, FieldNames: names, EnvNames: payload.EnvNames(), FilePaths: payload.FilePaths()}
			// Renderer output may contain random auxiliary material (Lark master
			// key). Dedup compares validated canonical credential fields instead;
			// the stored credential still retains the complete freshly rendered payload.
			canonical, e := json.Marshal(fields)
			defer clear(canonical)
			if e != nil {
				return e
			}
			p.ContentMAC, e = s.o.Hasher.Content(project, c.CLI, p.CredentialFormat, canonical)
			if e != nil {
				return e
			}
			p.Secret = slices.Clone(plain)
			return nil
		})
		prepared = append(prepared, p)
		if err != nil {
			failure(safeFailure(err), c.CandidateID)
			return
		}
	}
	recheck := func(c context.Context) error {
		for n, sel := range selections {
			if err := r.Recheck(c, sel); err != nil {
				return &sourceItemFailure{id: candidates[n].CandidateID, err: err}
			}
		}
		return nil
	}
	if err := s.st.SetCLIImportPhase(ctx, project, in.OperationID, s.o.InstanceID, "committing"); err != nil {
		failure("internal_error", uuid.Nil)
		return
	}
	op, changed, err := s.st.FinalizeCLIImport(ctx, project, in.OperationID, s.o.InstanceID, prepared, recheck)
	if store.IsCLIImportCommitUnknown(err) {
		// A pending read is not rollback proof. Keep the original lease without
		// re-extraction until a bounded read confirms terminal or our own deadline
		// expires; only then use the same-lock-order terminal CAS.
		confirmed, e := s.resolveUnknown(ctx, project, in.OperationID)
		if e != nil {
			return
		}
		op = confirmed
		if op.Status == "succeeded" {
			for _, item := range op.Items {
				if item.Status == "imported" {
					changed = true
				}
			}
		}
	} else if err != nil {
		failedID := uuid.Nil
		var item *store.CLIImportItemFailure
		var sourceItem *sourceItemFailure
		if errors.As(err, &item) {
			failedID = item.CandidateID
		}
		if errors.As(err, &sourceItem) {
			failedID = sourceItem.id
		}
		failure(safeFailure(err), failedID)
		return
	}
	if changed && op.Status == "succeeded" && s.o.Changed != nil {
		record, end := s.recordContext()
		defer end()
		s.o.Changed(record, project)
	}
}

func (s *Service) resolveUnknown(ctx context.Context, project, id uuid.UUID) (Operation, error) {
	for {
		record, end := s.recordContext()
		op, err := s.st.GetCLIImport(record, project, id)
		end()
		if err == nil && op.Terminal() {
			return op, nil
		}
		if ctx.Err() != nil {
			record, end = s.recordContext()
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				op, err = s.st.ExpireCLIImport(record, project, id)
			} else {
				op, err = s.st.CancelCLIImport(record, project, id)
			}
			end()
			return op, err
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
		}
	}
}
func safeFailure(err error) string {
	var item *store.CLIImportItemFailure
	if errors.As(err, &item) {
		return item.Code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	var f source.Failure
	if errors.As(err, &f) {
		switch f.Code {
		case "source_changed", "source_unreadable", "missing_field", "unsupported", "template_mismatch", "supplement_override", "invalid_credential", "consumer_failed", "cancelled":
			return f.Code
		}
		return "internal_error"
	}
	var a *apperr.Error
	if errors.As(err, &a) {
		switch a.Code {
		case "precondition_failed":
			return "precondition_failed"
		case "import_source_conflict", "import_duplicate_source":
			return "source_conflict"
		}
	}
	return "internal_error"
}

type sourceItemFailure struct {
	id  uuid.UUID
	err error
}

func (*sourceItemFailure) Error() string   { return "import source item failure" }
func (e *sourceItemFailure) Unwrap() error { return e.err }
