package fsops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"sync/atomic"
	"time"

	"filet/internal/apierr"
	"filet/internal/fsx"
	"filet/internal/places"
)

const segment = 64 << 20

// TestSegmentDelay slows every copy segment (set only by filettest builds so
// end-to-end tests can cancel a copy midway).
var TestSegmentDelay atomic.Int64

// Copier copies regular files into a destination place through temporary
// ".filet-copy-<jobId>-<n>" files that are renamed into place when complete.
type Copier struct {
	JobID string
	// Progress is called with the number of bytes copied in each segment.
	Progress func(n int64)
	// SetTemp records the in-progress temp file (rel "" clears it).
	SetTemp func(placeID, rel string)

	n atomic.Int64
}

// Temp is an in-progress copy target.
type Temp struct {
	Place *places.Place
	Dir   string
	Name  string
}

// Rel returns the temp file's place path.
func (t *Temp) Rel() string { return places.Join(t.Dir, t.Name) }

// CopyToTemp copies src (an open regular file with info fi) into a new temp
// file in dst/dstDir. The data is fdatasynced and the mtime set before it
// returns.
func (c *Copier) CopyToTemp(ctx context.Context, src *os.File, fi fs.FileInfo, dst *places.Place, dstDir string) (*Temp, error) {
	t := &Temp{Place: dst, Dir: dstDir, Name: fmt.Sprintf(".filet-copy-%s-%d", c.JobID, c.n.Add(1))}
	out, err := dst.Root.OpenFile(places.RootRel(t.Rel()), os.O_CREATE|os.O_EXCL|os.O_WRONLY, fi.Mode().Perm())
	if err != nil {
		return nil, apierr.FromFS(err)
	}
	if c.SetTemp != nil {
		c.SetTemp(dst.ID, t.Rel())
	}
	fail := func(err error) (*Temp, error) {
		_ = out.Close()
		c.Discard(t)
		if fsx.IsNoSpace(err) {
			return nil, apierr.ErrInsufficient
		}
		return nil, err
	}
	for {
		if d := TestSegmentDelay.Load(); d > 0 {
			select {
			case <-time.After(time.Duration(d)):
			case <-ctx.Done():
			}
		}
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		n, err := out.ReadFrom(&io.LimitedReader{R: src, N: segment})
		if n > 0 && c.Progress != nil {
			c.Progress(n)
		}
		if err != nil {
			return fail(err)
		}
		if n < segment {
			break
		}
	}
	if err := fsx.Datasync(out); err != nil {
		return fail(err)
	}
	if err := out.Close(); err != nil {
		c.Discard(t)
		return nil, err
	}
	_ = dst.Root.Chtimes(places.RootRel(t.Rel()), time.Now(), fi.ModTime())
	return t, nil
}

// Commit renames the temp file to name with RENAME_NOREPLACE. An EEXIST
// error (a race) leaves the temp file in place for another attempt.
func (c *Copier) Commit(t *Temp, name string) error {
	df, err := OpenDir(t.Place.Root, t.Dir)
	if err != nil {
		return err
	}
	defer df.Close()
	if err := fsx.RenameNoReplace(df, t.Name, df, name); err != nil {
		return err
	}
	if c.SetTemp != nil {
		c.SetTemp("", "")
	}
	return nil
}

// Discard removes the temp file.
func (c *Copier) Discard(t *Temp) {
	_ = t.Place.Root.Remove(places.RootRel(t.Rel()))
	if c.SetTemp != nil {
		c.SetTemp("", "")
	}
}

// CopyTree copies srcRel (file, directory or symlink) from src into
// dst/dstDir/name without conflict handling; dst/dstDir/name must not
// exist. Reserved names are skipped. It is used for cross-device trash and
// as the building block for cross-device moves without prompts.
func (c *Copier) CopyTree(ctx context.Context, src *places.View, srcRel string, dst *places.Place, dstDir, name string) error {
	lfi, err := src.Lstat(srcRel)
	if err != nil {
		return apierr.FromFS(err)
	}
	dstRel := places.Join(dstDir, name)
	switch {
	case lfi.Mode()&fs.ModeSymlink != 0:
		target, err := src.Root.Readlink(places.RootRel(srcRel))
		if err != nil {
			return err
		}
		return dst.Root.Symlink(target, places.RootRel(dstRel))
	case lfi.IsDir():
		if err := dst.Root.Mkdir(places.RootRel(dstRel), lfi.Mode().Perm()); err != nil {
			return apierr.FromFS(err)
		}
		names, err := readNames(src.Root, srcRel)
		if err != nil {
			return apierr.FromFS(err)
		}
		for _, n := range names {
			if places.IsReserved(n) {
				continue
			}
			if err := c.CopyTree(ctx, src, places.Join(srcRel, n), dst, dstRel, n); err != nil {
				return err
			}
		}
		_ = dst.Root.Chtimes(places.RootRel(dstRel), time.Now(), lfi.ModTime())
		return nil
	case lfi.Mode().IsRegular():
		f, err := src.Root.Open(places.RootRel(srcRel))
		if err != nil {
			return apierr.FromFS(err)
		}
		defer f.Close()
		t, err := c.CopyToTemp(ctx, f, lfi, dst, dstDir)
		if err != nil {
			return err
		}
		if err := c.Commit(t, name); err != nil {
			c.Discard(t)
			return err
		}
		return nil
	}
	return nil // sockets, devices: skipped
}

// readNames returns the sorted entry names of a directory.
func readNames(root *os.Root, rel string) ([]string, error) {
	f, err := root.Open(places.RootRel(rel))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	return names, nil
}

// ReadNames returns the sorted, non-reserved entry names of a directory.
func ReadNames(root *os.Root, rel string) ([]string, error) {
	names, err := readNames(root, rel)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(names, places.IsReserved), nil
}

// ErrExists reports an EEXIST-style error.
func ErrExists(err error) bool { return fsx.IsExist(err) || errors.Is(err, fs.ErrExist) }
