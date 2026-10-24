package smart

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"filet/internal/apierr"
	"filet/internal/db"
	"filet/internal/db/gen"
	"filet/internal/events"
	"filet/internal/places"
	"filet/internal/tools"
)

// Statuses, worst first.
const (
	StatusFailing     = "failing"
	StatusWarning     = "warning"
	StatusOK          = "ok"
	StatusStandby     = "standby"
	StatusUnavailable = "unavailable"
)

var rank = map[string]int{StatusFailing: 4, StatusWarning: 3, StatusOK: 2, StatusStandby: 1, StatusUnavailable: 0}

// Data is the normalised snapshot stored in smart_snapshots.data.
type Data struct {
	Device               string   `json:"device"`
	Protocol             string   `json:"protocol"`
	Model                string   `json:"model"`
	Serial               string   `json:"serial"`
	Firmware             string   `json:"firmware"`
	CapacityBytes        int64    `json:"capacityBytes"`
	RotationRate         int      `json:"rotationRate"`
	SmartPassed          *bool    `json:"smartPassed"`
	TemperatureC         *int     `json:"temperatureC"`
	PowerOnHours         *int64   `json:"powerOnHours"`
	PowerCycles          *int64   `json:"powerCycles"`
	ReallocatedSectors   *int64   `json:"reallocatedSectors"`
	PendingSectors       *int64   `json:"pendingSectors"`
	OfflineUncorrectable *int64   `json:"offlineUncorrectable"`
	UDMACRCErrors        *int64   `json:"udmaCrcErrors"`
	NVMe                 *NVMe    `json:"nvme"`
	AsleepAt             *int64   `json:"asleepAt"`
	Messages             []string `json:"messages"`
}

// NVMe holds NVMe health log fields.
type NVMe struct {
	CriticalWarning         int   `json:"criticalWarning"`
	PercentageUsed          int   `json:"percentageUsed"`
	AvailableSpare          int   `json:"availableSpare"`
	AvailableSpareThreshold int   `json:"availableSpareThreshold"`
	MediaErrors             int64 `json:"mediaErrors"`
	DataWrittenBytes        int64 `json:"dataWrittenBytes"`
	UnsafeShutdowns         int64 `json:"unsafeShutdowns"`
}

type smartctlOut struct {
	Smartctl struct {
		Messages []struct {
			String string `json:"string"`
		} `json:"messages"`
		ExitStatus int `json:"exit_status"`
	} `json:"smartctl"`
	Device struct {
		Protocol string `json:"protocol"`
	} `json:"device"`
	ModelName       string `json:"model_name"`
	SerialNumber    string `json:"serial_number"`
	FirmwareVersion string `json:"firmware_version"`
	UserCapacity    struct {
		Bytes int64 `json:"bytes"`
	} `json:"user_capacity"`
	RotationRate *int `json:"rotation_rate"`
	SmartStatus  *struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`
	Temperature *struct {
		Current int `json:"current"`
	} `json:"temperature"`
	PowerOnTime *struct {
		Hours int64 `json:"hours"`
	} `json:"power_on_time"`
	PowerCycleCount    *int64 `json:"power_cycle_count"`
	ATASmartAttributes *struct {
		Table []struct {
			ID  int `json:"id"`
			Raw struct {
				Value int64 `json:"value"`
			} `json:"raw"`
		} `json:"table"`
	} `json:"ata_smart_attributes"`
	NVMeLog *struct {
		CriticalWarning         int   `json:"critical_warning"`
		Temperature             int   `json:"temperature"`
		AvailableSpare          int   `json:"available_spare"`
		AvailableSpareThreshold int   `json:"available_spare_threshold"`
		PercentageUsed          int   `json:"percentage_used"`
		DataUnitsWritten        int64 `json:"data_units_written"`
		PowerCycles             int64 `json:"power_cycles"`
		PowerOnHours            int64 `json:"power_on_hours"`
		UnsafeShutdowns         int64 `json:"unsafe_shutdowns"`
		MediaErrors             int64 `json:"media_errors"`
	} `json:"nvme_smart_health_information_log"`
}

// Parse normalises smartctl --json output.
func Parse(device string, raw []byte) (*Data, error) {
	var o smartctlOut
	if err := json.Unmarshal(raw, &o); err != nil {
		return nil, err
	}
	d := &Data{
		Device: device, Protocol: strings.ToLower(o.Device.Protocol), Model: o.ModelName, Serial: o.SerialNumber,
		Firmware: o.FirmwareVersion, CapacityBytes: o.UserCapacity.Bytes, Messages: []string{},
	}
	for _, m := range o.Smartctl.Messages {
		d.Messages = append(d.Messages, m.String)
	}
	if o.RotationRate != nil {
		d.RotationRate = *o.RotationRate
	}
	if o.SmartStatus != nil {
		v := o.SmartStatus.Passed
		d.SmartPassed = &v
	}
	if o.Temperature != nil {
		v := o.Temperature.Current
		d.TemperatureC = &v
	}
	if o.PowerOnTime != nil {
		v := o.PowerOnTime.Hours
		d.PowerOnHours = &v
	}
	d.PowerCycles = o.PowerCycleCount
	if o.ATASmartAttributes != nil {
		for _, a := range o.ATASmartAttributes.Table {
			v := a.Raw.Value
			switch a.ID {
			case 5:
				d.ReallocatedSectors = &v
			case 197:
				d.PendingSectors = &v
			case 198:
				d.OfflineUncorrectable = &v
			case 199:
				d.UDMACRCErrors = &v
			}
		}
	}
	if n := o.NVMeLog; n != nil {
		d.NVMe = &NVMe{
			CriticalWarning: n.CriticalWarning, PercentageUsed: n.PercentageUsed, AvailableSpare: n.AvailableSpare,
			AvailableSpareThreshold: n.AvailableSpareThreshold, MediaErrors: n.MediaErrors,
			DataWrittenBytes: n.DataUnitsWritten * 512000, UnsafeShutdowns: n.UnsafeShutdowns,
		}
		if d.TemperatureC == nil && n.Temperature > 0 {
			t := n.Temperature
			d.TemperatureC = &t
		}
		if d.PowerOnHours == nil {
			d.PowerOnHours = &n.PowerOnHours
		}
		if d.PowerCycles == nil {
			d.PowerCycles = &n.PowerCycles
		}
	}
	return d, nil
}

// Evaluate derives the health status of one device.
func Evaluate(d *Data) string {
	if d.SmartPassed != nil && !*d.SmartPassed {
		return StatusFailing
	}
	if n := d.NVMe; n != nil {
		if n.CriticalWarning&(1<<2|1<<3) != 0 || (n.AvailableSpareThreshold > 0 && n.AvailableSpare < n.AvailableSpareThreshold) {
			return StatusFailing
		}
		if n.PercentageUsed >= 90 || n.MediaErrors > 0 || n.CriticalWarning != 0 {
			return StatusWarning
		}
	}
	for _, c := range []*int64{d.ReallocatedSectors, d.PendingSectors, d.OfflineUncorrectable} {
		if c != nil && *c > 0 {
			return StatusWarning
		}
	}
	if d.TemperatureC != nil {
		limit := 70
		if d.RotationRate > 0 && d.NVMe == nil {
			limit = 60
		}
		if *d.TemperatureC >= limit {
			return StatusWarning
		}
	}
	if d.SmartPassed == nil && d.NVMe == nil && d.Model == "" {
		return StatusUnavailable
	}
	return StatusOK
}

// Service polls drives and serves health data.
type Service struct {
	db       *db.DB
	reg      *places.Registry
	runner   *tools.Runner
	hub      *events.Hub
	log      *slog.Logger
	interval time.Duration
	enabled  bool
	resolver *Resolver

	mu          sync.Mutex
	resolved    map[string]Resolution // place id → drives
	lastRefresh map[string]time.Time
	reasons     map[string]string // device → unavailable reason
	// base is the service lifetime context (refreshes outlive requests).
	base context.Context
}

// New creates the service; enabled is false when disabled or smartctl is
// missing.
func New(d *db.DB, reg *places.Registry, runner *tools.Runner, hub *events.Hub, log *slog.Logger, interval time.Duration, enabled bool) *Service {
	return &Service{
		db: d, reg: reg, runner: runner, hub: hub, log: log, interval: interval, enabled: enabled,
		resolver: NewResolver(), resolved: map[string]Resolution{}, lastRefresh: map[string]time.Time{}, reasons: map[string]string{},
		base: context.Background(),
	}
}

// Enabled reports whether SMART UI should be shown.
func (s *Service) Enabled() bool { return s.enabled }

// Start resolves drives and runs the poll loop until ctx ends.
func (s *Service) Start(ctx context.Context) {
	if !s.enabled {
		return
	}
	devices := map[string]bool{}
	for _, p := range s.reg.All() {
		res := s.resolver.Resolve(p.Path, p.SmartDevices)
		s.mu.Lock()
		s.resolved[p.ID] = res
		s.mu.Unlock()
		if res.Reason != "" {
			s.log.Info("SMART unavailable for place", "place", p.Name, "reason", res.Reason)
		} else {
			s.log.Info("SMART devices for place", "place", p.Name, "devices", res.Devices)
		}
		for _, d := range res.Devices {
			devices[d] = true
		}
	}
	s.dropUnreferenced(ctx, devices)
	go func() {
		// Staggered startup polls, only for stale snapshots.
		for dev := range devices {
			snap, err := s.db.R.GetSmartSnapshot(ctx, dev)
			if err == nil && time.Since(time.UnixMilli(snap.PolledAt)) < s.interval {
				continue
			}
			s.Poll(ctx, dev)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
		t := time.NewTicker(s.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				for dev := range devices {
					s.Poll(ctx, dev)
				}
			}
		}
	}()
}

func (s *Service) dropUnreferenced(ctx context.Context, devices map[string]bool) {
	snaps, err := s.db.W.ListSmartSnapshots(ctx)
	if err != nil {
		return
	}
	for _, sn := range snaps {
		if !devices[sn.Device] {
			_ = s.db.W.DeleteSmartSnapshot(ctx, sn.Device)
		}
	}
}

// Sweep removes snapshots of devices no longer referenced by any place.
func (s *Service) Sweep(ctx context.Context) {
	if !s.enabled {
		return
	}
	devices := map[string]bool{}
	s.mu.Lock()
	for _, r := range s.resolved {
		for _, d := range r.Devices {
			devices[d] = true
		}
	}
	s.mu.Unlock()
	s.dropUnreferenced(ctx, devices)
}

// Poll queries one device and stores the snapshot.
func (s *Service) Poll(ctx context.Context, device string) {
	args := []string{"--json=c", "--nocheck=standby,99", "-a", device}
	res, err := s.runner.Run(ctx, tools.Opts{Timeout: 30 * time.Second}, s.runner.Names.Smartctl, args...)
	if res != nil && strings.Contains(strings.ToLower(string(res.Stdout)), "unknown usb bridge") {
		res, err = s.runner.Run(ctx, tools.Opts{Timeout: 30 * time.Second}, s.runner.Names.Smartctl,
			"--json=c", "--nocheck=standby,99", "-d", "sat", "-a", device)
	}
	if res == nil {
		s.store(ctx, device, StatusUnavailable, &Data{Device: device, Messages: []string{errString(err)}})
		return
	}
	now := time.Now().UnixMilli()
	if res.ExitCode == 99 {
		prev, perr := s.db.W.GetSmartSnapshot(ctx, device)
		if perr == nil {
			var d Data
			if json.Unmarshal([]byte(prev.Data), &d) == nil {
				if d.AsleepAt == nil {
					d.AsleepAt = &now
				}
				status := prev.Status
				s.storeAt(ctx, device, status, &d, prev.PolledAt)
				return
			}
		}
		s.store(ctx, device, StatusStandby, &Data{Device: device, AsleepAt: &now, Messages: []string{}})
		return
	}
	d, perr := Parse(device, res.Stdout)
	if perr != nil {
		s.store(ctx, device, StatusUnavailable, &Data{Device: device, Messages: []string{"could not parse smartctl output"}})
		return
	}
	status := Evaluate(d)
	if res.ExitCode&3 != 0 {
		status = StatusUnavailable
	}
	s.store(ctx, device, status, d)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Service) store(ctx context.Context, device, status string, d *Data) {
	s.storeAt(ctx, device, status, d, time.Now().UnixMilli())
}

func (s *Service) storeAt(ctx context.Context, device, status string, d *Data, polledAt int64) {
	raw, _ := json.Marshal(d)
	if err := s.db.W.UpsertSmartSnapshot(ctx, gen.UpsertSmartSnapshotParams{Device: device, PolledAt: polledAt, Status: status, Data: string(raw)}); err != nil {
		s.log.Error("store SMART snapshot", "device", device, "err", err)
		return
	}
	s.mu.Lock()
	var placeIDs []string
	for id, r := range s.resolved {
		for _, dv := range r.Devices {
			if dv == device {
				placeIDs = append(placeIDs, id)
			}
		}
	}
	s.mu.Unlock()
	for _, id := range placeIDs {
		s.hub.Emit("smart.updated", map[string]string{"place": id})
	}
}

// DeviceSummary is one device in GET /api/places.
type DeviceSummary struct {
	Device       string `json:"device"`
	Status       string `json:"status"`
	Model        string `json:"model,omitempty"`
	TemperatureC *int   `json:"temperatureC"`
	PolledAt     *int64 `json:"polledAt"`
	AsleepAt     *int64 `json:"asleepAt"`
	Reason       string `json:"reason,omitempty"`
}

// Summary is the smart field of GET /api/places.
type Summary struct {
	Status  string          `json:"status"`
	Devices []DeviceSummary `json:"devices"`
}

// DeviceDetail is one device in GET /api/places/{place}/smart.
type DeviceDetail struct {
	Status   string `json:"status"`
	PolledAt *int64 `json:"polledAt"`
	Reason   string `json:"reason,omitempty"`
	Data     *Data  `json:"data"`
}

// PlaceSummary returns the badge data for a place, or nil when disabled.
func (s *Service) PlaceSummary(ctx context.Context, placeID string) *Summary {
	if !s.enabled {
		return nil
	}
	details := s.Detail(ctx, placeID)
	sum := &Summary{Status: StatusUnavailable, Devices: []DeviceSummary{}}
	best := -1
	for _, d := range details {
		ds := DeviceSummary{Status: d.Status, PolledAt: d.PolledAt, Reason: d.Reason}
		if d.Data != nil {
			ds.Device, ds.Model, ds.TemperatureC, ds.AsleepAt = d.Data.Device, d.Data.Model, d.Data.TemperatureC, d.Data.AsleepAt
		}
		sum.Devices = append(sum.Devices, ds)
		if r := rank[d.Status]; r > best {
			best, sum.Status = r, d.Status
		}
	}
	return sum
}

// Detail returns the full data per device of a place.
func (s *Service) Detail(ctx context.Context, placeID string) []DeviceDetail {
	s.mu.Lock()
	res, ok := s.resolved[placeID]
	s.mu.Unlock()
	if !ok {
		return []DeviceDetail{{Status: StatusUnavailable, Reason: "not resolved yet"}}
	}
	if res.Reason != "" {
		return []DeviceDetail{{Status: StatusUnavailable, Reason: res.Reason}}
	}
	out := []DeviceDetail{}
	for _, dev := range res.Devices {
		snap, err := s.db.R.GetSmartSnapshot(ctx, dev)
		if errors.Is(err, sql.ErrNoRows) || err != nil {
			out = append(out, DeviceDetail{Status: StatusUnavailable, Reason: "not checked yet", Data: &Data{Device: dev, Messages: []string{}}})
			continue
		}
		var d Data
		_ = json.Unmarshal([]byte(snap.Data), &d)
		pa := snap.PolledAt
		dd := DeviceDetail{Status: snap.Status, PolledAt: &pa, Data: &d}
		if snap.Status == StatusUnavailable && len(d.Messages) > 0 {
			dd.Reason = d.Messages[0]
		}
		out = append(out, dd)
	}
	return out
}

// Refresh triggers an immediate poll of a place's drives (once per 60 s
// per device).
func (s *Service) Refresh(placeID string) error {
	if !s.enabled {
		return apierr.ToolUnavailable("smartctl")
	}
	s.mu.Lock()
	res := s.resolved[placeID]
	var due []string
	var wait time.Duration
	for _, d := range res.Devices {
		if since := time.Since(s.lastRefresh[d]); since < time.Minute {
			wait = max(wait, time.Minute-since)
			continue
		}
		s.lastRefresh[d] = time.Now()
		due = append(due, d)
	}
	s.mu.Unlock()
	if len(due) == 0 && wait > 0 {
		e := apierr.New(http.StatusTooManyRequests, "rate_limited", "Refreshed recently; try again shortly")
		e.RetryAfter = int(math.Ceil(wait.Seconds()))
		return e
	}
	go func() {
		for _, d := range due {
			s.Poll(s.base, d)
		}
	}()
	return nil
}
