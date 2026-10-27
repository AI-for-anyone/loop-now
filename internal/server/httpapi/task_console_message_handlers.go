package httpapi

import (
	"loop-now/internal/server/taskmodel"
	"net/http"
)

func (s *Server) getTaskNodeMessageCapability(w http.ResponseWriter, r *http.Request) {
	if !s.taskReady(w, r) {
		return
	}
	task, e := pathUUID(r, "taskId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	node, e := pathUUID(r, "nodeRunId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	out, e := s.Tasks.ConsoleMessageCapability(r.Context(), principal(r), task, node)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) sendTaskNodeMessage(w http.ResponseWriter, r *http.Request) {
	if !s.taskReady(w, r) {
		return
	}
	task, e := pathUUID(r, "taskId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	node, e := pathUUID(r, "nodeRunId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	var in taskmodel.TaskNodeMessageInput
	if e = readJSON(w, r, s.O.Limits.MaxMessage*4+1024, &in); e != nil {
		s.fail(w, r, e)
		return
	}
	if done, e := s.skipReplayedHook(r, principal(r), in.ActionID); e != nil {
		s.fail(w, r, e)
		return
	} else if !done {
		agent := in.ExpectedAgentID
		if in.Text, e = s.modifyText(r, "message.before_enqueue", s.taskProject(r, task), &agent, &task, in.Text,
			map[string]any{"from": party(principal(r)), "nodeRunId": node, "imageCount": len(in.ImageIDs)}); e != nil {
			s.fail(w, r, e)
			return
		}
	}
	out, e := s.Tasks.SendConsoleMessage(r.Context(), principal(r), task, node, in, int(s.O.Limits.MaxMessage))
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 201, out)
}
