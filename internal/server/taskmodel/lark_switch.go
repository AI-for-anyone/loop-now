package taskmodel

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/credentialmodel"
)

type TaskLarkSwitchMemberInput struct {
	AgentID                 uuid.UUID `json:"agentId"`
	RuntimeID               uuid.UUID `json:"runtimeId"`
	ExpectedBindingRevision int64     `json:"expectedBindingRevision"`
}
type TaskLarkSwitchInput struct {
	ActionID                    uuid.UUID                   `json:"actionId"`
	ExpectedTaskVersion         int64                       `json:"expectedTaskVersion"`
	ExpectedNodeRunID           uuid.UUID                   `json:"expectedNodeRunId"`
	ExpectedAgentID             uuid.UUID                   `json:"expectedAgentId"`
	ExpectedRuntimeID           uuid.UUID                   `json:"expectedRuntimeId"`
	ExpectedLarkBindingRevision int64                       `json:"expectedLarkBindingRevision"`
	ExpectedMembers             []TaskLarkSwitchMemberInput `json:"expectedMembers"`
	TargetAccountID             uuid.UUID                   `json:"targetAccountId"`
	TargetVersionID             uuid.UUID                   `json:"targetVersionId"`
	ExpectedAccountRevision     int64                       `json:"expectedAccountRevision"`
}

// Null integer CAS values must not silently become valid revision zero.
func (in *TaskLarkSwitchInput) UnmarshalJSON(raw []byte) error {
	type plain TaskLarkSwitchInput
	fields := []string{"actionId", "expectedTaskVersion", "expectedNodeRunId", "expectedAgentId", "expectedRuntimeId", "expectedLarkBindingRevision", "expectedMembers", "targetAccountId", "targetVersionId", "expectedAccountRevision"}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || len(m) != len(fields) {
		return errors.New("任务切换输入字段无效")
	}
	for _, key := range fields {
		if v, ok := m[key]; !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return errors.New("任务切换输入缺失或为空")
		}
	}
	var members []map[string]json.RawMessage
	if json.Unmarshal(m["expectedMembers"], &members) != nil || len(members) != 2 {
		return errors.New("必须明确两个任务成员")
	}
	for _, member := range members {
		if len(member) != 3 {
			return errors.New("任务成员字段无效")
		}
		for _, key := range []string{"agentId", "runtimeId", "expectedBindingRevision"} {
			if v, ok := member[key]; !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
				return errors.New("任务成员字段缺失或为空")
			}
		}
	}
	var out plain
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	*in = TaskLarkSwitchInput(out)
	return nil
}

type TaskLarkSwitchTarget struct {
	AccountID             uuid.UUID                    `json:"accountId"`
	VersionID             uuid.UUID                    `json:"versionId"`
	AccountRevision       int64                        `json:"accountRevision"`
	Identity              credentialmodel.LarkIdentity `json:"identity"`
	RuntimeVariantVersion string                       `json:"runtimeVariantVersion"`
}
type TaskLarkSwitchMember struct {
	AgentID                 uuid.UUID `json:"agentId"`
	RuntimeID               uuid.UUID `json:"runtimeId"`
	Role                    string    `json:"role"`
	PreviousBindingRevision int64     `json:"previousBindingRevision"`
	BaseSnapshotSHA         string    `json:"baseSnapshotSHA"`
}
type TaskLarkSwitchContinuation struct {
	NodeRunID uuid.UUID `json:"nodeRunId"`
	Attempt   int       `json:"attempt"`
	RunID     uuid.UUID `json:"runId"`
}
type TaskLarkSwitchReceipt struct {
	ActionID               uuid.UUID                   `json:"actionId"`
	TaskID                 uuid.UUID                   `json:"taskId"`
	Status                 string                      `json:"status"`
	ReservationTaskVersion int64                       `json:"reservationTaskVersion"`
	SourceNodeRunID        uuid.UUID                   `json:"sourceNodeRunId"`
	SourceAgentID          uuid.UUID                   `json:"sourceAgentId"`
	SourceRuntimeID        uuid.UUID                   `json:"sourceRuntimeId"`
	Target                 TaskLarkSwitchTarget        `json:"target"`
	Members                []TaskLarkSwitchMember      `json:"members"`
	BindingRevision        *int64                      `json:"bindingRevision"`
	Continuation           *TaskLarkSwitchContinuation `json:"continuation"`
	DispatchStatus         *string                     `json:"dispatchStatus"`
	RecoveryInstruction    *string                     `json:"recoveryInstruction"`
	ErrorCode              *string                     `json:"errorCode"`
	ErrorMessage           *string                     `json:"errorMessage"`
	CreatedAt              time.Time                   `json:"createdAt"`
	FinishedAt             *time.Time                  `json:"finishedAt"`
}
