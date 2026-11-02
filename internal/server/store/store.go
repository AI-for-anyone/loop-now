// Package store 是服务端对 SQLite 的访问层（经 internal/server/db）：启动时迁移，以及各业务表的读写。
package store

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/google/uuid"

	"loop-now/internal/server/db"
	"loop-now/internal/server/store/schema"
)

// Store 持有连接池。
type Store struct {
	ImageMaxCount int
	// sleepScan tracks a bounded round of due candidates; it is not sleep authority.
	sleepScan sleepScanProgress
	// TaskCommitted在任务事务提交后唤醒Redis中继；启动时配置，运行中不修改。
	TaskCommitted func()
	// VerificationInvalidated runs only after a credential/project mutation commits.
	VerificationInvalidated func(uuid.UUID, *uuid.UUID)
	// Import hooks are configured only by an App that assembled schema017 import.
	// They are not allow-missing-table fallbacks and never open a nested tx.
	CLIImportDeletingTx func(context.Context, *db.Tx, uuid.UUID) error
	CLIImportDeleted    func(uuid.UUID)
	Pool                *db.DB
	// InstanceID 写入运行记录事件，表示由哪个服务端实例记录（多实例预留）；为空时不记录。
	InstanceID *uuid.UUID
	// pluginWake 在写了插件事件的事务提交后调用，见 SetPluginEventsCommitted。
	pluginWake atomic.Pointer[func()]
}

// Open 打开数据目录下的库。迁移由调用方另外调用 Migrate。
func Open(ctx context.Context, o db.Options) (*Store, error) {
	d, err := db.Open(ctx, o)
	if err != nil {
		return nil, err
	}
	return New(d), nil
}

// New 在已打开的库上建 Store，并装上每个写事务提交前执行的钩子。
func New(d *db.DB) *Store {
	s := &Store{Pool: d}
	d.SetWriteHook(s.beforeCommit)
	return s
}

// Close 关闭连接池。
func (s *Store) Close() { _ = s.Pool.Close() }

// Ping 用于健康检查。
func (s *Store) Ping(ctx context.Context) error { return s.Pool.Ping(ctx) }

// Migrate 按顺序执行尚未执行的迁移。每个迁移在一个写事务中执行，失败时库保持在该迁移之前的状态。
func (s *Store) Migrate(ctx context.Context, logger *slog.Logger) error {
	return schema.Migrate(ctx, s.Pool, logger)
}

// uniqueTargets 把代码里沿用的 PostgreSQL 约束名对应到 SQLite 报唯一冲突时给出的表与列：
// SQLite 对普通唯一约束和部分唯一索引只报列，不报约束名。新增按名判断时在这里登记。
var uniqueTargets = map[string]struct {
	table string
	cols  []string
}{
	"agents_name_per_project":               {"agents", []string{"project_id", "name"}},
	"agents_name_platform":                  {"agents", []string{"name"}},
	"cli_credential_import_operations_pkey": {"cli_credential_import_operations", []string{"id"}},
	"external_identities_name_live":         {"external_identities", []string{"name"}},
	"model_auth_operations_one_active":      {"model_auth_operations", []string{"agent_id"}},
	"plugins_pkey":                          {"plugins", []string{"id"}},
	"projects_name_live":                    {"projects", []string{"organization_id", "name"}},
	"runs_one_active_per_agent":             {"runs", []string{"agent_id"}},
}

// isUniqueViolation 判断错误是否为指定约束的唯一冲突；constraint 为空时匹配任意唯一冲突。
func isUniqueViolation(err error, constraint string) bool {
	if constraint == "" {
		return db.IsAnyUniqueViolation(err)
	}
	target, ok := uniqueTargets[constraint]
	if !ok {
		panic(fmt.Sprintf("store: 唯一约束 %s 没有登记在 uniqueTargets", constraint))
	}
	return db.IsUniqueViolation(err, target.table, target.cols...)
}

// inTx 在写事务中执行 fn。
func (s *Store) inTx(ctx context.Context, fn func(*db.Tx) error) error {
	return s.Pool.InTx(ctx, fn)
}
