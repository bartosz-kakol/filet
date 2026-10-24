// Package smart resolves the physical drives behind each place, polls them
// with smartctl without waking sleeping drives, and evaluates health.
package smart

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// Resolution is the set of drives behind a place, or why there are none.
type Resolution struct {
	Devices []string
	Reason  string
}

// Resolver maps a directory to leaf block devices via /proc/self/mountinfo
// and /sys. Every filesystem access is relative to SysRoot so tests can use
// a fake tree.
type Resolver struct {
	SysRoot   string // "/" in production
	Mountinfo string // "/proc/self/mountinfo" in production
	// StatDev returns the device number of a path (st_dev), or for a block
	// device node its st_rdev.
	StatDev func(path string, rdev bool) (major, minor uint32, err error)
	// DevAccessible reports whether a /dev node can be opened.
	DevAccessible func(path string) bool
}

// NewResolver returns the production resolver.
func NewResolver() *Resolver {
	return &Resolver{
		SysRoot: "/", Mountinfo: "/proc/self/mountinfo",
		StatDev: func(path string, rdev bool) (uint32, uint32, error) {
			var st unix.Stat_t
			if err := unix.Stat(path, &st); err != nil {
				return 0, 0, err
			}
			d := uint64(st.Dev) //nolint:gosec,unconvert // platform width
			if rdev {
				d = uint64(st.Rdev) //nolint:gosec,unconvert // platform width
			}
			return unix.Major(d), unix.Minor(d), nil
		},
		DevAccessible: func(path string) bool {
			f, err := os.Open(path) //nolint:gosec // /dev path built from sysfs names
			if err != nil {
				return false
			}
			_ = f.Close()
			return true
		},
	}
}

var unsupportedFS = []string{"zfs", "nfs", "cifs", "fuse", "tmpfs", "overlay", "smb", "9p", "virtiofs"}

// Resolve finds the leaf disks behind path; override (smart_devices) wins.
func (r *Resolver) Resolve(path string, override []string) Resolution {
	if len(override) > 0 {
		return Resolution{Devices: override}
	}
	major, minor, err := r.StatDev(path, false)
	if err != nil {
		return Resolution{Reason: "cannot stat place: " + err.Error()}
	}
	if major == 0 {
		m, ok := r.mountFor(path)
		if !ok {
			return Resolution{Reason: "mount not found"}
		}
		if strings.HasPrefix(m.source, "/dev/") {
			major, minor, err = r.StatDev(m.source, true)
			if err != nil {
				return Resolution{Reason: "device not accessible"}
			}
		} else {
			for _, u := range unsupportedFS {
				if strings.HasPrefix(m.fstype, u) {
					return Resolution{Reason: fmt.Sprintf("filesystem type %s not supported; set smart_devices in config", m.fstype)}
				}
			}
			return Resolution{Reason: fmt.Sprintf("filesystem type %s not supported; set smart_devices in config", m.fstype)}
		}
	}
	link := filepath.Join(r.SysRoot, "sys/dev/block", fmt.Sprintf("%d:%d", major, minor))
	dir, err := filepath.EvalSymlinks(link)
	if err != nil {
		return Resolution{Reason: "block device not found in /sys"}
	}
	leaves := map[string]bool{}
	r.leaves(dir, leaves, 0)
	if len(leaves) == 0 {
		return Resolution{Reason: "no physical drive found"}
	}
	var devs []string
	for name := range leaves {
		dev := "/dev/" + name
		if r.DevAccessible != nil && !r.DevAccessible(dev) {
			return Resolution{Reason: "device not accessible"}
		}
		devs = append(devs, dev)
	}
	sort.Strings(devs)
	return Resolution{Devices: devs}
}

// leaves walks from a sysfs device dir to whole leaf disks.
func (r *Resolver) leaves(dir string, out map[string]bool, depth int) {
	if depth > 8 {
		return
	}
	if _, err := os.Stat(filepath.Join(dir, "partition")); err == nil {
		dir = filepath.Dir(dir)
	}
	name := filepath.Base(dir)
	if strings.HasPrefix(name, "dm-") || strings.HasPrefix(name, "md") {
		slavesDir := filepath.Join(r.SysRoot, "sys/block", name, "slaves")
		entries, err := os.ReadDir(slavesDir)
		if err != nil || len(entries) == 0 {
			return
		}
		for _, e := range entries {
			sd, err := filepath.EvalSymlinks(filepath.Join(slavesDir, e.Name()))
			if err != nil {
				continue
			}
			r.leaves(sd, out, depth+1)
		}
		return
	}
	out[name] = true
}

type mount struct {
	point, fstype, source string
}

// mountFor returns the mount with the longest mount point prefixing path.
func (r *Resolver) mountFor(path string) (mount, bool) {
	f, err := os.Open(r.Mountinfo)
	if err != nil {
		return mount{}, false
	}
	defer f.Close()
	var best mount
	found := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		sep := -1
		for i, fl := range fields {
			if fl == "-" {
				sep = i
				break
			}
		}
		if sep < 0 || len(fields) < 5 || sep+2 >= len(fields) {
			continue
		}
		point := unescapeMount(fields[4])
		if point != "/" && path != point && !strings.HasPrefix(path, point+"/") {
			continue
		}
		if !found || len(point) >= len(best.point) {
			best = mount{point: point, fstype: fields[sep+1], source: fields[sep+2]}
			found = true
		}
	}
	return best, found
}

// unescapeMount decodes the octal escapes used in mountinfo (\040 etc.).
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
