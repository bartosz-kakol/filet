// Package fsx wraps the few platform-specific filesystem calls Filet needs:
// preallocation, no-replace renames between directory fds, fdatasync and
// statfs. The server targets Linux; the non-Linux implementations exist only
// so the code can be developed and unit-tested on macOS.
package fsx

import (
	"errors"
	"io/fs"
	"os"
	"sync/atomic"
	"syscall"
	"time"
)

// DiskUsage is the result of a statfs call.
type DiskUsage struct {
	Total uint64 `json:"total"`
	Used  uint64 `json:"used"`
	Free  uint64 `json:"free"`
	Fsid  string `json:"fsid"`
}

// Identity is the subset of stat data used for cache keys and ETags.
type Identity struct {
	Dev     uint64
	Ino     uint64
	Size    int64
	MtimeNs int64
}

// IdentityOf extracts device, inode, size and mtime from a FileInfo.
func IdentityOf(fi fs.FileInfo) Identity {
	id := Identity{Size: fi.Size(), MtimeNs: fi.ModTime().UnixNano()}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		id.Dev = uint64(st.Dev) //nolint:gosec,unconvert // platform-dependent width
		id.Ino = uint64(st.Ino) //nolint:unconvert // platform-dependent width
	}
	return id
}

// Owner returns the uid and gid of a FileInfo, or ok=false if unavailable.
func Owner(fi fs.FileInfo) (uid, gid uint32, ok bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return st.Uid, st.Gid, true
}

// Dev returns the device id of a FileInfo.
func Dev(fi fs.FileInfo) uint64 { return IdentityOf(fi).Dev }

// IsExist reports whether err means "target exists".
func IsExist(err error) bool {
	return errors.Is(err, fs.ErrExist) || errors.Is(err, syscall.EEXIST) || errors.Is(err, syscall.ENOTEMPTY)
}

// IsCrossDevice reports whether err is EXDEV.
func IsCrossDevice(err error) bool { return errors.Is(err, syscall.EXDEV) }

// IsNoSpace reports whether err is ENOSPC or EDQUOT.
func IsNoSpace(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT)
}

// IsPermission reports whether err is EACCES/EPERM.
func IsPermission(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM)
}

// IsNotDir reports whether err is ENOTDIR.
func IsNotDir(err error) bool { return errors.Is(err, syscall.ENOTDIR) }

// Fallocate preallocates size bytes for f. When the filesystem does not
// support preallocation it falls back to a sparse Ftruncate.
func Fallocate(f *os.File, size int64) error {
	if size == 0 {
		return nil
	}
	err := fallocate(f, size)
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.EOPNOTSUPP) || errors.Is(err, syscall.ENOSYS) || errors.Is(err, syscall.EINVAL) {
		return f.Truncate(size)
	}
	return err
}

// FallocateHook lets tests simulate filesystems without fallocate support.
var fallocate = platformFallocate

// SetFallocateForTest replaces the fallocate implementation and returns a
// function restoring the original.
func SetFallocateForTest(fn func(f *os.File, size int64) error) (restore func()) {
	old := fallocate
	fallocate = fn
	return func() { fallocate = old }
}

// RenameNoReplace renames oldName in oldDir to newName in newDir, failing with
// an EEXIST error if newName already exists. Names must be single path
// segments; the directories are open directory handles obtained through an
// os.Root, so the operation stays confined.
func RenameNoReplace(oldDir *os.File, oldName string, newDir *os.File, newName string) error {
	if h := renameHook.Load(); h != nil {
		if err := (*h)(oldName, newName); err != nil {
			return &os.LinkError{Op: "renameat2", Old: oldName, New: newName, Err: err}
		}
	}
	err := renameNoReplace(int(oldDir.Fd()), oldName, int(newDir.Fd()), newName) //nolint:gosec // fd fits in int
	if err != nil {
		return &os.LinkError{Op: "renameat2", Old: oldName, New: newName, Err: err}
	}
	return nil
}

var renameHook atomic.Pointer[func(oldName, newName string) error]

// SetRenameHookForTest makes RenameNoReplace fail with the hook's error (for
// example EXDEV to simulate a cross-device move); nil removes it.
func SetRenameHookForTest(h func(oldName, newName string) error) {
	if h == nil {
		renameHook.Store(nil)
		return
	}
	renameHook.Store(&h)
}

// Datasync flushes file data to stable storage.
func Datasync(f *os.File) error { return datasync(f) }

// Fstatfs returns the disk usage of the filesystem containing f.
func Fstatfs(f *os.File) (DiskUsage, error) {
	if o := statfsOverride.Load(); o != nil {
		return *o, nil
	}
	return fstatfs(f)
}

var statfsOverride atomic.Pointer[DiskUsage]

// SetStatfsOverrideForTest makes every statfs call report du (nil restores
// the real values). Used by the filettest build to simulate full disks.
func SetStatfsOverrideForTest(du *DiskUsage) { statfsOverride.Store(du) }

// Statfs returns the disk usage of the filesystem containing path.
func Statfs(path string) (DiskUsage, error) {
	if o := statfsOverride.Load(); o != nil {
		return *o, nil
	}
	f, err := os.Open(path) //nolint:gosec // admin-configured path
	if err != nil {
		return DiskUsage{}, err
	}
	defer f.Close()
	return fstatfs(f)
}

// RealPath returns the absolute host path of an open file. On Linux it reads
// /proc/self/fd/<n>, which reflects where the kernel actually opened it.
func RealPath(f *os.File) (string, error) { return realPath(f) }

// MtimeOf returns a FileInfo's mtime in Unix milliseconds.
func MtimeOf(fi fs.FileInfo) int64 { return fi.ModTime().UnixMilli() }

// FromMillis converts Unix milliseconds to time.Time.
func FromMillis(ms int64) time.Time { return time.UnixMilli(ms) }
