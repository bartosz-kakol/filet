// Package uploads implements the chunked, BLAKE3-verified, resumable upload
// protocol: preflight, registration with preallocation, chunk verification
// with durable writes, finalization, leftovers and startup reconciliation.
package uploads

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/zeebo/blake3"

	"filet/internal/apierr"
	"filet/internal/clock"
	"filet/internal/db"
	"filet/internal/db/gen"
	"filet/internal/events"
	"filet/internal/fsops"
	"filet/internal/fsx"
	"filet/internal/ids"
	"filet/internal/places"
	"filet/internal/thumbs"
	"filet/internal/tools"
)

// Protocol constants.
const (
	ChunkSize     = 8 << 20
	MaxAttempts   = 3
	LeftoverAfter = 120 * time.Second
	bufferCount   = 16
	partialPrefix = ".filet-part-"
)

// Errors specific to uploads.
var (
	ErrGone            = apierr.New(http.StatusGone, "upload_gone", "This upload no longer exists")
	ErrChunkSize       = apierr.New(http.StatusBadRequest, "unsupported_chunk_size", "Unsupported chunk size")
	ErrInvalidHashes   = apierr.New(http.StatusBadRequest, "invalid_hashes", "Invalid chunk hash list")
	ErrChunkIndex      = apierr.New(http.StatusBadRequest, "invalid_chunk_index", "Chunk index out of range")
	ErrChunkLength     = apierr.New(http.StatusBadRequest, "chunk_length_mismatch", "Chunk length does not match")
	ErrChunkInProgress = apierr.New(http.StatusConflict, "chunk_in_progress", "This chunk is already being received")
	ErrCorrupted       = apierr.New(http.StatusUnprocessableEntity, "upload_corrupted", "A chunk failed verification 3 times; the upload was removed")
	// ErrClientGone means the request body ended early; no response should
	// be written.
	ErrClientGone = errors.New("client went away")

	errAbortCorrupted = errors.New("chunk failed verification too often")
)

// Service is the upload service.
type Service struct {
	db     *db.DB
	reg    *places.Registry
	hub    *events.Hub
	ops    *fsops.Ops
	thumbs *thumbs.Cache
	log    *slog.Logger

	pathLocks keyedMutex
	mu        sync.Mutex
	sessions  map[string]*session
	files     *fileCache
	bufSem    tools.Semaphore
	bufPool   sync.Pool

	// Test hooks.
	Statfs     func(f *os.File) (fsx.DiskUsage, error)
	BeforeMark func(id string, index int)
}

type session struct {
	mu      sync.RWMutex // R: chunk write; W: finalize/remove
	removed bool

	rowMu        sync.Mutex
	row          gen.UploadSession
	hashes       []byte
	lastProgress time.Time

	busyMu sync.Mutex
	busy   map[int]bool
}

func (s *session) snapshot() gen.UploadSession {
	s.rowMu.Lock()
	defer s.rowMu.Unlock()
	return s.row
}

// New creates the service.
func New(d *db.DB, reg *places.Registry, hub *events.Hub, ops *fsops.Ops, th *thumbs.Cache, log *slog.Logger) *Service {
	s := &Service{
		db: d, reg: reg, hub: hub, ops: ops, thumbs: th, log: log,
		sessions: map[string]*session{},
		bufSem:   tools.NewSemaphore(bufferCount),
		Statfs:   fsx.Fstatfs,
	}
	s.bufPool.New = func() any { b := make([]byte, ChunkSize); return &b }
	s.files = newFileCache(fsx.Datasync)
	return s
}

// SetSyncForTest replaces fdatasync for partial files.
func (s *Service) SetSyncForTest(fn func(*os.File) error) { s.files.sync = fn }

// Close syncs and closes partial files.
func (s *Service) Close() { s.files.closeAll() }

func nowMs() int64 { return clock.NowMs() }

func leftoverCutoff() int64 { return nowMs() - LeftoverAfter.Milliseconds() }

func isActive(row gen.UploadSession) bool { return row.LastActivityAt >= leftoverCutoff() }

func chunkCount(size int64) int64 { return (size + ChunkSize - 1) / ChunkSize }

func chunkLen(row gen.UploadSession, idx int64) int64 {
	if idx == row.ChunkCount-1 {
		if r := row.Size - idx*row.ChunkSize; r > 0 {
			return r
		}
	}
	return row.ChunkSize
}

func receivedBytes(row gen.UploadSession) int64 {
	return min(row.ReceivedCount*row.ChunkSize, row.Size)
}

// Targets implements fsops.UploadIndex.
func (s *Service) Targets(ctx context.Context, placeID string) ([]string, error) {
	rows, err := s.db.R.ListUploadSessionsInPlace(ctx, placeID)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.TargetPath
	}
	return out, nil
}

// Placeholder is an upload shown in a directory listing.
type Placeholder struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Size          int64  `json:"size"`
	ReceivedBytes int64  `json:"receivedBytes"`
	Active        bool   `json:"active"`
}

// InDir lists the upload sessions targeting dir.
func (s *Service) InDir(ctx context.Context, placeID, dir string) ([]Placeholder, error) {
	rows, err := s.db.R.ListUploadSessionsInDir(ctx, gen.ListUploadSessionsInDirParams{PlaceID: placeID, Dir: dir})
	if err != nil {
		return nil, err
	}
	out := make([]Placeholder, len(rows))
	for i, r := range rows {
		out[i] = Placeholder{ID: r.ID, Name: r.Name, Size: r.Size, ReceivedBytes: receivedBytes(r), Active: isActive(r)}
	}
	return out, nil
}

// --- registration ---

// RegisterRequest is the body of POST /api/uploads.
type RegisterRequest struct {
	Place     string `json:"place"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Mtime     *int64 `json:"mtime"`
	ChunkSize int64  `json:"chunkSize"`
	Hashes    string `json:"hashes"`
	Conflict  string `json:"conflict"`
}

// RegisterResponse is the registration result.
type RegisterResponse struct {
	ID         string        `json:"id,omitempty"`
	Status     string        `json:"status"`
	ChunkSize  int64         `json:"chunkSize,omitempty"`
	ChunkCount int64         `json:"chunkCount"`
	Missing    [][2]int64    `json:"missing"`
	Entry      *places.Entry `json:"entry,omitempty"`
	Renamed    bool          `json:"renamed,omitempty"`
	httpStatus int
}

// HTTPStatus returns 201 for new sessions and 200 otherwise.
func (r *RegisterResponse) HTTPStatus() int { return r.httpStatus }

// Register handles POST /api/uploads.
func (s *Service) Register(ctx context.Context, req RegisterRequest) (*RegisterResponse, error) {
	p, err := s.reg.Get(req.Place)
	if err != nil {
		return nil, err
	}
	if err := p.Writable(); err != nil {
		return nil, err
	}
	target, err := places.ValidateRelPath(req.Path)
	if err != nil {
		return nil, err
	}
	if target == "/" {
		return nil, apierr.ErrInvalidPath
	}
	if req.ChunkSize != ChunkSize {
		return nil, ErrChunkSize
	}
	if req.Size < 0 {
		return nil, apierr.Invalid("size", "Size must not be negative")
	}
	switch req.Conflict {
	case "":
		req.Conflict = "fail"
	case "fail", "overwrite":
	default:
		return nil, apierr.Invalid("conflict", "conflict must be fail or overwrite")
	}
	hashes, err := ids.FromB64(req.Hashes)
	count := chunkCount(req.Size)
	if err != nil || int64(len(hashes)) != 32*count {
		return nil, ErrInvalidHashes
	}
	dir, name := places.Split(target)

	// (b) parents
	_, statErr := p.Root.Stat(places.RootRel(dir))
	if err := p.Root.MkdirAll(places.RootRel(dir), 0o755); err != nil {
		if fsx.IsNotDir(err) || fsx.IsExist(err) {
			return nil, apierr.ErrParentNotDir
		}
		return nil, apierr.FromFS(err)
	}
	if statErr != nil {
		parent, _ := places.Split(dir)
		s.hub.FS(p.ID, parent)
	}

	unlock := s.pathLocks.lock(p.ID + "\x00" + target)
	defer unlock()

	digest := blake3.Sum256(hashes)
	// (c) existing session
	existing, err := s.db.W.GetUploadSessionByTarget(ctx, gen.GetUploadSessionByTargetParams{PlaceID: p.ID, TargetPath: target})
	switch {
	case err == nil:
		if req.Size > 0 && existing.Size == req.Size && subtle.ConstantTimeCompare(existing.HashListDigest, digest[:]) == 1 {
			return s.resume(ctx, p, existing)
		}
		if isActive(existing) && req.Conflict != "overwrite" {
			return nil, apierr.New(http.StatusConflict, "upload_conflict", "Another upload to this name is in progress").With("active", true)
		}
		if err := s.removeByID(ctx, existing.ID, "replaced"); err != nil {
			return nil, err
		}
	case !errors.Is(err, sql.ErrNoRows):
		return nil, err
	}

	// (d) target on disk
	if _, err := p.Root.Lstat(places.RootRel(target)); err == nil {
		if req.Conflict == "fail" {
			return nil, apierr.ErrAlreadyExists.With("suggestedName", s.suggest(ctx, p, dir, name, false, ""))
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, apierr.FromFS(err)
	}

	if req.Size == 0 {
		return s.createEmpty(ctx, p, target, req)
	}

	// (e) free space
	df, err := fsops.OpenDir(p.Root, dir)
	if err != nil {
		return nil, apierr.FromFS(err)
	}
	du, err := s.Statfs(df)
	_ = df.Close()
	if err == nil && du.Free < uint64(req.Size) {
		return nil, insufficient(req.Size, du.Free)
	}

	// (f) partial file
	id := ids.New()
	partial := partialPrefix + id
	partialRel := places.RootRel(places.Join(dir, partial))
	f, err := p.Root.OpenFile(partialRel, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
	if err != nil {
		return nil, apierr.FromFS(err)
	}
	if err := fsx.Fallocate(f, req.Size); err != nil {
		_ = f.Close()
		_ = p.Root.Remove(partialRel)
		if fsx.IsNoSpace(err) {
			return nil, insufficient(req.Size, du.Free)
		}
		return nil, err
	}

	// (g) persist
	now := nowMs()
	row := gen.UploadSession{
		ID: id, PlaceID: p.ID, Dir: dir, Name: name, TargetPath: target, PartialName: partial, Size: req.Size,
		ChunkSize: ChunkSize, ChunkCount: count, HashListDigest: digest[:], ConflictPolicy: req.Conflict,
		CreatedAt: now, LastActivityAt: now,
	}
	if req.Mtime != nil {
		row.ClientMtime = sql.NullInt64{Int64: *req.Mtime, Valid: true}
	}
	err = s.db.Tx(ctx, func(q *gen.Queries, tx *sql.Tx) error {
		if err := q.InsertUploadSession(ctx, gen.InsertUploadSessionParams{
			ID: row.ID, PlaceID: row.PlaceID, Dir: row.Dir, Name: row.Name, TargetPath: row.TargetPath,
			PartialName: row.PartialName, Size: row.Size, ChunkSize: row.ChunkSize, ChunkCount: row.ChunkCount,
			HashListDigest: row.HashListDigest, ClientMtime: row.ClientMtime, ConflictPolicy: row.ConflictPolicy,
			CreatedAt: row.CreatedAt, LastActivityAt: row.LastActivityAt,
		}); err != nil {
			return err
		}
		return db.InsertChunks(ctx, tx, id, hashes)
	})
	if err != nil {
		_ = f.Close()
		_ = p.Root.Remove(partialRel)
		return nil, err
	}
	s.files.put(id, f)
	s.mu.Lock()
	s.sessions[id] = &session{row: row, hashes: hashes, busy: map[int]bool{}}
	s.mu.Unlock()

	// (h)
	s.hub.Emit("upload.registered", map[string]any{"id": id, "place": p.ID, "dir": dir, "name": name, "size": req.Size})
	s.hub.FS(p.ID, dir)
	return &RegisterResponse{
		ID: id, Status: "new", ChunkSize: ChunkSize, ChunkCount: count,
		Missing: [][2]int64{{0, count - 1}}, httpStatus: http.StatusCreated,
	}, nil
}

func insufficient(required int64, available uint64) error {
	return apierr.ErrInsufficient.With("required", required).With("available", available)
}

func (s *Service) resume(ctx context.Context, p *places.Place, row gen.UploadSession) (*RegisterResponse, error) {
	sess, err := s.load(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.removed {
		return nil, ErrGone
	}
	now := nowMs()
	partialRel := places.RootRel(places.Join(row.Dir, row.PartialName))
	fi, err := p.Root.Lstat(partialRel)
	if err != nil || fi.Size() != row.Size {
		// The partial vanished: recreate and preallocate it, nothing received.
		s.files.close(row.ID)
		_ = p.Root.Remove(partialRel)
		f, err := p.Root.OpenFile(partialRel, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
		if err != nil {
			return nil, apierr.FromFS(err)
		}
		if err := fsx.Fallocate(f, row.Size); err != nil {
			_ = f.Close()
			_ = p.Root.Remove(partialRel)
			return nil, apierr.FromFS(err)
		}
		s.files.put(row.ID, f)
		if err := s.db.Tx(ctx, func(q *gen.Queries, _ *sql.Tx) error {
			if err := q.ResetChunks(ctx, row.ID); err != nil {
				return err
			}
			return q.ResetUploadReceived(ctx, gen.ResetUploadReceivedParams{LastActivityAt: now, ID: row.ID})
		}); err != nil {
			return nil, err
		}
	} else if err := s.db.W.TouchUploadSession(ctx, gen.TouchUploadSessionParams{LastActivityAt: now, ID: row.ID}); err != nil {
		return nil, err
	}
	missing, received, err := s.missing(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	sess.rowMu.Lock()
	sess.row.LastActivityAt = now
	sess.row.ReceivedCount = received
	sess.rowMu.Unlock()
	return &RegisterResponse{ID: row.ID, Status: "resumed", ChunkSize: row.ChunkSize, ChunkCount: row.ChunkCount, Missing: missing, httpStatus: http.StatusOK}, nil
}

// missing returns the inclusive ranges of unreceived chunks.
func (s *Service) missing(ctx context.Context, id string) ([][2]int64, int64, error) {
	flags, err := s.db.W.ListReceivedFlags(ctx, id)
	if err != nil {
		return nil, 0, err
	}
	out := [][2]int64{}
	var received int64
	start := int64(-1)
	for _, f := range flags {
		if f.Received == 1 {
			received++
			if start >= 0 {
				out = append(out, [2]int64{start, f.Idx - 1})
				start = -1
			}
		} else if start < 0 {
			start = f.Idx
		}
	}
	if start >= 0 {
		out = append(out, [2]int64{start, int64(len(flags)) - 1})
	}
	return out, received, nil
}

func (s *Service) createEmpty(ctx context.Context, p *places.Place, target string, req RegisterRequest) (*RegisterResponse, error) {
	dir, name := places.Split(target)
	if _, err := p.Root.Lstat(places.RootRel(target)); err == nil && req.Conflict == "overwrite" {
		if err := s.trashExisting(ctx, p, target); err != nil {
			return nil, err
		}
	}
	f, err := p.Root.OpenFile(places.RootRel(target), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if fsx.IsExist(err) {
			return nil, apierr.ErrAlreadyExists.With("suggestedName", s.suggest(ctx, p, dir, name, false, ""))
		}
		return nil, apierr.FromFS(err)
	}
	_ = f.Close()
	if req.Mtime != nil {
		_ = p.Root.Chtimes(places.RootRel(target), time.Now(), time.UnixMilli(*req.Mtime))
	}
	e, err := p.FullView().StatEntry(target)
	if err != nil {
		return nil, err
	}
	s.hub.Emit("upload.completed", map[string]any{"id": "", "place": p.ID, "dir": dir, "name": name, "path": target})
	s.hub.FS(p.ID, dir)
	return &RegisterResponse{Status: "complete", Entry: &e, Missing: [][2]int64{}, httpStatus: http.StatusOK}, nil
}

// trashExisting moves an item being overwritten to the trash and drops its
// thumbnails.
func (s *Service) trashExisting(ctx context.Context, p *places.Place, rel string) error {
	if err := s.ops.CheckNotUploadAncestor(ctx, p.ID, rel); err != nil {
		return err
	}
	fi, err := p.Root.Lstat(places.RootRel(rel))
	if err != nil {
		return nil
	}
	if _, err := s.ops.MoveToTrash(ctx, p, rel, nil); err != nil {
		return err
	}
	if fi.Mode().IsRegular() && s.thumbs != nil {
		s.thumbs.Delete(p, fsx.IdentityOf(fi))
	}
	return nil
}

// suggest computes the keep-both name in dir. Names on disk and names of
// other upload sessions are taken, except selfID's.
func (s *Service) suggest(ctx context.Context, p *places.Place, dir, name string, isDir bool, selfID string) string {
	rows, _ := s.db.R.ListUploadSessionsInDir(ctx, gen.ListUploadSessionsInDirParams{PlaceID: p.ID, Dir: dir})
	held := map[string]bool{}
	for _, r := range rows {
		if r.ID != selfID {
			held[r.Name] = true
		}
	}
	return places.KeepBothName(name, isDir, func(n string) bool {
		if held[n] {
			return true
		}
		_, err := p.Root.Lstat(places.RootRel(places.Join(dir, n)))
		return err == nil
	})
}

// --- sessions ---

func (s *Service) load(ctx context.Context, id string) (*session, error) {
	s.mu.Lock()
	sess, ok := s.sessions[id]
	s.mu.Unlock()
	if ok {
		return sess, nil
	}
	row, err := s.db.W.GetUploadSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrGone
	}
	if err != nil {
		return nil, err
	}
	chunks, err := s.db.W.ListChunks(ctx, id)
	if err != nil {
		return nil, err
	}
	hashes := make([]byte, 0, len(chunks)*32)
	for _, c := range chunks {
		hashes = append(hashes, c.Hash...)
	}
	sess = &session{row: row, hashes: hashes, busy: map[int]bool{}}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.sessions[id]; ok {
		return cur, nil
	}
	s.sessions[id] = sess
	return sess, nil
}

func (s *Service) forget(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

// Info is the GET /api/uploads/{id} response.
type Info struct {
	ID             string     `json:"id"`
	Place          string     `json:"place"`
	Path           string     `json:"path"`
	Size           int64      `json:"size"`
	ChunkCount     int64      `json:"chunkCount"`
	ReceivedCount  int64      `json:"receivedCount"`
	ReceivedBytes  int64      `json:"receivedBytes"`
	Active         bool       `json:"active"`
	CreatedAt      int64      `json:"createdAt"`
	LastActivityAt int64      `json:"lastActivityAt"`
	Missing        [][2]int64 `json:"missing"`
}

// Get returns a session's state.
func (s *Service) Get(ctx context.Context, id string) (*Info, error) {
	row, err := s.db.R.GetUploadSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrGone
	}
	if err != nil {
		return nil, err
	}
	missing, _, err := s.missing(ctx, id)
	if err != nil {
		return nil, err
	}
	return &Info{
		ID: row.ID, Place: row.PlaceID, Path: row.TargetPath, Size: row.Size, ChunkCount: row.ChunkCount,
		ReceivedCount: row.ReceivedCount, ReceivedBytes: receivedBytes(row), Active: isActive(row),
		CreatedAt: row.CreatedAt, LastActivityAt: row.LastActivityAt, Missing: missing,
	}, nil
}

// Cancel removes a session (user cancel or leftover removal).
func (s *Service) Cancel(ctx context.Context, id string) error {
	row, err := s.db.W.GetUploadSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	reason := "cancelled"
	if !isActive(row) {
		reason = "removed"
	}
	return s.removeByID(ctx, id, reason)
}

// removeByID deletes a session's partial file and rows and emits
// upload.removed.
func (s *Service) removeByID(ctx context.Context, id, reason string) error {
	sess, err := s.load(ctx, id)
	if errors.Is(err, ErrGone) {
		return nil
	}
	if err != nil {
		return err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return s.removeLocked(ctx, sess, reason)
}

func (s *Service) removeLocked(ctx context.Context, sess *session, reason string) error {
	if sess.removed {
		return nil
	}
	row := sess.snapshot()
	s.files.close(row.ID)
	if p, err := s.reg.Get(row.PlaceID); err == nil {
		_ = p.Root.Remove(places.RootRel(places.Join(row.Dir, row.PartialName)))
	}
	if _, err := s.db.W.DeleteUploadSession(context.WithoutCancel(ctx), row.ID); err != nil {
		return err
	}
	sess.removed = true
	s.forget(row.ID)
	s.hub.Emit("upload.removed", map[string]any{"id": row.ID, "place": row.PlaceID, "dir": row.Dir, "reason": reason})
	s.hub.FS(row.PlaceID, row.Dir)
	return nil
}

// --- chunks ---

// ChunkResult is the response of a chunk PUT.
type ChunkResult struct {
	ReceivedCount int64         `json:"receivedCount,omitempty"`
	Status        string        `json:"status,omitempty"`
	Entry         *places.Entry `json:"entry,omitempty"`
	Renamed       bool          `json:"renamed,omitempty"`
}

func (s *Service) acquireBuf(ctx context.Context) (*[]byte, error) {
	if err := s.bufSem.Acquire(ctx); err != nil {
		return nil, err
	}
	return s.bufPool.Get().(*[]byte), nil
}

func (s *Service) releaseBuf(b *[]byte) {
	s.bufPool.Put(b)
	s.bufSem.Release()
}

// PutChunk receives, verifies and durably stores one chunk.
func (s *Service) PutChunk(ctx context.Context, id string, index int64, final bool, body io.Reader, contentLength int64) (*ChunkResult, error) {
	sess, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	row := sess.snapshot()
	if index < 0 || index >= row.ChunkCount {
		return nil, ErrChunkIndex
	}
	expected := chunkLen(row, index)
	if contentLength != expected {
		return nil, ErrChunkLength.With("expected", expected)
	}
	sess.busyMu.Lock()
	if sess.busy[int(index)] {
		sess.busyMu.Unlock()
		return nil, ErrChunkInProgress
	}
	sess.busy[int(index)] = true
	sess.busyMu.Unlock()
	defer func() {
		sess.busyMu.Lock()
		delete(sess.busy, int(index))
		sess.busyMu.Unlock()
	}()

	bp, err := s.acquireBuf(ctx)
	if err != nil {
		return nil, ErrClientGone
	}
	defer s.releaseBuf(bp)
	buf := (*bp)[:expected]
	if _, err := io.ReadFull(body, buf); err != nil {
		return nil, ErrClientGone
	}

	received, err := s.storeChunk(ctx, sess, index, buf)
	if errors.Is(err, errAbortCorrupted) {
		// Third mismatch: remove the partial, the rows and the hashes.
		if err := s.removeByID(ctx, id, "corrupted"); err != nil {
			return nil, err
		}
		return nil, ErrCorrupted
	}
	if err != nil {
		return nil, err
	}
	if !final {
		return &ChunkResult{ReceivedCount: received}, nil
	}
	if received < row.ChunkCount {
		missing, _, err := s.missing(ctx, id)
		if err != nil {
			return nil, err
		}
		return nil, apierr.New(http.StatusConflict, "chunks_missing", "Some chunks have not been received").With("missing", missing)
	}
	return s.finalize(ctx, sess)
}

func (s *Service) storeChunk(ctx context.Context, sess *session, index int64, buf []byte) (int64, error) {
	sess.mu.RLock()
	defer sess.mu.RUnlock()
	if sess.removed {
		return 0, ErrGone
	}
	row := sess.snapshot()
	sum := blake3.Sum256(buf)
	if subtle.ConstantTimeCompare(sum[:], sess.hashes[index*32:index*32+32]) != 1 {
		failures, err := s.db.W.IncrementChunkFailures(ctx, gen.IncrementChunkFailuresParams{UploadID: row.ID, Idx: index})
		if err != nil {
			return 0, err
		}
		if failures < MaxAttempts {
			return 0, apierr.New(http.StatusUnprocessableEntity, "chunk_hash_mismatch", "Chunk failed verification; send it again").
				With("attempt", failures).With("maxAttempts", MaxAttempts)
		}
		return 0, errAbortCorrupted
	}
	p, err := s.reg.Get(row.PlaceID)
	if err != nil {
		return 0, ErrGone
	}
	partialRel := places.RootRel(places.Join(row.Dir, row.PartialName))
	of, err := s.files.acquire(row.ID, func() (*os.File, error) {
		return p.Root.OpenFile(partialRel, os.O_RDWR, 0)
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, ErrGone
		}
		return 0, apierr.FromFS(err)
	}
	err = of.writeDurable(buf, index*row.ChunkSize)
	s.files.release(of)
	if err != nil {
		if fsx.IsNoSpace(err) {
			return 0, apierr.ErrInsufficient
		}
		return 0, err
	}
	if s.BeforeMark != nil {
		s.BeforeMark(row.ID, int(index))
	}
	now := nowMs()
	var received int64
	err = s.db.Tx(ctx, func(q *gen.Queries, _ *sql.Tx) error {
		n, err := q.MarkChunkReceived(ctx, gen.MarkChunkReceivedParams{UploadID: row.ID, Idx: index})
		if err != nil {
			return err
		}
		if n == 1 {
			received, err = q.IncrementReceivedCount(ctx, gen.IncrementReceivedCountParams{LastActivityAt: now, ID: row.ID})
			return err
		}
		if err := q.TouchUploadSession(ctx, gen.TouchUploadSessionParams{LastActivityAt: now, ID: row.ID}); err != nil {
			return err
		}
		r, err := q.GetUploadSession(ctx, row.ID)
		received = r.ReceivedCount
		return err
	})
	if err != nil {
		return 0, err
	}
	sess.rowMu.Lock()
	sess.row.ReceivedCount = received
	sess.row.LastActivityAt = now
	publish := time.Since(sess.lastProgress) >= time.Second
	if publish {
		sess.lastProgress = time.Now()
	}
	sess.rowMu.Unlock()
	if publish {
		r := sess.snapshot()
		s.hub.Emit("upload.progress", map[string]any{
			"id": r.ID, "place": r.PlaceID, "dir": r.Dir, "receivedCount": received,
			"chunkCount": r.ChunkCount, "receivedBytes": receivedBytes(r),
		})
	}
	return received, nil
}

// finalize renames the partial into place once every chunk is received.
func (s *Service) finalize(ctx context.Context, sess *session) (*ChunkResult, error) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.removed {
		return nil, ErrGone
	}
	ctx = context.WithoutCancel(ctx)
	row, err := s.db.W.GetUploadSession(ctx, sess.snapshot().ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrGone
	}
	if err != nil {
		return nil, err
	}
	if row.ReceivedCount < row.ChunkCount {
		missing, _, _ := s.missing(ctx, row.ID)
		return nil, apierr.New(http.StatusConflict, "chunks_missing", "Some chunks have not been received").With("missing", missing)
	}
	p, err := s.reg.Get(row.PlaceID)
	if err != nil {
		return nil, ErrGone
	}
	s.files.close(row.ID)
	partialRel := places.RootRel(places.Join(row.Dir, row.PartialName))
	mtime := time.Now()
	if row.ClientMtime.Valid {
		mtime = time.UnixMilli(row.ClientMtime.Int64)
	}
	if err := p.Root.Chtimes(partialRel, time.Now(), mtime); err != nil {
		return nil, apierr.FromFS(err)
	}
	df, err := fsops.OpenDir(p.Root, row.Dir)
	if err != nil {
		return nil, apierr.FromFS(err)
	}
	defer df.Close()
	finalName := row.Name
	renamed := false
	for attempt := 0; ; attempt++ {
		target := places.Join(row.Dir, finalName)
		if _, err := p.Root.Lstat(places.RootRel(target)); err == nil {
			if row.ConflictPolicy == "overwrite" && !renamed && attempt < 3 {
				if err := s.trashExisting(ctx, p, target); err != nil {
					return nil, err
				}
			} else {
				finalName = s.suggest(ctx, p, row.Dir, row.Name, false, row.ID)
				renamed = true
			}
		}
		err := fsx.RenameNoReplace(df, row.PartialName, df, finalName)
		if err == nil {
			break
		}
		if !fsx.IsExist(err) || attempt > 20 {
			return nil, apierr.FromFS(err)
		}
	}
	if _, err := s.db.W.DeleteUploadSession(ctx, row.ID); err != nil {
		return nil, err
	}
	sess.removed = true
	s.forget(row.ID)
	finalPath := places.Join(row.Dir, finalName)
	s.hub.Emit("upload.completed", map[string]any{"id": row.ID, "place": p.ID, "dir": row.Dir, "name": finalName, "path": finalPath})
	s.hub.FS(p.ID, row.Dir)
	v := p.FullView()
	if s.thumbs != nil {
		s.thumbs.Enqueue(v, finalPath, thumbs.Background)
	}
	e, err := v.StatEntry(finalPath)
	if err != nil {
		return nil, err
	}
	return &ChunkResult{Status: "complete", Entry: &e, Renamed: renamed}, nil
}

// --- leftovers ---

// Leftover is one row of the Leftovers view.
type Leftover struct {
	ID             string `json:"id"`
	Place          string `json:"place"`
	PlaceName      string `json:"placeName"`
	Path           string `json:"path"`
	Size           int64  `json:"size"`
	ChunkCount     int64  `json:"chunkCount"`
	ReceivedCount  int64  `json:"receivedCount"`
	ReceivedBytes  int64  `json:"receivedBytes"`
	CreatedAt      int64  `json:"createdAt"`
	LastActivityAt int64  `json:"lastActivityAt"`
	PlaceMissing   bool   `json:"placeMissing"`
}

// Leftovers lists sessions idle for more than 120 s.
func (s *Service) Leftovers(ctx context.Context) ([]Leftover, error) {
	rows, err := s.db.R.ListLeftovers(ctx, leftoverCutoff())
	if err != nil {
		return nil, err
	}
	out := make([]Leftover, 0, len(rows))
	for _, r := range rows {
		l := Leftover{
			ID: r.ID, Place: r.PlaceID, Path: r.TargetPath, Size: r.Size, ChunkCount: r.ChunkCount,
			ReceivedCount: r.ReceivedCount, ReceivedBytes: receivedBytes(r), CreatedAt: r.CreatedAt, LastActivityAt: r.LastActivityAt,
		}
		if p, err := s.reg.Get(r.PlaceID); err == nil {
			l.PlaceName = p.Name
		} else {
			l.PlaceMissing = true
		}
		out = append(out, l)
	}
	return out, nil
}

// LeftoverCount returns the number of leftovers.
func (s *Service) LeftoverCount(ctx context.Context) (int64, error) {
	return s.db.R.CountLeftovers(ctx, leftoverCutoff())
}

// RemoveLeftovers removes every current leftover and returns the count.
func (s *Service) RemoveLeftovers(ctx context.Context) (int, error) {
	rows, err := s.db.W.ListLeftovers(ctx, leftoverCutoff())
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rows {
		if err := s.removeByID(ctx, r.ID, "removed"); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
