package jobs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sync"
	"time"

	"github.com/charlievieth/fastwalk"

	"filet/internal/apierr"
	"filet/internal/db/gen"
	"filet/internal/fsops"
	"filet/internal/fsx"
	"filet/internal/ids"
	"filet/internal/places"
)

func (r *Runner) validate(ctx context.Context, s *Spec) error {
	switch s.Conflict {
	case "", "overwrite", "rename", "skip":
	default:
		return apierr.Invalid("conflict", "conflict must be overwrite, rename or skip")
	}
	switch s.Type {
	case "copy", "move":
		if s.Src == nil || s.Dst == nil || len(s.Src.Paths) == 0 {
			return apierr.Invalid("src", "src and dst are required")
		}
		sp, err := r.reg.Get(s.Src.Place)
		if err != nil {
			return err
		}
		dp, err := r.reg.Get(s.Dst.Place)
		if err != nil {
			return err
		}
		if s.Type == "move" {
			if err := sp.Writable(); err != nil {
				return err
			}
		}
		if err := dp.Writable(); err != nil {
			return err
		}
		dstDir, err := places.ValidateRelPath(s.Dst.Dir)
		if err != nil {
			return err
		}
		s.Dst.Dir = dstDir
		fi, err := dp.Root.Stat(places.RootRel(dstDir))
		if err != nil {
			return apierr.FromFS(err)
		}
		if !fi.IsDir() {
			return apierr.ErrNotADirectory
		}
		var targets []string
		if s.Type == "move" {
			if targets, err = r.ops.Uploads.Targets(ctx, sp.ID); err != nil {
				return err
			}
		}
		for i, p := range s.Src.Paths {
			p, err := places.ValidateRelPath(p)
			if err != nil {
				return err
			}
			if p == "/" && s.Type == "move" {
				return apierr.ErrInvalidPath
			}
			s.Src.Paths[i] = p
			if _, err := sp.Root.Lstat(places.RootRel(p)); err != nil {
				return apierr.FromFS(err)
			}
			if sameDir(sp, dp) && p != "/" && places.IsWithin(dstDir, p) {
				return apierr.ErrInvalidDest
			}
			if sameDir(sp, dp) && p == "/" {
				return apierr.ErrInvalidDest
			}
			if n := fsops.UploadsBelow(targets, p); s.Type == "move" && n > 0 {
				return apierr.ErrUploadInProgress.With("uploads", n)
			}
		}
	case "trash":
		p, err := r.reg.Get(s.Place)
		if err != nil {
			return err
		}
		if err := p.Writable(); err != nil {
			return err
		}
		if !p.HasTrash {
			return apierr.ErrTrashUnavailable
		}
		if len(s.Paths) == 0 {
			return apierr.Invalid("paths", "paths are required")
		}
		targets, err := r.ops.Uploads.Targets(ctx, p.ID)
		if err != nil {
			return err
		}
		for i, rel := range s.Paths {
			rel, err := places.ValidateRelPath(rel)
			if err != nil {
				return err
			}
			if rel == "/" {
				return apierr.ErrInvalidPath
			}
			s.Paths[i] = rel
			if _, err := p.Root.Lstat(places.RootRel(rel)); err != nil {
				return apierr.FromFS(err)
			}
			if n := fsops.UploadsBelow(targets, rel); n > 0 {
				return apierr.ErrUploadInProgress.With("uploads", n)
			}
		}
	case "restore", "purge":
		p, err := r.reg.Get(s.Place)
		if err != nil {
			return err
		}
		if err := p.Writable(); err != nil {
			return err
		}
		if !p.HasTrash {
			return apierr.ErrTrashUnavailable
		}
		if s.Type == "purge" && s.All {
			return nil
		}
		if len(s.IDs) == 0 {
			return apierr.Invalid("ids", "ids are required")
		}
		for _, id := range s.IDs {
			row, err := r.db.R.GetTrashItem(ctx, id)
			if err != nil || row.PlaceID != p.ID {
				return apierr.ErrNotFound
			}
		}
	default:
		return apierr.Invalid("type", "unknown job type")
	}
	return nil
}

// sameDir reports whether two places are the same configured place.
func sameDir(a, b *places.Place) bool { return a.ID == b.ID }

// run is the execution state of one job.
type run struct {
	r      *Runner
	jb     *job
	ctx    context.Context
	cp     *fsops.Copier
	dirsMu sync.Mutex
	dirs   map[[2]string]bool
}

func (r *Runner) execute(ctx context.Context, jb *job) {
	defer func() { //nolint:contextcheck // the next job gets its own context
		r.mu.Lock()
		r.running--
		r.mu.Unlock()
		r.wg.Done()
		r.schedule()
	}()
	now := time.Now().UnixMilli()
	jb.mu.Lock()
	jb.j.Status = StatusRunning
	jb.j.StartedAt = &now
	jb.mu.Unlock()
	r.persist(jb)
	r.publish(jb, true)

	x := &run{r: r, jb: jb, ctx: ctx, dirs: map[[2]string]bool{}}
	x.cp = &fsops.Copier{
		JobID: jb.j.ID,
		Progress: func(n int64) {
			jb.mu.Lock()
			jb.j.BytesDone += n
			jb.mu.Unlock()
			r.progress(jb)
		},
		SetTemp: func(placeID, rel string) {
			jb.mu.Lock()
			jb.tempPlace, jb.tempPath = placeID, rel
			jb.mu.Unlock()
			r.persist(jb)
		},
	}
	stopFlush := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-stopFlush:
				return
			case <-t.C:
				x.flushDirs()
			}
		}
	}()

	var err error
	func() {
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("panic: %v", p)
				r.log.Error("job panicked", "id", jb.j.ID, "panic", p)
			}
		}()
		switch jb.spec.Type {
		case "copy", "move":
			err = x.copyMove()
		case "trash":
			err = x.trash()
		case "restore":
			err = x.restore()
		case "purge":
			err = x.purge()
		}
	}()
	close(stopFlush)
	x.flushDirs()

	fin := time.Now().UnixMilli()
	jb.mu.Lock()
	jb.j.FinishedAt = &fin
	jb.j.CurrentItem = nil
	jb.j.PendingConflict = nil
	cause := context.Cause(ctx)
	switch {
	case err == nil:
		jb.j.Status = StatusCompleted
	case errors.Is(cause, errShutdown):
		jb.j.Status = StatusInterrupted
		jb.j.Error = &JobError{Code: codeRestarted, Message: msgInterrupted}
	case ctx.Err() != nil:
		jb.j.Status = StatusCancelled
	default:
		jb.j.Status = StatusFailed
		if ae, ok := apierr.As(err); ok {
			jb.j.Error = &JobError{Code: ae.Code, Message: ae.Message}
		} else if fsx.IsNoSpace(err) {
			jb.j.Error = &JobError{Code: "insufficient_storage", Message: "Not enough free space"}
		} else {
			r.log.Error("job failed", "id", jb.j.ID, "err", err)
			jb.j.Error = &JobError{Code: "internal", Message: "The operation failed"}
		}
	}
	tempPlace, tempPath := jb.tempPlace, jb.tempPath
	jb.tempPlace, jb.tempPath = "", ""
	jb.cancel(nil)
	jb.mu.Unlock()
	if tempPath != "" {
		if p, err := r.reg.Get(tempPlace); err == nil {
			_ = p.Root.Remove(places.RootRel(tempPath))
		}
	}
	r.persist(jb)
	r.publish(jb, true)
	r.forget(jb.j.ID)
}

func (x *run) changed(placeID, dir string) {
	x.dirsMu.Lock()
	x.dirs[[2]string{placeID, dir}] = true
	x.dirsMu.Unlock()
}

func (x *run) flushDirs() {
	x.dirsMu.Lock()
	dirs := x.dirs
	x.dirs = map[[2]string]bool{}
	x.dirsMu.Unlock()
	for d := range dirs {
		x.r.hub.FS(d[0], d[1])
	}
}

func (x *run) setCurrent(p string) {
	x.jb.mu.Lock()
	x.jb.j.CurrentItem = &p
	x.jb.mu.Unlock()
	x.r.progress(x.jb)
}

func (x *run) itemDone() {
	x.jb.mu.Lock()
	x.jb.j.ItemsDone++
	x.jb.mu.Unlock()
	x.r.progress(x.jb)
}

func (x *run) itemErr(path, code string) {
	x.jb.mu.Lock()
	if len(x.jb.j.Result.Errors) < maxErrors {
		x.jb.j.Result.Errors = append(x.jb.j.Result.Errors, ItemError{Path: path, Code: code})
	}
	x.jb.mu.Unlock()
}

func (x *run) skipped() {
	x.jb.mu.Lock()
	x.jb.j.Result.Skipped++
	x.jb.mu.Unlock()
}

func (x *run) renamed(from, to string) {
	x.jb.mu.Lock()
	x.jb.j.Result.Renamed = append(x.jb.j.Result.Renamed, Rename{From: from, To: to})
	x.jb.mu.Unlock()
}

func errCode(err error) string {
	if ae, ok := apierr.As(apierr.FromFS(err)); ok {
		return ae.Code
	}
	return "error"
}

// fatal reports whether err must fail the whole job.
func fatal(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || fsx.IsNoSpace(err) {
		return true
	}
	return apierr.Is(err, "insufficient_storage") || apierr.Is(err, "trash_unavailable")
}

// --- conflicts ---

// ask resolves a conflict using conflict_default or by prompting.
func (x *run) ask(c Conflict) (string, error) {
	allowed := func(a string) bool { return a != "overwrite" || (!c.SameItem && !c.UploadInProgress) }
	x.jb.mu.Lock()
	def := x.jb.conflictDefault
	x.jb.mu.Unlock()
	if def != "" && allowed(def) {
		return def, nil
	}
	c.ID = ids.New()
	x.jb.mu.Lock()
	x.jb.j.Status = StatusAwaiting
	x.jb.j.PendingConflict = &c
	x.jb.mu.Unlock()
	x.r.persist(x.jb)
	x.r.publish(x.jb, true)
	select {
	case res := <-x.jb.resolveCh:
		x.jb.mu.Lock()
		if res.applyToAll {
			x.jb.conflictDefault = res.action
		}
		x.jb.mu.Unlock()
		x.r.persist(x.jb)
		return res.action, nil
	case <-x.ctx.Done():
		return "", x.ctx.Err()
	}
}

// destination describes where an item goes and whether that name is taken.
type destination struct {
	p    *places.Place
	dir  string
	name string
}

func (d destination) rel() string { return places.Join(d.dir, d.name) }

func (x *run) taken(d destination) (fs.FileInfo, bool, error) {
	targets, err := x.r.ops.Uploads.Targets(x.ctx, d.p.ID)
	if err != nil {
		return nil, false, err
	}
	held := fsops.Held(targets, d.rel())
	fi, err := d.p.Root.Lstat(places.RootRel(d.rel()))
	if err != nil {
		fi = nil
	}
	return fi, held, nil
}

func (x *run) suggest(d destination, isDir bool) string {
	targets, _ := x.r.ops.Uploads.Targets(x.ctx, d.p.ID)
	return places.KeepBothName(d.name, isDir, func(n string) bool {
		if fsops.Held(targets, places.Join(d.dir, n)) {
			return true
		}
		_, err := d.p.Root.Lstat(places.RootRel(places.Join(d.dir, n)))
		return err == nil
	})
}

// resolveName handles a taken destination. It returns the name to use (or
// "" to skip) and whether the existing item must be trashed before commit.
func (x *run) resolveName(srcV *places.View, srcRel string, srcFi fs.FileInfo, d destination, existing fs.FileInfo, held, sameItem bool) (string, bool, error) {
	srcEntry := srcV.BuildEntry(srcRel, d.name, srcFi, nil)
	var dstEntry places.Entry
	if existing != nil {
		dstEntry = d.p.FullView().BuildEntry(d.rel(), d.name, existing, nil)
	} else {
		dstEntry = places.Entry{Name: d.name, Type: "file", Kind: places.KindOf(d.name)}
	}
	c := Conflict{
		Src: Side{Path: srcRel, Entry: srcEntry}, Dst: Side{Path: d.rel(), Entry: dstEntry},
		SameItem: sameItem, UploadInProgress: held, SuggestedName: x.suggest(d, srcFi.IsDir()),
	}
	action, err := x.ask(c)
	if err != nil {
		return "", false, err
	}
	switch action {
	case "overwrite":
		if !d.p.HasTrash {
			return "", false, apierr.ErrTrashUnavailable
		}
		return d.name, true, nil
	case "rename":
		name := x.suggest(d, srcFi.IsDir())
		x.renamed(d.rel(), places.Join(d.dir, name))
		return name, false, nil
	default:
		x.skipped()
		return "", false, nil
	}
}

// trashExisting moves a destination item being overwritten to the trash.
func (x *run) trashExisting(d destination) error {
	if err := x.r.ops.CheckNotUploadAncestor(x.ctx, d.p.ID, d.rel()); err != nil {
		return err
	}
	fi, err := d.p.Root.Lstat(places.RootRel(d.rel()))
	if err != nil {
		return nil // already gone
	}
	if _, err := x.r.ops.MoveToTrash(x.ctx, d.p, d.rel(), x.cp); err != nil {
		return err
	}
	if fi.Mode().IsRegular() && x.r.thumbs != nil {
		x.r.thumbs.Delete(d.p, fsx.IdentityOf(fi))
	}
	return nil
}

// --- copy & move ---

func (x *run) copyMove() error {
	spec := x.jb.spec
	sp, err := x.r.reg.Get(spec.Src.Place)
	if err != nil {
		return err
	}
	dp, err := x.r.reg.Get(spec.Dst.Place)
	if err != nil {
		return err
	}
	move := spec.Type == "move"
	dstFi, err := dp.Root.Stat(places.RootRel(spec.Dst.Dir))
	if err != nil {
		return apierr.FromFS(err)
	}
	// Plan.
	var items, bytes int64
	needsCopy := !move
	for _, p := range spec.Src.Paths {
		lfi, err := sp.Root.Lstat(places.RootRel(p))
		if err != nil {
			continue
		}
		if move && fsx.Dev(lfi) == fsx.Dev(dstFi) {
			items++
			continue
		}
		needsCopy = true
		n, b := x.planTree(sp, p, lfi)
		items += n
		bytes += b
	}
	x.jb.mu.Lock()
	x.jb.j.ItemsTotal, x.jb.j.BytesTotal = items, bytes
	x.jb.mu.Unlock()
	x.r.persist(x.jb)
	x.r.publish(x.jb, true)
	if needsCopy && bytes > 0 {
		if du, err := fsx.Statfs(filepath.Join(dp.Path, filepath.FromSlash(places.RootRel(spec.Dst.Dir)))); err == nil && uint64(bytes) > du.Free {
			return apierr.ErrInsufficient.With("required", bytes).With("available", du.Free)
		}
	}
	sv := sp.FullView()
	for _, p := range spec.Src.Paths {
		if err := x.ctx.Err(); err != nil {
			return err
		}
		_, name := places.Split(p)
		if p == "/" {
			name = sp.Name
		}
		d := destination{p: dp, dir: spec.Dst.Dir, name: name}
		if move {
			err = x.moveItem(sv, p, d)
		} else {
			err = x.copyItem(sv, p, d)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// planTree counts items and bytes below rel (no symlink following).
func (x *run) planTree(p *places.Place, rel string, lfi fs.FileInfo) (int64, int64) {
	if !lfi.IsDir() {
		if lfi.Mode()&fs.ModeSymlink != 0 {
			if fi, err := p.Root.Stat(places.RootRel(rel)); err == nil && fi.Mode().IsRegular() {
				return 1, fi.Size()
			}
		}
		return 1, lfi.Size()
	}
	realPath, err := p.FullView().RealPath(rel)
	if err != nil {
		return 1, 0
	}
	var mu sync.Mutex
	var items, bytes int64 = 1, 0
	_ = fastwalk.Walk(&fastwalk.Config{Follow: false}, realPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == realPath {
			return nil
		}
		if places.IsReserved(d.Name()) {
			if d.IsDir() {
				return fastwalk.SkipDir
			}
			return nil
		}
		var size int64
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				size = fi.Size()
			}
		}
		mu.Lock()
		items++
		bytes += size
		mu.Unlock()
		return nil
	})
	return items, bytes
}

// sourceInfo resolves symlinks per the confinement rules. It returns nil
// (and records an error) when the item must be skipped.
func (x *run) sourceInfo(sv *places.View, rel string, followFiles bool) (lfi, fi fs.FileInfo) {
	lfi, err := sv.Lstat(rel)
	if err != nil {
		x.itemErr(rel, errCode(err))
		return nil, nil
	}
	if lfi.Mode()&fs.ModeSymlink == 0 {
		return lfi, lfi
	}
	tfi, err := sv.Stat(rel)
	if err != nil || tfi.IsDir() || !tfi.Mode().IsRegular() {
		if followFiles || err != nil {
			x.itemErr(rel, "symlink_skipped")
			return nil, nil
		}
		return lfi, lfi
	}
	if followFiles {
		return lfi, tfi
	}
	return lfi, lfi
}

func (x *run) copyItem(sv *places.View, srcRel string, d destination) error {
	if err := x.ctx.Err(); err != nil {
		return err
	}
	_, fi := x.sourceInfo(sv, srcRel, true)
	if fi == nil {
		x.itemDone()
		return nil
	}
	x.setCurrent(srcRel)
	sameItem := sv.Place.ID == d.p.ID && srcRel == d.rel()
	if fi.IsDir() {
		return x.copyDir(sv, srcRel, fi, d, sameItem)
	}
	if !fi.Mode().IsRegular() {
		x.itemErr(srcRel, "unsupported_type")
		x.itemDone()
		return nil
	}
	f, err := sv.Open(srcRel)
	if err != nil {
		x.itemErr(srcRel, errCode(err))
		x.itemDone()
		return nil
	}
	defer f.Close()
	var t *fsops.Temp
	for {
		existing, held, err := x.taken(d)
		if err != nil {
			return err
		}
		overwrite := false
		if existing != nil || held {
			name, ow, err := x.resolveName(sv, srcRel, fi, d, existing, held, sameItem)
			if err != nil {
				if t != nil {
					x.cp.Discard(t)
				}
				return err
			}
			if name == "" {
				if t != nil {
					x.cp.Discard(t)
				}
				x.itemDone()
				return nil
			}
			d.name, overwrite = name, ow
			sameItem = false
		}
		if t == nil {
			t, err = x.cp.CopyToTemp(x.ctx, f, fi, d.p, d.dir)
			if err != nil {
				if fatal(err) {
					return err
				}
				x.itemErr(srcRel, errCode(err))
				x.itemDone()
				return nil
			}
		}
		if overwrite {
			if err := x.trashExisting(d); err != nil {
				x.cp.Discard(t)
				return err
			}
		}
		if err := x.cp.Commit(t, d.name); err != nil {
			if fsops.ErrExists(err) {
				continue // raced: resolve again
			}
			x.cp.Discard(t)
			x.itemErr(srcRel, errCode(err))
			x.itemDone()
			return nil
		}
		break
	}
	x.changed(d.p.ID, d.dir)
	x.itemDone()
	return nil
}

// prepareDir makes d an existing directory to merge into. It returns false
// when the item was skipped.
func (x *run) prepareDir(sv *places.View, srcRel string, fi fs.FileInfo, d *destination, sameItem bool) (created bool, ok bool, err error) {
	for {
		existing, held, err := x.taken(*d)
		if err != nil {
			return false, false, err
		}
		if existing != nil && existing.IsDir() && !held && !sameItem {
			return false, true, nil // merge (A4)
		}
		if existing != nil || held {
			name, ow, err := x.resolveName(sv, srcRel, fi, *d, existing, held, sameItem)
			if err != nil {
				return false, false, err
			}
			if name == "" {
				return false, false, nil
			}
			if ow {
				if err := x.trashExisting(*d); err != nil {
					return false, false, err
				}
			}
			d.name = name
			sameItem = false
		}
		if err := d.p.Root.Mkdir(places.RootRel(d.rel()), fi.Mode().Perm()|0o700); err != nil {
			if fsx.IsExist(err) {
				continue
			}
			return false, false, apierr.FromFS(err)
		}
		x.changed(d.p.ID, d.dir)
		return true, true, nil
	}
}

func (x *run) copyDir(sv *places.View, srcRel string, fi fs.FileInfo, d destination, sameItem bool) error {
	created, ok, err := x.prepareDir(sv, srcRel, fi, &d, sameItem)
	if err != nil || !ok {
		x.itemDone()
		return err
	}
	names, err := fsops.ReadNames(sv.Root, srcRel)
	if err != nil {
		x.itemErr(srcRel, errCode(err))
	}
	dstRel := d.rel()
	x.itemDone()
	for _, n := range names {
		if err := x.copyItem(sv, places.Join(srcRel, n), destination{p: d.p, dir: dstRel, name: n}); err != nil {
			return err
		}
	}
	if created {
		_ = d.p.Root.Chmod(places.RootRel(dstRel), fi.Mode().Perm())
		_ = d.p.Root.Chtimes(places.RootRel(dstRel), time.Now(), fi.ModTime())
	}
	return nil
}

func (x *run) moveItem(sv *places.View, srcRel string, d destination) error {
	if err := x.ctx.Err(); err != nil {
		return err
	}
	// A same-filesystem move renames symlinks themselves, broken or not.
	lfi, err := sv.Lstat(srcRel)
	if err != nil {
		x.itemErr(srcRel, errCode(err))
		x.itemDone()
		return nil
	}
	x.setCurrent(srcRel)
	srcDir, srcName := places.Split(srcRel)
	sameItem := sv.Place.ID == d.p.ID && srcRel == d.rel()
	for {
		existing, held, err := x.taken(d)
		if err != nil {
			return err
		}
		if existing != nil && existing.IsDir() && lfi.IsDir() && !held && !sameItem {
			return x.mergeDir(sv, srcRel, d)
		}
		overwrite := false
		if existing != nil || held {
			name, ow, err := x.resolveName(sv, srcRel, lfi, d, existing, held, sameItem)
			if err != nil {
				return err
			}
			if name == "" {
				x.itemDone()
				return nil
			}
			d.name, overwrite = name, ow
			sameItem = false
		}
		if overwrite {
			if err := x.trashExisting(d); err != nil {
				return err
			}
		}
		err = x.rename(sv.Place, srcDir, srcName, d)
		if err == nil {
			x.changed(sv.Place.ID, srcDir)
			x.changed(d.p.ID, d.dir)
			x.itemDone()
			return nil
		}
		if fsops.ErrExists(err) {
			continue
		}
		if fsx.IsCrossDevice(err) {
			return x.moveAcross(sv, srcRel, lfi, d)
		}
		x.itemErr(srcRel, errCode(err))
		x.itemDone()
		return nil
	}
}

func (x *run) rename(sp *places.Place, srcDir, srcName string, d destination) error {
	sf, err := fsops.OpenDir(sp.Root, srcDir)
	if err != nil {
		return err
	}
	defer sf.Close()
	df, err := fsops.OpenDir(d.p.Root, d.dir)
	if err != nil {
		return err
	}
	defer df.Close()
	return fsx.RenameNoReplace(sf, srcName, df, d.name)
}

// mergeDir moves the children of srcRel into the existing directory d.
func (x *run) mergeDir(sv *places.View, srcRel string, d destination) error {
	names, err := fsops.ReadNames(sv.Root, srcRel)
	if err != nil {
		x.itemErr(srcRel, errCode(err))
		x.itemDone()
		return nil
	}
	dstRel := d.rel()
	for _, n := range names {
		if err := x.moveItem(sv, places.Join(srcRel, n), destination{p: d.p, dir: dstRel, name: n}); err != nil {
			return err
		}
	}
	if err := sv.Root.Remove(places.RootRel(srcRel)); err == nil {
		dir, _ := places.Split(srcRel)
		x.changed(sv.Place.ID, dir)
	}
	x.itemDone()
	return nil
}

// moveAcross copies an item to another filesystem, then removes the source.
func (x *run) moveAcross(sv *places.View, srcRel string, lfi fs.FileInfo, d destination) error {
	srcDir, _ := places.Split(srcRel)
	switch {
	case lfi.Mode()&fs.ModeSymlink != 0:
		tfi, err := sv.Stat(srcRel)
		if err != nil || !tfi.Mode().IsRegular() {
			x.itemErr(srcRel, "symlink_skipped")
			x.itemDone()
			return nil
		}
		if err := x.copyFileTo(sv, srcRel, tfi, d); err != nil {
			return err
		}
		_ = sv.Root.Remove(places.RootRel(srcRel))
	case lfi.IsDir():
		if err := d.p.Root.Mkdir(places.RootRel(d.rel()), lfi.Mode().Perm()|0o700); err != nil && !fsx.IsExist(err) {
			x.itemErr(srcRel, errCode(err))
			x.itemDone()
			return nil
		}
		x.changed(d.p.ID, d.dir)
		names, err := fsops.ReadNames(sv.Root, srcRel)
		if err != nil {
			x.itemErr(srcRel, errCode(err))
		}
		x.itemDone()
		for _, n := range names {
			if err := x.moveItem(sv, places.Join(srcRel, n), destination{p: d.p, dir: d.rel(), name: n}); err != nil {
				return err
			}
		}
		_ = d.p.Root.Chmod(places.RootRel(d.rel()), lfi.Mode().Perm())
		_ = d.p.Root.Chtimes(places.RootRel(d.rel()), time.Now(), lfi.ModTime())
		_ = sv.Root.Remove(places.RootRel(srcRel)) // only succeeds when empty
		x.changed(sv.Place.ID, srcDir)
		return nil
	case lfi.Mode().IsRegular():
		if err := x.copyFileTo(sv, srcRel, lfi, d); err != nil {
			return err
		}
		if err := sv.Root.Remove(places.RootRel(srcRel)); err != nil {
			x.itemErr(srcRel, errCode(err))
		}
	default:
		x.itemErr(srcRel, "unsupported_type")
	}
	x.changed(sv.Place.ID, srcDir)
	x.changed(d.p.ID, d.dir)
	x.itemDone()
	return nil
}

// copyFileTo copies a regular file to d (whose name was already resolved).
func (x *run) copyFileTo(sv *places.View, srcRel string, fi fs.FileInfo, d destination) error {
	f, err := sv.Open(srcRel)
	if err != nil {
		x.itemErr(srcRel, errCode(err))
		return nil
	}
	defer f.Close()
	t, err := x.cp.CopyToTemp(x.ctx, f, fi, d.p, d.dir)
	if err != nil {
		if fatal(err) {
			return err
		}
		x.itemErr(srcRel, errCode(err))
		return nil
	}
	if err := x.cp.Commit(t, d.name); err != nil {
		x.cp.Discard(t)
		x.itemErr(srcRel, errCode(err))
	}
	return nil
}

// --- trash, restore, purge ---

func (x *run) trash() error {
	p, err := x.r.reg.Get(x.jb.spec.Place)
	if err != nil {
		return err
	}
	x.jb.mu.Lock()
	x.jb.j.ItemsTotal = int64(len(x.jb.spec.Paths))
	x.jb.mu.Unlock()
	for _, rel := range x.jb.spec.Paths {
		if err := x.ctx.Err(); err != nil {
			return err
		}
		x.setCurrent(rel)
		if err := x.r.ops.CheckNotUploadAncestor(x.ctx, p.ID, rel); err != nil {
			x.itemErr(rel, "upload_in_progress")
			x.itemDone()
			continue
		}
		if _, err := x.r.ops.MoveToTrash(x.ctx, p, rel, x.cp); err != nil {
			if fatal(err) {
				return err
			}
			x.itemErr(rel, errCode(err))
		}
		dir, _ := places.Split(rel)
		x.changed(p.ID, dir)
		x.itemDone()
	}
	return nil
}

func (x *run) restore() error {
	p, err := x.r.reg.Get(x.jb.spec.Place)
	if err != nil {
		return err
	}
	x.jb.mu.Lock()
	x.jb.j.ItemsTotal = int64(len(x.jb.spec.IDs))
	x.jb.mu.Unlock()
	trashView := &places.View{Place: p, Root: p.Root, Base: "/"}
	for _, id := range x.jb.spec.IDs {
		if err := x.ctx.Err(); err != nil {
			return err
		}
		row, err := x.r.db.W.GetTrashItem(x.ctx, id)
		if err != nil {
			x.itemErr(id, "not_found")
			x.itemDone()
			continue
		}
		x.setCurrent(row.OriginalPath)
		if err := x.restoreOne(p, trashView, row); err != nil {
			if fatal(err) {
				return err
			}
			x.itemErr(row.OriginalPath, errCode(err))
		}
		x.itemDone()
	}
	return nil
}

func (x *run) restoreOne(p *places.Place, tv *places.View, row gen.TrashItem) error {
	itemRel := "/" + fsops.TrashItemPath(row)
	lfi, err := p.Root.Lstat(places.RootRel(itemRel))
	if err != nil {
		_ = x.r.db.W.DeleteTrashItem(x.ctx, row.ID)
		_ = fsops.RemoveTrashDir(p, row.ID)
		return apierr.ErrNotFound
	}
	dir, name := places.Split(row.OriginalPath)
	if err := p.Root.MkdirAll(places.RootRel(dir), 0o755); err != nil {
		return apierr.FromFS(err)
	}
	d := destination{p: p, dir: dir, name: name}
	trashDir := "/" + fsops.TrashDirRel(row.ID)
	for {
		existing, held, err := x.taken(d)
		if err != nil {
			return err
		}
		overwrite := false
		if existing != nil || held {
			n, ow, err := x.resolveName(tv, itemRel, lfi, d, existing, held, false)
			if err != nil {
				return err
			}
			if n == "" {
				return nil
			}
			d.name, overwrite = n, ow
		}
		if overwrite {
			if err := x.trashExisting(d); err != nil {
				return err
			}
		}
		err = x.rename(p, trashDir, row.Name, d)
		if err == nil {
			break
		}
		if fsops.ErrExists(err) {
			continue
		}
		if !fsx.IsCrossDevice(err) {
			return err
		}
		if err := x.cp.CopyTree(x.ctx, tv, itemRel, p, d.dir, d.name); err != nil {
			return err
		}
		break
	}
	if err := x.r.db.W.DeleteTrashItem(x.ctx, row.ID); err != nil {
		return err
	}
	_ = fsops.RemoveTrashDir(p, row.ID)
	x.changed(p.ID, d.dir)
	return nil
}

func (x *run) purge() error {
	p, err := x.r.reg.Get(x.jb.spec.Place)
	if err != nil {
		return err
	}
	idsToPurge := x.jb.spec.IDs
	if x.jb.spec.All {
		rows, err := x.r.db.W.ListTrashItems(x.ctx, p.ID)
		if err != nil {
			return err
		}
		idsToPurge = nil
		for _, r := range rows {
			idsToPurge = append(idsToPurge, r.ID)
		}
	}
	x.jb.mu.Lock()
	x.jb.j.ItemsTotal = int64(len(idsToPurge))
	x.jb.mu.Unlock()
	for _, id := range idsToPurge {
		if err := x.ctx.Err(); err != nil {
			return err
		}
		row, err := x.r.db.W.GetTrashItem(x.ctx, id)
		if err != nil {
			x.itemDone()
			continue
		}
		x.setCurrent(row.OriginalPath)
		x.dropThumbs(p, fsops.TrashDirRel(id))
		if err := fsops.RemoveTrashDir(p, id); err != nil {
			x.itemErr(row.OriginalPath, errCode(err))
			x.itemDone()
			continue
		}
		_ = x.r.db.W.DeleteTrashItem(x.ctx, id)
		x.itemDone()
	}
	return nil
}

// dropThumbs deletes the thumbnail cache entries of every regular file
// below a root-relative directory (best effort).
func (x *run) dropThumbs(p *places.Place, rootRel string) {
	if x.r.thumbs == nil {
		return
	}
	var walk func(rel string)
	walk = func(rel string) {
		f, err := p.Root.Open(rel)
		if err != nil {
			return
		}
		entries, err := f.ReadDir(-1)
		_ = f.Close()
		if err != nil {
			return
		}
		for _, e := range entries {
			child := rel + "/" + e.Name()
			switch {
			case e.IsDir():
				walk(child)
			case e.Type().IsRegular():
				if fi, err := e.Info(); err == nil {
					x.r.thumbs.Delete(p, fsx.IdentityOf(fi))
				}
			}
		}
	}
	walk(rootRel)
}
