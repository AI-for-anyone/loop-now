package store_test

import (
	"context"
	"sync"
	"testing"

	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
)

func TestEnsureDefaultsIdempotent(t *testing.T) {
	s := pgtest.NewStore(t)
	ctx := context.Background()

	first, err := s.EnsureDefaults(ctx, "默认组织", "默认项目")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make([]store.DefaultContext, 3)
	errs := make([]error, 3)
	for i := range 3 {
		wg.Go(func() { results[i], errs[i] = s.EnsureDefaults(ctx, "另一个名字", "另一个项目") })
	}
	wg.Wait()
	for i := range 3 {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if results[i].Organization.ID != first.Organization.ID || results[i].Project.ID != first.Project.ID {
			t.Fatalf("重复初始化后 ID 变化：%+v vs %+v", results[i], first)
		}
		if results[i].Organization.Name != "默认组织" || results[i].Project.Name != "默认项目" {
			t.Fatalf("名称只应在首次创建时使用：%+v", results[i])
		}
	}
	var orgs, projects int
	if err := s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM organizations), (SELECT count(*) FROM projects)`).Scan(&orgs, &projects); err != nil {
		t.Fatal(err)
	}
	if orgs != 1 || projects != 1 {
		t.Fatalf("应各只有一条：organizations=%d projects=%d", orgs, projects)
	}
}

func TestPutBlobDeduplicates(t *testing.T) {
	s := pgtest.NewStore(t)
	ctx := context.Background()
	a, err := s.PutBlob(ctx, []byte("same content"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.PutBlob(ctx, []byte("same content"))
	if err != nil || a != b {
		t.Fatalf("相同内容应得到相同摘要：%s %s %v", a, b, err)
	}
	var n int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM blobs`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("blobs 应只有一行：%d %v", n, err)
	}
	data, err := s.ReadBlob(ctx, a)
	if err != nil || string(data) != "same content" {
		t.Fatalf("读取内容不对：%q %v", data, err)
	}
}
