package app

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"

	"github.com/google/uuid"
)

// importProvenanceKeyID marks receipts digested with plain SHA-256. Receipts
// under earlier key IDs stay as history and never match new imports.
const importProvenanceKeyID = "sha256-v1"

// importHasher computes non-secret SHA-256 provenance digests. Neither source
// locations nor these digests belong in public DTOs.
type importHasher struct{}

func (importHasher) KeyID() string { return importProvenanceKeyID }
func (importHasher) digest(domain string, project uuid.UUID, parts ...[]byte) ([]byte, error) {
	if project == uuid.Nil {
		return nil, errors.New("invalid import project")
	}
	h := sha256.New()
	for _, v := range append([][]byte{[]byte(domain), project[:]}, parts...) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(v)))
		h.Write(n[:])
		h.Write(v)
	}
	return h.Sum(nil), nil
}
func (h importHasher) Origin(project uuid.UUID, value []byte) ([]byte, error) {
	return h.digest("origin-v1", project, value)
}
func (h importHasher) Content(project uuid.UUID, cli, format string, value []byte) ([]byte, error) {
	return h.digest("content-v1", project, []byte(cli), []byte(format), value)
}
func (importHasher) String() string   { return "[import provenance hasher]" }
func (importHasher) GoString() string { return "[import provenance hasher]" }
