package tasks

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/taskmodel"
)

// Nil Store makes any accidental access visible. Identity and body checks must
// reject locally before task lookups, auth resolution or dispatch side effects.
func TestTaskConsoleRejectsBeforeStore(t *testing.T) {
	s := &Service{}
	ctx := context.Background()
	task, node := uuid.New(), uuid.New()
	valid := taskmodel.TaskNodeMessageInput{ActionID: uuid.New(), ExpectedAgentID: uuid.New(), ExpectedRuntimeID: uuid.New(), Text: "synthetic", WhenBusy: "queue"}
	for _, kind := range []auth.Kind{auth.KindAgent, auth.KindExternal} {
		if _, err := s.SendConsoleMessage(ctx, auth.Principal{Kind: kind}, task, node, valid, 4096); !apperr.Is(err, apperr.CodeForbidden) {
			t.Fatal("management identity accepted", err)
		}
		if _, err := s.ConsoleMessageCapability(ctx, auth.Principal{Kind: kind}, task, node); !apperr.Is(err, apperr.CodeForbidden) {
			t.Fatal("capability identity accepted", err)
		}
	}
	for _, input := range []taskmodel.TaskNodeMessageInput{{}, func() taskmodel.TaskNodeMessageInput { v := valid; v.WhenBusy = "invalid"; return v }(), func() taskmodel.TaskNodeMessageInput { v := valid; v.Text = " \n "; return v }()} {
		if _, err := s.SendConsoleMessage(ctx, auth.Console(), task, node, input, 4096); !apperr.Is(err, apperr.CodeValidationFailed) {
			t.Fatal("invalid input reached Store", err)
		}
	}
	if _, err := s.SendConsoleMessage(ctx, auth.Console(), task, node, valid, 1); !apperr.Is(err, apperr.CodePayloadTooLarge) {
		t.Fatal("oversized text reached Store", err)
	}
}
