package taskmodel

import "github.com/google/uuid"

// LarkSwitchParticipant is read-only console metadata from the frozen task
// instance and its effective overlay. It contains no current Agent config,
// credential payload, session path or promise that a future POST will succeed.
type LarkSwitchParticipant struct {
	AgentID           uuid.UUID `json:"agentId"`
	RuntimeID         uuid.UUID `json:"runtimeId"`
	BaseSnapshotSHA   string    `json:"baseSnapshotSHA"`
	LarkAllowed       *bool     `json:"larkAllowed"`
	BindingRevision   int64     `json:"bindingRevision"`
	Prepared          bool      `json:"prepared"`
	Available         bool      `json:"available"`
	UnavailableReason *string   `json:"unavailableReason"`
	StepIDs           []string  `json:"stepIds"`
}
