package store_test

import (
	"context"
	"sync"
	"testing"

	"loop-now/internal/server/store"
	"loop-now/internal/testutil/sqlitetest"
)

func TestEnsureDefaultsIdempotent(t *testing.T) {
	s := sqlitetest.NewStore(t)
	ctx := context.Background()

	first, err := s.EnsureDefaults(ctx, "Default organization", "Default project")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make([]store.DefaultContext, 3)
	errs := make([]error, 3)
	for i := range 3 {
		wg.Go(func() { results[i], errs[i] = s.EnsureDefaults(ctx, "Another name", "Another project") })
	}
	wg.Wait()
	for i := range 3 {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if results[i].Organization.ID != first.Organization.ID || results[i].Project.ID != first.Project.ID {
			t.Fatalf("IDs changed after repeated initialization: %+v vs %+v", results[i], first)
		}
		if results[i].Organization.Name != "Default organization" || results[i].Project.Name != "Default project" {
			t.Fatalf("the names should only be used on first creation: %+v", results[i])
		}
	}
	var orgs, projects int
	if err := s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM organizations), (SELECT count(*) FROM projects)`).Scan(&orgs, &projects); err != nil {
		t.Fatal(err)
	}
	if orgs != 1 || projects != 1 {
		t.Fatalf("there should be exactly one of each: organizations=%d projects=%d", orgs, projects)
	}
}

func TestPutBlobDeduplicates(t *testing.T) {
	s := sqlitetest.NewStore(t)
	ctx := context.Background()
	a, err := s.PutBlob(ctx, []byte("same content"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.PutBlob(ctx, []byte("same content"))
	if err != nil || a != b {
		t.Fatalf("the same content should get the same digest: %s %s %v", a, b, err)
	}
	var n int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM blobs`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("blobs should have a single row: %d %v", n, err)
	}
	data, err := s.ReadBlob(ctx, a)
	if err != nil || string(data) != "same content" {
		t.Fatalf("wrong content read: %q %v", data, err)
	}
}
