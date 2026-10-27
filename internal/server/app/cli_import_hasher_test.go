package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"github.com/google/uuid"
	"testing"
)

// expectedImportDigest restates the receipt digest independently: SHA-256 over
// an 8-byte big-endian length prefix plus content for domain, project, parts.
func expectedImportDigest(domain string, project uuid.UUID, parts ...[]byte) []byte {
	h := sha256.New()
	for _, v := range append([][]byte{[]byte(domain), project[:]}, parts...) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(v)))
		h.Write(n[:])
		h.Write(v)
	}
	return h.Sum(nil)
}

func TestImportHasherPlainSHA256(t *testing.T) {
	var a importHasher
	if a.KeyID() != "sha256-v1" {
		t.Fatalf("key id = %q", a.KeyID())
	}
	p := uuid.New()
	origin, e := a.Origin(p, []byte("source"))
	if e != nil || !bytes.Equal(origin, expectedImportDigest("origin-v1", p, []byte("source"))) {
		t.Fatal("origin digest is not the length-prefixed SHA-256", e)
	}
	content, e := a.Content(p, "gh", "env-v1", []byte("payload"))
	if e != nil || !bytes.Equal(content, expectedImportDigest("content-v1", p, []byte("gh"), []byte("env-v1"), []byte("payload"))) {
		t.Fatal("content digest is not the length-prefixed SHA-256", e)
	}
	// A fresh value computes the same digest: there is no per-process key.
	again, _ := importHasher{}.Origin(p, []byte("source"))
	if !bytes.Equal(origin, again) {
		t.Fatal("digest not stable across hashers")
	}
}

func TestImportHasherDomainProjectAndLengthIsolation(t *testing.T) {
	var a importHasher
	p := uuid.New()
	q := uuid.New()
	x, _ := a.Origin(p, []byte("source"))
	otherProject, _ := a.Origin(q, []byte("source"))
	content, _ := a.Content(p, "gh", "env-v1", []byte("source"))
	first, _ := a.Content(p, "a", "bc", []byte("d"))
	second, _ := a.Content(p, "ab", "c", []byte("d"))
	for _, v := range [][]byte{otherProject, content} {
		if bytes.Equal(x, v) {
			t.Fatal("domain/project not isolated")
		}
	}
	if bytes.Equal(first, second) {
		t.Fatal("length collision")
	}
	if _, e := a.Origin(uuid.Nil, nil); e == nil {
		t.Fatal("zero project accepted")
	}
	if _, e := a.Content(uuid.Nil, "gh", "env-v1", nil); e == nil {
		t.Fatal("zero project accepted")
	}
	if fmt.Sprintf("%v %#v", a, a) != "[import provenance hasher] [import provenance hasher]" {
		t.Fatal("hasher formatted unexpectedly")
	}
}
