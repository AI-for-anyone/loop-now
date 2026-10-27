// Package cliimport owns explicit console-local scans and durable atomic imports.
// It has no default host-root discovery, HTTP listener, or provider access.
package cliimport

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/store"
)

type Operation = store.CLIImportOperation
type Item = store.CLIImportItem
type State struct {
	Operation Operation `json:"operation"`
}
type SourceSummary struct {
	CLI        string  `json:"cli"`
	Status     string  `json:"status"`
	ReasonCode *string `json:"reasonCode"`
}
type Candidate struct {
	CandidateID          uuid.UUID `json:"candidateId"`
	CLI                  string    `json:"cli"`
	SourceKind           string    `json:"sourceKind"`
	Label                string    `json:"label"`
	Status               string    `json:"status"`
	MissingFields        []string  `json:"missingFields"`
	SupplementableFields []string  `json:"supplementableFields"`
	ReasonCode           *string   `json:"reasonCode"`
	PreviouslyImported   bool      `json:"previouslyImported"`
}
type Scan struct {
	ScanID              uuid.UUID       `json:"scanId"`
	ProjectID           uuid.UUID       `json:"projectId"`
	ExpiresAt           time.Time       `json:"expiresAt"`
	CredentialsRevision int64           `json:"credentialsRevision"`
	Sources             []SourceSummary `json:"sources"`
	Candidates          []Candidate     `json:"candidates"`
}
type Supplement struct {
	CandidateID uuid.UUID `json:"candidateId"`
	Database    *string   `json:"database,omitempty"`
	SSLMode     *string   `json:"sslMode,omitempty"`
	CACert      *string   `json:"caCert,omitempty"`
}
type StartInput struct {
	OperationID                 uuid.UUID    `json:"operationId"`
	ScanID                      uuid.UUID    `json:"scanId"`
	CandidateIDs                []uuid.UUID  `json:"candidateIds"`
	ExpectedCredentialsRevision int64        `json:"expectedCredentialsRevision"`
	Supplements                 []Supplement `json:"supplements,omitempty"`
}

// Reject duplicate keys/null at every depth before decode; unknown nested fields
// also fail. No failed parse returns native input or sensitive field values.
func strictInput(data []byte, v any) error {
	if len(data) > 2<<20 {
		return errors.New("import input too large")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	budget := 4096
	var walk func(int) error
	walk = func(depth int) error {
		budget--
		if budget < 0 || depth > 12 {
			return errors.New("invalid import input")
		}
		token, e := d.Token()
		if e != nil || token == nil {
			return errors.New("invalid import input")
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		if delimiter == '{' {
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				name, ok := k.(string)
				if e != nil || !ok || seen[name] {
					return errors.New("invalid import input")
				}
				seen[name] = true
				if e = walk(depth + 1); e != nil {
					return e
				}
			}
		} else if delimiter == '[' {
			for d.More() {
				if e := walk(depth + 1); e != nil {
					return e
				}
			}
		} else {
			return errors.New("invalid import input")
		}
		_, e = d.Token()
		return e
	}
	if walk(0) != nil || d.Decode(new(any)) != io.EOF {
		return errors.New("invalid import input")
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return errors.New("invalid import input")
	}
	return nil
}
func (in *StartInput) UnmarshalJSON(data []byte) error {
	type plain StartInput
	var v plain
	if strictInput(data, &v) != nil {
		return errors.New("导入请求字段或格式无效")
	}
	*in = StartInput(v)
	return in.Validate()
}
func (in StartInput) Validate() error {
	if in.OperationID == uuid.Nil || in.ScanID == uuid.Nil || in.ExpectedCredentialsRevision < 1 || len(in.CandidateIDs) < 1 || len(in.CandidateIDs) > 20 {
		return apperr.Invalid("operationId", "导入输入无效")
	}
	ids := map[uuid.UUID]bool{}
	for _, id := range in.CandidateIDs {
		if id == uuid.Nil || ids[id] {
			return apperr.Invalid("candidateIds", "候选无效或重复")
		}
		ids[id] = true
	}
	seen := map[uuid.UUID]bool{}
	if len(in.Supplements) > 20 {
		return apperr.Invalid("supplements", "补项过多")
	}
	for _, s := range in.Supplements {
		if s.CandidateID == uuid.Nil || !ids[s.CandidateID] || seen[s.CandidateID] || (s.Database == nil && s.SSLMode == nil && s.CACert == nil) {
			return apperr.Invalid("supplements", "补项无效")
		}
		seen[s.CandidateID] = true
		for _, v := range []*string{s.Database, s.SSLMode, s.CACert} {
			if v != nil && (*v == "" || len(*v) > 65536) {
				return apperr.Invalid("supplements", "补项无效")
			}
		}
		if s.Database != nil && len(*s.Database) > 1024 || s.SSLMode != nil && len(*s.SSLMode) > 32 {
			return apperr.Invalid("supplements", "补项超限")
		}
		if s.CACert != nil && !publicCA(*s.CACert) {
			return apperr.Invalid("supplements.caCert", "仅接受有效公开证书PEM")
		}
	}
	return nil
}

func publicCA(value string) bool {
	remaining := []byte(strings.TrimSpace(value))
	count := 0
	for len(remaining) > 0 {
		if !bytes.HasPrefix(remaining, []byte("-----BEGIN CERTIFICATE-----")) {
			return false
		}
		const begin = "-----BEGIN CERTIFICATE-----"
		const end = "-----END CERTIFICATE-----"
		endIndex := bytes.Index(remaining, []byte(end))
		if endIndex < 0 {
			return false
		}
		segment := remaining[:endIndex+len(end)]
		if bytes.Contains(segment[len(begin):], []byte("-----BEGIN ")) {
			return false
		}
		// pem.Decode may skip malformed blocks: give it only the first segment.
		block, rest := pem.Decode(segment)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return false
		}
		if len(bytes.TrimSpace(rest)) != 0 {
			return false
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return false
		}
		count++
		remaining = bytes.TrimSpace(remaining[len(segment):])
	}
	return count > 0
}
