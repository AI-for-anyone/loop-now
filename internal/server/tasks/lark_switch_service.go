package tasks

import (
	"bytes"
	"context"
	"crypto/sha256"
	"slices"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
)

// ReserveLarkSwitch is internal phase A, not a public HTTP DTO or a complete
// switch. It validates captured expectations under the real task transaction.
// No process/provider calls, notification or continuation occur in phase A.
func (s *Service) ReserveLarkSwitch(ctx context.Context, p auth.Principal, in store.TaskLarkFenceReservation) (store.TaskLarkActionState, bool, error) {
	var state store.TaskLarkActionState
	if p.Kind != auth.KindConsole {
		return state, false, apperr.Forbidden("任务凭证切换仅限控制台")
	}
	if in.ActionID == uuid.Nil || in.TaskID == uuid.Nil || in.NodeRunID == uuid.Nil || in.ExpectedTaskVersion < 1 || in.Capture == nil || len(in.Members) != 2 || len(in.Capture.Members) != 2 {
		return state, false, apperr.Invalid("switch", "必须提供原任务、节点、两个成员及固定目标版本")
	}
	// Only explicit CAS locators belong to request identity. Runtime metadata,
	// session paths and server-observed identity are recaptured, never trusted.
	type expectedMember struct {
		AgentID, RuntimeID uuid.UUID
		Role               string
		PreviousRevision   int64
		BaseSnapshotSHA    string
	}
	members := make([]expectedMember, 0, 2)
	for _, member := range in.Members {
		matched := false
		for _, expected := range in.Capture.Members {
			if member.AgentID == expected.AgentID && member.RuntimeID == expected.RuntimeID && member.Role == expected.Role {
				if matched || expected.PreviousRevision < 0 {
					return state, false, apperr.Invalid("members", "原成员期望值无效")
				}
				members = append(members, expectedMember{member.AgentID, member.RuntimeID, member.Role, expected.PreviousRevision, expected.BaseSnapshotSHA})
				matched = true
			}
		}
		if !matched {
			return state, false, apperr.Invalid("members", "原成员期望值不匹配")
		}
	}
	slices.SortFunc(members, func(a, b expectedMember) int { return bytes.Compare(a.RuntimeID[:], b.RuntimeID[:]) })
	target := in.Capture.Target
	request := struct {
		TaskID, NodeRunID, AccountID, VersionID       uuid.UUID
		TaskVersion, AccountRevision, BindingRevision int64
		Members                                       []expectedMember
	}{in.TaskID, in.NodeRunID, target.AccountID, target.VersionID, in.ExpectedTaskVersion, target.AccountRevision, in.Capture.ExpectedBindingRevision, members}
	action := bodyAction(p, in.ActionID, "reserveTaskLarkSwitch", request)
	in.ActorKey = action.ActorKey
	in.RequestHash = sha256.Sum256(action.Body)
	created := false
	_, err := s.Store.MutateTask(ctx, in.TaskID, action, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		capture := &store.TaskLarkSwitchCapture{ExpectedBindingRevision: request.BindingRevision}
		var err error
		capture.Target, err = s.Store.CaptureTaskLarkTargetTx(ctx, m, target.AccountID, target.VersionID, target.AccountRevision)
		if err != nil {
			return nil, err
		}
		for _, expected := range members {
			actual, err := s.Store.CaptureTaskLarkMemberTx(ctx, m, store.TaskLarkFenceMember{AgentID: expected.AgentID, RuntimeID: expected.RuntimeID, Role: expected.Role})
			if err != nil {
				return nil, err
			}
			if expected.BaseSnapshotSHA != "" && actual.BaseSnapshotSHA != expected.BaseSnapshotSHA || actual.PreviousRevision != expected.PreviousRevision {
				return nil, conflict("原任务实例绑定或快照已变化")
			}
			capture.Members = append(capture.Members, actual)
		}
		in.Capture = capture
		created, err = s.Store.ReserveTaskLarkFenceTx(ctx, m, in)
		if err != nil {
			return nil, err
		}
		return map[string]any{"actionId": in.ActionID}, nil
	})
	if err != nil {
		return state, false, err
	}
	// Failure to read this receipt is unknown, not evidence that A rolled back.
	state, err = s.Store.TaskLarkAction(ctx, in.TaskID, in.ActorKey, in.ActionID)
	return state, created, err
}
