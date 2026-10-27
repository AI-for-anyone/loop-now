package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/tasks"
)

func TestTaskLarkSwitchHandlersDenyAgentAndExternalBeforeServices(t *testing.T) {
	s := &Server{}
	for _, p := range []auth.Principal{auth.External(uuid.New(), "outside"), {Kind: auth.KindAgent, ID: uuid.New()}} {
		for _, handler := range []http.HandlerFunc{s.switchTaskLarkAccount, s.getTaskLarkAccountSwitch} {
			r := httptest.NewRequest("POST", "/unused", nil)
			r = r.WithContext(auth.With(r.Context(), p))
			w := httptest.NewRecorder()
			handler(w, r)
			if w.Code != 403 {
				t.Fatal("management permission widened", w.Code)
			}
		}
	}
}
func TestTaskLarkSwitchNoNativeManagerDoesNotTouchStore(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	ra, rb := uuid.New(), uuid.New()
	in := taskmodel.TaskLarkSwitchInput{ActionID: uuid.New(), ExpectedTaskVersion: 1, ExpectedNodeRunID: uuid.New(), ExpectedAgentID: a, ExpectedRuntimeID: ra, ExpectedMembers: []taskmodel.TaskLarkSwitchMemberInput{{AgentID: a, RuntimeID: ra}, {AgentID: b, RuntimeID: rb}}, TargetAccountID: uuid.New(), TargetVersionID: uuid.New(), ExpectedAccountRevision: 1}
	raw, _ := json.Marshal(in)
	// Nil Store/Executor deliberately proves rejection happens before A/DB.
	s := &Server{Tasks: tasks.New(nil, nil, nil, tasks.Options{MaxRequest: 1 << 16}, nil)}
	r := httptest.NewRequest("POST", "/unused", bytes.NewReader(raw))
	r.SetPathValue("taskId", uuid.NewString())
	r = r.WithContext(auth.With(r.Context(), auth.Console()))
	w := httptest.NewRecorder()
	s.switchTaskLarkAccount(w, r)
	if w.Code != 503 {
		t.Fatal("missing actual gate became accepted", w.Code, w.Body.String())
	}
}
