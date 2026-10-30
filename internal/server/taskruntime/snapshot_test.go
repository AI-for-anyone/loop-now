package taskruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/prompts"
)

const secretSentinel = "fictional-task-key-do-not-log"

func fixture(t *testing.T) (Scope, uuid.UUID, CaptureInput) {
	t.Helper()
	a, task := uuid.New(), uuid.New()
	scope, err := TaskScope(a, task)
	if err != nil {
		t.Fatal(err)
	}
	input := CaptureInput{
		Configuration: Configuration{Prompts: &prompts.Layers{Format: prompts.FormatV1, AgentVersion: 1, AgentPrompt: "private prompt text"}, AgentID: a, ConfigVersion: 1, Provider: "example-provider", ModelID: "example-model", Prompt: "private prompt text", CLIs: []string{"lark-cli", "gh"}, Skills: []Skill{{Name: "review", Files: []File{{Path: "SKILL.md", Content: []byte("skill contents")}, {Path: "scripts/check.sh", Content: []byte("echo fixture")}}}}},
		Credentials: Credentials{Model: map[string]string{"EXAMPLE_MODEL_KEY": secretSentinel}, CLI: map[string]CredentialPayload{
			"gh":       {Env: map[string]string{"GH_TOKEN": secretSentinel}},
			"lark-cli": {Files: []File{{Path: ".config/example.json", Content: []byte(secretSentinel)}}},
		}},
	}
	return scope, uuid.New(), input
}

func captureFixture(t *testing.T) *Snapshot {
	t.Helper()
	scope, id, input := fixture(t)
	s, err := Capture(scope, id, input)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// encodeEnvelope stores a hand-built envelope exactly as a converted row would.
func encodeEnvelope(t *testing.T, e envelope) []byte {
	t.Helper()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// withCredentialsJSON replaces the stored credentials object verbatim.
func withCredentialsJSON(t *testing.T, s *Snapshot, raw []byte) []byte {
	t.Helper()
	b, err := s.Encode()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err = json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m["credentials"] = raw
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// rejected reports whether a stored snapshot fails either decoding or credential validation.
func rejected(data []byte) bool {
	snap, err := Decode(data)
	if err != nil {
		return true
	}
	_, err = snap.Credentials()
	return err != nil
}

func TestScopeIsolation(t *testing.T) {
	a, t1, t2 := uuid.New(), uuid.New(), uuid.New()
	n, _ := NormalScope(a)
	s1, _ := TaskScope(a, t1)
	same, _ := TaskScope(a, t1)
	s2, _ := TaskScope(a, t2)
	other, _ := TaskScope(uuid.New(), t1)
	if s1 != same || s1.Key() != same.Key() {
		t.Fatal("same task agent must reuse scope")
	}
	seen := map[string]bool{}
	for _, s := range []Scope{n, s1, s2, other} {
		if seen[s.Key()] || !s.Valid() {
			t.Fatal("colliding scopes")
		}
		seen[s.Key()] = true
	}
	if n.IsTask() || !s1.IsTask() || s1.AgentID() != a || s1.TaskID() != t1 {
		t.Fatal("scope identity mismatch")
	}
	if _, err := NormalScope(uuid.Nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("zero agent accepted")
	}
	if _, err := TaskScope(a, uuid.Nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("zero task accepted")
	}
	if _, err := TaskScope(uuid.Nil, t1); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("zero agent accepted")
	}
	if (Scope{}).Valid() || (Scope{}).Key() != "invalid" {
		t.Fatal("zero value must be invalid")
	}
}

func TestImmutableCaptureAndReadCopies(t *testing.T) {
	scope, id, input := fixture(t)
	s, err := Capture(scope, id, input)
	if err != nil {
		t.Fatal(err)
	}
	wantConfig := s.Configuration()
	wantCreds, err := s.Credentials()
	if err != nil {
		t.Fatal(err)
	}
	input.Configuration.Prompt = "changed"
	input.Configuration.Prompts.AgentPrompt = "changed"
	input.Configuration.CLIs[0] = "other"
	input.Configuration.Skills[0].Files[0].Content[0] = 'X'
	input.Credentials.Model["EXAMPLE_MODEL_KEY"] = "changed"
	input.Credentials.CLI["gh"].Env["GH_TOKEN"] = "changed"
	input.Credentials.CLI["lark-cli"].Files[0].Content[0] = 'X'
	read := s.Configuration()
	read.Skills[0].Files[0].Content[0] = 'Y'
	read.CLIs[0] = "other"
	read.Prompt = "changed"
	creds, _ := s.Credentials()
	creds.Model["EXAMPLE_MODEL_KEY"] = "changed"
	creds.CLI["gh"].Env["GH_TOKEN"] = "changed"
	creds.CLI["lark-cli"].Files[0].Content[0] = 'Y'
	meta := s.Metadata()
	meta.CLIs[0] = "other"
	meta.SkillNames[0] = "other"
	actualCreds, err := s.Credentials()
	if err != nil || !reflect.DeepEqual(actualCreds, wantCreds) || !reflect.DeepEqual(s.Configuration(), wantConfig) {
		t.Fatal("capture shares mutable inputs or outputs")
	}
	input.Configuration.CLIs = []string{"gh", "lark-cli"}
	newSnap, err := Capture(scope, uuid.New(), input)
	if err != nil {
		t.Fatal(err)
	}
	newCreds, err := newSnap.Credentials()
	if err != nil || newSnap.Configuration().Prompt != "changed" || newCreds.Model["EXAMPLE_MODEL_KEY"] != "changed" {
		t.Fatal("new capture ignored changed input")
	}
}

func TestEncodingStoresPlaintextAndSafeDiagnostics(t *testing.T) {
	s := captureFixture(t)
	b, err := s.Encode()
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err = json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if string(wire["schemaVersion"]) != "10" || wire["sourceVersion"] != nil || !bytes.Contains(wire["credentials"], []byte(secretSentinel)) {
		t.Fatal("new capture is not a plaintext v10 snapshot without sourceVersion")
	}
	restored, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	clear(b)
	if !reflect.DeepEqual(restored.Configuration(), s.Configuration()) || restored.Scope() != s.Scope() {
		t.Fatal("roundtrip changed configuration")
	}
	creds, err := restored.Credentials()
	if err != nil || creds.Model["EXAMPLE_MODEL_KEY"] != secretSentinel || string(creds.CLI["lark-cli"].Files[0].Content) != secretSentinel {
		t.Fatal("credentials not restored")
	}
	again, err := restored.Encode()
	if err != nil {
		t.Fatal(err)
	}
	original, _ := s.Encode()
	if !bytes.Equal(again, original) {
		t.Fatal("roundtrip changed encoding")
	}
	meta, _ := json.Marshal(s.Metadata())
	for _, secret := range []string{secretSentinel, "private prompt text", "skill contents"} {
		if bytes.Contains(meta, []byte(secret)) {
			t.Fatal("metadata exposes content")
		}
	}
	_, _, input := fixture(t)
	for _, obj := range []any{s, creds, creds.CLI["gh"], input} {
		if _, err := json.Marshal(obj); !errors.Is(err, ErrUnsafeSerialization) {
			t.Fatal("default JSON must be rejected")
		}
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			text := fmt.Sprintf(verb, obj)
			if strings.Contains(text, secretSentinel) || strings.Contains(text, "private prompt text") {
				t.Fatal("diagnostic exposes content")
			}
		}
	}
}

func mutateEncoding(t *testing.T, s *Snapshot, fn func(map[string]any)) []byte {
	t.Helper()
	b, err := s.Encode()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err = json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	fn(m)
	b, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDecodeRejectsInvalidData(t *testing.T) {
	s := captureFixture(t)
	cases := map[string]func(map[string]any){
		"version":             func(m map[string]any) { m["schemaVersion"] = 999 },
		"missing version":     func(m map[string]any) { delete(m, "schemaVersion") },
		"negative source":     func(m map[string]any) { m["sourceVersion"] = -1 },
		"future source":       func(m map[string]any) { m["sourceVersion"] = 8 },
		"unknown field":       func(m map[string]any) { m["unexpected"] = true },
		"unknown nested":      func(m map[string]any) { m["configuration"].(map[string]any)["unexpected"] = true },
		"unknown credential":  func(m map[string]any) { m["credentials"].(map[string]any)["unexpected"] = true },
		"zero snapshot":       func(m map[string]any) { m["snapshotId"] = uuid.Nil.String() },
		"bad uuid":            func(m map[string]any) { m["snapshotId"] = "bad" },
		"normal scope":        func(m map[string]any) { m["scope"].(map[string]any)["taskId"] = uuid.Nil.String() },
		"mismatched agent":    func(m map[string]any) { m["configuration"].(map[string]any)["agentId"] = uuid.New().String() },
		"duplicate cli":       func(m map[string]any) { m["configuration"].(map[string]any)["clis"] = []string{"gh", "gh"} },
		"credentials string":  func(m map[string]any) { m["credentials"] = "plain" },
		"missing credentials": func(m map[string]any) { delete(m, "credentials") },
		"null credentials":    func(m map[string]any) { m["credentials"] = nil },
		"sealed credentials": func(m map[string]any) {
			m["credentials"] = map[string]any{"Alg": "A256GCM-envelope-v1", "KeyID": "local-x", "WrappedDEK": "AA==", "Nonce": "AA==", "Ciphertext": "AA=="}
		},
		"credential env type": func(m map[string]any) { m["credentials"].(map[string]any)["model"] = []string{"not-a-map"} },
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(mutateEncoding(t, s, fn)); err == nil {
				t.Fatal("invalid data accepted")
			}
		})
	}
	b, _ := s.Encode()
	for _, data := range [][]byte{nil, []byte("null"), []byte("{"), append(b, []byte(" {}")...)} {
		if _, err := Decode(data); err == nil {
			t.Fatal("invalid JSON accepted")
		}
	}
}

func TestDecodeRejectsEncryptedSchemaVersions(t *testing.T) {
	s := captureFixture(t)
	for version := 1; version <= 7; version++ {
		data := mutateEncoding(t, s, func(m map[string]any) { m["schemaVersion"] = version })
		if _, err := Decode(data); !errors.Is(err, ErrUnsupportedVersion) {
			t.Fatalf("schema %d accepted: %v", version, err)
		}
	}
}

func TestCaptureValidation(t *testing.T) {
	scope, id, input := fixture(t)
	mutations := map[string]func(*CaptureInput){
		"scope mismatch": func(i *CaptureInput) { i.Configuration.AgentID = uuid.New() },
		"config version": func(i *CaptureInput) { i.Configuration.ConfigVersion = 0 },
		"duplicate skills": func(i *CaptureInput) {
			i.Configuration.Skills = append(i.Configuration.Skills, i.Configuration.Skills[0])
		},
		"duplicate file": func(i *CaptureInput) {
			i.Configuration.Skills[0].Files = append(i.Configuration.Skills[0].Files, i.Configuration.Skills[0].Files[0])
		},
		"escape file":          func(i *CaptureInput) { i.Configuration.Skills[0].Files[0].Path = "../bad" },
		"unallowed credential": func(i *CaptureInput) { i.Credentials.CLI["other"] = CredentialPayload{} },
		"bad env name":         func(i *CaptureInput) { i.Credentials.Model["KEY=bad"] = "ignored" },
		"non utf8 prompt":      func(i *CaptureInput) { i.Configuration.Prompt = string([]byte{0xff}) },
		"non utf8 credential":  func(i *CaptureInput) { i.Credentials.Model["EXAMPLE_MODEL_KEY"] = string([]byte{0xff}) },
		"missing prompts":      func(i *CaptureInput) { i.Configuration.Prompts = nil },
	}
	for name, fn := range mutations {
		t.Run(name, func(t *testing.T) {
			fresh := CaptureInput{Configuration: cloneConfig(input.Configuration), Credentials: cloneCredentials(input.Credentials)}
			fn(&fresh)
			if s, err := Capture(scope, id, fresh); s != nil || !errors.Is(err, ErrInvalidInput) {
				t.Fatal("invalid input accepted")
			}
		})
	}
	normal, _ := NormalScope(scope.AgentID())
	for _, bad := range []Scope{Scope{}, normal} {
		if s, err := Capture(bad, id, input); s != nil || err == nil {
			t.Fatal("non-task capture accepted")
		}
	}
	if s, err := Capture(scope, uuid.Nil, input); s != nil || err == nil {
		t.Fatal("zero snapshot accepted")
	}
}

func TestConcurrentReadIsolation(t *testing.T) {
	s := captureFixture(t)
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			for range 20 {
				cfg := s.Configuration()
				cfg.CLIs[0] = "changed"
				cfg.Skills[0].Files[0].Content[0] = 'X'
				c, err := s.Credentials()
				if err != nil {
					t.Error("read failed")
					return
				}
				c.Model["EXAMPLE_MODEL_KEY"] = "changed"
				c.CLI["lark-cli"].Files[0].Content[0] = 'X'
				b, err := s.Encode()
				if err != nil {
					t.Error("encode failed")
					return
				}
				if _, err = Decode(b); err != nil {
					t.Error("decode failed")
					return
				}
			}
		})
	}
	wg.Wait()
	if c, err := s.Credentials(); err != nil || c.Model["EXAMPLE_MODEL_KEY"] != secretSentinel || string(c.CLI["lark-cli"].Files[0].Content) != secretSentinel {
		t.Fatal("concurrent readers changed snapshot")
	}
}

func TestInvalidPayloadReturnsNoPartialCredentials(t *testing.T) {
	s := captureFixture(t)
	// 模型部分有效，CLI部分不在允许列表中；不能返回先读出的模型凭证。
	s.env.Credentials = &credentialsWire{Model: map[string]string{"EXAMPLE_MODEL_KEY": secretSentinel}, CLI: map[string]payloadWire{"unallowed": {}}}
	c, err := s.Credentials()
	if !errors.Is(err, ErrCredentials) || c.Model != nil || c.CLI != nil {
		t.Fatal("invalid payload returned partial credentials")
	}
	if strings.Contains(err.Error(), secretSentinel) {
		t.Fatal("error exposes plaintext")
	}
	if !rejected(withCredentialsJSON(t, captureFixture(t), []byte(`{"model":{"EXAMPLE_MODEL_KEY":"x"},"cli":{"unallowed":{"env":{},"files":[]}}}`))) {
		t.Fatal("stored invalid payload accepted")
	}
}

// A converted v1 payload keeps its original credential map; no current Agent
// lookup or rewrite happens on read.
func TestConvertedSourceVersion1PreservesCredentialMap(t *testing.T) {
	scope, id, input := fixture(t)
	cfg := cloneConfig(input.Configuration)
	cfg.Prompts = nil
	data := encodeEnvelope(t, envelope{SchemaVersion: frozenSourcesSchema, SourceVersion: 1, SnapshotID: id, Scope: scope.wire(), Configuration: cfg, Credentials: &credentialsWire{Model: input.Credentials.Model}})
	restored, err := Decode(data)
	if err != nil {
		t.Fatal("converted v1 snapshot rejected", err)
	}
	credentials, err := restored.Credentials()
	if err != nil || !reflect.DeepEqual(credentials.Model, input.Credentials.Model) {
		t.Fatal("v1 credential map changed", err)
	}
	roundtrip, err := restored.Encode()
	if err != nil || !bytes.Equal(data, roundtrip) {
		t.Fatal("converted snapshot was rewritten", err)
	}
	ref := modelcredential.Binding{VersionID: uuid.New(), AgentID: scope.AgentID(), Provider: cfg.Provider}
	restored.env.Credentials = &credentialsWire{ModelAuth: &ref}
	if _, err := restored.Credentials(); !errors.Is(err, ErrCredentials) {
		t.Fatal("source v1 accepted a model grant", err)
	}
}

func TestSnapshotGrantBindingIsImmutableAndScoped(t *testing.T) {
	scope, id, input := fixture(t)
	ref := modelcredential.Binding{VersionID: uuid.New(), AgentID: scope.AgentID(), Provider: input.Configuration.Provider}
	input.Credentials.Model = nil
	input.Credentials.ModelAuth = &ref
	snap, err := Capture(scope, id, input)
	if err != nil {
		t.Fatal(err)
	}
	original := ref
	original.OwnerKind = "agent"
	ref.VersionID = uuid.New()
	credentials, err := snap.Credentials()
	if err != nil || *credentials.ModelAuth != original {
		t.Fatal("grant was rebound", err)
	}
	credentials.ModelAuth.VersionID = uuid.New()
	data, _ := snap.Encode()
	restored, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	credentials, err = restored.Credentials()
	if err != nil || *credentials.ModelAuth != original {
		t.Fatal("grant did not survive snapshot roundtrip", err)
	}
	for _, mutate := range []func(*modelcredential.Binding){
		func(b *modelcredential.Binding) { b.AgentID = uuid.New() },
		func(b *modelcredential.Binding) { b.Provider = "other" },
	} {
		bad := original
		mutate(&bad)
		input.Credentials.ModelAuth = &bad
		if _, err := Capture(scope, uuid.New(), input); err == nil {
			t.Fatal("cross identity grant accepted")
		}
		restored.env.Credentials = &credentialsWire{ModelAuth: &bad}
		if _, err := restored.Credentials(); !errors.Is(err, ErrCredentials) {
			t.Fatal("stored cross identity grant accepted")
		}
	}
}

func TestConvertedSourceVersion2PromptRules(t *testing.T) {
	scope, id, input := fixture(t)
	cfg := cloneConfig(input.Configuration)
	cfg.Prompts = nil
	ref := modelcredential.Binding{VersionID: uuid.New(), AgentID: scope.AgentID(), Provider: cfg.Provider}
	data := encodeEnvelope(t, envelope{SchemaVersion: frozenSourcesSchema, SourceVersion: 2, SnapshotID: id, Scope: scope.wire(), Configuration: cfg, Credentials: &credentialsWire{ModelAuth: &ref, CLI: map[string]payloadWire{}}})
	restored, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	creds, err := restored.Credentials()
	if err != nil || creds.ModelAuth == nil || *creds.ModelAuth != ref {
		t.Fatal("v2 grant binding changed", err)
	}
	got, err := restored.Encode()
	if err != nil || !bytes.Equal(got, data) || restored.Configuration().Prompts != nil {
		t.Fatal("v2 rewritten", err)
	}
	for _, source := range []int{3, 0} {
		bad := mutateEncoding(t, restored, func(m map[string]any) {
			if source == 0 {
				delete(m, "sourceVersion")
			} else {
				m["sourceVersion"] = source
			}
		})
		if _, err = Decode(bad); err == nil {
			t.Fatalf("source %d accepted without explicit layers", source)
		}
	}
	withPrompts := cloneConfig(input.Configuration)
	if _, err = Decode(encodeEnvelope(t, envelope{SchemaVersion: frozenSourcesSchema, SourceVersion: 2, SnapshotID: id, Scope: scope.wire(), Configuration: withPrompts, Credentials: &credentialsWire{CLI: map[string]payloadWire{}}})); err == nil {
		t.Fatal("source v2 accepted prompt layers")
	}
}

func TestConvertedLegacySourceVersionsRejectNewIdentityFields(t *testing.T) {
	for _, version := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			scope, id, input := fixture(t)
			cfg := cloneConfig(input.Configuration)
			if version < 3 {
				cfg.Prompts = nil
			}
			encoded := encodeEnvelope(t, envelope{SchemaVersion: frozenSourcesSchema, SourceVersion: version, SnapshotID: id, Scope: scope.wire(), Configuration: cfg, Credentials: &credentialsWire{Model: map[string]string{"SYNTHETIC_OLD_KEY": "old-value"}, CLI: map[string]payloadWire{}}})
			restored, err := Decode(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = restored.Credentials(); err != nil {
				t.Fatal("converted credentials rejected", err)
			}
			roundtrip, _ := restored.Encode()
			if !bytes.Equal(encoded, roundtrip) {
				t.Fatal("converted snapshot rewritten")
			}
			for _, field := range []string{"credentialIdentity", "credentialsRevision"} {
				var wire map[string]any
				if err = json.Unmarshal(encoded, &wire); err != nil {
					t.Fatal(err)
				}
				value := any("forged-new-identity")
				if field == "credentialsRevision" {
					value = 2
				}
				wire["configuration"].(map[string]any)[field] = value
				bad, _ := json.Marshal(wire)
				if _, err = Decode(bad); err == nil {
					t.Fatal("old source schema accepted new identity field")
				}
			}
		})
	}
}

// 新捕获复制基础 Agent 的文件夹；schema 8 的旧任务保留冻结来源，schema 9 继续可读。
func TestSchemaVersionTellsFolderCopyFromFrozenSources(t *testing.T) {
	scope, id, input := fixture(t)
	snap, err := Capture(scope, id, input)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := snap.Encode()
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := Decode(encoded)
	if err != nil || !fresh.CopiesAgentFolder() {
		t.Fatalf("新捕获应复制文件夹：%v", err)
	}
	env := snap.env
	env.SchemaVersion = frozenSourcesSchema
	old, err := Decode(encodeEnvelope(t, env))
	if err != nil || old.CopiesAgentFolder() {
		t.Fatalf("schema 8 的旧任务应保留冻结来源：%v", err)
	}
	env.SchemaVersion = copiedAgentFolderSchema
	v9, err := Decode(encodeEnvelope(t, env))
	if err != nil || !v9.CopiesAgentFolder() {
		t.Fatalf("schema 9 的旧任务应继续复制文件夹：%v", err)
	}
	env.SchemaVersion, env.SourceVersion = schemaVersion, 5
	if _, err := Decode(encodeEnvelope(t, env)); err == nil {
		t.Fatal("当前 schema 不能带 sourceVersion")
	}
	env.SchemaVersion, env.SourceVersion = schemaVersion+1, 0
	if _, err := Decode(encodeEnvelope(t, env)); err == nil {
		t.Fatal("未知 schema 应拒绝")
	}
}
