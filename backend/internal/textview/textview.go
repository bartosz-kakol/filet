// Package textview builds line indexes for the virtualised large-file viewer.
package textview

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"filet/internal/apierr"
	"filet/internal/fsx"
	"filet/internal/lru"
	"filet/internal/places"
)

// Step is the number of lines per offset entry.
const Step = 1000

const indexTimeout = 120 * time.Second

// Index is the text-index response.
type Index struct {
	Size      int64   `json:"size"`
	Mtime     int64   `json:"mtime"`
	LineCount int64   `json:"lineCount"`
	Step      int     `json:"step"`
	Offsets   []int64 `json:"offsets"`
}

// Service indexes files with an identity-keyed LRU of 32 entries.
type Service struct {
	cache *lru.Cache[fsx.Identity, *Index]
}

// New creates the service.
func New() *Service { return &Service{cache: lru.New[fsx.Identity, *Index](32)} }

// ErrTimeout is returned when indexing exceeds 120 s.
var ErrTimeout = apierr.New(http.StatusGatewayTimeout, "index_timeout", "File too large to index")

// Index returns the line index for rel.
func (s *Service) Index(ctx context.Context, v *places.View, rel string) (*Index, error) {
	f, fi, err := v.OpenRegular(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	id := fsx.IdentityOf(fi)
	if ix, ok := s.cache.Get(id); ok {
		return ix, nil
	}
	ctx, cancel := context.WithTimeout(ctx, indexTimeout)
	defer cancel()
	ix, err := Build(ctx, f, fi.Size())
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrTimeout
		}
		return nil, err
	}
	ix.Mtime = fi.ModTime().UnixMilli()
	s.cache.Add(id, ix)
	return ix, nil
}

// Build scans r (size bytes) and records the byte offset of every Step-th
// line start.
func Build(ctx context.Context, r io.Reader, size int64) (*Index, error) {
	ix := &Index{Size: size, Step: Step, Offsets: []int64{0}}
	buf := make([]byte, 1<<20)
	var pos, newlines int64
	var last byte
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := r.Read(buf)
		chunk := buf[:n]
		base := pos
		for len(chunk) > 0 {
			i := bytes.IndexByte(chunk, '\n')
			if i < 0 {
				break
			}
			newlines++
			start := base + int64(i) + 1
			if newlines%Step == 0 && start < size {
				ix.Offsets = append(ix.Offsets, start)
			}
			base = start
			chunk = chunk[i+1:]
		}
		if n > 0 {
			last = buf[n-1] //nolint:gosec // n > 0 and n <= len(buf)
			pos += int64(n)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	ix.Size = pos
	ix.LineCount = newlines
	if pos > 0 && last != '\n' {
		ix.LineCount++
	}
	if pos == 0 {
		ix.Offsets = []int64{}
	}
	return ix, nil
}
