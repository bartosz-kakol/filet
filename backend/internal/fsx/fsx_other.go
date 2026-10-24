//go:build !linux

package fsx

import (
	"bytes"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func platformFallocate(f *os.File, size int64) error {
	// macOS has F_PREALLOCATE, but a sparse truncate is enough for development.
	return f.Truncate(size)
}

func renameNoReplace(oldfd int, oldName string, newfd int, newName string) error {
	return unix.RenameatxNp(oldfd, oldName, newfd, newName, unix.RENAME_EXCL)
}

// datasync uses plain fsync: os.File.Sync on macOS issues F_FULLFSYNC,
// which takes seconds per call and makes development unbearably slow.
func datasync(f *os.File) error { return unix.Fsync(int(f.Fd())) } //nolint:gosec // fd fits in int

func fstatfs(f *os.File) (DiskUsage, error) {
	var st unix.Statfs_t
	if err := unix.Fstatfs(int(f.Fd()), &st); err != nil { //nolint:gosec // fd fits in int
		return DiskUsage{}, err
	}
	bs := uint64(st.Bsize)
	return DiskUsage{
		Total: st.Blocks * bs,
		Used:  (st.Blocks - st.Bfree) * bs,
		Free:  st.Bavail * bs,
		Fsid:  fmt.Sprintf("%x-%x", uint32(st.Fsid.Val[0]), uint32(st.Fsid.Val[1])), //nolint:gosec // bit pattern
	}, nil
}

func realPath(f *os.File) (string, error) {
	// F_GETPATH (50) fills a MAXPATHLEN buffer with the file's current path.
	var buf [1024]byte
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), 50, uintptr(unsafe.Pointer(&buf[0]))) //nolint:gosec // documented fcntl ABI
	if errno != 0 {
		return "", errno
	}
	n := bytes.IndexByte(buf[:], 0)
	if n < 0 {
		n = len(buf)
	}
	return string(buf[:n]), nil
}
