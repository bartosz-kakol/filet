// Package archive lists zip, tar, tar.gz and 7z archives and streams single
// entries. Nothing is ever extracted to disk.
package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bodgit/sevenzip"

	"filet/internal/apierr"
	"filet/internal/fsx"
	"filet/internal/lru"
	"filet/internal/places"
)

const (
	maxEntries  = 200000
	listTimeout = 120 * time.Second
	// MaxInline is the largest entry that may be previewed inline.
	MaxInline = 256 << 20
)

// ErrEncrypted is returned for encrypted entries.
var ErrEncrypted = apierr.New(http.StatusForbidden, "entry_encrypted", "This entry is encrypted")

// Entry is one archive member.
type Entry struct {
	Path           string `json:"path"`
	Size           int64  `json:"size"`
	CompressedSize *int64 `json:"compressedSize,omitempty"`
	Mtime          int64  `json:"mtime"`
	IsDir          bool   `json:"isDir"`
	Encrypted      bool   `json:"encrypted"`
	orig           string
}

// Listing is the archive tree response.
type Listing struct {
	Format     string  `json:"format"`
	EntryCount int     `json:"entryCount"`
	TotalSize  int64   `json:"totalSize"`
	Truncated  bool    `json:"truncated"`
	Entries    []Entry `json:"entries"`
}

// Service lists archives with an identity-keyed LRU.
type Service struct {
	cache *lru.Cache[fsx.Identity, *Listing]
}

// New creates the service.
func New() *Service { return &Service{cache: lru.New[fsx.Identity, *Listing](16)} }

// Format returns the archive format for a file name, or "".
func Format(name string) string {
	switch places.Ext(name) {
	case "zip":
		return "zip"
	case "tar":
		return "tar"
	case "tar.gz", "tgz":
		return "tar.gz"
	case "7z":
		return "7z"
	}
	return ""
}

// Normalize makes an entry path safe for display: strips a leading "/" or
// "./" and replaces ".." segments with "__".
func Normalize(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	for {
		switch {
		case strings.HasPrefix(p, "/"):
			p = p[1:]
		case strings.HasPrefix(p, "./"):
			p = p[2:]
		default:
			segs := strings.Split(strings.TrimSuffix(p, "/"), "/")
			for i, s := range segs {
				if s == ".." {
					segs[i] = "__"
				}
			}
			return strings.Join(segs, "/")
		}
	}
}

// List lists the archive at rel.
func (s *Service) List(ctx context.Context, v *places.View, rel string) (*Listing, error) {
	_, name := places.Split(v.PlacePath(rel))
	format := Format(name)
	if format == "" {
		return nil, apierr.ErrUnsupportedFormat
	}
	f, fi, err := v.OpenRegular(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	id := fsx.IdentityOf(fi)
	if l, ok := s.cache.Get(id); ok {
		return l, nil
	}
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	l, err := list(ctx, f, fi.Size(), format)
	if err != nil {
		return nil, err
	}
	s.cache.Add(id, l)
	return l, nil
}

func list(ctx context.Context, f *os.File, size int64, format string) (*Listing, error) {
	l := &Listing{Format: format, Entries: []Entry{}}
	add := func(e Entry) bool {
		l.EntryCount++
		if !e.IsDir {
			l.TotalSize += e.Size
		}
		if len(l.Entries) >= maxEntries {
			l.Truncated = true
			return false
		}
		l.Entries = append(l.Entries, e)
		return true
	}
	bad := apierr.New(http.StatusUnprocessableEntity, "archive_unreadable", "The archive could not be read")
	switch format {
	case "zip":
		zr, err := zip.NewReader(f, size)
		if err != nil {
			return nil, bad
		}
		for i, zf := range zr.File {
			if i%1000 == 0 && ctx.Err() != nil {
				return nil, timeoutErr(ctx)
			}
			cs := int64(zf.CompressedSize64) //nolint:gosec // archive sizes fit int64
			e := Entry{
				Path: Normalize(zf.Name), orig: zf.Name, Size: int64(zf.UncompressedSize64), CompressedSize: &cs, //nolint:gosec // fits
				Mtime: zf.Modified.UnixMilli(), IsDir: strings.HasSuffix(zf.Name, "/") || zf.Mode().IsDir(), Encrypted: zf.Flags&1 != 0,
			}
			if e.Path == "" {
				continue
			}
			if !add(e) {
				break
			}
		}
	case "tar", "tar.gz":
		var r io.Reader = f
		if format == "tar.gz" {
			gz, err := gzip.NewReader(f)
			if err != nil {
				return nil, bad
			}
			defer func() { _ = gz.Close() }()
			r = gz
		}
		tr := tar.NewReader(r)
		for {
			if ctx.Err() != nil {
				return nil, timeoutErr(ctx)
			}
			h, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				if len(l.Entries) == 0 {
					return nil, bad
				}
				break
			}
			if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
				continue
			}
			e := Entry{Path: Normalize(h.Name), orig: h.Name, Size: h.Size, Mtime: h.ModTime.UnixMilli(), IsDir: h.Typeflag == tar.TypeDir}
			if e.Path == "" {
				continue
			}
			if !add(e) {
				l.EntryCount = len(l.Entries) // unknown beyond the cap
				break
			}
		}
	case "7z":
		zr, err := sevenzip.NewReader(f, size)
		if err != nil {
			var re *sevenzip.ReadError
			if errors.As(err, &re) && re.Encrypted {
				return nil, ErrEncrypted
			}
			return nil, bad
		}
		for _, zf := range zr.File {
			e := Entry{
				Path: Normalize(zf.Name), orig: zf.Name, Size: int64(zf.UncompressedSize), //nolint:gosec // fits
				Mtime: zf.Modified.UnixMilli(), IsDir: zf.FileInfo().IsDir(),
			}
			if e.Path == "" {
				continue
			}
			if !add(e) {
				break
			}
		}
	}
	return l, nil
}

func timeoutErr(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return apierr.New(http.StatusGatewayTimeout, "index_timeout", "Reading the archive took too long")
	}
	return ctx.Err()
}

// OpenEntry opens the entry whose normalised path is entry. The returned
// reader must be closed; size is the uncompressed size.
func (s *Service) OpenEntry(ctx context.Context, v *places.View, rel, entry string) (io.ReadCloser, int64, error) {
	_, name := places.Split(v.PlacePath(rel))
	format := Format(name)
	if format == "" {
		return nil, 0, apierr.ErrUnsupportedFormat
	}
	f, fi, err := v.OpenRegular(rel)
	if err != nil {
		return nil, 0, err
	}
	closeF := true
	defer func() {
		if closeF {
			_ = f.Close()
		}
	}()
	switch format {
	case "zip":
		zr, err := zip.NewReader(f, fi.Size())
		if err != nil {
			return nil, 0, apierr.ErrUnsupportedFormat
		}
		for _, zf := range zr.File {
			if Normalize(zf.Name) != entry || strings.HasSuffix(zf.Name, "/") {
				continue
			}
			if zf.Flags&1 != 0 {
				return nil, 0, ErrEncrypted
			}
			rc, err := zf.Open()
			if err != nil {
				return nil, 0, apierr.ErrUnsupportedFormat
			}
			closeF = false
			return multiCloser{rc, f}, int64(zf.UncompressedSize64), nil //nolint:gosec // fits
		}
	case "tar", "tar.gz":
		var r io.Reader = f
		var gz *gzip.Reader
		if format == "tar.gz" {
			if gz, err = gzip.NewReader(f); err != nil {
				return nil, 0, apierr.ErrUnsupportedFormat
			}
			r = gz
		}
		tr := tar.NewReader(r)
		for {
			if ctx.Err() != nil {
				return nil, 0, ctx.Err()
			}
			h, err := tr.Next()
			if err != nil {
				break
			}
			if h.Typeflag == tar.TypeReg && Normalize(h.Name) == entry {
				closeF = false
				var c io.Closer = f
				if gz != nil {
					c = multiCloser{gz, f}
				}
				return readCloser{tr, c}, h.Size, nil
			}
		}
	case "7z":
		zr, err := sevenzip.NewReader(f, fi.Size())
		if err != nil {
			var re *sevenzip.ReadError
			if errors.As(err, &re) && re.Encrypted {
				return nil, 0, ErrEncrypted
			}
			return nil, 0, apierr.ErrUnsupportedFormat
		}
		for _, zf := range zr.File {
			if Normalize(zf.Name) != entry || zf.FileInfo().IsDir() {
				continue
			}
			rc, err := zf.Open()
			if err != nil {
				var re *sevenzip.ReadError
				if errors.As(err, &re) && re.Encrypted {
					return nil, 0, ErrEncrypted
				}
				return nil, 0, apierr.ErrUnsupportedFormat
			}
			closeF = false
			return multiCloser{encryptedReader{rc}, f}, int64(zf.UncompressedSize), nil //nolint:gosec // fits
		}
	}
	return nil, 0, apierr.ErrNotFound
}

type readCloser struct {
	io.Reader
	c io.Closer
}

func (r readCloser) Close() error { return r.c.Close() }

type multiCloser struct {
	io.ReadCloser
	f io.Closer
}

func (m multiCloser) Close() error {
	_ = m.ReadCloser.Close()
	return m.f.Close()
}

type encryptedReader struct{ io.ReadCloser }

func (e encryptedReader) Read(p []byte) (int, error) {
	n, err := e.ReadCloser.Read(p)
	var re *sevenzip.ReadError
	if err != nil && errors.As(err, &re) && re.Encrypted {
		return n, ErrEncrypted
	}
	return n, err
}
