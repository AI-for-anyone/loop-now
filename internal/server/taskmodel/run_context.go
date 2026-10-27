package taskmodel

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// Ordinary legacy callers may omit runId. Explicit null, zero and casefolded
// aliases never become omission, including before a recovery-specific gate.
func decodeRunContext(data []byte, out any) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for key, value := range fields {
		if !strings.EqualFold(key, "runId") {
			continue
		}
		var id uuid.UUID
		if key != "runId" || bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &id) != nil || id == uuid.Nil {
			return errors.New("runId必须是非空运行ID")
		}
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	d.DisallowUnknownFields()
	return d.Decode(out)
}
func (in *SubmitInput) UnmarshalJSON(data []byte) error {
	type plain SubmitInput
	var value plain
	if err := decodeRunContext(data, &value); err != nil {
		return err
	}
	*in = SubmitInput(value)
	return nil
}
func (in *FailInput) UnmarshalJSON(data []byte) error {
	type plain FailInput
	var value plain
	if err := decodeRunContext(data, &value); err != nil {
		return err
	}
	*in = FailInput(value)
	return nil
}
