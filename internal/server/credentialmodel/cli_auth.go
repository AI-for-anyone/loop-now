package credentialmodel

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"time"
)

type CLIAuthStart struct {
	OperationID            uuid.UUID  `json:"operationId"`
	CLI                    string     `json:"cli"`
	AuthType               string     `json:"authType"`
	Name                   *string    `json:"name,omitempty"`
	AccountID              *uuid.UUID `json:"accountId,omitempty"`
	ExpectedRevision       *int64     `json:"expectedRevision,omitempty"`
	SourceAccountID        *uuid.UUID `json:"sourceAccountId,omitempty"`
	ExpectedSourceRevision *int64     `json:"expectedSourceRevision,omitempty"`
}

func (v *CLIAuthStart) UnmarshalJSON(b []byte) error {
	type wire CLIAuthStart
	var in wire
	if strict(b, &in) != nil {
		return errors.New("CLI登录字段无效")
	}
	var fields map[string]json.RawMessage
	json.Unmarshal(b, &fields)
	for _, k := range []string{"operationId", "cli", "authType"} {
		if _, ok := fields[k]; !ok || string(fields[k]) == "null" {
			return errors.New("CLI登录字段无效")
		}
	}
	if in.OperationID == uuid.Nil || !(in.CLI == "gcloud" && in.AuthType == "google_user" || in.CLI == "gh" && in.AuthType == "github_user" || in.CLI == "lark-cli" && in.AuthType == "lark_user") {
		return errors.New("CLI登录字段无效")
	}
	if in.CLI == "lark-cli" {
		allowed := map[string]bool{"operationId": true, "cli": true, "authType": true, "sourceAccountId": true, "expectedSourceRevision": true, "accountId": true, "expectedRevision": true}
		for key := range fields {
			if !allowed[key] {
				return errors.New("Lark登录字段无效")
			}
		}
		if fields["name"] != nil {
			return errors.New("Lark登录不接受名称")
		}
		if in.AccountID == nil {
			if fields["accountId"] != nil || fields["expectedRevision"] != nil || in.SourceAccountID == nil || *in.SourceAccountID == uuid.Nil || in.ExpectedSourceRevision == nil || *in.ExpectedSourceRevision < 1 {
				return errors.New("Lark应用来源无效")
			}
		} else if *in.AccountID == uuid.Nil || in.ExpectedRevision == nil || *in.ExpectedRevision < 1 || fields["sourceAccountId"] != nil || fields["expectedSourceRevision"] != nil {
			return errors.New("Lark重登字段无效")
		}
		*v = CLIAuthStart(in)
		return nil
	}
	if fields["sourceAccountId"] != nil || fields["expectedSourceRevision"] != nil {
		return errors.New("CLI应用来源字段无效")
	}
	if in.AccountID == nil {
		if in.ExpectedRevision != nil || fields["accountId"] != nil || fields["expectedRevision"] != nil || in.CLI == "gcloud" && in.Name == nil || in.CLI == "gh" && fields["name"] != nil {
			return errors.New("CLI登录新建字段无效")
		}
	} else if *in.AccountID == uuid.Nil || in.ExpectedRevision == nil || *in.ExpectedRevision < 1 || fields["name"] != nil {
		return errors.New("CLI登录重登字段无效")
	}
	*v = CLIAuthStart(in)
	return nil
}

type CLIAuthOperation struct {
	ID               uuid.UUID  `json:"id"`
	ProjectID        uuid.UUID  `json:"projectId"`
	CLI              string     `json:"cli"`
	AuthType         string     `json:"authType"`
	TargetAccountID  *uuid.UUID `json:"targetAccountId"`
	TargetRevision   *int64     `json:"targetRevision"`
	Name             *string    `json:"name"`
	Status           string     `json:"status"`
	AccountID        *uuid.UUID `json:"accountId"`
	StartedAt        time.Time  `json:"startedAt"`
	ExpiresAt        time.Time  `json:"expiresAt"`
	CompletedAt      *time.Time `json:"completedAt"`
	AuthorizationURL *string    `json:"authorizationUrl"`
	UserCode         *string    `json:"userCode,omitempty"`
	SourceAccountID  *uuid.UUID `json:"sourceAccountId,omitempty"`
	SourceVersionID  *uuid.UUID `json:"sourceVersionId,omitempty"`
	SourceRevision   *int64     `json:"sourceRevision,omitempty"`
	ErrorCode        *string    `json:"errorCode"`
	ErrorMessage     *string    `json:"errorMessage"`
}

// Only GitHub has a userCode wire property. Its null is mandatory, whereas
// Google must retain the old shape even if a caller supplied a non-nil value.
func (o CLIAuthOperation) MarshalJSON() ([]byte, error) {
	type wire CLIAuthOperation
	if o.CLI == "lark-cli" {
		return json.Marshal(struct {
			wire
			UserCode        *string    `json:"userCode"`
			SourceAccountID *uuid.UUID `json:"sourceAccountId"`
			SourceVersionID *uuid.UUID `json:"sourceVersionId"`
			SourceRevision  *int64     `json:"sourceRevision"`
		}{wire(o), o.UserCode, o.SourceAccountID, o.SourceVersionID, o.SourceRevision})
	}
	o.SourceAccountID = nil
	o.SourceVersionID = nil
	o.SourceRevision = nil
	if o.CLI != "gh" {
		o.UserCode = nil
		return json.Marshal(wire(o))
	}
	return json.Marshal(struct {
		wire
		UserCode *string `json:"userCode"`
	}{wire(o), o.UserCode})
}

type CLIAuthState struct {
	Operation CLIAuthOperation `json:"operation"`
	Account   *Account         `json:"account"`
}
type CLIAuthOperations struct {
	Operations []CLIAuthOperation `json:"operations"`
}
