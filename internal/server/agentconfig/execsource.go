package agentconfig

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/dbcli"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/prompts"
	"loop-now/internal/server/store"
)

// ErrCredentialUnavailable 表示该 Agent 至少有一条凭证当前无法使用（需要重新录入或已失效）。
var ErrCredentialUnavailable = errors.New("凭证不可用")

// Snapshot 是执行器启动 Pi 所需的配置快照，含凭证内容。只在内存中使用。
type Snapshot struct {
	// Resolved level for this round. Nil is reserved for legacy task snapshots.
	ThinkingLevel   *string
	ServiceTier     *string
	LarkBinding     *clicredential.LarkBinding
	GoogleBinding   *clicredential.Binding
	ModelCredential modelcredential.Credential
	ModelBinding    *modelcredential.Binding
	// ConfigurationAgentID and TaskID are internal log attribution for task runtimes.
	ConfigurationAgentID uuid.UUID
	TaskID               uuid.UUID
	AgentID              uuid.UUID
	ConfigVersion        int64
	CredentialIdentity   string
	Provider             string
	ModelID              string
	PromptLayers         *prompts.Layers
	// ContextProjectID 是可见的上下文层：项目 Agent（含任务内实例）为其项目，平台 Agent 为空（全局）。
	ContextProjectID *uuid.UUID
	Prompt           string
	Skills           []SkillSnapshot
	Sources          []store.FolderSource
	CLIs             []string
	ModelEnv         map[string]string
	CLIPayloads      map[string]clitemplate.Payload
	Session          store.AgentSession
	// ForkSessionFile 仅任务实例首次启动使用，由平台稳定复制生成，不来自用户配置。
	ForkSessionFile string
	// Plugins 是已授权插件交给 Pi 的东西，启动 Pi 时读取。
	Plugins PluginAssets
}

// PluginAssets 是 Agent 已授权插件的 Agent 半边（plugins/SPEC.md §2.1），路径都是绝对路径；没有授权插件或全局停用时为空。
type PluginAssets struct {
	Extensions []string                   // 每个一条 -e，在平台自己的 -e 之后
	Skills     []string                   // 每个一条 --skill
	MCPServers map[string]json.RawMessage // 合并写入 Pi 目录的 mcp.json，并加 -e builtin:mcp
	Bin        []string                   // 加入 PATH
	Env        map[string]string          // 插件配置与账号
}

// SkillSnapshot 是一个 Skill 副本的文件清单。
type SkillSnapshot struct {
	Name  string
	Files []store.SkillFile
}

// ExecutionSnapshot 读取 Agent 的执行配置。任一凭证不可用时返回 ErrCredentialUnavailable，
// 不会只返回部分凭证。
func (s *Service) ExecutionSnapshot(ctx context.Context, agentID uuid.UUID) (Snapshot, error) {
	captured, err := s.Store.CaptureExecutionIdentity(ctx, agentID)
	if err != nil {
		var failure *apperr.Error
		if errors.As(err, &failure) && (failure.Code == apperr.CodeCredentialSelectionRequired || failure.Code == apperr.CodeCredentialAccountDeleted || failure.Code == apperr.CodeCredentialScopeMismatch || failure.Code == apperr.CodeValidationFailed && len(failure.Fields) == 1 && failure.Fields[0].Field == "credentials") {
			return Snapshot{}, ErrCredentialUnavailable
		}
		return Snapshot{}, err
	}
	a := captured.Agent
	snap := Snapshot{ContextProjectID: a.ProjectID, AgentID: a.ID, ConfigVersion: a.ConfigVersion, CredentialIdentity: captured.CredentialIdentity, Provider: a.ModelProvider, ModelID: a.ModelID, Prompt: a.Prompt, PromptLayers: prompts.Clone(captured.Prompts),
		ModelEnv: map[string]string{}, CLIPayloads: map[string]clitemplate.Payload{}}
	level, err := s.Models.ResolveThinking(a.ModelProvider, a.ModelID, a.ThinkingLevel)
	if err != nil {
		return Snapshot{}, err
	}
	snap.ThinkingLevel = &level
	snap.ServiceTier, err = ResolveServiceTier(a.ModelProvider, a.ServiceTier)
	if err != nil {
		return Snapshot{}, err
	}
	snap.Prompt, err = captured.Prompts.Compose()
	if err != nil {
		return Snapshot{}, err
	}
	if snap.Session, err = s.Store.GetAgentSession(ctx, agentID); err != nil {
		return Snapshot{}, err
	}
	skills, err := s.Store.ListAgentSkills(ctx, agentID)
	if err != nil {
		return Snapshot{}, err
	}
	for _, k := range skills {
		full, err := s.Store.GetAgentSkill(ctx, agentID, k.ID)
		if err != nil {
			return Snapshot{}, err
		}
		snap.Skills = append(snap.Skills, SkillSnapshot{Name: full.Name, Files: full.Files})
	}
	// 文件夹来源已退役：常态与平台 Agent 的文件夹就是它的工作目录，启动时不再同步任何来源。
	snap.CLIs = captured.CLIs
	managed := captured.ModelAuthManaged
	if managed {
		snap.ModelBinding = captured.ModelAuth
		if snap.ModelBinding != nil {
			if s.ModelAuth == nil || s.ModelAuth.Coordinator == nil {
				return Snapshot{}, ErrCredentialUnavailable
			}
			snap.ModelCredential, err = s.ModelAuth.Coordinator.Resolve(ctx, *snap.ModelBinding)
			if err != nil {
				return Snapshot{}, ErrCredentialUnavailable
			}
		}
	}
	rows := captured.CredentialRows
	dbEntries := map[string][]clitemplate.DatabaseEntry{}
	for _, r := range rows {
		if managed && r.Purpose == store.PurposeModel {
			continue
		}
		if r.LarkBinding != nil {
			if !r.LarkBinding.Valid() || r.LarkBinding.AgentID != agentID || a.ProjectID == nil || r.LarkBinding.ProjectID != *a.ProjectID {
				return Snapshot{}, ErrCredentialUnavailable
			}
			c, e := clicredential.DecodeLark(r.Secret)
			if e != nil || !c.Matches(*r.LarkBinding) {
				return Snapshot{}, ErrCredentialUnavailable
			}
			b := *r.LarkBinding
			snap.LarkBinding = &b
			continue
		}
		if r.GoogleBinding != nil {
			if !r.GoogleBinding.Valid() || r.GoogleBinding.AgentID != agentID || a.ProjectID == nil || r.GoogleBinding.ProjectID != *a.ProjectID {
				return Snapshot{}, ErrCredentialUnavailable
			}
			if _, e := clicredential.DecodeGoogle(r.Secret); e != nil {
				return Snapshot{}, ErrCredentialUnavailable
			}
			binding := *r.GoogleBinding
			snap.GoogleBinding = &binding
			continue
		}
		if s.view(r).Status != "set" {
			return Snapshot{}, ErrCredentialUnavailable
		}
		var p clitemplate.Payload
		if err := json.Unmarshal(r.Secret, &p); err != nil {
			return Snapshot{}, ErrCredentialUnavailable
		}
		if r.Purpose == store.PurposeModel {
			snap.ModelEnv = p.Env
		} else {
			if r.CLIName == "psql" || r.CLIName == "mysql" {
				item := dbcli.Connection{ProjectID: captured.Agent.ProjectID, AccountID: r.ID, VersionID: r.ID, Name: r.CLIName}
				if r.DBConnection != nil {
					item.AccountID = r.DBConnection.AccountID
					item.Name = r.DBConnection.Name
					if r.DBConnection.VersionID != nil {
						item.VersionID = *r.DBConnection.VersionID
					}
				}
				dbEntries[r.CLIName] = append(dbEntries[r.CLIName], clitemplate.DatabaseEntry{Connection: item, Payload: p})
			} else {
				snap.CLIPayloads[r.CLIName] = p
			}
		}
	}
	for _, cli := range snap.CLIs {
		if cli == "psql" || cli == "mysql" {
			p, e := clitemplate.AggregateDatabase(cli, dbEntries[cli])
			if e != nil {
				return Snapshot{}, ErrCredentialUnavailable
			}
			snap.CLIPayloads[cli] = p
		}
	}
	if !managed {
		snap.ModelCredential, err = modelcredential.LegacySingleKey(snap.Provider, snap.ModelEnv)
		if err != nil {
			return Snapshot{}, ErrCredentialUnavailable
		}
	}
	if s.PluginAssets != nil {
		if snap.Plugins, err = s.PluginAssets(ctx, agentID); err != nil {
			return Snapshot{}, err
		}
	}
	return snap, nil
}

// ReadBlob 读取内容。
func (s *Service) ReadBlob(ctx context.Context, sha string) ([]byte, error) {
	return s.Store.ReadBlob(ctx, sha)
}

// SourceFilesForSync 返回来源的全部文件记录（含待删除）。
func (s *Service) SourceFilesForSync(ctx context.Context, sourceID uuid.UUID) ([]store.SyncFile, error) {
	return s.Store.ListSourceFilesForSync(ctx, sourceID)
}

// MarkSourceSynced 记录来源已同步，并清除已处理的人工修改记录。
func (s *Service) MarkSourceSynced(ctx context.Context, sourceID uuid.UUID, processed []store.SyncFile) error {
	return s.Store.MarkSourceSynced(ctx, sourceID, processed)
}

// RecordSessionFile 记录已写入磁盘的会话文件。
func (s *Service) RecordSessionFile(ctx context.Context, agentID uuid.UUID, path string) error {
	return s.Store.RecordSessionFile(ctx, agentID, path)
}

// MarkSessionMissing 把会话标记为丢失，runID 为发现丢失的那次执行。
func (s *Service) MarkSessionMissing(ctx context.Context, agentID uuid.UUID, runID *uuid.UUID) error {
	return s.Store.MarkSessionMissing(ctx, agentID, runID)
}

// DeletedAgentIDs 返回已删除的 Agent。
func (s *Service) DeletedAgentIDs(ctx context.Context) ([]uuid.UUID, error) {
	return s.Store.DeletedAgentIDs(ctx)
}
