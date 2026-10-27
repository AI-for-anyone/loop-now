package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/taskmodel"
)

func (s *Server) taskReady(w http.ResponseWriter, r *http.Request) bool {
	if s.Tasks == nil {
		s.fail(w, r, apperr.NotFound("任务服务"))
		return false
	}
	return true
}
func (s *Server) taskBody(w http.ResponseWriter, r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(w, r.Body, s.Tasks.Options.MaxRequest)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return apperr.TooLarge("任务请求超限")
	}
	if _, ok := out.(*taskmodel.SubmitInput); ok {
		var fields map[string]json.RawMessage
		if json.Unmarshal(data, &fields) != nil {
			return apperr.Invalid("body", "任务请求正文无效")
		}
		if _, present := fields["edgeId"]; !present {
			return apperr.Invalid("edgeId", "须显式提交出口，最后节点提交null")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return apperr.Invalid("body", "任务请求正文无效或超限")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return apperr.Invalid("body", "只能提交一个JSON对象")
	}
	return nil
}
func (s *Server) taskID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, e := pathUUID(r, "taskId")
	if e != nil {
		s.fail(w, r, e)
		return uuid.Nil, false
	}
	return id, true
}
func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	if !s.taskReady(w, r) {
		return
	}
	id, ok := s.taskID(w, r)
	if !ok {
		return
	}
	if p := principal(r); p.Kind == auth.KindConsole {
		d, participants, err := s.Tasks.GetLarkSwitchParticipants(r.Context(), p, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, apitypes.ConsoleTaskDetail{Detail: d, LarkSwitchParticipants: &participants})
		return
	}
	d, e := s.Tasks.Get(r.Context(), principal(r), id)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, d)
}
func (s *Server) getCurrentTask(w http.ResponseWriter, r *http.Request) {
	if !s.taskReady(w, r) {
		return
	}
	p := principal(r)
	if p.Kind != auth.KindAgent || p.TaskID == uuid.Nil {
		s.fail(w, r, apperr.NotFound("当前任务"))
		return
	}
	d, e := s.Tasks.Get(r.Context(), p, p.TaskID)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, d)
}
func (s *Server) taskNode(w http.ResponseWriter, r *http.Request) (taskmodel.NodeRun, bool) {
	id, ok := s.taskID(w, r)
	if !ok {
		return taskmodel.NodeRun{}, false
	}
	node, e := pathUUID(r, "nodeRunId")
	if e != nil {
		s.fail(w, r, e)
		return taskmodel.NodeRun{}, false
	}
	d, e := s.Tasks.Get(r.Context(), principal(r), id)
	if e != nil {
		s.fail(w, r, e)
		return taskmodel.NodeRun{}, false
	}
	for _, n := range d.Nodes {
		if n.ID == node {
			return n, true
		}
	}
	s.fail(w, r, apperr.NotFound("节点运行"))
	return taskmodel.NodeRun{}, false
}
func (s *Server) getTaskNode(w http.ResponseWriter, r *http.Request) {
	if !s.taskReady(w, r) {
		return
	}
	n, ok := s.taskNode(w, r)
	if ok {
		writeJSON(w, 200, n)
	}
}
func (s *Server) listTaskNodeMessages(w http.ResponseWriter, r *http.Request) {
	if !s.taskReady(w, r) {
		return
	}
	n, ok := s.taskNode(w, r)
	if !ok {
		return
	}
	limit, e := taskLimit(r)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	page, e := s.Store.TaskNodeMessages(r.Context(), n.TaskID, n.ID, r.URL.Query().Get("cursor"), limit)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, page)
}
func taskLimit(r *http.Request) (int, error) {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return 20, nil
	}
	n, e := strconv.Atoi(v)
	if e != nil || n < 1 || n > 100 {
		return 0, apperr.Invalid("limit", "必须在1到100之间")
	}
	return n, nil
}
func (s *Server) listProjectTasks(w http.ResponseWriter, r *http.Request) {
	if !s.taskReady(w, r) {
		return
	}
	id, e := pathUUID(r, "projectId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	if _, e = s.Store.GetProject(r.Context(), id); e != nil {
		s.fail(w, r, e)
		return
	}
	limit, e := taskLimit(r)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	page, e := s.Store.ListTasks(r.Context(), id, r.URL.Query().Get("cursor"), limit)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, page)
}
func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	if !s.taskReady(w, r) {
		return
	}
	var in taskmodel.CreateInput
	if e := s.taskBody(w, r, &in); e != nil {
		s.fail(w, r, e)
		return
	}
	p := principal(r)
	if done, e := s.skipReplayedHook(r, p, in.ActionID); e != nil {
		s.fail(w, r, e)
		return
	} else if !done {
		project, agent, _ := s.principalScope(r, p)
		if e := s.intercept(r, "task.before_start", project, agent, nil, nil, map[string]any{"title": in.Title, "stepCount": len(in.Workflow.Steps),
			"sourceInboxMessageId": in.SourceInboxMessageID}); e != nil {
			s.fail(w, r, e)
			return
		}
	}
	out, e := s.Tasks.Create(r.Context(), p, in)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 201, out)
}
func (s *Server) retryTask(w http.ResponseWriter, r *http.Request) {
	if !s.taskReady(w, r) {
		return
	}
	id, ok := s.taskID(w, r)
	if !ok {
		return
	}
	var in taskmodel.RetryInput
	if e := s.taskBody(w, r, &in); e != nil {
		s.fail(w, r, e)
		return
	}
	out, e := s.Tasks.Retry(r.Context(), principal(r), id, in)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) cancelTask(w http.ResponseWriter, r *http.Request) {
	if !s.taskReady(w, r) {
		return
	}
	id, ok := s.taskID(w, r)
	if !ok {
		return
	}
	var in taskmodel.CancelInput
	if e := s.taskBody(w, r, &in); e != nil {
		s.fail(w, r, e)
		return
	}
	out, e := s.Tasks.Cancel(r.Context(), principal(r), id, in)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) submitTaskNode(w http.ResponseWriter, r *http.Request) {
	if !s.taskReady(w, r) {
		return
	}
	id, ok := s.taskID(w, r)
	if !ok {
		return
	}
	node, e := pathUUID(r, "nodeRunId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	var in taskmodel.SubmitInput
	if e = s.taskBody(w, r, &in); e != nil {
		s.fail(w, r, e)
		return
	}
	p := principal(r)
	if done, e := s.skipReplayedHook(r, p, in.ActionID); e != nil {
		s.fail(w, r, e)
		return
	} else if !done {
		_, agent, _ := s.principalScope(r, p)
		if e := s.intercept(r, "node.before_submit", s.taskProject(r, id), agent, &id, in.RunID, map[string]any{"nodeRunId": node, "edgeId": in.EdgeID}); e != nil {
			s.fail(w, r, e)
			return
		}
	}
	out, e := s.Tasks.Submit(r.Context(), p, id, node, in)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) failTaskNode(w http.ResponseWriter, r *http.Request) {
	if !s.taskReady(w, r) {
		return
	}
	id, ok := s.taskID(w, r)
	if !ok {
		return
	}
	node, e := pathUUID(r, "nodeRunId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	var in taskmodel.FailInput
	if e = s.taskBody(w, r, &in); e != nil {
		s.fail(w, r, e)
		return
	}
	out, e := s.Tasks.Fail(r.Context(), principal(r), id, node, in)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) publishTaskArtifact(w http.ResponseWriter, r *http.Request) {
	if !s.taskReady(w, r) {
		return
	}
	id, ok := s.taskID(w, r)
	if !ok {
		return
	}
	action, e := uuid.Parse(r.URL.Query().Get("actionId"))
	if e != nil {
		s.fail(w, r, apperr.Invalid("actionId", "无效UUID"))
		return
	}
	node, e := uuid.Parse(r.URL.Query().Get("nodeRunId"))
	if e != nil {
		s.fail(w, r, apperr.Invalid("nodeRunId", "无效UUID"))
		return
	}
	if r.Header.Get("Content-Type") != "application/octet-stream" {
		s.fail(w, r, apperr.Invalid("Content-Type", "须为application/octet-stream"))
		return
	}
	data, e := io.ReadAll(http.MaxBytesReader(w, r.Body, s.Tasks.Options.MaxArtifact))
	if e != nil {
		s.fail(w, r, apperr.TooLarge("成果超限"))
		return
	}
	out, e := s.Tasks.Publish(r.Context(), principal(r), id, node, action, r.URL.Query().Get("name"), r.URL.Query().Get("contentType"), data)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 201, out)
}
func (s *Server) downloadTaskArtifact(w http.ResponseWriter, r *http.Request) {
	if !s.taskReady(w, r) {
		return
	}
	id, ok := s.taskID(w, r)
	if !ok {
		return
	}
	artifact, e := pathUUID(r, "artifactId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	a, data, e := s.Tasks.Download(r.Context(), principal(r), id, artifact)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.Name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Content-SHA256", a.SHA256)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(200)
	_, _ = w.Write(data)
}
func (s *Server) streamTaskEvents(w http.ResponseWriter, r *http.Request) { s.streamTasks(w, r, false) }
func (s *Server) streamProjectTaskEvents(w http.ResponseWriter, r *http.Request) {
	s.streamTasks(w, r, true)
}
func (s *Server) streamTasks(w http.ResponseWriter, r *http.Request, project bool) {
	if !s.taskReady(w, r) {
		return
	}
	ctx := r.Context()
	var taskID, projectID *uuid.UUID
	var snapshot any
	var position int64
	var e error
	if project {
		id, err := pathUUID(r, "projectId")
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if _, e = s.Store.GetProject(ctx, id); e != nil {
			s.fail(w, r, e)
			return
		}
		projectID = &id
		snapshot = map[string]any{"projectId": id}
		position, e = s.Store.TaskEventPosition(ctx, nil, projectID)
	} else {
		id, ok := s.taskID(w, r)
		if !ok {
			return
		}
		if _, e = s.Tasks.Get(ctx, principal(r), id); e != nil {
			s.fail(w, r, e)
			return
		}
		taskID = &id
		var detail taskmodel.Detail
		detail, position, e = s.Store.TaskSnapshot(ctx, id)
		if e == nil {
			detail, e = s.Tasks.QuestionViews(ctx, principal(r), detail)
		}
		snapshot = struct {
			Task taskmodel.Detail `json:"task"`
		}{Task: detail}
	}
	if e != nil {
		s.fail(w, r, e)
		return
	}
	streamKey := ""
	if projectID != nil {
		streamKey = coord.ProjectTaskStream(projectID.String())
	} else {
		streamKey = coord.TaskStream(taskID.String())
	}
	redisCursor := "0-0"
	if s.Coord != nil {
		if _, last, err := s.Coord.Snapshot(ctx, streamKey, ""); err == nil && last != "" {
			redisCursor = last
		}
	}
	cursor := position
	resume := false
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err == nil && parsed >= 0 && parsed <= position {
			cursor = parsed
			resume = true
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	rc := http.NewResponseController(w)
	if !resume {
		data, _ := json.Marshal(snapshot)
		if !writeEvent(w, strconv.FormatInt(cursor, 10), "snapshot", data) {
			return
		}
	}
	if rc.Flush() != nil {
		return
	}
	interval := s.O.TaskEventPoll
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	poll := time.NewTicker(interval)
	defer poll.Stop()
	keepalive := s.O.SSEKeepalive
	if keepalive <= 0 {
		keepalive = 15 * time.Second
	}
	keep := time.NewTicker(keepalive)
	defer keep.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-keep.C:
			if _, e = io.WriteString(w, ": keepalive\n\n"); e != nil {
				return
			}
			if rc.Flush() != nil {
				return
			}
		case <-poll.C:
			// Redis中继游标只用于消费提醒。持久游标必须由PG有序补齐推进，
			// 新中继不能越过重连积压或超过单批100条的历史。
			if s.Coord != nil {
				if relayed, err := s.Coord.ReadEvents(ctx, streamKey, redisCursor, 100, 0); err == nil && len(relayed) > 0 {
					redisCursor = relayed[len(relayed)-1].ID
				}
			}

			events, err := s.Store.TaskEvents(ctx, taskID, projectID, cursor, 100)
			if err != nil {
				return
			}
			for _, event := range events {
				cursor = event.ID
				if project && event.Type != "task.updated" {
					continue
				}
				data, e := s.taskEventView(ctx, principal(r), event.Type, event.Data)
				if e != nil {
					return
				}
				if !writeEvent(w, strconv.FormatInt(cursor, 10), event.Type, data) {
					return
				}
			}
			if len(events) > 0 && rc.Flush() != nil {
				return
			}
		}
	}
}

func (s *Server) listTaskAgents(w http.ResponseWriter, r *http.Request) {
	if !s.taskReady(w, r) {
		return
	}
	out, e := s.Tasks.AvailableAgents(r.Context(), principal(r))
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) taskEventView(ctx context.Context, p auth.Principal, kind string, data json.RawMessage) (json.RawMessage, error) {
	if kind == "message.created" {
		var message taskmodel.Message
		if e := json.Unmarshal(data, &message); e != nil {
			return nil, e
		}
		message.ErrorMessage = taskmodel.PublicError(message.ErrorCode, message.ErrorMessage)
		return json.Marshal(message)
	}
	if kind != "node.updated" {
		return data, nil
	}
	var event struct {
		Node taskmodel.NodeRun `json:"node"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&event); err != nil {
		return nil, err
	}
	if q := event.Node.Question; q != nil {
		view, e := s.Tasks.QuestionView(ctx, p, *q)
		if e != nil {
			return nil, e
		}
		event.Node.Question = &view
	}
	return json.Marshal(event)
}

func (s *Server) listManagerTasks(w http.ResponseWriter, r *http.Request) {
	if !s.taskReady(w, r) {
		return
	}
	limit, e := taskLimit(r)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	out, e := s.Tasks.ManagerTasks(r.Context(), principal(r), r.URL.Query().Get("cursor"), limit)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
