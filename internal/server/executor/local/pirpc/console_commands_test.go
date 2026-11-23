package pirpc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPipeCompactProtocol(t *testing.T) {
	f := newPipeFake(t)
	obs := &obsRec{}
	f.c.SetObserver(obs)
	ctx := testCtx(t)
	type compactOut struct {
		res CompactResult
		err error
	}
	compactDone := make(chan compactOut, 1)
	go func() {
		res, err := f.c.Compact(ctx, "Keep the key decisions")
		compactDone <- compactOut{res, err}
	}()
	cmd := f.next()
	if cmd["type"] != "compact" || cmd["customInstructions"] != "Keep the key decisions" {
		t.Fatalf("unexpected compact parameters: %v", cmd)
	}
	id := cmd["id"].(string)
	f.send(
		`{"type":"compaction_start","reason":"manual"}`,
		jsonLine(map[string]any{"type": "compaction_end", "reason": "manual", "aborted": false, "willRetry": false,
			"result": map[string]any{"summary": "s", "firstKeptEntryId": "e1", "tokensBefore": 4200, "estimatedTokensAfter": 350, "usage": usageJSON(300, 40, 0, 0)}}),
		okResp(id, "compact", map[string]any{"tokensBefore": 4200, "estimatedTokensAfter": 350}),
	)
	out := <-compactDone
	if out.err != nil || out.res != (CompactResult{TokensBefore: 4200, EstimatedTokensAfter: 350}) {
		t.Fatalf("unexpected compact result: %+v %v", out.res, out.err)
	}
	obs.waitUsage(t, 1)
	usage, inTurn, _, _ := obs.snapshot()
	if len(usage) != 1 || usage[0].Kind != UsageCompaction || !inTurn[0] {
		t.Fatalf("compaction usage should belong to the current maintenance run: %+v %v", usage, inTurn)
	}

}

func TestPipeCompactOmitsEmptyInstructions(t *testing.T) {
	f := newPipeFake(t)
	done := make(chan error, 1)
	go func() {
		_, err := f.c.Compact(testCtx(t), "")
		done <- err
	}()
	cmd := f.next()
	if _, exists := cmd["customInstructions"]; exists {
		t.Fatalf("empty instructions should not send customInstructions: %v", cmd)
	}
	f.send(okResp(cmd["id"].(string), "compact", map[string]any{"tokensBefore": 10, "estimatedTokensAfter": 5}))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestFakeCompactAbortAndContinue(t *testing.T) {
	capture := filepath.Join(t.TempDir(), "instructions.txt")
	p := startFake(t, []string{"FAKEPI_COMPACT_DELAY=30s", "FAKEPI_COMPACT_CAPTURE=" + capture})
	ctx := testCtx(t)
	done := make(chan error, 1)
	go func() {
		_, err := p.Compact(ctx, "Keep only the to-dos")
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if data, err := os.ReadFile(capture); err == nil {
			if string(data) != "Keep only the to-dos" {
				t.Fatalf("unexpected compaction instructions: %q", data)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for compact to start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	abortCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := p.Abort(abortCtx); err != nil {
		t.Fatalf("Abort should cancel compact: %v", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrCompactAborted) {
			t.Fatalf("a compact cancelled by the platform should return ErrCompactAborted: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("compact did not return after Abort")
	}
	res, err := p.Prompt(ctx, "after", nil)
	if err != nil || res.Outcome != OutcomeCompleted || res.Text != "echo: after" {
		t.Fatalf("the process should stay usable after the compaction is cancelled: %+v %v", res, err)
	}
}

// A "did not compact" that Pi reports as a failed compact is a normal result: session too short, already compacted, or
// cancelled by an extension (the platform sent no abort); any other failure is still an error.
func TestCompactSkippedOutcomes(t *testing.T) {
	for message, want := range map[string]string{
		"Nothing to compact (session too small)": CompactTooSmall,
		"Already compacted":                      CompactAlreadyDone,
		"Compaction cancelled":                   CompactCancelled,
		"Compaction failed: model unavailable":   "",
	} {
		t.Run(message, func(t *testing.T) {
			p := startFake(t, []string{"FAKEPI_COMPACT_ERROR=" + message})
			res, err := p.Compact(testCtx(t), "")
			if want == "" {
				if err == nil || !strings.Contains(err.Error(), "model unavailable") {
					t.Fatalf("other failures should be errors: %+v %v", res, err)
				}
				return
			}
			if err != nil || res.Skipped != want || res.TokensBefore != 0 {
				t.Fatalf("should not compact (%s): %+v %v", want, res, err)
			}
		})
	}
}
