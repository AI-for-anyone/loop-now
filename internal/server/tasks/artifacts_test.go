package tasks

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/taskmodel"
)

func TestArtifactValidationIdempotencyAndAccess(t *testing.T) {
	s, id, p := fixture(t, false)
	s.Options.MaxArtifact = 8
	ctx := context.Background()
	r := current(t, s, id)
	node := r.Current().ID
	for _, name := range []string{"", ".", "..", "../other", "a/b", "a\\b", "x\n"} {
		if _, e := s.Publish(ctx, p, id, node, uuid.New(), name, "text/plain", []byte("file")); !apperr.Is(e, apperr.CodeValidationFailed) {
			t.Fatal("invalid filename accepted", name, e)
		}
	}
	if _, e := s.Publish(ctx, p, id, node, uuid.New(), "file", "bad media type", []byte("file")); !apperr.Is(e, apperr.CodeValidationFailed) {
		t.Fatal("invalid media accepted", e)
	}
	if _, e := s.Publish(ctx, p, id, node, uuid.New(), "file", "text/plain", []byte("123456789")); !apperr.Is(e, apperr.CodePayloadTooLarge) {
		t.Fatal("oversize artifact accepted", e)
	}
	action := uuid.New()
	first, e := s.Publish(ctx, p, id, node, action, "file.txt", "text/plain", []byte("file"))
	if e != nil {
		t.Fatal(e)
	}
	replay, e := s.Publish(ctx, p, id, node, action, "file.txt", "text/plain", []byte("file"))
	if e != nil || string(first) != string(replay) {
		t.Fatal("artifact replay unstable", e)
	}
	if _, e = s.Publish(ctx, p, id, node, action, "file.txt", "text/plain", []byte("changed")); !apperr.Is(e, apperr.CodeTaskConflict) {
		t.Fatal("artifact action body changed", e)
	}
	var artifact taskmodel.Artifact
	if e = json.Unmarshal(first, &artifact); e != nil {
		t.Fatal(e)
	}
	_, data, e := s.Download(ctx, p, id, artifact.ID)
	if e != nil || string(data) != "file" {
		t.Fatal("artifact content differs", e)
	}
	if _, _, e = s.Download(ctx, auth.External(uuid.New(), "external"), id, artifact.ID); !apperr.Is(e, apperr.CodeForbidden) {
		t.Fatal("external artifact access allowed", e)
	}
}
