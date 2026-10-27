package store_test

import (
	"context"

	"github.com/google/uuid"

	"loop-now/internal/server/store"
)

// enqueueBackedInboxTurn queues an ordinary task Inbox reminder that carries one
// real unread message, as delivery does: a reminder without any pending
// message is voided when claimed.
func enqueueBackedInboxTurn(ctx context.Context, s *store.Store, m *store.TaskMutation, inst store.TaskInstance, text string, priority int) (store.TaskTurn, error) {
	msg := store.NewInboxMessage{ID: uuid.Must(uuid.NewV7()), From: store.ConsoleParty, To: store.Party{Kind: "agent", ID: inst.AgentID, RuntimeID: inst.ID}, Text: text, WhenBusy: "queue"}
	if err := store.InsertInboxMessage(ctx, m.Tx, msg); err != nil {
		return store.TaskTurn{}, err
	}
	turn, err := s.EnqueueTaskInboxTurn(ctx, m, inst, text, priority, &msg.ID)
	if err != nil {
		return turn, err
	}
	return turn, store.LinkInboxTurnTx(ctx, m.Tx, msg.ID, turn.ID)
}
