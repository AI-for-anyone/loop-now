package httpapi

import (
	"context"
	"net/http"
	"slices"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
)

// pluginScopes lists the operations plugin tokens may call and the scope each needs, matching x-plugin-scope in the
// contract (checked by TestRouteCoverage).
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

// managedIdentityOps are the operations a plugin that manages external identities (plugins/SPEC.md §12) may also call
// with identities.manage: the identity in a read path must be managed by this plugin (checked by pluginGate), and a
// reply may only carry an asIdentityId the plugin manages (checked by replyInbox).
var managedIdentityOps = map[string]bool{"listExternalIdentityInbox": true, "getExternalIdentityInboxMessage": true, "replyInboxMessage": true}

// PluginScopes returns the operations plugin tokens may call and the scope each needs (for contract tests).
func PluginScopes() map[string]string { return pluginScopes }

// consoleAuth sets the identity of loopback /api/v1 requests to the console operator; requests with an lnp_ plugin
// token get that plugin as identity, and any other request with an Authorization header gets a 403.
func (s *Server) consoleAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, present := auth.BearerToken(r)
		if !present {
			next.ServeHTTP(w, r.WithContext(auth.With(r.Context(), auth.Console())))
			return
		}
		prefix, id, secret, ok := auth.ParseToken(tok)
		if !ok || prefix != auth.PluginPrefix {
			writeError(w, apperr.Forbidden("The console API accepts only plugin tokens. External identities use the external port; Agents use /agent/v1."))
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

// pluginAllowed checks whether a plugin token may call this route: the operation must have an x-plugin-scope that is
// within the token's scopes; a project, Agent or task in the path must belong to a project that has the plugin enabled
// (platform Agents, the global context folder and external identities are not limited by project). Rejections are
// written to the plugin log.
func (s *Server) pluginAllowed(r *http.Request, rt Route, p auth.Principal) error {
	err := s.pluginGate(r, rt, p)
	if err != nil && apperr.Is(err, apperr.CodeForbidden) {
		if lerr := s.Store.AppendPluginHostLog(context.WithoutCancel(r.Context()), p.Plugin, "warn", "Call to "+rt.Op+" was denied: "+err.Error()); lerr != nil {
			s.Log.Warn("failed to write the plugin log", "plugin", p.Plugin, "err", lerr)
		}
	}
	return err
}

// checkManaged requires the external identity to be managed by this plugin and the plugin manifest to have the
// identities.manage scope; otherwise 403.
func (s *Server) checkManaged(ctx context.Context, p auth.Principal, identity uuid.UUID) error {
	if !slices.Contains(p.Scopes, "identities.manage") {
		return apperr.Forbidden("The plugin manifest does not declare the identities.manage scope.")
	}
	ok, err := s.Store.IdentityManagedBy(ctx, identity, p.Plugin)
	if err != nil {
		return err
	}
	if !ok {
		return apperr.Forbidden("The plugin does not manage this external identity.")
	}
	return nil
}

func (s *Server) pluginGate(r *http.Request, rt Route, p auth.Principal) error {
	scope := pluginScopes[rt.Op]
	if scope == "" {
		return apperr.Forbidden("Plugin tokens cannot call this endpoint.")
	}
	ctx := r.Context()
	if !slices.Contains(p.Scopes, scope) {
		if !managedIdentityOps[rt.Op] || !slices.Contains(p.Scopes, "identities.manage") {
			return apperr.Forbidden("The plugin manifest does not declare the " + scope + " scope.")
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
			return apperr.NotFound("Task")
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
		return apperr.Forbidden("The plugin is not enabled in this project.")
	}
	return nil
}
