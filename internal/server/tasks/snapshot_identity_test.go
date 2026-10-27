package tasks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"loop-now/internal/server/taskmodel"
)

// Lark switch participants expose the frozen task_instances.snapshot_sha, not a
// hash of the current snapshot bytes; converted rows keep their original value.
func TestLarkParticipantsUseFrozenSnapshotIdentity(t *testing.T) {
	s, id, p := fixture(t, false)
	ctx := context.Background()
	var raw []byte
	var sha string
	if e := s.Store.Pool.QueryRow(ctx, `SELECT snapshot,snapshot_sha FROM task_instances WHERE id=$1`, p.RuntimeID).Scan(&raw, &sha); e != nil {
		t.Fatal(e)
	}
	digest := sha256.Sum256(raw)
	if sha != hex.EncodeToString(digest[:]) {
		t.Fatal("new instance identity is not sha256 of its snapshot bytes")
	}
	participant := func() taskmodel.LarkSwitchParticipant {
		t.Helper()
		_, list, e := s.Store.TaskLarkParticipants(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		for _, v := range list {
			if v.RuntimeID == p.RuntimeID {
				return v
			}
		}
		t.Fatal("participant missing")
		return taskmodel.LarkSwitchParticipant{}
	}
	if got := participant().BaseSnapshotSHA; got != sha {
		t.Fatal("participant identity differs from stored snapshot_sha")
	}
	frozen := strings.Repeat("ab", 32)
	if _, e := s.Store.Pool.Exec(ctx, `UPDATE task_instances SET snapshot_sha=$2 WHERE id=$1`, p.RuntimeID, frozen); e != nil {
		t.Fatal(e)
	}
	if got := participant().BaseSnapshotSHA; got != frozen {
		t.Fatal("participant identity was recomputed from snapshot bytes")
	}
	if _, e := s.Store.Pool.Exec(ctx, `UPDATE task_instances SET snapshot_sha='not-a-digest' WHERE id=$1`, p.RuntimeID); e == nil {
		t.Fatal("malformed snapshot identity accepted")
	}
}
