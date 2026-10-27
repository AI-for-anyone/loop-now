package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/plugins"
	"loop-now/internal/server/store"
)

// 插件第二版的管理接口（plugins/SPEC.md §6、§7），实现在 plugins.Service。

var pluginIDPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)

func pluginID(r *http.Request) (string, error) {
	id := r.PathValue("pluginId")
	if !pluginIDPattern.MatchString(id) {
		return "", apperr.NotFound("插件")
	}
	return id, nil
}

func (s *Server) listPlugins(w http.ResponseWriter, r *http.Request) {
	list, err := s.Plugins.List(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": list})
}

func (s *Server) previewPlugin(w http.ResponseWriter, r *http.Request) {
	var in plugins.PreviewRequest
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.Plugins.Preview(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) installPlugin(w http.ResponseWriter, r *http.Request) {
	var in plugins.InstallInput
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.Plugins.Install(r.Context(), in, "")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (s *Server) getPlugin(w http.ResponseWriter, r *http.Request) {
	id, err := pluginID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d, err := s.Plugins.Get(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) updatePlugin(w http.ResponseWriter, r *http.Request) {
	id, err := pluginID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in plugins.InstallInput
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := s.Store.GetPlugin(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.Plugins.Install(r.Context(), in, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) uninstallPlugin(w http.ResponseWriter, r *http.Request) {
	id, err := pluginID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	deleteData := false
	if v := r.URL.Query().Get("deleteData"); v != "" {
		if deleteData, err = strconv.ParseBool(v); err != nil {
			s.fail(w, r, apperr.Invalid("deleteData", "只能是 true 或 false"))
			return
		}
	}
	if err := s.Plugins.Uninstall(r.Context(), id, deleteData); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getPluginConfig(w http.ResponseWriter, r *http.Request) {
	id, err := pluginID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	c, err := s.Plugins.Config(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) putPluginConfig(w http.ResponseWriter, r *http.Request) {
	id, err := pluginID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in plugins.ConfigInput
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	c, err := s.Plugins.PutConfig(r.Context(), id, in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// queryLimit 解析 limit：不传时为 def，超出 [1, max] 时返回 422。
func queryLimit(r *http.Request, def, max int) (int, error) {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > max {
		return 0, apperr.Invalid("limit", "必须是 1 到 "+strconv.Itoa(max)+" 之间的整数")
	}
	return n, nil
}

func (s *Server) listPluginLogs(w http.ResponseWriter, r *http.Request) {
	id, err := pluginID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	limit, err := queryLimit(r, 100, 500)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var before int64
	if v := r.URL.Query().Get("before"); v != "" {
		if before, err = strconv.ParseInt(v, 10, 64); err != nil || before < 1 {
			s.fail(w, r, apperr.Invalid("before", "必须是正整数"))
			return
		}
	}
	if _, err := s.Store.GetPlugin(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	entries, err := s.Store.PluginLogs(r.Context(), id, before, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var next *int64
	if len(entries) == limit {
		next = &entries[len(entries)-1].ID
	}
	if entries == nil {
		entries = []store.PluginLogEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "nextBefore": next})
}

type pluginDeliveryView struct {
	EventID    string     `json:"eventId"`
	EventType  string     `json:"eventType"`
	At         any        `json:"at"`
	ProjectID  *uuid.UUID `json:"projectId"`
	AgentID    *uuid.UUID `json:"agentId"`
	TaskID     *uuid.UUID `json:"taskId"`
	RunID      *uuid.UUID `json:"runId"`
	Status     string     `json:"status"`
	Attempts   int        `json:"attempts"`
	LastError  *string    `json:"lastError"`
	AckedAt    any        `json:"ackedAt"`
	DurationMs *int64     `json:"durationMs"`
}

func (s *Server) listPluginDeliveries(w http.ResponseWriter, r *http.Request) {
	id, err := pluginID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	limit, err := queryLimit(r, 50, 200)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	q := store.PluginDeliveryQuery{Status: r.URL.Query().Get("status"), Before: r.URL.Query().Get("before"), Limit: limit}
	switch q.Status {
	case "", "pending", "delivered", "failed":
	default:
		s.fail(w, r, apperr.Invalid("status", "只能是 pending、delivered 或 failed"))
		return
	}
	if v := r.URL.Query().Get("projectId"); v != "" {
		pid, err := uuid.Parse(v)
		if err != nil {
			s.fail(w, r, apperr.Invalid("projectId", "必须是 UUID"))
			return
		}
		q.Project = &pid
	}
	if _, err := s.Store.GetPlugin(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	rows, err := s.Store.PluginDeliveries(r.Context(), id, q)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]pluginDeliveryView, 0, len(rows))
	for _, d := range rows {
		v := pluginDeliveryView{EventID: d.ID, EventType: d.Type, At: d.At, ProjectID: d.ProjectID, AgentID: d.AgentID, TaskID: d.TaskID, RunID: d.RunID,
			Status: d.Status, Attempts: d.Attempts, LastError: d.LastError, DurationMs: d.DurationMs}
		if d.AckedAt != nil {
			v.AckedAt = *d.AckedAt
		}
		out = append(out, v)
	}
	var next *string
	if len(rows) == limit {
		next = &rows[len(rows)-1].ID
	}
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": out, "nextBefore": next})
}

func (s *Server) listPluginState(w http.ResponseWriter, r *http.Request) {
	id, err := pluginID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var project *uuid.UUID
	if v := r.URL.Query().Get("projectId"); v != "" {
		pid, err := uuid.Parse(v)
		if err != nil {
			s.fail(w, r, apperr.Invalid("projectId", "必须是 UUID"))
			return
		}
		project = &pid
	}
	if _, err := s.Store.GetPlugin(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	entries, err := s.Store.PluginStateList(r.Context(), id, project, r.URL.Query().Get("prefix"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if entries == nil {
		entries = []store.PluginStateEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (s *Server) listBuiltinPluginSources(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"sources": s.Plugins.BuiltinSources()})
}

func (s *Server) getPluginHost(w http.ResponseWriter, r *http.Request) {
	v, err := s.Plugins.HostStatus(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) restartPluginHost(w http.ResponseWriter, r *http.Request) {
	v, err := s.Plugins.RestartHost(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, v)
}

func (s *Server) getPluginSettings(w http.ResponseWriter, r *http.Request) {
	v, err := s.Plugins.Settings(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) putPluginSettings(w http.ResponseWriter, r *http.Request) {
	var in store.PluginSettings
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.Plugins.PutSettings(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) listProjectPlugins(w http.ResponseWriter, r *http.Request) {
	project, err := pathUUID(r, "projectId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, err := s.Plugins.ProjectPlugins(r.Context(), project)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": list})
}

func (s *Server) putProjectPlugin(w http.ResponseWriter, r *http.Request) {
	project, err := pathUUID(r, "projectId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	id, err := pluginID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in plugins.ProjectInput
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.Plugins.PutProject(r.Context(), project, id, in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// projectAndPlugin 解析路径中的项目与插件。
func projectAndPlugin(r *http.Request) (uuid.UUID, string, error) {
	project, err := pathUUID(r, "projectId")
	if err != nil {
		return uuid.Nil, "", err
	}
	id, err := pluginID(r)
	return project, id, err
}

func (s *Server) listPluginAccounts(w http.ResponseWriter, r *http.Request) {
	project, id, err := projectAndPlugin(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, err := s.Plugins.Accounts(r.Context(), project, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": list})
}

func (s *Server) createPluginAccount(w http.ResponseWriter, r *http.Request) {
	project, id, err := projectAndPlugin(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in plugins.AccountCreate
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	a, err := s.Plugins.CreateAccount(r.Context(), project, id, in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) updatePluginAccount(w http.ResponseWriter, r *http.Request) {
	project, id, err := projectAndPlugin(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	account, err := pathUUID(r, "accountId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in plugins.AccountUpdate
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	a, err := s.Plugins.UpdateAccount(r.Context(), project, id, account, in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) deletePluginAccount(w http.ResponseWriter, r *http.Request) {
	project, id, err := projectAndPlugin(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	account, err := pathUUID(r, "accountId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Store.DeletePluginAccount(r.Context(), project, id, account); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listAgentPlugins(w http.ResponseWriter, r *http.Request) {
	agent, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, err := s.Plugins.AgentPlugins(r.Context(), agent)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": list})
}

func (s *Server) putAgentPlugin(w http.ResponseWriter, r *http.Request) {
	agent, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	id, err := pluginID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in plugins.AgentInput
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.Plugins.PutAgentPlugin(r.Context(), agent, id, in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// folderChanged 在文件夹写入、建目录、移动或删除成功后写 folder.changed 事件；失败只记日志。
func (s *Server) folderChanged(r *http.Request, path string) {
	ctx := context.WithoutCancel(r.Context())
	var project, agent *uuid.UUID
	level := "global"
	if v := r.PathValue("agentId"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			agent, level = &id, "agent"
			if a, err := s.Store.GetAgent(ctx, id); err == nil {
				project = a.ProjectID
			}
		}
	} else if v := r.PathValue("projectId"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			project, level = &id, "project"
		}
	}
	p := principal(r)
	by := map[string]any{"kind": string(p.Kind), "id": nil, "address": nil}
	if p.ID != uuid.Nil {
		by["id"] = p.ID
	}
	if p.Address != "" {
		by["address"] = p.Address
	}
	if err := s.Store.EmitPluginEvent(ctx, "folder.changed", project, agent, nil, nil, map[string]any{"level": level, "path": path, "by": by}); err != nil {
		s.Log.Warn("写入文件夹变化事件失败", "err", err)
	}
}

func (s *Server) invokePluginAction(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Where plugins.ActionWhere `json:"where"`
		Input map[string]any      `json:"input"`
	}
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	res, err := s.Plugins.Action(r.Context(), r.PathValue("pluginId"), r.PathValue("actionId"), in.Where, in.Input)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) listPluginMarkets(w http.ResponseWriter, r *http.Request) {
	markets, err := s.Plugins.Markets(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"markets": markets})
}

// pluginIngress 是插件的 HTTP 入口（plugins/SPEC.md §11），不在契约的操作里：方法、路径、查询、头与正文（不超过 1 MiB）
// 原样交给插件，插件的状态码、头与正文原样返回。via 为 external（对外端口 /hooks，无令牌）或 console（回环，控制台或该插件自己的令牌）。
func (s *Server) pluginIngress(via string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("pluginId")
		if p, ok := auth.From(r.Context()); ok && p.Kind == auth.KindPlugin && p.Plugin != id {
			s.fail(w, r, apperr.Forbidden("插件令牌只能调用自己的 HTTP 入口"))
			return
		}
		if s.Plugins == nil {
			s.fail(w, r, apperr.NotFound("插件 HTTP 入口"))
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, plugins.MaxHTTPBody))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				err = apperr.TooLarge("请求正文超过 1 MiB")
			}
			s.fail(w, r, err)
			return
		}
		res, err := s.Plugins.HTTP(r.Context(), id, plugins.HTTPRequest{Via: via, Method: r.Method, Path: "/" + r.PathValue("path"),
			Query: r.URL.RawQuery, Headers: plugins.HTTPRequestHeaders(r.Header), Body: body})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		for k, v := range res.Headers {
			w.Header()[http.CanonicalHeaderKey(k)] = v
		}
		w.WriteHeader(res.Status)
		_, _ = w.Write(res.Body)
	}
}
