package places

import (
	"io/fs"
	"os"

	"filet/internal/fsx"
)

// procIdentity is the process's effective uid/gid and groups, fetched once.
type procIdentity struct {
	euid   uint32
	egid   uint32
	groups map[uint32]bool
}

func currentIdentity() procIdentity {
	id := procIdentity{euid: uint32(os.Geteuid()), egid: uint32(os.Getegid()), groups: map[uint32]bool{}} //nolint:gosec // ids are non-negative
	id.groups[id.egid] = true
	if gs, err := os.Getgroups(); err == nil {
		for _, g := range gs {
			id.groups[uint32(g)] = true //nolint:gosec // ids are non-negative
		}
	}
	return id
}

// bits returns the rwx triplet that applies to the process.
func (id procIdentity) bits(fi fs.FileInfo) fs.FileMode {
	perm := fi.Mode().Perm()
	uid, gid, ok := fsx.Owner(fi)
	if !ok {
		return 0o7
	}
	switch {
	case uid == id.euid:
		return (perm >> 6) & 7
	case id.groups[gid]:
		return (perm >> 3) & 7
	default:
		return perm & 7
	}
}

func (id procIdentity) canRead(fi fs.FileInfo, dir bool) bool {
	if id.euid == 0 {
		return true
	}
	b := id.bits(fi)
	if dir {
		return b&5 == 5
	}
	return b&4 != 0
}

func (id procIdentity) canWrite(fi fs.FileInfo) bool {
	if id.euid == 0 {
		return true
	}
	return id.bits(fi)&3 == 3
}
