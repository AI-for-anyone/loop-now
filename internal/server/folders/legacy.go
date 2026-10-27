package folders

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path"

	"loop-now/internal/server/content"
	"loop-now/internal/server/store"
)

// LegacySummary 是退役文件夹来源一次性回放的结果。
type LegacySummary struct {
	Sources         int // 这次回放的上传资料来源
	FilesWritten    int
	FilesDeleted    int
	FilesSkipped    int // 路径无效或目标是软链接、目录，未写入
	UnclonedGitDirs int // 从未克隆成功的代码仓库来源（目录不存在）
}

// ApplyLegacyFolderSources 把退役的上传资料来源里尚未落到磁盘的人工修改，一次性写入各 Agent 的文件夹：
// 从未同步过的来源写入全部文件；已同步过的只写待写入的文件、删除待删除的文件。每个来源回放后记一行，之后不再处理。
// 代码仓库来源不处理：已克隆的目录原样保留为普通文件，从未克隆成功的只计数。旧表的数据不修改。
// 须在任何 Pi 启动之前调用。
func (s *Service) ApplyLegacyFolderSources(ctx context.Context) (LegacySummary, error) {
	var sum LegacySummary
	sources, err := s.Store.PendingLegacyFolderSources(ctx)
	if err != nil {
		return sum, err
	}
	for _, src := range sources {
		f := Folder{Dir: AgentWorkspace(s.O.WorkRoot, src.AgentID)}
		written, deleted, skipped, err := s.applyLegacySource(ctx, f, src)
		if err != nil {
			return sum, err
		}
		if err := s.Store.RecordLegacyFolderApplied(ctx, src, written, deleted); err != nil {
			return sum, err
		}
		sum.Sources++
		sum.FilesWritten += written
		sum.FilesDeleted += deleted
		sum.FilesSkipped += skipped
	}
	gits, err := s.Store.LegacyGitSources(ctx)
	if err != nil {
		return sum, err
	}
	for _, g := range gits {
		if _, err := os.Lstat(path.Join(AgentWorkspace(s.O.WorkRoot, g.AgentID), g.Path)); errors.Is(err, fs.ErrNotExist) {
			sum.UnclonedGitDirs++
		}
	}
	return sum, nil
}

func (s *Service) applyLegacySource(ctx context.Context, f Folder, src store.LegacyFolderSource) (written, deleted, skipped int, err error) {
	full := src.SyncedAt == nil
	for _, file := range src.Files {
		rel, perr := content.CleanRelPath(path.Join(src.Path, file.Path))
		if perr != nil {
			skipped++
			continue
		}
		switch {
		case file.Pending == "delete":
			if full {
				continue
			}
			if e := f.removeFile(rel); e != nil {
				skipped++
				continue
			}
			deleted++
		case full || file.Pending == "write":
			data, e := s.Store.ReadBlob(ctx, file.SHA256)
			if e != nil {
				return written, deleted, skipped, e
			}
			if _, e := f.Write(rel, bytes.NewReader(data), int64(len(data)), false); e != nil {
				skipped++
				continue
			}
			written++
		}
	}
	return written, deleted, skipped, nil
}

// removeFile 删除一个普通文件或软链接本身；不存在时视为已删除，目录不删除。
func (f Folder) removeFile(rel string) error {
	root, err := f.open()
	if err != nil {
		return err
	}
	defer root.Close()
	info, err := root.Lstat(rel)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return errors.New("目标是目录")
	}
	return root.Remove(rel)
}
