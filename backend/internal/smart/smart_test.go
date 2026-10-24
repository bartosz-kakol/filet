package smart

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"filet/internal/config"
	"filet/internal/db"
	"filet/internal/events"
	"filet/internal/places"
	"filet/internal/tools"
)

func TestEvaluateFixtures(t *testing.T) {
	for file, want := range map[string]string{
		"hdd_ok.json": StatusOK, "hdd_realloc.json": StatusWarning, "hdd_failing.json": StatusFailing, "nvme_worn.json": StatusWarning,
	} {
		raw, err := os.ReadFile(filepath.Join("testdata", file))
		if err != nil {
			t.Fatal(err)
		}
		d, err := Parse("/dev/x", raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := Evaluate(d); got != want {
			t.Errorf("%s: %s want %s", file, got, want)
		}
	}
	raw, _ := os.ReadFile("testdata/nvme_worn.json")
	d, _ := Parse("/dev/nvme0n1", raw)
	if d.NVMe.PercentageUsed != 92 || d.NVMe.DataWrittenBytes != 512000000000 || *d.TemperatureC != 48 {
		t.Fatalf("nvme: %+v", d.NVMe)
	}
	hot := &Data{Model: "x", RotationRate: 7200, TemperatureC: ptr(61)}
	if Evaluate(hot) != StatusWarning {
		t.Error("hot HDD")
	}
	ssd := &Data{Model: "x", TemperatureC: ptr(65)}
	if Evaluate(ssd) != StatusOK {
		t.Error("65 °C SSD is fine")
	}
}

func ptr[T any](v T) *T { return &v }

// fakeTree builds a sysfs/mountinfo tree:
//
//	8:1   sda1, a partition of sda
//	253:0 dm-0 (LVM) over dm-1 (LUKS) over sdb2 and sdc2
//	9:0   md0 (RAID1) over sdd1 and sde1
func fakeTree(t *testing.T) *Resolver {
	root := t.TempDir()
	mk := func(p string) string {
		full := filepath.Join(root, p)
		_ = os.MkdirAll(full, 0o755)
		return full
	}
	link := func(target, name string) {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755)
		if err := os.Symlink(filepath.Join(root, target), filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	// whole disks with partitions
	for _, d := range []string{"sda", "sdb", "sdc", "sdd", "sde"} {
		mk("sys/devices/pci/" + d)
		for _, part := range []string{"1", "2"} {
			p := mk("sys/devices/pci/" + d + "/" + d + part)
			_ = os.WriteFile(filepath.Join(p, "partition"), []byte(part), 0o644)
		}
	}
	mk("sys/devices/virtual/block/dm-0")
	mk("sys/devices/virtual/block/dm-1")
	mk("sys/devices/virtual/block/md0")
	link("sys/devices/pci/sda/sda1", "sys/dev/block/8:1")
	link("sys/devices/virtual/block/dm-0", "sys/dev/block/253:0")
	link("sys/devices/virtual/block/md0", "sys/dev/block/9:0")
	// LVM (dm-0) over LUKS (dm-1) over two partitions
	link("sys/devices/virtual/block/dm-1", "sys/block/dm-0/slaves/dm-1")
	link("sys/devices/pci/sdb/sdb2", "sys/block/dm-1/slaves/sdb2")
	link("sys/devices/pci/sdc/sdc2", "sys/block/dm-1/slaves/sdc2")
	link("sys/devices/pci/sdd/sdd1", "sys/block/md0/slaves/sdd1")
	link("sys/devices/pci/sde/sde1", "sys/block/md0/slaves/sde1")
	mountinfo := filepath.Join(root, "mountinfo")
	_ = os.WriteFile(mountinfo, []byte(
		"22 1 0:21 / /tank rw - zfs tank/data rw\n"+
			"23 1 0:22 / /srv/btrfs rw - btrfs /dev/sda1 rw\n"), 0o644)
	devs := map[string][2]uint32{"/srv/plain": {8, 1}, "/srv/lvm": {253, 0}, "/srv/raid": {9, 0}, "/tank/x": {0, 21}, "/srv/btrfs/sub": {0, 22}, "/dev/sda1": {8, 1}}
	return &Resolver{
		SysRoot: root, Mountinfo: mountinfo,
		StatDev: func(p string, _ bool) (uint32, uint32, error) {
			d := devs[p]
			return d[0], d[1], nil
		},
		DevAccessible: func(string) bool { return true },
	}
}

func TestResolve(t *testing.T) {
	r := fakeTree(t)
	cases := map[string][]string{
		"/srv/plain":     {"/dev/sda"},
		"/srv/lvm":       {"/dev/sdb", "/dev/sdc"},
		"/srv/raid":      {"/dev/sdd", "/dev/sde"},
		"/srv/btrfs/sub": {"/dev/sda"},
	}
	for path, want := range cases {
		res := r.Resolve(path, nil)
		if res.Reason != "" || len(res.Devices) != len(want) {
			t.Errorf("%s: %+v", path, res)
			continue
		}
		for i := range want {
			if res.Devices[i] != want[i] {
				t.Errorf("%s: %v want %v", path, res.Devices, want)
			}
		}
	}
	if res := r.Resolve("/tank/x", nil); res.Reason == "" || len(res.Devices) != 0 {
		t.Errorf("zfs: %+v", res)
	}
	if res := r.Resolve("/tank/x", []string{"/dev/nvme0n1"}); len(res.Devices) != 1 || res.Devices[0] != "/dev/nvme0n1" {
		t.Errorf("override: %+v", res)
	}
	r.DevAccessible = func(string) bool { return false }
	if res := r.Resolve("/srv/plain", nil); res.Reason != "device not accessible" {
		t.Errorf("inaccessible: %+v", res)
	}
}

func TestStandbyKeepsSnapshot(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "smartctl")
	ok, _ := os.ReadFile("testdata/hdd_ok.json")
	_ = os.WriteFile(filepath.Join(dir, "ok.json"), ok, 0o644)
	_ = os.WriteFile(script, []byte("#!/bin/sh\nif [ -f "+dir+"/asleep ]; then echo '{}'; exit 99; fi\ncat "+dir+"/ok.json\n"), 0o755)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	d, err := db.Open(context.Background(), filepath.Join(dir, "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	runner := tools.New(tools.Names{Smartctl: script}, log)
	place, _ := filepath.EvalSymlinks(t.TempDir())
	reg, _ := places.NewRegistry([]config.PlaceConfig{{ID: "p", Name: "P", ResolvedPath: place, SmartDevices: []string{"/dev/sda"}}}, dir, &runner.Caps, log)
	defer reg.Close()
	s := New(d, reg, runner, events.NewHub(), log, 3600e9, true)
	s.resolved["p"] = Resolution{Devices: []string{"/dev/sda"}}

	// Never read and asleep → standby.
	_ = os.WriteFile(filepath.Join(dir, "asleep"), nil, 0o644)
	s.Poll(context.Background(), "/dev/sda")
	if sum := s.PlaceSummary(context.Background(), "p"); sum.Status != StatusStandby {
		t.Fatalf("standby: %+v", sum)
	}
	// Awake → ok.
	_ = os.Remove(filepath.Join(dir, "asleep"))
	s.Poll(context.Background(), "/dev/sda")
	if sum := s.PlaceSummary(context.Background(), "p"); sum.Status != StatusOK || sum.Devices[0].Model != "WDC WD80EFZZ" {
		t.Fatalf("ok: %+v", sum)
	}
	// Asleep again → the previous snapshot is kept and marked asleep.
	_ = os.WriteFile(filepath.Join(dir, "asleep"), nil, 0o644)
	s.Poll(context.Background(), "/dev/sda")
	sum := s.PlaceSummary(context.Background(), "p")
	if sum.Status != StatusOK || sum.Devices[0].AsleepAt == nil {
		t.Fatalf("asleep keeps data: %+v", sum.Devices[0])
	}
}
