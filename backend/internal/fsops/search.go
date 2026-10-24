package fsops

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charlievieth/fastwalk"

	"filet/internal/apierr"
	"filet/internal/places"
)

// Search limits.
const (
	SearchDefaultLimit = 500
	SearchMaxLimit     = 2000
	searchTimeout      = 60 * time.Second
)

// SearchHit is one NDJSON result line.
type SearchHit struct {
	Path  string       `json:"path"`
	Entry places.Entry `json:"entry"`
}

// SearchDone is the final NDJSON line.
type SearchDone struct {
	Done      bool  `json:"done"`
	Truncated bool  `json:"truncated"`
	TimedOut  bool  `json:"timedOut"`
	Scanned   int64 `json:"scanned"`
}

// ErrQueryTooShort is returned for queries under 2 characters.
var ErrQueryTooShort = apierr.New(400, "query_too_short", "Type at least 2 characters")

// SearchTerms normalises a query into lower-cased terms.
func SearchTerms(q string) ([]string, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if len([]rune(q)) < 2 {
		return nil, ErrQueryTooShort
	}
	return strings.Fields(q), nil
}

// Search walks the whole place and calls emit for every entry whose name
// contains every term. emit runs on a single goroutine.
func Search(ctx context.Context, p *places.Place, terms []string, hidden bool, limit int, emit func(SearchHit)) SearchDone {
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	var scanned atomic.Int64
	type match struct {
		rel, name string
		d         fs.DirEntry
	}
	matches := make(chan match, 256)
	var truncated atomic.Bool
	stop := errors.New("stop")
	walkDone := make(chan struct{})
	root := p.Path
	go func() {
		defer close(walkDone)
		defer close(matches)
		// Errors (permission denied, our own stop) are expected and not reported.
		_ = fastwalk.Walk(&fastwalk.Config{Follow: false}, root, func(full string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return stop
			}
			if full == root {
				return nil
			}
			if err != nil {
				if d != nil && d.IsDir() {
					return fastwalk.SkipDir
				}
				return nil
			}
			name := d.Name()
			if places.IsReserved(name) || (!hidden && strings.HasPrefix(name, ".")) {
				if d.IsDir() {
					return fastwalk.SkipDir
				}
				return nil
			}
			scanned.Add(1)
			lower := strings.ToLower(name)
			for _, t := range terms {
				if !strings.Contains(lower, t) {
					return nil
				}
			}
			select {
			case matches <- match{rel: strings.TrimPrefix(full, root), name: name, d: d}:
			case <-ctx.Done():
				return stop
			}
			return nil
		})
	}()
	v := p.FullView()
	count := 0
	for m := range matches {
		if count >= limit {
			truncated.Store(true)
			cancel()
			continue
		}
		info, ierr := m.d.Info()
		emit(SearchHit{Path: m.rel, Entry: v.BuildEntry(m.rel, m.name, info, ierr)})
		count++
	}
	<-walkDone
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded) && !truncated.Load()
	return SearchDone{Done: true, Truncated: truncated.Load(), TimedOut: timedOut, Scanned: scanned.Load()}
}
