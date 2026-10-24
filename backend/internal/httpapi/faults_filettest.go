//go:build filettest

package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"filet/internal/clock"
	"filet/internal/fsops"
	"filet/internal/fsx"
)

// faults reacts to test-only request headers (filettest builds only):
//
//	X-Filet-Test-Corrupt-Chunk: 1        flip one byte of the request body
//	X-Filet-Test-Delay-Ms: <n>           delay the handler
//	X-Filet-Test-Drop-After-Bytes: <n>   read n bytes, then drop the connection
func faults(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/__test/") {
			logMu.Lock()
			testRequests = append(testRequests, r.Method+" "+r.URL.Path)
			logMu.Unlock()
		}
		if ms, err := strconv.Atoi(r.Header.Get("X-Filet-Test-Delay-Ms")); err == nil && ms > 0 {
			select {
			case <-time.After(time.Duration(ms) * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
		if r.Header.Get("X-Filet-Test-Corrupt-Chunk") == "1" && r.Body != nil {
			r.Body = &corrupter{ReadCloser: r.Body}
		}
		if n, err := strconv.ParseInt(r.Header.Get("X-Filet-Test-Drop-After-Bytes"), 10, 64); err == nil && n >= 0 && r.Body != nil {
			r.Body = &dropper{ReadCloser: r.Body, left: n, w: w}
		}
		next.ServeHTTP(w, r)
	})
}

type corrupter struct {
	io.ReadCloser
	done bool
}

func (c *corrupter) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	if n > 0 && !c.done {
		p[0] ^= 0xff
		c.done = true
	}
	return n, err
}

type dropper struct {
	io.ReadCloser
	left int64
	w    http.ResponseWriter
}

func (d *dropper) Read(p []byte) (int, error) {
	if d.left <= 0 {
		if conn, _, err := http.NewResponseController(d.w).Hijack(); err == nil {
			_ = conn.Close()
		}
		return 0, io.ErrUnexpectedEOF
	}
	if int64(len(p)) > d.left {
		p = p[:d.left]
	}
	n, err := d.ReadCloser.Read(p)
	d.left -= int64(n)
	return n, err
}

var (
	logMu        sync.Mutex
	testRequests []string
)

// testRoutes adds the test clock and request counter endpoints.
func testRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /__test/clock", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			AdvanceMs int64 `json:"advanceMs"`
			Reset     bool  `json:"reset"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Reset {
			clock.Reset()
		}
		clock.Advance(time.Duration(body.AdvanceMs) * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	})
	// {"free": <bytes>} fakes statfs everywhere; {"free": -1} restores it.
	m.HandleFunc("POST /__test/statfs", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Free int64 `json:"free"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Free < 0 {
			fsx.SetStatfsOverrideForTest(nil)
		} else {
			fsx.SetStatfsOverrideForTest(&fsx.DiskUsage{Total: uint64(body.Free) * 2, Used: uint64(body.Free), Free: uint64(body.Free), Fsid: "fake"}) //nolint:gosec // test only
		}
		w.WriteHeader(http.StatusNoContent)
	})
	// {"ms": n} delays every copy segment of jobs.
	m.HandleFunc("POST /__test/copydelay", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Ms int64 `json:"ms"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		fsops.TestSegmentDelay.Store(int64(time.Duration(body.Ms) * time.Millisecond))
		w.WriteHeader(http.StatusNoContent)
	})
	// Returns and optionally clears the request log ("METHOD /path" lines).
	m.HandleFunc("GET /__test/requests", func(w http.ResponseWriter, r *http.Request) {
		logMu.Lock()
		out := append([]string(nil), testRequests...)
		if r.URL.Query().Get("clear") == "1" {
			testRequests = nil
		}
		logMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
}
