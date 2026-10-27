package httpapi

import (
	"context"
	"net/http"
	"slices"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
)

// pluginScopes 是插件令牌可以调用的操作及所需范围，与契约中的 x-plugin-scope 一致（TestRouteCoverage 核对）。
var pluginScopes = map[string]string{
	"getAgent": "agents.read", "getAgentStatus": "agents.read", "listProjectAgents": "agents.read", "listPlatformAgents": "agents.read", "getProject": "agents.read",
	"updateAgent": "agents.write", "sendMessage": "agents.write",
	"listProjectTasks": "tasks.read", "getTask": "tasks.read", "getTaskNodeRun": "tasks.read", "listTaskNodeMessages": "tasks.read",
	"retryTask": "tasks.write", "cancelTask": "tasks.write", "sendTaskNodeMessage": "tasks.write",
	"listMessages": "runs.read", "getRunTimeline": "runs.read", "getTaskNodeRunTimeline": "runs.read",
	"listAgentInbox": "inbox.read", "getAgentInboxMessage": "inbox.read", "listExternalIdentityInbox": "inbox.read", "getExternalIdentityInboxMessage": "inbox.read",
	"listInboxMessages": "inbox.read", "readInboxMessage": "inbox.read", "listInboxThreads": "inbox.read", "getInboxThread": "inbox.read", "readInboxThread": "inbox.read",
	"sendInboxMessage": "inbox.send", "replyInboxMessage": "inbox.send",
	"listProjectContextFolderEntries": "folders.read", "getProjectContextFolderFileContent": "folders.read",
	"listGlobalContextFolderEntries": "folders.read", "getGlobalContextFolderFileContent": "folders.read",
	"putProjectContextFolderFileContent": "folders.write", "deleteProjectContextFolderEntry": "folders.write",
	"createProjectContextFolderDirectory": "folders.write", "moveProjectContextFolderEntry": "folders.write",
	"putGlobalContextFolderFileContent": "folders.write", "deleteGlobalContextFolderEntry": "folders.write",
	"createGlobalContextFolderDirectory": "folders.write", "moveGlobalContextFolderEntry": "folders.write",
	"listExternalIdentities": "identities.read", "getExternalIdentity": "identities.read",
}

// managedIdentityOps 是代管外部身份的插件（plugins/SPEC.md §12）凭 identities.manage 也能调用的操作：
// 读路径里的身份要由这个插件代管（pluginGate 核对），回复只能带它代管的 asIdentityId（replyInbox 核对）。
var managedIdentityOps = map[string]bool{"listExternalIdentityInbox": true, "getExternalIdentityInboxMessage": true, "replyInboxMessage": true}

// PluginScopes 返回插件令牌可以调用的操作及所需范围（契约测试用）。
func PluginScopes() map[string]string { return pluginScopes }

// consoleAuth 把回环 /api/v1 的请求身份设为控制台操作者；带 lnp_ 插件令牌的设为该插件，
// 其余带 Authorization 头的请求返回 403。
func (s *Server) consoleAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, present := auth.BearerToken(r)
		if !present {
			next.ServeHTTP(w, r.WithContext(auth.With(r.Context(), auth.Console())))
			return
		}
		prefix, id, secret, ok := auth.ParseToken(tok)
		if !ok || prefix != auth.PluginPrefix {
			writeError(w, apperr.Forbidden("控制台接口不接受令牌：外部身份请使用对外端口，Agent 请使用 /agent/v1"))
			return
		}
		t, err := s.Store.LookupPluginToken(r.Context(), id)
		if err != nil {
			s.fail(w, r, unauthenticatedIfNotFound(err))
			return
		}
		if t.Revoked || !auth.SecretMatches(t.Hash, secret) {
			s.fail(w, r, unauthenticated())
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.With(r.Context(), auth.PluginPrincipal(t.PluginID, t.Scopes))))
	})
}

// pluginAllowed 检查插件令牌能否调用这条路由：操作须有 x-plugin-scope 且在令牌范围内；路径中的项目、Agent、任务
// 须属于启用了该插件的项目（平台 Agent、全局上下文文件夹与外部身份不受项目限制）。拒绝时记入插件日志。
func (s *Server) pluginAllowed(r *http.Request, rt Route, p auth.Principal) error {
	err := s.pluginGate(r, rt, p)
	if err != nil && apperr.Is(err, apperr.CodeForbidden) {
		if lerr := s.Store.AppendPluginHostLog(context.WithoutCancel(r.Context()), p.Plugin, "warn", "调用 "+rt.Op+" 被拒绝："+err.Error()); lerr != nil {
			s.Log.Warn("写入插件日志失败", "plugin", p.Plugin, "err", lerr)
		}
	}
	return err
}

// checkManaged 要求外部身份由这个插件代管，且插件清单有 identities.manage 范围；否则 403。
func (s *Server) checkManaged(ctx context.Context, p auth.Principal, identity uuid.UUID) error {
	if !slices.Contains(p.Scopes, "identities.manage") {
		return apperr.Forbidden("插件清单没有声明 identities.manage 范围")
	}
	ok, err := s.Store.IdentityManagedBy(ctx, identity, p.Plugin)
	if err != nil {
		return err
	}
	if !ok {
		return apperr.Forbidden("插件没有代管这个外部身份")
	}
	return nil
}

func (s *Server) pluginGate(r *http.Request, rt Route, p auth.Principal) error {
	scope := pluginScopes[rt.Op]
	if scope == "" {
		return apperr.Forbidden("插件令牌不能调用这个接口")
	}
	ctx := r.Context()
	if !slices.Contains(p.Scopes, scope) {
		if !managedIdentityOps[rt.Op] || !slices.Contains(p.Scopes, "identities.manage") {
			return apperr.Forbidden("插件清单没有声明 " + scope + " 范围")
		}
		if r.PathValue("identityId") != "" {
			identity, err := pathUUID(r, "identityId")
			if err != nil {
				return err
			}
			if err := s.checkManaged(ctx, p, identity); err != nil {
				return err
			}
		}
	}
	var project *uuid.UUID
	switch {
	case r.PathValue("projectId") != "":
		id, err := pathUUID(r, "projectId")
		if err != nil {
			return err
		}
		project = &id
	case r.PathValue("taskId") != "":
		id, err := pathUUID(r, "taskId")
		if err != nil {
			return err
		}
		var pid uuid.UUID
		if err := s.Store.Pool.QueryRow(ctx, `SELECT project_id FROM task_records WHERE id=$1`, id).Scan(&pid); err != nil {
			return apperr.NotFound("任务")
		}
		project = &pid
	case r.PathValue("agentId") != "":
		id, err := pathUUID(r, "agentId")
		if err != nil {
			return err
		}
		a, err := s.Store.GetAgent(ctx, id)
		if err != nil {
			return err
		}
		project = a.ProjectID
	}
	if project == nil {
		return nil
	}
	on, err := s.Store.PluginEnabledIn(ctx, p.Plugin, *project)
	if err != nil {
		return err
	}
	if !on {
		return apperr.Forbidden("插件没有在这个项目启用")
	}
	return nil
}
