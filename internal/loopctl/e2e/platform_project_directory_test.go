package e2e_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"loop-now/internal/testutil/redistest"
)

func platformDirectoryRedis(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	opts, err := redis.ParseURL(redistest.URL(t))
	if err != nil {
		t.Fatal("invalid TEST Redis URL")
	}
	r := redis.NewClient(opts)
	if err = r.Ping(ctx).Err(); err != nil {
		t.Fatal("TEST Redis unavailable")
	}
	prefix := "loopnow:test:platform-directory:" + uuid.NewString() + ":"
	t.Cleanup(func() {
		defer r.Close()
		if err := redistest.DeletePrefix(ctx, r, prefix); err != nil {
			t.Error(err)
		}
		keys, err := r.Keys(ctx, prefix+"*").Result()
		if err != nil || len(keys) != 0 {
			t.Error("Redis prefix cleanup failed")
		} else {
			t.Log("Redis prefix cleanup verified")
		}
	})
	return prefix
}
