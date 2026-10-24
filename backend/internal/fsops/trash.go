package fsops

import (
	"context"
	"database/sql"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/charlievieth/fastwalk"

	"filet/internal/apierr"
	"filet/internal/db/gen"
	"filet/internal/fsx"
	"filet/internal/ids"
	"filet/internal/places"
)

// TrashDirRel returns the root-relative directory of a trash item.
func TrashDirRel(id string) string { return places.TrashRel + "/" + id }

// MoveToTrash moves rel into the place's trash and records it. On EXDEV
// (rel sits on another filesystem mounted inside the place) it copies with
// cp and removes the source. Callers check reserved names and upload
// ancestors first.
func (o *Ops) MoveToTrash(ctx context.Context, p *places.Place, rel string, cp *Copier) (gen.TrashItem, error) {
	if !p.HasTrash {
		return gen.TrashItem{}, apierr.ErrTrashUnavailable
	}
	if rel == "/" {
		return gen.TrashItem{}, apierr.ErrInvalidPath
	}
	lfi, err := p.Root.Lstat(places.RootRel(rel))
	if err != nil {
		return gen.TrashItem{}, apierr.FromFS(err)
	}
	id := ids.New()
	if err := p.Root.Mkdir(TrashDirRel(id), 0o700); err != nil {
		return gen.TrashItem{}, apierr.ErrTrashUnavailable
	}
	dir, name := places.Split(rel)
	if err := o.renameInto(p, dir, name, TrashDirRel(id)); err != nil {
		if !fsx.IsCrossDevice(err) {
			_ = p.Root.Remove(TrashDirRel(id))
			return gen.TrashItem{}, apierr.FromFS(err)
		}
		if cp == nil {
			cp = &Copier{JobID: id}
		}
		if err := cp.CopyTree(ctx, p.FullView(), rel, p, "/"+TrashDirRel(id), name); err != nil {
			_ = p.Root.RemoveAll(TrashDirRel(id))
			return gen.TrashItem{}, err
		}
		if err := p.Root.RemoveAll(places.RootRel(rel)); err != nil {
			return gen.TrashItem{}, apierr.FromFS(err)
		}
	}
	isDir := lfi.IsDir()
	row := gen.TrashItem{ID: id, PlaceID: p.ID, OriginalPath: rel, Name: name, DeletedAt: time.Now().UnixMilli()}
	if isDir {
		row.IsDir = 1
	} else {
		row.Size = sql.NullInt64{Int64: lfi.Size(), Valid: true}
	}
	if err := o.DB.W.InsertTrashItem(ctx, gen.InsertTrashItemParams{
		ID: row.ID, PlaceID: row.PlaceID, OriginalPath: row.OriginalPath, Name: row.Name, IsDir: row.IsDir,
		Size: row.Size, DeletedAt: row.DeletedAt,
	}); err != nil {
		return gen.TrashItem{}, err
	}
	if isDir {
		// The size is computed after the request returns, on purpose.
		go o.ComputeTrashSize(context.WithoutCancel(ctx), p, id)
	}
	return row, nil
}

// renameInto renames dir/name into the root-relative directory dstDirRel
// keeping its name.
func (o *Ops) renameInto(p *places.Place, dir, name, dstDirRel string) error {
	src, err := OpenDir(p.Root, dir)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := p.Root.Open(dstDirRel)
	if err != nil {
		return err
	}
	defer dst.Close()
	return fsx.RenameNoReplace(src, name, dst, name)
}

// ComputeTrashSize sums the regular-file sizes below a trash item and
// stores the result. It runs in a low-priority goroutine.
func (o *Ops) ComputeTrashSize(ctx context.Context, p *places.Place, id string) {
	size := DirSize(filepath.Join(p.Path, filepath.FromSlash(TrashDirRel(id))))
	_ = o.DB.W.UpdateTrashSize(ctx, gen.UpdateTrashSizeParams{Size: sql.NullInt64{Int64: size, Valid: true}, ID: id})
}

// DirSize returns the total size of regular files below root (no symlink
// following, reserved names skipped).
func DirSize(root string) int64 {
	var total atomic.Int64
	_ = fastwalk.Walk(&fastwalk.Config{Follow: false}, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if places.IsReserved(d.Name()) && path != root {
			if d.IsDir() {
				return fastwalk.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				total.Add(fi.Size())
			}
		}
		return nil
	})
	return total.Load()
}

// TrashItemPath returns the root-relative path of a trashed item's content.
func TrashItemPath(row gen.TrashItem) string { return TrashDirRel(row.ID) + "/" + row.Name }

// RemoveTrashDir removes the (now empty or purged) trash directory.
func RemoveTrashDir(p *places.Place, id string) error {
	err := p.Root.RemoveAll(TrashDirRel(id))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ReconcileTrash repairs trash rows and directories at startup.
func (o *Ops) ReconcileTrash(ctx context.Context, log *slog.Logger) error {
	for _, p := range o.Reg.All() {
		if p.ReadOnly || !p.HasTrash {
			continue
		}
		rows, err := o.DB.W.ListTrashItems(ctx, p.ID)
		if err != nil {
			return err
		}
		byID := map[string]bool{}
		for _, r := range rows {
			byID[r.ID] = true
		}
		dirs, err := readNames(p.Root, "/"+places.TrashRel)
		if err != nil {
			continue
		}
		onDisk := map[string]bool{}
		var added, removed, dropped int
		for _, id := range dirs {
			onDisk[id] = true
			if byID[id] {
				continue
			}
			children, err := readNames(p.Root, "/"+TrashDirRel(id))
			if err != nil {
				continue
			}
			switch {
			case len(children) == 0:
				_ = p.Root.Remove(TrashDirRel(id))
				removed++
				continue
			case len(children) > 1:
				log.Warn("trash directory has several entries; using the first", "place", p.Name, "id", id)
			}
			child := children[0]
			lfi, err := p.Root.Lstat(TrashDirRel(id) + "/" + child)
			if err != nil {
				continue
			}
			row := gen.InsertTrashItemParams{
				ID: id, PlaceID: p.ID, OriginalPath: "/" + child, Name: child, DeletedAt: lfi.ModTime().UnixMilli(), OriginUnknown: 1,
			}
			if lfi.IsDir() {
				row.IsDir = 1
			} else {
				row.Size = sql.NullInt64{Int64: lfi.Size(), Valid: true}
			}
			if err := o.DB.W.InsertTrashItem(ctx, row); err != nil {
				return err
			}
			if lfi.IsDir() {
				go o.ComputeTrashSize(ctx, p, id)
			}
			added++
		}
		for _, r := range rows {
			if !onDisk[r.ID] {
				if err := o.DB.W.DeleteTrashItem(ctx, r.ID); err != nil {
					return err
				}
				dropped++
			}
		}
		if added+removed+dropped > 0 {
			log.Info("reconciled trash", "place", p.Name, "recovered", added, "emptyRemoved", removed, "rowsDropped", dropped)
		}
	}
	return nil
}
