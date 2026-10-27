package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type homeOwner struct {
	Protocol    int       `json:"protocol"`
	InstanceID  uuid.UUID `json:"instanceId"`
	OperationID uuid.UUID `json:"operationId"`
}

func privateDirectory(path string) error {
	if e := os.Mkdir(path, 0700); e != nil && !os.IsExist(e) {
		return e
	}
	info, e := os.Lstat(path)
	if e != nil || !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("验证私有目录无效")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) {
		return errors.New("验证目录归属无效")
	}
	return nil
}
func (s *Service) privateRoots() error {
	for _, p := range []string{filepath.Join(s.Options.WorkRoot, ".cli-verification"), filepath.Join(s.Options.WorkRoot, ".cli-verification-locks"), filepath.Join(s.Options.WorkRoot, ".cli-verification", s.InstanceID.String())} {
		if e := privateDirectory(p); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) operationHome(op uuid.UUID) (string, *os.File, error) {
	home := filepath.Join(s.Options.WorkRoot, ".cli-verification", s.InstanceID.String(), op.String())
	path := filepath.Join(s.Options.WorkRoot, ".cli-verification-locks", op.String()+".lock")
	lease, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return "", nil, e
	}
	cleanup := true
	defer func() {
		if cleanup {
			lease.Close()
			os.Remove(path)
		}
	}()
	if e = syscall.Flock(int(lease.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return "", nil, e
	}
	if e = os.Mkdir(home, 0700); e != nil {
		return "", nil, e
	}
	raw, _ := json.Marshal(homeOwner{1, s.InstanceID, op})
	if e = os.WriteFile(filepath.Join(home, ".owner"), raw, 0600); e != nil {
		os.RemoveAll(home)
		return "", nil, e
	}
	cleanup = false
	return home, lease, nil
}
func (s *Service) releaseHome(home string, lease *os.File) {
	_ = os.RemoveAll(home)
	if lease != nil {
		path := lease.Name()
		_ = lease.Close()
		_ = os.Remove(path)
	}
}
func readOwner(home string) (homeOwner, error) {
	var owner homeOwner
	path := filepath.Join(home, ".owner")
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return owner, errors.New("验证owner标记无效")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) {
		return owner, errors.New("验证owner归属无效")
	}
	raw, e := os.ReadFile(path)
	if e != nil || len(raw) > 4096 {
		return owner, errors.New("验证owner标记不可读")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&owner) != nil || d.Decode(new(any)) != io.EOF || owner.Protocol != 1 || owner.InstanceID == uuid.Nil || owner.OperationID == uuid.Nil {
		return owner, errors.New("验证owner标记无效")
	}
	return owner, nil
}
func (s *Service) recoverHomes() error {
	root := filepath.Join(s.Options.WorkRoot, ".cli-verification")
	instances, e := os.ReadDir(root)
	if e != nil {
		return e
	}
	for _, instance := range instances {
		id, e := uuid.Parse(instance.Name())
		if e != nil || id == uuid.Nil || id == s.InstanceID || id.String() != instance.Name() {
			continue
		}
		parent := filepath.Join(root, instance.Name())
		info, e := os.Lstat(parent)
		if e != nil || !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		operations, e := os.ReadDir(parent)
		if e != nil {
			return errors.New("验证旧目录不可读")
		}
		for _, operation := range operations {
			op, e := uuid.Parse(operation.Name())
			if e != nil || op == uuid.Nil || op.String() != operation.Name() {
				continue
			}
			home := filepath.Join(parent, operation.Name())
			info, e := os.Lstat(home)
			if e != nil || !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			owner, e := readOwner(home)
			if e != nil || owner.InstanceID != id || owner.OperationID != op {
				continue
			}
			leasePath := filepath.Join(s.Options.WorkRoot, ".cli-verification-locks", op.String()+".lock")
			lease, e := os.OpenFile(leasePath, os.O_RDWR|syscall.O_NOFOLLOW, 0600)
			if e != nil {
				continue
			}
			before, e := os.Lstat(leasePath)
			opened, oe := lease.Stat()
			if e != nil || oe != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0600 || !os.SameFile(before, opened) {
				lease.Close()
				continue
			}
			stat, ok := opened.Sys().(*syscall.Stat_t)
			if !ok || stat.Uid != uint32(os.Getuid()) {
				lease.Close()
				continue
			}
			if e = syscall.Flock(int(lease.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
				lease.Close()
				continue
			}
			// Recheck after acquiring the original inode. An active guard holds this
			// same description; a replacement lock/marker cannot authorize cleanup.
			after, ae := os.Lstat(leasePath)
			again, re := readOwner(home)
			if ae == nil && re == nil && os.SameFile(opened, after) && again == owner {
				if e = os.RemoveAll(home); e == nil {
					_ = os.Remove(leasePath)
				} else {
					lease.Close()
					return errors.New("验证旧目录清理失败")
				}
			}
			lease.Close()
		}
		_ = os.Remove(parent) // only an empty private instance directory
	}
	return s.recoverOrphanLeases()
}

// An EOF guard may already have removed its HOME before this instance starts.
// Its external lock can only be removed after taking that exact, now-unheld
// inode and proving no operation HOME remains anywhere in the private layout.
func (s *Service) recoverOrphanLeases() error {
	root := filepath.Join(s.Options.WorkRoot, ".cli-verification-locks")
	entries, e := os.ReadDir(root)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		name := entry.Name()
		if filepath.Ext(name) != ".lock" {
			continue
		}
		id, e := uuid.Parse(strings.TrimSuffix(name, ".lock"))
		if e != nil || id == uuid.Nil || id.String()+".lock" != name {
			continue
		}
		homes, e := filepath.Glob(filepath.Join(s.Options.WorkRoot, ".cli-verification", "*", id.String()))
		if e != nil || len(homes) > 0 {
			continue
		}
		path := filepath.Join(root, name)
		file, e := os.OpenFile(path, os.O_RDWR|syscall.O_NOFOLLOW, 0600)
		if e != nil {
			continue
		}
		before, e := os.Lstat(path)
		opened, oe := file.Stat()
		if e != nil || oe != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0600 || !os.SameFile(before, opened) {
			file.Close()
			continue
		}
		stat, ok := opened.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uint32(os.Getuid()) {
			file.Close()
			continue
		}
		if e = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
			file.Close()
			continue
		}
		again, ae := os.Lstat(path)
		homes, he := filepath.Glob(filepath.Join(s.Options.WorkRoot, ".cli-verification", "*", id.String()))
		if ae == nil && he == nil && len(homes) == 0 && os.SameFile(opened, again) {
			_ = os.Remove(path)
		}
		file.Close()
	}
	return nil
}
