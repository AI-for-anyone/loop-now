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

// LegacySummary is the result of the one-time replay of retired folder sources.
type LegacySummary struct {
	Sources         int // upload sources replayed this time
	FilesWritten    int
	FilesDeleted    int
	FilesSkipped    int // not written: invalid path, or the target is a symlink or a directory
	UnclonedGitDirs int // code repository sources that never cloned successfully (directory missing)
}

// ApplyLegacyFolderSources writes, once, the manual edits in retired upload sources that never reached disk into each
// Agent's folder: for a source that was never synced, all files are written; for one already synced, only the files
// pending write are written and the files pending deletion are deleted. After each source is replayed, a row is
// recorded and it is never processed again. Code repository sources are not processed: directories already cloned stay
// as plain files, and those that never cloned successfully are only counted. Data in the old tables is not modified.
// Must be called before any Pi starts.
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

// removeFile deletes a regular file or a symlink itself; a missing file counts as deleted, and directories are not
// deleted.
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
