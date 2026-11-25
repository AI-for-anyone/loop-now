package store_test

import (
	"context"
	"testing"

	"loop-now/internal/server/store"
	"loop-now/internal/testutil/sqlitetest"
)

// Who writes with AI starts as Automatic (nil); a choice is saved whole and replaced whole, and nil goes back to
// Automatic.
func TestTextGenerationSelection(t *testing.T) {
	s := sqlitetest.NewStore(t)
	ctx := context.Background()
	if got, err := s.GetTextGenerationSelection(ctx); err != nil || got != nil {
		t.Fatalf("starts as Automatic: %+v %v", got, err)
	}
	low := "low"
	for _, want := range []*store.TextGenerationSelection{
		{Provider: "codex-cli", ModelID: "gpt-mini", ThinkingLevel: &low},
		{Provider: "claude-code", ModelID: "haiku"},
	} {
		if err := s.PutTextGenerationSelection(ctx, want); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetTextGenerationSelection(ctx)
		if err != nil || got == nil || got.Provider != want.Provider || got.ModelID != want.ModelID || (got.ThinkingLevel == nil) != (want.ThinkingLevel == nil) ||
			(got.ThinkingLevel != nil && *got.ThinkingLevel != *want.ThinkingLevel) {
			t.Fatalf("saved %+v, read %+v %v", want, got, err)
		}
	}
	if err := s.PutTextGenerationSelection(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetTextGenerationSelection(ctx); err != nil || got != nil {
		t.Fatalf("back to Automatic: %+v %v", got, err)
	}
	bad := "loud"
	if err := s.PutTextGenerationSelection(ctx, &store.TextGenerationSelection{Provider: "codex-cli", ModelID: "m", ThinkingLevel: &bad}); err == nil {
		t.Fatal("an unknown thinking level is refused by the database")
	}
}
