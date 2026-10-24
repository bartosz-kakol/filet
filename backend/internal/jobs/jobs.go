// Package jobs runs copy, move, trash, restore and purge operations as
// persistent background jobs with progress, cancellation and interactive
// conflict resolution.
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"filet/internal/apierr"
	"filet/internal/db"
	"filet/internal/db/gen"
	"filet/internal/events"
	"filet/internal/fsops"
	"filet/internal/ids"
	"filet/internal/places"
	"filet/internal/thumbs"
)

// Statuses.
const (
	StatusQueued      = "queued"
	StatusRunning     = "running"
	StatusAwaiting    = "awaiting_input"
	StatusCompleted   = "completed"
	StatusFailed      = "failed"
	StatusCancelled   = "cancelled"
	StatusInterrupted = "interrupted"
)

const (
	maxRunning     = 2
	publishEvery   = 250 * time.Millisecond
	persistEvery   = time.Second
	maxErrors      = 100
	codeRestarted  = "server_restarted"
	msgInterrupted = "Interrupted by server restart"
)

// Errors.
var (
	ErrNotFound  = apierr.New(http.StatusNotFound, "job_not_found", "Unknown job")
	ErrNotActive = apierr.New(http.StatusConflict, "job_not_active", "The job is not active")
	ErrStale     = apierr.New(http.StatusConflict, "stale_conflict", "This conflict is no longer pending")
	errShutdown  = errors.New("server shutting down")
)

// Spec is a job submission.
type Spec struct {
	Type     string   `json:"type"`
	Src      *Src     `json:"src,omitempty"`
	Dst      *Dst     `json:"dst,omitempty"`
	Place    string   `json:"place,omitempty"`
	Paths    []string `json:"paths,omitempty"`
	IDs      []string `json:"ids,omitempty"`
	All      bool     `json:"all,omitempty"`
	Conflict string   `json:"conflict,omitempty"`
}

// Src is the source of a copy or move.
type Src struct {
	Place string   `json:"place"`
	Paths []string `json:"paths"`
}

// Dst is the destination of a copy or move.
type Dst struct {
	Place string `json:"place"`
	Dir   string `json:"dir"`
}

// Side is one side of a conflict.
type Side struct {
	Path  string       `json:"path"`
	Entry places.Entry `json:"entry"`
}

// Conflict is a pending conflict prompt.
type Conflict struct {
	ID               string `json:"id"`
	Src              Side   `json:"src"`
	Dst              Side   `json:"dst"`
	SameItem         bool   `json:"sameItem"`
	UploadInProgress bool   `json:"uploadInProgress"`
	SuggestedName    string `json:"suggestedName"`
}

// Rename records a keep-both rename.
type Rename struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ItemError is a per-item failure.
type ItemError struct {
	Path string `json:"path"`
	Code string `json:"code"`
}

// Result is the job summary.
type Result struct {
	Skipped int         `json:"skipped"`
	Renamed []Rename    `json:"renamed"`
	Errors  []ItemError `json:"errors"`
}

// JobError is a job-level failure.
type JobError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Job is the JSON job object.
type Job struct {
	ID              string          `json:"id"`
	Type            string          `json:"type"`
	Status          string          `json:"status"`
	Spec            json.RawMessage `json:"spec"`
	BytesTotal      int64           `json:"bytesTotal"`
	BytesDone       int64           `json:"bytesDone"`
	ItemsTotal      int64           `json:"itemsTotal"`
	ItemsDone       int64           `json:"itemsDone"`
	CurrentItem     *string         `json:"currentItem"`
	PendingConflict *Conflict       `json:"pendingConflict"`
	Result          Result          `json:"result"`
	Error           *JobError       `json:"error"`
	CreatedAt       int64           `json:"createdAt"`
	StartedAt       *int64          `json:"startedAt"`
	FinishedAt      *int64          `json:"finishedAt"`
}

// Terminal reports whether the status is final.
func Terminal(status string) bool {
	switch status {
	case StatusCompleted, StatusFailed, StatusCancelled, StatusInterrupted:
		return true
	}
	return false
}

type resolution struct {
	action     string
	applyToAll bool
}

type job struct {
	mu              sync.Mutex
	j               Job
	spec            Spec
	conflictDefault string
	tempPlace       string
	tempPath        string
	cancel          context.CancelCauseFunc
	resolveCh       chan resolution
	lastPublish     time.Time
	lastPersist     time.Time
}

// Runner executes jobs, two at a time, FIFO.
type Runner struct {
	db     *db.DB
	reg    *places.Registry
	hub    *events.Hub
	ops    *fsops.Ops
	thumbs *thumbs.Cache
	log    *slog.Logger

	// base is the service lifetime context: jobs outlive the request that
	// submitted them.
	base context.Context

	mu      sync.Mutex
	jobs    map[string]*job
	queue   []*job
	running int
	closed  bool
	wg      sync.WaitGroup
}

// New creates a runner.
func New(d *db.DB, reg *places.Registry, hub *events.Hub, ops *fsops.Ops, th *thumbs.Cache, log *slog.Logger) *Runner {
	return &Runner{db: d, reg: reg, hub: hub, ops: ops, thumbs: th, log: log, jobs: map[string]*job{}, base: context.Background()}
}

// Reconcile marks jobs left unfinished by a crash as interrupted and removes
// their temporary files.
func (r *Runner) Reconcile(ctx context.Context) error {
	rows, err := r.db.W.ListUnfinishedJobs(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.TempPath.Valid && row.TempPlaceID.Valid {
			if p, err := r.reg.Get(row.TempPlaceID.String); err == nil {
				_ = p.Root.Remove(places.RootRel(row.TempPath.String))
			}
		}
		row.Status = StatusInterrupted
		row.FinishedAt = sql.NullInt64{Int64: time.Now().UnixMilli(), Valid: true}
		row.ErrorCode = sql.NullString{String: codeRestarted, Valid: true}
		row.ErrorMessage = sql.NullString{String: msgInterrupted, Valid: true}
		row.TempPath, row.TempPlaceID, row.PendingConflict = sql.NullString{}, sql.NullString{}, sql.NullString{}
		if err := r.db.W.UpdateJob(ctx, updateParams(row)); err != nil {
			return err
		}
	}
	if len(rows) > 0 {
		r.log.Info("reconciled jobs", "interrupted", len(rows))
	}
	return nil
}

func updateParams(row gen.Job) gen.UpdateJobParams {
	return gen.UpdateJobParams{
		Status: row.Status, ConflictDefault: row.ConflictDefault, BytesTotal: row.BytesTotal, BytesDone: row.BytesDone,
		ItemsTotal: row.ItemsTotal, ItemsDone: row.ItemsDone, CurrentItem: row.CurrentItem,
		PendingConflict: row.PendingConflict, TempPlaceID: row.TempPlaceID, TempPath: row.TempPath, Result: row.Result,
		ErrorCode: row.ErrorCode, ErrorMessage: row.ErrorMessage, StartedAt: row.StartedAt, FinishedAt: row.FinishedAt, ID: row.ID,
	}
}

func nullStr(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func nullStrp(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

func nullInt(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

// rowOf converts in-memory state to a DB row. Caller holds jb.mu.
func (jb *job) rowOf() gen.Job {
	res, _ := json.Marshal(jb.j.Result)
	row := gen.Job{
		ID: jb.j.ID, Type: jb.j.Type, Status: jb.j.Status, Spec: string(jb.j.Spec),
		ConflictDefault: nullStr(jb.conflictDefault), BytesTotal: jb.j.BytesTotal, BytesDone: jb.j.BytesDone,
		ItemsTotal: jb.j.ItemsTotal, ItemsDone: jb.j.ItemsDone, CurrentItem: nullStrp(jb.j.CurrentItem),
		TempPlaceID: nullStr(jb.tempPlace), TempPath: nullStr(jb.tempPath), Result: sql.NullString{String: string(res), Valid: true},
		CreatedAt: jb.j.CreatedAt, StartedAt: nullInt(jb.j.StartedAt), FinishedAt: nullInt(jb.j.FinishedAt),
	}
	if jb.j.PendingConflict != nil {
		pc, _ := json.Marshal(jb.j.PendingConflict)
		row.PendingConflict = sql.NullString{String: string(pc), Valid: true}
	}
	if jb.j.Error != nil {
		row.ErrorCode = nullStr(jb.j.Error.Code)
		row.ErrorMessage = nullStr(jb.j.Error.Message)
	}
	return row
}

// fromRow converts a DB row to the JSON object.
func fromRow(row gen.Job) Job {
	j := Job{
		ID: row.ID, Type: row.Type, Status: row.Status, Spec: json.RawMessage(row.Spec),
		BytesTotal: row.BytesTotal, BytesDone: row.BytesDone, ItemsTotal: row.ItemsTotal, ItemsDone: row.ItemsDone,
		CreatedAt: row.CreatedAt, Result: Result{Renamed: []Rename{}, Errors: []ItemError{}},
	}
	if row.CurrentItem.Valid {
		j.CurrentItem = &row.CurrentItem.String
	}
	if row.PendingConflict.Valid {
		var c Conflict
		if json.Unmarshal([]byte(row.PendingConflict.String), &c) == nil {
			j.PendingConflict = &c
		}
	}
	if row.Result.Valid {
		_ = json.Unmarshal([]byte(row.Result.String), &j.Result)
		if j.Result.Renamed == nil {
			j.Result.Renamed = []Rename{}
		}
		if j.Result.Errors == nil {
			j.Result.Errors = []ItemError{}
		}
	}
	if row.ErrorCode.Valid {
		j.Error = &JobError{Code: row.ErrorCode.String, Message: row.ErrorMessage.String}
	}
	if row.StartedAt.Valid {
		j.StartedAt = &row.StartedAt.Int64
	}
	if row.FinishedAt.Valid {
		j.FinishedAt = &row.FinishedAt.Int64
	}
	return j
}

// Submit validates spec synchronously and queues the job.
func (r *Runner) Submit(ctx context.Context, spec Spec) (Job, error) {
	if err := r.validate(ctx, &spec); err != nil {
		return Job{}, err
	}
	raw, _ := json.Marshal(spec)
	now := time.Now().UnixMilli()
	jb := &job{
		spec: spec, conflictDefault: spec.Conflict, resolveCh: make(chan resolution, 1),
		j: Job{
			ID: ids.New(), Type: spec.Type, Status: StatusQueued, Spec: raw, CreatedAt: now,
			Result: Result{Renamed: []Rename{}, Errors: []ItemError{}},
		},
	}
	if err := r.db.W.InsertJob(ctx, gen.InsertJobParams{
		ID: jb.j.ID, Type: spec.Type, Status: StatusQueued, Spec: string(raw), ConflictDefault: nullStr(spec.Conflict), CreatedAt: now,
	}); err != nil {
		return Job{}, err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return Job{}, apierr.New(http.StatusServiceUnavailable, "shutting_down", "The server is shutting down")
	}
	r.jobs[jb.j.ID] = jb
	r.queue = append(r.queue, jb)
	r.mu.Unlock()
	r.publish(jb, true)
	r.schedule() //nolint:contextcheck // jobs run detached from the submitting request
	return jb.snapshot(), nil
}

func (jb *job) snapshot() Job {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	j := jb.j
	j.Result.Renamed = append([]Rename{}, jb.j.Result.Renamed...)
	j.Result.Errors = append([]ItemError{}, jb.j.Result.Errors...)
	return j
}

func (r *Runner) schedule() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for !r.closed && r.running < maxRunning && len(r.queue) > 0 {
		jb := r.queue[0]
		r.queue = r.queue[1:]
		jb.mu.Lock()
		if jb.j.Status != StatusQueued {
			jb.mu.Unlock()
			continue
		}
		ctx, cancel := context.WithCancelCause(r.base)
		jb.cancel = cancel
		jb.mu.Unlock()
		r.running++
		r.wg.Add(1)
		go r.execute(ctx, jb)
	}
}

// Get returns one job.
func (r *Runner) Get(ctx context.Context, id string) (Job, error) {
	r.mu.Lock()
	jb, ok := r.jobs[id]
	r.mu.Unlock()
	if ok {
		return jb.snapshot(), nil
	}
	row, err := r.db.R.GetJob(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	return fromRow(row), nil
}

// List returns all non-dismissed jobs, newest first.
func (r *Runner) List(ctx context.Context) ([]Job, error) {
	rows, err := r.db.R.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(rows))
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, row := range rows {
		if jb, ok := r.jobs[row.ID]; ok {
			out = append(out, jb.snapshot())
		} else {
			out = append(out, fromRow(row))
		}
	}
	return out, nil
}

// Cancel cancels a queued, running or waiting job.
func (r *Runner) Cancel(ctx context.Context, id string) (Job, error) {
	r.mu.Lock()
	jb, ok := r.jobs[id]
	r.mu.Unlock()
	if !ok {
		if _, err := r.Get(ctx, id); err != nil {
			return Job{}, err
		}
		return Job{}, ErrNotActive
	}
	jb.mu.Lock()
	status := jb.j.Status
	switch {
	case Terminal(status):
		jb.mu.Unlock()
		return Job{}, ErrNotActive
	case status == StatusQueued && jb.cancel == nil:
		now := time.Now().UnixMilli()
		jb.j.Status = StatusCancelled
		jb.j.FinishedAt = &now
		jb.mu.Unlock()
		r.persist(jb)
		r.publish(jb, true)
		r.forget(id)
	default:
		cancel := jb.cancel
		jb.mu.Unlock()
		cancel(context.Canceled)
	}
	return jb.snapshot(), nil
}

// Resolve answers a pending conflict.
func (r *Runner) Resolve(ctx context.Context, id, conflictID, action string, applyToAll bool) (Job, error) {
	r.mu.Lock()
	jb, ok := r.jobs[id]
	r.mu.Unlock()
	if !ok {
		if _, err := r.Get(ctx, id); err != nil {
			return Job{}, err
		}
		return Job{}, ErrNotActive
	}
	switch action {
	case "overwrite", "rename", "skip":
	default:
		return Job{}, apierr.Invalid("action", "action must be overwrite, rename or skip")
	}
	jb.mu.Lock()
	pc := jb.j.PendingConflict
	if Terminal(jb.j.Status) {
		jb.mu.Unlock()
		return Job{}, ErrNotActive
	}
	if jb.j.Status != StatusAwaiting || pc == nil || pc.ID != conflictID {
		jb.mu.Unlock()
		return Job{}, ErrStale
	}
	if action == "overwrite" && (pc.SameItem || pc.UploadInProgress) {
		jb.mu.Unlock()
		return Job{}, apierr.Invalid("action", "Overwrite is not possible for this conflict")
	}
	jb.j.PendingConflict = nil
	jb.j.Status = StatusRunning
	jb.mu.Unlock()
	jb.resolveCh <- resolution{action: action, applyToAll: applyToAll}
	r.publish(jb, true)
	return jb.snapshot(), nil
}

// Dismiss deletes a terminal job.
func (r *Runner) Dismiss(ctx context.Context, id string) error {
	j, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	if !Terminal(j.Status) {
		return ErrNotActive
	}
	r.forget(id)
	if err := r.db.W.DeleteJob(ctx, id); err != nil {
		return err
	}
	r.hub.Emit("job.removed", map[string]string{"id": id})
	return nil
}

func (r *Runner) forget(id string) {
	r.mu.Lock()
	delete(r.jobs, id)
	r.mu.Unlock()
}

// Sweep deletes terminal jobs finished more than 24 h ago.
func (r *Runner) Sweep(ctx context.Context) {
	cutoff := time.Now().Add(-24 * time.Hour).UnixMilli()
	idsOld, err := r.db.W.ListOldFinishedJobIDs(ctx, sql.NullInt64{Int64: cutoff, Valid: true})
	if err != nil {
		return
	}
	for _, id := range idsOld {
		r.forget(id)
		if r.db.W.DeleteJob(ctx, id) == nil {
			r.hub.Emit("job.removed", map[string]string{"id": id})
		}
	}
}

// Shutdown cancels running jobs (marked interrupted) and waits for them.
func (r *Runner) Shutdown() {
	r.mu.Lock()
	r.closed = true
	queued := r.queue
	r.queue = nil
	var cancels []context.CancelCauseFunc
	for _, jb := range r.jobs {
		jb.mu.Lock()
		if jb.cancel != nil && !Terminal(jb.j.Status) {
			cancels = append(cancels, jb.cancel)
		}
		jb.mu.Unlock()
	}
	r.mu.Unlock()
	for _, c := range cancels {
		c(errShutdown)
	}
	for _, jb := range queued {
		jb.mu.Lock()
		now := time.Now().UnixMilli()
		jb.j.Status = StatusInterrupted
		jb.j.FinishedAt = &now
		jb.j.Error = &JobError{Code: codeRestarted, Message: msgInterrupted}
		jb.mu.Unlock()
		r.persist(jb)
	}
	r.wg.Wait()
}

// persist writes the job row.
func (r *Runner) persist(jb *job) {
	jb.mu.Lock()
	row := jb.rowOf()
	jb.lastPersist = time.Now()
	jb.mu.Unlock()
	if err := r.db.W.UpdateJob(r.base, updateParams(row)); err != nil {
		r.log.Error("persist job", "id", row.ID, "err", err)
	}
}

// publish emits job.updated, throttled unless force.
func (r *Runner) publish(jb *job, force bool) {
	jb.mu.Lock()
	if !force && time.Since(jb.lastPublish) < publishEvery {
		jb.mu.Unlock()
		return
	}
	jb.lastPublish = time.Now()
	jb.mu.Unlock()
	r.hub.Emit("job.updated", jb.snapshot())
}

// progress publishes and persists at their throttled rates.
func (r *Runner) progress(jb *job) {
	r.publish(jb, false)
	jb.mu.Lock()
	due := time.Since(jb.lastPersist) >= persistEvery
	jb.mu.Unlock()
	if due {
		r.persist(jb)
	}
}
