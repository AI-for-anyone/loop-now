package inbox

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
)

// permissionQuestionTTL: a permission question does not expire while its request is pending; it closes with it.
const permissionQuestionTTL = 10 * 365 * 24 * time.Hour

// AskPermission asks the console about permission request requestID, as a question from agentID (the Agent; for a task
// instance, the Agent it runs): Approve or Reject, where a typed reply rejects and is the reason. Answering it answers
// the request; its answer does not remind the Agent.
func (s *Service) AskPermission(ctx context.Context, agentID, requestID uuid.UUID, text string) (uuid.UUID, error) {
	p := auth.Principal{Kind: auth.KindAgent, ID: agentID}
	msg := store.NewInboxMessage{ID: uuid.Must(uuid.NewV7()), From: store.AgentParty(agentID), To: store.ConsoleParty, Text: text}
	q, err := s.questionDeliver(ctx, p, msg, QuestionInput{Options: []string{"Approve", "Reject"}, ExpiresIn: permissionQuestionTTL, Permission: &requestID})
	return q.ID, err
}

// AnswerPermission answers a permission question as the console would from the Inbox: Approve, or Reject with the
// reason as a typed reply (none: the Reject option).
func (s *Service) AnswerPermission(ctx context.Context, questionID uuid.UUID, approve bool, reason string) error {
	in, text := QuestionInput{}, ""
	switch reason = strings.TrimSpace(reason); {
	case approve:
		opt := store.PermissionOptionApprove
		in.OptionID = &opt
	case reason != "":
		text = reason
	default:
		opt := store.PermissionOptionReject
		in.OptionID = &opt
	}
	_, err := s.Reply(ctx, auth.Principal{Kind: auth.KindConsole}, questionID, text, "", in)
	return err
}
