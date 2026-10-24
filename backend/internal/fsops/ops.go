// Package fsops implements immediate file operations (mkdir, rename), the
// trash primitive, the copy engine shared by jobs, streamed zip downloads and
// filename search.
package fsops

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"strings"

	"filet/internal/apierr"
	"filet/internal/db"
	"filet/internal/events"
	"filet/internal/fsx"
	"filet/internal/places"
	"filet/internal/thumbs"
)

// UploadIndex reports the target paths held by upload sessions.
type UploadIndex interface {
	Targets(ctx context.Context, placeID string) ([]string, error)
}

// Ops bundles the dependencies of file operations.
type Ops struct {
	DB      *db.DB
	Reg     *places.Registry
	Hub     *events.Hub
	Thumbs  *thumbs.Cache
	Uploads UploadIndex
	Log     *slog.Logger
}

// Held reports whether path is the target of an upload session.
func Held(targets []string, path string) bool {
	for _, t := range targets {
		if t == path {
			return true
		}
	}
	return false
}

// UploadsBelow counts upload targets strictly below dir.
func UploadsBelow(targets []string, dir string) int {
	n := 0
	for _, t := range targets {
		if dir == "/" || strings.HasPrefix(t, dir+"/") {
			n++
		}
	}
	return n
}

// CheckNotUploadAncestor returns upload_in_progress if any upload targets a
// path below rel.
func (o *Ops) CheckNotUploadAncestor(ctx context.Context, placeID, rel string) error {
	targets, err := o.Uploads.Targets(ctx, placeID)
	if err != nil {
		return err
	}
	if n := UploadsBelow(targets, rel); n > 0 {
		return apierr.ErrUploadInProgress.With("uploads", n)
	}
	return nil
}

// OpenDir opens a directory of a place root.
func OpenDir(root *os.Root, rel string) (*os.File, error) {
	f, err := root.Open(places.RootRel(rel))
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !fi.IsDir() {
		_ = f.Close()
		return nil, apierr.ErrNotADirectory
	}
	return f, nil
}

// Mkdir creates a directory. With parents, existing directories are fine.
func (o *Ops) Mkdir(ctx context.Context, p *places.Place, rel string, parents bool) (places.Entry, error) {
	if err := p.Writable(); err != nil {
		return places.Entry{}, err
	}
	rel, err := places.ValidateRelPath(rel)
	if err != nil {
		return places.Entry{}, err
	}
	if rel == "/" {
		if parents {
			return p.FullView().StatEntry("/")
		}
		return places.Entry{}, apierr.ErrAlreadyExists
	}
	targets, err := o.Uploads.Targets(ctx, p.ID)
	if err != nil {
		return places.Entry{}, err
	}
	if Held(targets, rel) {
		return places.Entry{}, apierr.ErrUploadInProgress
	}
	changed, _ := places.Split(rel)
	if parents {
		// Report the directory whose listing gains an entry: the parent of the
		// first missing component.
		probe := rel
		for probe != "/" {
			d, _ := places.Split(probe)
			if _, err := p.Root.Lstat(places.RootRel(d)); err == nil {
				changed = d
				break
			}
			probe = d
		}
		err = p.Root.MkdirAll(places.RootRel(rel), 0o755)
		if err != nil {
			if fsx.IsNotDir(err) || errors.Is(err, fs.ErrExist) {
				return places.Entry{}, apierr.ErrParentNotDir
			}
			return places.Entry{}, mkdirErr(err)
		}
		if fi, err := p.Root.Stat(places.RootRel(rel)); err == nil && !fi.IsDir() {
			return places.Entry{}, apierr.ErrParentNotDir
		}
	} else if err := p.Root.Mkdir(places.RootRel(rel), 0o755); err != nil {
		return places.Entry{}, mkdirErr(err)
	}
	o.Hub.FS(p.ID, changed)
	return p.FullView().StatEntry(rel)
}

func mkdirErr(err error) error {
	switch {
	case fsx.IsExist(err):
		return apierr.ErrAlreadyExists
	case fsx.IsNotDir(err):
		return apierr.ErrParentNotDir
	}
	return apierr.FromFS(err)
}

// Rename renames an entry within its directory.
func (o *Ops) Rename(ctx context.Context, p *places.Place, rel, newName string) (places.Entry, error) {
	if err := p.Writable(); err != nil {
		return places.Entry{}, err
	}
	rel, err := places.ValidateRelPath(rel)
	if err != nil {
		return places.Entry{}, err
	}
	if rel == "/" {
		return places.Entry{}, apierr.ErrInvalidPath
	}
	if err := places.ValidateName(newName); err != nil {
		return places.Entry{}, err
	}
	dir, name := places.Split(rel)
	if name == newName {
		return p.FullView().StatEntry(rel)
	}
	targets, err := o.Uploads.Targets(ctx, p.ID)
	if err != nil {
		return places.Entry{}, err
	}
	newRel := places.Join(dir, newName)
	if Held(targets, newRel) {
		return places.Entry{}, apierr.ErrUploadInProgress
	}
	if n := UploadsBelow(targets, rel); n > 0 {
		return places.Entry{}, apierr.ErrUploadInProgress.With("uploads", n)
	}
	if _, err := p.Root.Lstat(places.RootRel(rel)); err != nil {
		return places.Entry{}, apierr.FromFS(err)
	}
	df, err := OpenDir(p.Root, dir)
	if err != nil {
		return places.Entry{}, apierr.FromFS(err)
	}
	defer df.Close()
	if err := fsx.RenameNoReplace(df, name, df, newName); err != nil {
		if fsx.IsExist(err) {
			return places.Entry{}, apierr.ErrAlreadyExists
		}
		return places.Entry{}, apierr.FromFS(err)
	}
	o.Hub.FS(p.ID, dir)
	return p.FullView().StatEntry(newRel)
}
