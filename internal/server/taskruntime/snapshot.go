package taskruntime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/dbcli"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/prompts"
)

// schemaVersion 8 stores credentials as a plaintext object. Snapshots converted
// from schemas 1–7 keep that schema in sourceVersion; its validation rules stay.
// Schema 9 copies the base Agent folder. Schema 10 additionally freezes the
// selected Provider's native models.json fragment. Schemas 8 and 9 stay readable.
const schemaVersion = 10

// frozenSourcesSchema is the last schema whose tasks sync frozen folder sources.
const frozenSourcesSchema = 8
const copiedAgentFolderSchema = 9

// latestSemantics are the rules of schema 7, applied to every new capture.
const latestSemantics = 7

// File 是显式提供的文件内容；组件不读磁盘或 Blob。
type File struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
}

type Skill struct {
	Name  string `json:"name"`
	Files []File `json:"files"`
}

type Configuration struct {
	ServiceTier *string `json:"serviceTier,omitempty"`
	// New captures store the concrete resolved level. Absent legacy fields stay absent.
	ThinkingLevel       *string         `json:"thinkingLevel,omitempty"`
	CredentialIdentity  string          `json:"credentialIdentity,omitempty"`
	ModelsJSON          json.RawMessage `json:"modelsJson,omitempty"`
	CredentialsRevision int64           `json:"credentialsRevision,omitempty"`
	Prompts             *prompts.Layers `json:"prompts,omitempty"`
	AgentID             uuid.UUID       `json:"agentId"`
	ConfigVersion       int64           `json:"configVersion"`
	Provider            string          `json:"provider"`
	ModelID             string          `json:"modelId"`
	Prompt              string          `json:"prompt"`
	Skills              []Skill         `json:"skills"`
	CLIs                []string        `json:"clis"`
}

// CredentialPayload 只供显式的内部执行读取，不允许默认序列化或诊断打印。
type CredentialPayload struct {
	Env   map[string]string
	Files []File
}

type Credentials struct {
	LarkBinding   *clicredential.LarkBinding
	GoogleBinding *clicredential.Binding
	ModelAuth     *modelcredential.Binding
	Model         map[string]string
	CLI           map[string]CredentialPayload
}

type CaptureInput struct {
	Configuration Configuration
	Credentials   Credentials
}

type Snapshot struct{ env envelope }

type Metadata struct {
	SnapshotID    uuid.UUID `json:"snapshotId"`
	ScopeKey      string    `json:"scopeKey"`
	ConfigVersion int64     `json:"configVersion"`
	Provider      string    `json:"provider"`
	ModelID       string    `json:"modelId"`
	SkillNames    []string  `json:"skillNames"`
	CLIs          []string  `json:"clis"`
}

type envelope struct {
	SchemaVersion int              `json:"schemaVersion"`
	SourceVersion int              `json:"sourceVersion,omitempty"`
	SnapshotID    uuid.UUID        `json:"snapshotId"`
	Scope         scopeWire        `json:"scope"`
	Configuration Configuration    `json:"configuration"`
	Credentials   *credentialsWire `json:"credentials"`
}

// semantics is the schema whose validation rules apply to this snapshot.
func (e envelope) semantics() int {
	if e.SourceVersion != 0 {
		return e.SourceVersion
	}
	return latestSemantics
}

// 独立 wire 类型让默认诊断和 JSON 不会意外导出执行读取对象。
type payloadWire struct {
	Env   map[string]string `json:"env"`
	Files []File            `json:"files"`
}
type credentialsWire struct {
	LarkBinding   *clicredential.LarkBinding `json:"larkBinding,omitempty"`
	GoogleBinding *clicredential.Binding     `json:"googleBinding,omitempty"`
	ModelAuth     *modelcredential.Binding   `json:"modelAuth,omitempty"`
	Model         map[string]string          `json:"model"`
	CLI           map[string]payloadWire     `json:"cli"`
}

func Capture(scope Scope, snapshotID uuid.UUID, input CaptureInput) (*Snapshot, error) {
	if !scope.Valid() || !scope.IsTask() || snapshotID == uuid.Nil {
		return nil, ErrInvalidInput
	}
	cfg := cloneConfig(input.Configuration)
	if b := input.Credentials.ModelAuth; b != nil && (!b.Valid() || b.AgentID != scope.AgentID() || b.Provider != cfg.Provider || (b.OwnerKind == "project" && (cfg.Prompts == nil || cfg.Prompts.ProjectID == nil || *cfg.Prompts.ProjectID != *b.ProjectID))) {
		return nil, ErrInvalidInput
	}
	if err := validateConfig(cfg, scope); err != nil {
		return nil, err
	}
	if cfg.Prompts == nil || cfg.Prompts.Validate() != nil || cfg.Prompts.AgentPrompt != cfg.Prompt {
		return nil, ErrInvalidInput
	}
	creds := cloneCredentials(input.Credentials)
	if creds.ModelAuth != nil && creds.ModelAuth.OwnerKind == "" {
		creds.ModelAuth.OwnerKind = "agent"
	}
	if err := validateCredentials(creds, cfg.CLIs); err != nil {
		return nil, err
	}
	if validateGoogleBinding(creds, cfg, scope) != nil || validateLarkBinding(creds, cfg, scope) != nil || validateDBCredentials(creds, cfg) != nil {
		return nil, ErrInvalidInput
	}
	return &Snapshot{env: envelope{SchemaVersion: schemaVersion, SnapshotID: snapshotID, Scope: scope.wire(), Configuration: cfg, Credentials: new(toWire(creds))}}, nil
}

func (s *Snapshot) Scope() Scope {
	if s == nil {
		return Scope{}
	}
	return Scope{s.env.Scope.AgentID, s.env.Scope.TaskID}
}

func (s *Snapshot) Configuration() Configuration {
	if s == nil {
		return Configuration{}
	}
	return cloneConfig(s.env.Configuration)
}

func (s *Snapshot) Metadata() Metadata {
	if s == nil {
		return Metadata{}
	}
	c := s.env.Configuration
	m := Metadata{SnapshotID: s.env.SnapshotID, ScopeKey: s.Scope().Key(), ConfigVersion: c.ConfigVersion, Provider: c.Provider, ModelID: c.ModelID, CLIs: slices.Clone(c.CLIs)}
	for _, skill := range c.Skills {
		m.SkillNames = append(m.SkillNames, skill.Name)
	}
	return m
}

// Credentials validates and returns a copy of the captured credentials.
func (s *Snapshot) Credentials() (Credentials, error) {
	if s == nil || validateEnvelope(s.env) != nil {
		return Credentials{}, ErrInvalidInput
	}
	version := s.env.semantics()
	c := cloneCredentials(fromWire(*s.env.Credentials))
	if b := c.ModelAuth; b != nil && (version == 1 || !b.Valid() || b.AgentID != s.Scope().AgentID() || b.Provider != s.env.Configuration.Provider) {
		return Credentials{}, ErrCredentials
	}
	if c.ModelAuth != nil {
		if version < 4 && (c.ModelAuth.OwnerKind != "" || c.ModelAuth.ProjectID != nil || c.ModelAuth.AccountID != nil) {
			return Credentials{}, ErrCredentials
		}
		if version >= 4 && c.ModelAuth.OwnerKind == "" {
			return Credentials{}, ErrCredentials
		}
		if c.ModelAuth.OwnerKind == "project" && (s.env.Configuration.Prompts == nil || s.env.Configuration.Prompts.ProjectID == nil || *s.env.Configuration.Prompts.ProjectID != *c.ModelAuth.ProjectID) {
			return Credentials{}, ErrCredentials
		}
	}
	if validateCredentials(c, s.env.Configuration.CLIs) != nil {
		return Credentials{}, ErrCredentials
	}
	if c.GoogleBinding != nil && (version < 6 || validateGoogleBinding(c, s.env.Configuration, s.Scope()) != nil) {
		return Credentials{}, ErrCredentials
	}
	if c.LarkBinding != nil && (version < 7 || validateLarkBinding(c, s.env.Configuration, s.Scope()) != nil) {
		return Credentials{}, ErrCredentials
	}
	if version >= 5 && validateDBCredentials(c, s.env.Configuration) != nil {
		return Credentials{}, ErrCredentials
	}
	return c, nil
}

// Encode 是可保存快照的唯一编码入口；结果含明文凭证，只写入任务快照。
func (s *Snapshot) Encode() ([]byte, error) {
	if s == nil || validateEnvelope(s.env) != nil {
		return nil, ErrInvalidInput
	}
	b, err := json.Marshal(s.env)
	if err != nil {
		return nil, ErrInvalidEncoding
	}
	return b, nil
}

// CopiesAgentFolder 报告这个任务的工作区是否复制自基础 Agent 的文件夹（schema 9 起）；
// 否则按旧规则同步捕获时冻结的文件夹来源。
func (s *Snapshot) CopiesAgentFolder() bool { return s.env.SchemaVersion >= copiedAgentFolderSchema }

func supportedSchema(version int) bool {
	return version == frozenSourcesSchema || version == copiedAgentFolderSchema || version == schemaVersion
}

// Decode 验证格式，不调用外部服务；读取凭证时再验证凭证内容。
func Decode(data []byte) (*Snapshot, error) {
	var e envelope
	if strictJSON(data, &e) != nil {
		return nil, ErrInvalidEncoding
	}
	if !supportedSchema(e.SchemaVersion) {
		return nil, ErrUnsupportedVersion
	}
	if validateEnvelope(e) != nil {
		return nil, ErrInvalidEncoding
	}
	e.Configuration = cloneConfig(e.Configuration)
	return &Snapshot{env: e}, nil
}

func strictJSON(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return ErrInvalidEncoding
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrInvalidEncoding
	}
	return nil
}

func validateEnvelope(e envelope) error {
	scope := Scope{e.Scope.AgentID, e.Scope.TaskID}
	if !supportedSchema(e.SchemaVersion) || e.SchemaVersion == schemaVersion && e.SourceVersion != 0 || e.SourceVersion < 0 || e.SourceVersion > latestSemantics || e.SnapshotID == uuid.Nil || e.Credentials == nil || !scope.Valid() || !scope.IsTask() {
		return ErrInvalidEncoding
	}
	if e.SchemaVersion < schemaVersion && len(e.Configuration.ModelsJSON) > 0 {
		return ErrInvalidEncoding
	}
	version := e.semantics()
	if version < 4 && (e.Configuration.CredentialIdentity != "" || e.Configuration.CredentialsRevision != 0) {
		return ErrInvalidEncoding
	}
	if version < 3 {
		if e.Configuration.Prompts != nil {
			return ErrInvalidEncoding
		}
	} else if e.Configuration.Prompts == nil || e.Configuration.Prompts.Validate() != nil || e.Configuration.Prompts.AgentPrompt != e.Configuration.Prompt {
		return ErrInvalidEncoding
	}
	return validateConfig(e.Configuration, scope)
}

func validateConfig(c Configuration, s Scope) error {
	if c.AgentID != s.AgentID() || c.ConfigVersion < 1 || strings.TrimSpace(c.Provider) == "" || strings.TrimSpace(c.ModelID) == "" || !utf8.ValidString(c.Provider) || !utf8.ValidString(c.ModelID) || !utf8.ValidString(c.Prompt) {
		return ErrInvalidInput
	}
	if len(c.ModelsJSON) > 0 {
		var fragment struct {
			Providers map[string]json.RawMessage `json:"providers"`
		}
		if strictJSON(c.ModelsJSON, &fragment) != nil || len(fragment.Providers) != 1 || fragment.Providers[c.Provider] == nil {
			return ErrInvalidInput
		}
	}
	seen := map[string]bool{}
	for _, name := range c.CLIs {
		if !validName(name) || seen[name] {
			return ErrInvalidInput
		}
		seen[name] = true
	}
	seen = map[string]bool{}
	for _, skill := range c.Skills {
		if strings.TrimSpace(skill.Name) == "" || !utf8.ValidString(skill.Name) || seen[skill.Name] || len(skill.Files) == 0 {
			return ErrInvalidInput
		}
		seen[skill.Name] = true
		if !validFiles(skill.Files) {
			return ErrInvalidInput
		}
	}
	return nil
}

func validateCredentials(c Credentials, clis []string) error {
	if c.LarkBinding != nil {
		if !c.LarkBinding.Valid() || !slices.Contains(clis, "lark-cli") {
			return ErrInvalidInput
		}
		if _, ok := c.CLI["lark-cli"]; ok {
			return ErrInvalidInput
		}
	}

	if c.GoogleBinding != nil {
		if !c.GoogleBinding.Valid() || !slices.Contains(clis, "gcloud") {
			return ErrInvalidInput
		}
		if _, exists := c.CLI["gcloud"]; exists {
			return ErrInvalidInput
		}
	}

	for name, p := range c.CLI {
		if !slices.Contains(clis, name) || !validFiles(p.Files) || !validEnv(p.Env) {
			return ErrInvalidInput
		}
	}
	if c.ModelAuth != nil && (!c.ModelAuth.Valid() || len(c.Model) > 0) {
		return ErrInvalidInput
	}
	if !validEnv(c.Model) {
		return ErrInvalidInput
	}
	return nil
}

func validEnv(env map[string]string) bool {
	for k, v := range env {
		if k == "" || !utf8.ValidString(v) {
			return false
		}
		for i, r := range k {
			if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9') {
				return false
			}
		}
	}
	return true
}

func validName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for i, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || i > 0 && (r == '.' || r == '_' || r == '-')) {
			return false
		}
	}
	return true
}

func validFiles(files []File) bool {
	seen := map[string]bool{}
	for _, f := range files {
		if f.Path == "" || !utf8.ValidString(f.Path) || path.IsAbs(f.Path) || path.Clean(f.Path) != f.Path || f.Path == "." || f.Path == ".." || strings.HasPrefix(f.Path, "../") || strings.ContainsAny(f.Path, "\\\x00") || seen[f.Path] {
			return false
		}
		seen[f.Path] = true
	}
	return true
}

func cloneFiles(fs []File) []File {
	if fs == nil {
		return nil
	}
	out := make([]File, len(fs))
	for i, f := range fs {
		out[i] = File{f.Path, slices.Clone(f.Content)}
	}
	slices.SortFunc(out, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	return out
}
func cloneConfig(c Configuration) Configuration {
	if c.ServiceTier != nil {
		value := *c.ServiceTier
		c.ServiceTier = &value
	}
	if c.ThinkingLevel != nil {
		level := *c.ThinkingLevel
		c.ThinkingLevel = &level
	}
	c.ModelsJSON = slices.Clone(c.ModelsJSON)
	c.Prompts = prompts.Clone(c.Prompts)
	c.CLIs = slices.Clone(c.CLIs)
	slices.Sort(c.CLIs)
	c.Skills = slices.Clone(c.Skills)
	for i := range c.Skills {
		c.Skills[i].Files = cloneFiles(c.Skills[i].Files)
	}
	slices.SortFunc(c.Skills, func(a, b Skill) int { return strings.Compare(a.Name, b.Name) })
	return c
}
func cloneCredentials(c Credentials) Credentials {
	out := Credentials{LarkBinding: cloneLarkBinding(c.LarkBinding), GoogleBinding: cloneGoogleBinding(c.GoogleBinding), ModelAuth: cloneBinding(c.ModelAuth), Model: maps.Clone(c.Model), CLI: map[string]CredentialPayload{}}
	for k, p := range c.CLI {
		out.CLI[k] = CredentialPayload{maps.Clone(p.Env), cloneFiles(p.Files)}
	}
	return out
}
func toWire(c Credentials) credentialsWire {
	w := credentialsWire{LarkBinding: cloneLarkBinding(c.LarkBinding), GoogleBinding: cloneGoogleBinding(c.GoogleBinding), ModelAuth: cloneBinding(c.ModelAuth), Model: maps.Clone(c.Model), CLI: map[string]payloadWire{}}
	for k, p := range c.CLI {
		w.CLI[k] = payloadWire{maps.Clone(p.Env), cloneFiles(p.Files)}
	}
	return w
}
func fromWire(w credentialsWire) Credentials {
	c := Credentials{LarkBinding: cloneLarkBinding(w.LarkBinding), GoogleBinding: cloneGoogleBinding(w.GoogleBinding), ModelAuth: cloneBinding(w.ModelAuth), Model: w.Model, CLI: map[string]CredentialPayload{}}
	for k, p := range w.CLI {
		c.CLI[k] = CredentialPayload{p.Env, p.Files}
	}
	return c
}

func (*Snapshot) MarshalJSON() ([]byte, error)         { return nil, ErrUnsafeSerialization }
func (Credentials) MarshalJSON() ([]byte, error)       { return nil, ErrUnsafeSerialization }
func (CredentialPayload) MarshalJSON() ([]byte, error) { return nil, ErrUnsafeSerialization }
func (CaptureInput) MarshalJSON() ([]byte, error)      { return nil, ErrUnsafeSerialization }
func (s Snapshot) Format(f fmt.State, _ rune) {
	io.WriteString(f, "taskruntime.Snapshot<"+s.env.SnapshotID.String()+">")
}
func (Credentials) Format(f fmt.State, _ rune) {
	io.WriteString(f, "taskruntime.Credentials<redacted>")
}
func (CredentialPayload) Format(f fmt.State, _ rune) {
	io.WriteString(f, "taskruntime.CredentialPayload<redacted>")
}
func (CaptureInput) Format(f fmt.State, _ rune) {
	io.WriteString(f, "taskruntime.CaptureInput<redacted>")
}

func cloneBinding(b *modelcredential.Binding) *modelcredential.Binding {
	if b == nil {
		return nil
	}
	copy := b.Clone()
	return &copy
}

// Schema5 validates the entire static connection set and its owner.
// Converted older schemas keep their original rules through sourceVersion.
func validateDBCredentials(c Credentials, cfg Configuration) error {
	for cli, p := range c.CLI {
		if cli != "psql" && cli != "mysql" {
			continue
		}
		if len(p.Env) != 0 || len(p.Files) != 1 || p.Files[0].Path != dbcli.ConfigPath(cli) {
			return ErrInvalidInput
		}
		var connections []dbcli.Connection
		if strictJSON(p.Files[0].Content, &connections) != nil || connections == nil {
			return ErrInvalidInput
		}
		seen := map[uuid.UUID]bool{}
		for _, connection := range connections {
			if connection.AccountID == uuid.Nil || connection.VersionID == uuid.Nil || seen[connection.AccountID] || connection.ProjectID == nil || *connection.ProjectID == uuid.Nil || cfg.Prompts == nil || cfg.Prompts.ProjectID == nil || *connection.ProjectID != *cfg.Prompts.ProjectID || dbcli.Validate(cli, connection.Fields) != nil {
				return ErrInvalidInput
			}
			seen[connection.AccountID] = true
		}
	}
	return nil
}

func cloneGoogleBinding(b *clicredential.Binding) *clicredential.Binding {
	if b == nil {
		return nil
	}
	copy := *b
	return &copy
}
func validateGoogleBinding(c Credentials, cfg Configuration, scope Scope) error {
	b := c.GoogleBinding
	if b == nil {
		return nil
	}
	if !b.Valid() || b.AgentID != scope.AgentID() || b.AgentID != cfg.AgentID || cfg.Prompts == nil || cfg.Prompts.ProjectID == nil || b.ProjectID != *cfg.Prompts.ProjectID {
		return ErrInvalidInput
	}
	return nil
}

func cloneLarkBinding(b *clicredential.LarkBinding) *clicredential.LarkBinding {
	if b == nil {
		return nil
	}
	copy := *b
	return &copy
}
func validateLarkBinding(c Credentials, cfg Configuration, scope Scope) error {
	b := c.LarkBinding
	if b == nil {
		return nil
	}
	if !b.Valid() || b.AgentID != scope.AgentID() || b.AgentID != cfg.AgentID || cfg.Prompts == nil || cfg.Prompts.ProjectID == nil || b.ProjectID != *cfg.Prompts.ProjectID {
		return ErrInvalidInput
	}
	return nil
}
