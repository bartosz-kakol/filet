package fsops

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"

	"filet/internal/apierr"
	"filet/internal/places"
)

// ZipName returns the download file name for a selection.
func ZipName(v *places.View, dir string, names []string) string {
	if len(names) == 1 {
		return names[0] + ".zip"
	}
	full := v.PlacePath(dir)
	if full == "/" {
		return v.Place.Name + ".zip"
	}
	return path.Base(full) + ".zip"
}

// ValidateZip checks the zip request parameters before streaming starts.
func ValidateZip(v *places.View, dir string, names []string) (string, []string, error) {
	dir, err := v.Check(dir)
	if v.OnlyName != "" {
		// File shares: the only valid request is the shared file itself.
		dir, err = "/", nil
	}
	if err != nil {
		return "", nil, err
	}
	if len(names) == 0 {
		return "", nil, apierr.Invalid("name", "Select at least one item")
	}
	names = slices.Clone(names)
	slices.Sort(names)
	names = slices.Compact(names)
	for _, n := range names {
		if err := places.ValidateName(n); err != nil {
			return "", nil, err
		}
		if v.OnlyName != "" && n != v.OnlyName {
			return "", nil, apierr.ErrNotFound
		}
		if v.Public && strings.HasPrefix(n, ".") {
			return "", nil, apierr.ErrNotFound
		}
		if _, err := v.Lstat(places.Join(dir, n)); err != nil {
			return "", nil, apierr.FromFS(err)
		}
	}
	return dir, names, nil
}

type zipper struct {
	ctx     context.Context
	v       *places.View
	zw      *zip.Writer
	skipped []string
}

// WriteZip streams a store-only zip of dir/names to w. Unreadable items are
// skipped and listed in a final _filet_skipped.txt entry.
func WriteZip(ctx context.Context, w io.Writer, v *places.View, dir string, names []string) error {
	z := &zipper{ctx: ctx, v: v, zw: zip.NewWriter(w)}
	for _, n := range names {
		if err := z.add(places.Join(dir, n), n); err != nil {
			return err
		}
	}
	if len(z.skipped) > 0 {
		fw, err := z.zw.CreateHeader(&zip.FileHeader{Name: "_filet_skipped.txt", Method: zip.Store})
		if err != nil {
			return err
		}
		_, _ = io.WriteString(fw, "These items could not be included:\n\n"+strings.Join(z.skipped, "\n")+"\n")
	}
	return z.zw.Close()
}

func (z *zipper) skip(arc, reason string) { z.skipped = append(z.skipped, arc+"\t"+reason) }

func (z *zipper) add(rel, arc string) error {
	if err := z.ctx.Err(); err != nil {
		return err
	}
	lfi, err := z.v.Lstat(rel)
	if err != nil {
		z.skip(arc, "not_found")
		return nil
	}
	fi := lfi
	if lfi.Mode()&fs.ModeSymlink != 0 {
		tfi, err := z.v.Stat(rel)
		if err != nil || !tfi.Mode().IsRegular() {
			z.skip(arc, "symlink_skipped")
			return nil
		}
		fi = tfi
	}
	switch {
	case fi.IsDir():
		names, err := ReadNames(z.v.Root, rel)
		if err != nil {
			z.skip(arc+"/", "permission_denied")
			return nil
		}
		h := &zip.FileHeader{Name: arc + "/", Method: zip.Store, Modified: fi.ModTime()}
		h.SetMode(fi.Mode())
		if _, err := z.zw.CreateHeader(h); err != nil {
			return err
		}
		for _, n := range names {
			if z.v.Public && strings.HasPrefix(n, ".") {
				continue
			}
			if err := z.add(places.Join(rel, n), arc+"/"+n); err != nil {
				return err
			}
		}
	case fi.Mode().IsRegular():
		f, err := z.v.Open(rel)
		if err != nil {
			z.skip(arc, "permission_denied")
			return nil
		}
		defer f.Close()
		h := &zip.FileHeader{Name: arc, Method: zip.Store, Modified: fi.ModTime(), UncompressedSize64: uint64(fi.Size())} //nolint:gosec // size ≥ 0
		h.SetMode(fi.Mode())
		fw, err := z.zw.CreateHeader(h)
		if err != nil {
			return err
		}
		if _, err := io.Copy(ctxWriter{z.ctx, fw}, f); err != nil {
			if z.ctx.Err() != nil {
				return z.ctx.Err()
			}
			return fmt.Errorf("zip %s: %w", arc, err)
		}
	default:
		z.skip(arc, "unsupported_type")
	}
	return nil
}

type ctxWriter struct {
	ctx context.Context
	w   io.Writer
}

func (c ctxWriter) Write(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.w.Write(p)
}
