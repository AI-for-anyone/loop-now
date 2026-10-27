package apitypes

import "loop-now/internal/server/taskmodel"

// ConsoleTaskDetail is used only for the console GET. Keeping the projection
// outside taskmodel.Detail prevents storage, task SSE and Agent responses from
// implicitly acquiring console-only management metadata. Nil means that this
// backend has not supplied the capability; a supported empty list is &[]{}.
type ConsoleTaskDetail struct {
	taskmodel.Detail
	LarkSwitchParticipants *[]taskmodel.LarkSwitchParticipant `json:"larkSwitchParticipants,omitempty"`
}
