package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"net/http"
	"slices"
	"strconv"
	"time"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/identity"
	"loop-now/internal/server/inbox"
	"loop-now/internal/server/store"
)

// ---- 当前身份与按地址查找 ----

func (s *Server) whoami(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, apitypes.FromPrincipal(principal(r)))
}

// findAgents 按地址查找 Agent，返回 0 或 1 项；找不到时返回空列表。
func (s *Server) findAgents(w http.ResponseWriter, r *http.Request) {
	addr := r.URL.Query().Get("address")
	if addr == "" {
		s.fail(w, r, apperr.Invalid("address", "必须提供地址"))
		return
	}
	out := apitypes.AgentList{Agents: []apitypes.AgentListItem{}}
	a, err := s.Inbox.FindAgent(r.Context(), addr)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if a != nil {
		it, err := s.Store.GetAgentListItem(r.Context(), a.ID)
		if err != nil && !store.IsNotFound(err) {
			s.fail(w, r, err)
			return
		}
		if err == nil {
			out.Agents = append(out.Agents, apitypes.FromAgentListItem(it))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- 我的 Inbox（控制台、外部身份与 Agent 共用，当前身份取自请求） ----

// inboxQuery 解析列表参数 unread、cursor 与 limit。
func (s *Server) inboxQuery(r *http.Request) (store.InboxListQuery, error) {
	q := r.URL.Query()
	unread := false
	if v := q.Get("unread"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return store.InboxListQuery{}, apperr.Invalid("unread", "只能是 true 或 false")
		}
		unread = b
	}
	limit := 0
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return store.InboxListQuery{}, apperr.Invalid("limit", "必须是至少为 1 的整数")
		}
		limit = n
	}
	return s.Inbox.Query(unread, q.Get("cursor"), limit)
}

func (s *Server) listInbox(w http.ResponseWriter, r *http.Request) {
	q, err := s.inboxQuery(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	page, err := s.Inbox.List(r.Context(), principal(r), q)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.FromInboxPage(page))
}

func (s *Server) inboxBodyLimit() int64 { return s.O.Limits.MaxInboxMessage*4 + 1024 }

func (s *Server) sendInbox(w http.ResponseWriter, r *http.Request) {
	var in apitypes.InboxSendInput
	if err := readJSON(w, r, s.inboxBodyLimit(), &in); err != nil {
		s.fail(w, r, err)
		return
	}
	var run uuid.UUID
	if in.RunID != nil {
		run = *in.RunID
	}
	p := principal(r)
	project, agent, task := s.principalScope(r, p)
	if project == nil && agent == nil && s.Hooks != nil {
		// 控制台与外部身份发给 Agent：按收件 Agent 的项目决定哪些插件参与。
		if a, e := s.Inbox.FindAgent(r.Context(), in.To); e == nil && a != nil {
			project = a.ProjectID
		}
	}
	fields := map[string]any{"from": party(p), "to": in.To, "isQuestion": in.Options != nil}
	if err := s.intercept(r, "inbox.before_send", project, agent, task, nil, fields); err != nil {
		s.fail(w, r, err)
		return
	}
	if p.Kind == auth.KindAgent {
		text, err := s.modifyText(r, "inbox.before_deliver", project, agent, task, in.Text, map[string]any{"from": party(p), "to": in.To})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		in.Text = text
	}
	m, err := s.Inbox.Send(r.Context(), p, in.To, in.Text, in.WhenBusy, inbox.QuestionInput{Options: in.Options, NodeRunID: in.NodeRunID, RunID: run,
		Channel: in.ChannelID, SenderName: in.SenderName})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, apitypes.FromInboxMessage(m))
}

func (s *Server) readInbox(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "messageId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	run, err := s.optionalInboxRun(w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	m, err := s.Inbox.Read(r.Context(), principal(r), id, run)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.FromInboxMessage(m))
}

func (s *Server) replyInbox(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "messageId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in apitypes.InboxReplyInput
	if err := readJSON(w, r, s.inboxBodyLimit(), &in); err != nil {
		s.fail(w, r, err)
		return
	}
	var run uuid.UUID
	if in.RunID != nil {
		run = *in.RunID
	}
	p, err := s.replyAs(r, principal(r), in.AsIdentityID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	project, agent, task := s.principalScope(r, p)
	if project == nil && agent == nil && s.Hooks != nil {
		// 回复给 Agent：按原消息里 Agent 一方的项目决定哪些插件参与。
		if orig, e := s.Store.GetInboxMessage(r.Context(), id, 1); e == nil {
			for _, side := range []store.PartyView{orig.From, orig.To} {
				if side.Kind == "agent" && side.ProjectID != nil {
					project = side.ProjectID
				}
			}
		}
	}
	fields := map[string]any{"from": party(p), "replyTo": id, "isQuestion": in.Options != nil}
	if err := s.intercept(r, "inbox.before_send", project, agent, task, nil, fields); err != nil {
		s.fail(w, r, err)
		return
	}
	if p.Kind == auth.KindAgent {
		text, err := s.modifyText(r, "inbox.before_deliver", project, agent, task, in.Text, map[string]any{"from": party(p), "replyTo": id})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		in.Text = text
	}
	m, err := s.Inbox.Reply(r.Context(), p, id, in.Text, in.WhenBusy, inbox.QuestionInput{Options: in.Options, OptionID: in.OptionID, NodeRunID: in.NodeRunID, RunID: run})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, apitypes.FromInboxMessage(m))
}

// replyAs 返回回复的身份：插件令牌带 asIdentityId 时是它代管的那个外部身份（plugins/SPEC.md §12），效果与该身份亲自回复相同，
// 审计记下代表的身份；不带时插件须有 inbox.send 范围。其他调用方带 asIdentityId 时 422。
func (s *Server) replyAs(r *http.Request, p auth.Principal, as *uuid.UUID) (auth.Principal, error) {
	if as == nil {
		if p.Kind == auth.KindPlugin && !slices.Contains(p.Scopes, "inbox.send") {
			return p, apperr.Forbidden("插件清单没有声明 inbox.send 范围")
		}
		return p, nil
	}
	if p.Kind != auth.KindPlugin {
		return p, apperr.Invalid("asIdentityId", "只有插件令牌可以代表外部身份回复")
	}
	if err := s.checkManaged(r.Context(), p, *as); err != nil {
		return p, err
	}
	x, err := s.Store.GetExternalIdentity(r.Context(), *as)
	if err == nil && x.DeletedAt != nil {
		err = apperr.NotFound("外部身份")
	}
	if err != nil {
		return p, err
	}
	noteOnBehalfOf(r, x.ID)
	return auth.External(x.ID, x.Name), nil
}

func (s *Server) listThreads(w http.ResponseWriter, r *http.Request) {
	q, err := s.inboxQuery(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	page, total, err := s.Inbox.Threads(r.Context(), principal(r), r.URL.Query().Get("cursor"), q.Limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.FromThreadPage(page, total))
}

func (s *Server) getThread(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "threadId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var run uuid.UUID
	if raw, ok := r.URL.Query()["runId"]; ok {
		if len(raw) != 1 {
			s.fail(w, r, apperr.Invalid("runId", "仅接受单值UUID"))
			return
		}
		var err error
		encoded, _ := json.Marshal(raw[0])
		run, err = parseExplicitInboxRun(encoded)
		if err != nil {
			s.fail(w, r, err)
			return
		}
	}
	msgs, err := s.Inbox.Thread(r.Context(), principal(r), id, run)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := apitypes.InboxThread{ThreadID: id, Messages: make([]apitypes.InboxMessage, 0, len(msgs))}
	for _, m := range msgs {
		out.Messages = append(out.Messages, apitypes.FromInboxMessage(m))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) readThread(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "threadId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	run, err := s.optionalInboxRun(w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	total, err := s.Inbox.ReadThread(r.Context(), principal(r), id, run)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.InboxUnreadTotal{UnreadTotal: total})
}

// streamInbox 推送收件箱事件流，快照与续传规则同 Agent 事件流。
func (s *Server) streamInbox(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cursor, resume := r.Header.Get("Last-Event-ID"), false
	if cursor != "" {
		first, err := s.Coord.InboxFirstID(ctx)
		if err == nil && first != "" && coord.CompareIDs(cursor, first) >= 0 {
			last, err := s.Coord.InboxLastID(ctx)
			resume = err == nil && coord.CompareIDs(cursor, last) <= 0
		}
	}
	var snap apitypes.SseInboxSnapshot
	if !resume {
		var err error
		snap, cursor, err = s.Inbox.Snapshot(ctx)
		if err != nil {
			s.fail(w, r, err)
			return
		}
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if !resume {
		data, _ := json.Marshal(snap)
		if !writeEvent(w, cursor, apitypes.EventInboxSnapshot, data) {
			return
		}
	}
	_ = http.NewResponseController(w).Flush()
	s.pumpEvents(w, r, cursor, func(ctx context.Context, after string, block time.Duration) ([]coord.Event, error) {
		return s.Coord.ReadInbox(ctx, after, 100, block)
	}, "stream", "inbox")
}

// ---- 控制台查看 Agent 与外部身份的收件箱（只读） ----

func (s *Server) listAgentInbox(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	q, err := s.inboxQuery(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	page, err := s.Inbox.AgentInbox(r.Context(), id, q)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.FromInboxPage(page))
}

func (s *Server) listAgentConsoleMessages(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	q, err := s.inboxQuery(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	page, err := s.Inbox.ConsoleMessages(r.Context(), id, q)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.FromConversationPage(page))
}

func (s *Server) getInboxPending(w http.ResponseWriter, r *http.Request) {
	questions, unread, err := s.Inbox.Pending(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.FromPending(questions, unread))
}

func (s *Server) getAgentInboxMessage(w http.ResponseWriter, r *http.Request) {
	agentID, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	msgID, err := pathUUID(r, "messageId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	m, err := s.Inbox.AgentInboxMessage(r.Context(), agentID, msgID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.FromInboxMessage(m))
}

func (s *Server) listIdentityInbox(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "identityId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	q, err := s.inboxQuery(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	page, err := s.Inbox.IdentityInbox(r.Context(), id, r.URL.Query().Get("direction"), q)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.FromInboxPage(page))
}

func (s *Server) getIdentityInboxMessage(w http.ResponseWriter, r *http.Request) {
	identityID, err := pathUUID(r, "identityId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	msgID, err := pathUUID(r, "messageId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	m, err := s.Inbox.IdentityInboxMessage(r.Context(), identityID, msgID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.FromInboxMessage(m))
}

// ---- 外部身份与令牌（只限控制台） ----

func (s *Server) listIdentities(w http.ResponseWriter, r *http.Request) {
	xs, err := s.Store.ListExternalIdentities(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := apitypes.ExternalIdentityList{Identities: make([]apitypes.ExternalIdentity, 0, len(xs))}
	for _, x := range xs {
		out.Identities = append(out.Identities, apitypes.FromIdentity(x))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createIdentity(w http.ResponseWriter, r *http.Request) {
	var in apitypes.ExternalIdentityCreate
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if msg := identity.ValidateName(in.Name); msg != "" {
		s.fail(w, r, apperr.Invalid("name", msg))
		return
	}
	x, err := s.Store.CreateExternalIdentity(r.Context(), in.Name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, apitypes.FromIdentity(x))
}

func (s *Server) getIdentity(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "identityId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	x, err := s.Store.GetExternalIdentity(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.FromIdentity(x))
}

func (s *Server) deleteIdentity(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "identityId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := s.Store.DeleteExternalIdentity(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "identityId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ts, err := s.Store.ListExternalTokens(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := apitypes.ExternalTokenList{Tokens: make([]apitypes.ExternalToken, 0, len(ts))}
	for _, t := range ts {
		out.Tokens = append(out.Tokens, apitypes.FromToken(t))
	}
	writeJSON(w, http.StatusOK, out)
}

// issueToken 签发令牌；请求体可以不传，不传或为 {} 表示永不过期。令牌明文只在这次响应中返回。
func (s *Server) issueToken(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "identityId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	body, err := readBody(w, r, jsonLimit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in apitypes.ExternalTokenCreate
	if len(bytes.TrimSpace(body)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			s.fail(w, r, apperr.Invalid("body", "请求体不是合法的 JSON 或包含未知字段："+err.Error()))
			return
		}
	}
	t, secret, err := identity.IssueToken(r.Context(), s.Store, id, in.ExpiresAt)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, apitypes.ExternalTokenIssued{Token: apitypes.FromToken(t), Secret: secret})
}

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) {
	identityID, err := pathUUID(r, "identityId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tokenID, err := pathUUID(r, "tokenId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := s.Store.RevokeExternalToken(r.Context(), identityID, tokenID); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setIdentityManager(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "identityId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		ManagedBy *store.IdentityManager `json:"managedBy"`
	}
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	var x store.ExternalIdentity
	switch {
	case s.Plugins != nil:
		x, err = s.Plugins.SetIdentityManager(r.Context(), id, in.ManagedBy)
	case in.ManagedBy != nil:
		err = apperr.Invalid("managedBy.pluginId", "插件 "+in.ManagedBy.PluginID+" 没有安装")
	default:
		x, err = s.Store.SetIdentityManager(r.Context(), id, nil)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.FromIdentity(x))
}
