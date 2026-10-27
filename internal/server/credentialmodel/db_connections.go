package credentialmodel

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
)

type DBConnection struct {
	AccountID uuid.UUID  `json:"accountId"`
	Name      string     `json:"name"`
	VersionID *uuid.UUID `json:"versionId"`
	Status    string     `json:"status"`
}
type DBConnections struct {
	AgentID             uuid.UUID      `json:"agentId"`
	ProjectID           uuid.UUID      `json:"projectId"`
	CLI                 string         `json:"cli"`
	ConfigVersion       int64          `json:"configVersion"`
	CredentialsRevision int64          `json:"credentialsRevision"`
	Mode                string         `json:"mode"`
	AccountIDs          []uuid.UUID    `json:"accountIds"`
	Connections         []DBConnection `json:"connections"`
	Status              string         `json:"status"`
}
type DBConnectionsPut struct {
	AccountIDs            []uuid.UUID `json:"accountIds"`
	ExpectedConfigVersion int64       `json:"expectedConfigVersion"`
}

func (v *DBConnectionsPut) UnmarshalJSON(b []byte) error {
	var raw struct {
		AccountIDs json.RawMessage `json:"accountIds"`
		Expected   *int64          `json:"expectedConfigVersion"`
	}
	if strict(b, &raw) != nil || raw.Expected == nil || len(raw.AccountIDs) == 0 || string(raw.AccountIDs) == "null" {
		return errors.New("数据库连接集合字段无效")
	}
	var ids []json.RawMessage
	if json.Unmarshal(raw.AccountIDs, &ids) != nil || ids == nil {
		return errors.New("数据库连接集合字段无效")
	}
	out := make([]uuid.UUID, 0, len(ids))
	for _, item := range ids {
		var value string
		if json.Unmarshal(item, &value) != nil {
			return errors.New("数据库连接ID无效")
		}
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil {
			return errors.New("数据库连接ID无效")
		}
		out = append(out, id)
	}
	*v = DBConnectionsPut{out, *raw.Expected}
	return nil
}

type CredentialSelectionModeConflictDetails struct {
	AgentID       uuid.UUID `json:"agentId"`
	CLI           string    `json:"cli"`
	Mode          string    `json:"mode"`
	ConfigVersion int64     `json:"configVersion"`
}
