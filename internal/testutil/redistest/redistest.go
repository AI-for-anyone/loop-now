// Package redistest 为组件测试提供隔离的 Redis 环境：每次调用使用独立键前缀，
// 测试结束后按前缀用 SCAN + UNLINK 删除。从不执行 FLUSHDB/FLUSHALL。
package redistest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"loop-now/internal/testutil/testenv"
)

// EnvTestRedisURL 指向测试用的 Redis（默认 DB 10）。
const EnvTestRedisURL = "LOOP_NOW_TEST_REDIS_URL"

// RootPrefix 是所有测试键的公共前缀。
const RootPrefix = "loopnow:test:"

const staleAfter = 24 * time.Hour

var sweepOnce sync.Once

// URL 返回测试 Redis 连接串；未配置时跳过测试并说明原因。
func URL(t testing.TB) string {
	t.Helper()
	u := testenv.Get(EnvTestRedisURL)
	if u == "" {
		t.Skipf("未设置 %s（也没有 .env.local），跳过需要 Redis 的测试", EnvTestRedisURL)
	}
	return u
}

// New 返回 Redis 客户端与本次测试独占的键前缀（以冒号结尾）。
func New(t testing.TB) (*redis.Client, string) {
	t.Helper()
	opts, err := redis.ParseURL(URL(t))
	if err != nil {
		t.Fatalf("解析测试 Redis 连接串失败：%v", err)
	}
	client := redis.NewClient(opts)
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("测试 Redis 不可用：%v", err)
	}
	sweepOnce.Do(func() { sweepStale(ctx, client) })

	var b [4]byte
	_, _ = rand.Read(b[:])
	prefix := fmt.Sprintf("%s%d-%s:", RootPrefix, time.Now().Unix(), hex.EncodeToString(b[:]))
	t.Cleanup(func() {
		if err := DeletePrefix(context.Background(), client, prefix); err != nil {
			t.Errorf("清理测试键 %s* 失败：%v", prefix, err)
		}
		_ = client.Close()
	})
	return client, prefix
}

// DeletePrefix 删除指定前缀下的全部键。恢复测试也用它模拟“Redis 数据丢失”。
func DeletePrefix(ctx context.Context, client *redis.Client, prefix string) error {
	if !strings.HasPrefix(prefix, "loopnow:") {
		return fmt.Errorf("拒绝删除非 loopnow: 前缀的键：%q", prefix)
	}
	iter := client.Scan(ctx, 0, prefix+"*", 500).Iterator()
	var batch []string
	for iter.Next(ctx) {
		batch = append(batch, iter.Val())
		if len(batch) >= 500 {
			if err := client.Unlink(ctx, batch...).Err(); err != nil {
				return err
			}
			batch = batch[:0]
		}
	}
	if err := iter.Err(); err != nil {
		return err
	}
	if len(batch) > 0 {
		return client.Unlink(ctx, batch...).Err()
	}
	return nil
}

// sweepStale 删除超过 24 小时的遗留测试键（前缀形如 loopnow:test:<unix>-<hex>:）。
func sweepStale(ctx context.Context, client *redis.Client) {
	iter := client.Scan(ctx, 0, RootPrefix+"*", 500).Iterator()
	seen := map[string]bool{}
	for iter.Next(ctx) {
		rest := strings.TrimPrefix(iter.Val(), RootPrefix)
		id, _, ok := strings.Cut(rest, ":")
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		sec, _, _ := strings.Cut(id, "-")
		n, err := strconv.ParseInt(sec, 10, 64)
		if err != nil || time.Since(time.Unix(n, 0)) < staleAfter {
			continue
		}
		_ = DeletePrefix(ctx, client, RootPrefix+id+":")
	}
}
