// Package maintenance 定时维护本地数据库：
//   - 每 5 分钟一次被动检查点，WAL 超过阈值时记告警；
//   - 每天一次截断检查点、PRAGMA optimize、清理 events.db 里 90 天前的日志，并用 VACUUM INTO 备份，
//     只保留最近几份。
//
// 每日维护按上一次备份的时刻排期：服务频繁重启也不会跳过，刚备份过也不会重复。
package maintenance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"loop-now/internal/server/db"
	"loop-now/internal/server/store"
)

// backupLayout 是备份子目录的名字（UTC 时刻），按字典序就是按时间排序。
const backupLayout = "2006-01-02T150405Z"

// partialPrefix 是正在写的备份目录的前缀；写完才改成正式名字，失败时删除。
const partialPrefix = ".partial-"

// Options 是维护的配置；为 0 的项取默认值（测试可以改小）。
type Options struct {
	BackupDir  string
	BackupKeep int

	CheckpointInterval time.Duration // 被动检查点的间隔，默认 5 分钟
	DailyInterval      time.Duration // 每日维护的间隔，默认 24 小时
	StartDelay         time.Duration // 启动后第一次每日维护至少等多久，默认 1 分钟
	LogRetention       time.Duration // events.db 日志保留多久，默认 90 天
	WALWarnBytes       int64         // 单个 WAL 超过多少字节就告警，默认 256 MiB
	PruneBatch         int           // 每个删除事务最多删多少行，默认 10000
	Now                func() time.Time
}

func (o *Options) defaults() {
	if o.CheckpointInterval <= 0 {
		o.CheckpointInterval = 5 * time.Minute
	}
	if o.DailyInterval <= 0 {
		o.DailyInterval = 24 * time.Hour
	}
	if o.StartDelay <= 0 {
		o.StartDelay = time.Minute
	}
	if o.LogRetention <= 0 {
		o.LogRetention = 90 * 24 * time.Hour
	}
	if o.WALWarnBytes <= 0 {
		o.WALWarnBytes = 256 << 20
	}
	if o.PruneBatch <= 0 {
		o.PruneBatch = 10000
	}
	if o.BackupKeep <= 0 {
		o.BackupKeep = 7
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

// Service 在后台运行维护。
type Service struct {
	st     *store.Store
	o      Options
	log    *slog.Logger
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex // 每日维护与手动触发互斥
}

// Start 启动后台维护。
func Start(st *store.Store, o Options, log *slog.Logger) *Service {
	o.defaults()
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{st: st, o: o, log: log, cancel: cancel, done: make(chan struct{})}
	go s.loop(ctx)
	return s
}

// Stop 停止后台维护，等进行中的一步结束或 ctx 到期。
func (s *Service) Stop(ctx context.Context) error {
	s.cancel()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) loop(ctx context.Context) {
	defer close(s.done)
	checkpoint := time.NewTicker(s.o.CheckpointInterval)
	defer checkpoint.Stop()
	daily := time.NewTimer(s.untilDaily())
	defer daily.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-checkpoint.C:
			s.Passive(ctx)
		case <-daily.C:
			if err := s.Daily(ctx); err != nil && ctx.Err() == nil {
				s.log.Error("每日数据库维护失败", "err", err)
			}
			daily.Reset(s.untilDaily())
		}
	}
}

// untilDaily 返回距下一次每日维护的时长：上一次备份之后满一个间隔，且至少等 StartDelay。
func (s *Service) untilDaily() time.Duration {
	wait := s.o.StartDelay
	if last, ok := lastBackup(s.o.BackupDir); ok {
		if due := last.Add(s.o.DailyInterval).Sub(s.o.Now()); due > wait {
			wait = due
		}
	}
	return wait
}

// Passive 执行一次被动检查点，WAL 过大时记告警。
func (s *Service) Passive(ctx context.Context) {
	if _, err := s.st.Pool.Checkpoint(ctx, db.CheckpointPassive); err != nil && ctx.Err() == nil {
		s.log.Warn("被动检查点失败", "err", err)
	}
	s.warnWAL()
}

func (s *Service) warnWAL() {
	data, events := s.st.Pool.WALSize()
	if data > s.o.WALWarnBytes || events > s.o.WALWarnBytes {
		s.log.Warn("WAL 过大：可能有长时间不结束的读事务挡住了检查点", "dataWALBytes", data, "eventsWALBytes", events, "thresholdBytes", s.o.WALWarnBytes)
	}
}

// Daily 执行一次每日维护：截断检查点、optimize、清理旧日志、备份并只保留最近几份。
// 一步失败不影响后面的步骤，最后合并返回各步的错误。
func (s *Service) Daily(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []error
	results, err := s.st.Pool.Checkpoint(ctx, db.CheckpointTruncate)
	if err != nil {
		errs = append(errs, fmt.Errorf("截断检查点：%w", err))
	}
	for _, r := range results {
		if r.Busy {
			s.log.Warn("截断检查点没有做完：有读事务占着旧快照", "schema", r.Schema, "logFrames", r.LogFrames, "checkpointed", r.Checkpointed)
		}
	}
	if err := s.st.Pool.Optimize(ctx); err != nil {
		errs = append(errs, fmt.Errorf("optimize：%w", err))
	}
	before := s.o.Now().Add(-s.o.LogRetention)
	if n, err := s.st.PruneEventLogs(ctx, before, s.o.PruneBatch); err != nil {
		errs = append(errs, fmt.Errorf("清理旧日志：%w", err))
	} else if n > 0 {
		s.log.Info("已清理旧日志", "rows", n, "before", before)
	}
	if path, err := Backup(ctx, s.st.Pool, s.o.BackupDir, s.o.Now()); err != nil {
		errs = append(errs, fmt.Errorf("备份：%w", err))
	} else {
		s.log.Info("已备份数据库", "dir", path)
		if err := prune(s.o.BackupDir, s.o.BackupKeep); err != nil {
			errs = append(errs, fmt.Errorf("清理旧备份：%w", err))
		}
	}
	s.warnWAL()
	return errors.Join(errs...)
}

// Backup 把两个库备份到 dir 下以时刻命名的新子目录（data.db、events.db），返回该子目录。
// 先写到临时目录，完整写完才改成正式名字；失败时删除临时目录。服务运行中也可以调用。
func Backup(ctx context.Context, d *db.DB, dir string, now time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := now.UTC().Format(backupLayout)
	final := filepath.Join(dir, name)
	if _, err := os.Stat(final); err == nil {
		return "", fmt.Errorf("备份 %s 已存在", final)
	}
	partial := filepath.Join(dir, partialPrefix+name)
	if err := os.Mkdir(partial, 0o700); err != nil {
		return "", err
	}
	if err := d.BackupTo(ctx, partial); err != nil {
		_ = os.RemoveAll(partial)
		return "", err
	}
	if err := os.Rename(partial, final); err != nil {
		_ = os.RemoveAll(partial)
		return "", err
	}
	return final, nil
}

// backups 返回 dir 下正式的备份子目录名，从旧到新。
func backups(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), partialPrefix) {
			continue
		}
		if _, err := time.Parse(backupLayout, e.Name()); err == nil {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	return names, nil
}

func lastBackup(dir string) (time.Time, bool) {
	names, err := backups(dir)
	if err != nil || len(names) == 0 {
		return time.Time{}, false
	}
	t, err := time.Parse(backupLayout, names[len(names)-1])
	return t, err == nil
}

// prune 只保留最新的 keep 份备份；不碰名字不是备份时刻的目录与文件。
func prune(dir string, keep int) error {
	names, err := backups(dir)
	if err != nil {
		return err
	}
	var errs []error
	for len(names) > keep {
		if err := os.RemoveAll(filepath.Join(dir, names[0])); err != nil {
			errs = append(errs, err)
		}
		names = names[1:]
	}
	return errors.Join(errs...)
}
