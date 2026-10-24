//go:build linux

package fsx

import (
	"fmt"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

func platformFallocate(f *os.File, size int64) error {
	return unix.Fallocate(int(f.Fd()), 0, 0, size) //nolint:gosec // fd fits in int
}

func renameNoReplace(oldfd int, oldName string, newfd int, newName string) error {
	return unix.Renameat2(oldfd, oldName, newfd, newName, unix.RENAME_NOREPLACE)
}

func datasync(f *os.File) error {
	return unix.Fdatasync(int(f.Fd())) //nolint:gosec // fd fits in int
}

func fstatfs(f *os.File) (DiskUsage, error) {
	var st unix.Statfs_t
	if err := unix.Fstatfs(int(f.Fd()), &st); err != nil { //nolint:gosec // fd fits in int
		return DiskUsage{}, err
	}
	bs := uint64(st.Bsize) //nolint:gosec // block size is positive
	return DiskUsage{
		Total: st.Blocks * bs,
		Used:  (st.Blocks - st.Bfree) * bs,
		Free:  st.Bavail * bs,
		Fsid:  fmt.Sprintf("%x-%x", uint32(st.Fsid.Val[0]), uint32(st.Fsid.Val[1])), //nolint:gosec // bit pattern
	}, nil
}

func realPath(f *os.File) (string, error) {
	return os.Readlink("/proc/self/fd/" + strconv.Itoa(int(f.Fd()))) //nolint:gosec // fd fits in int
}
