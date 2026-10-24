package httpapi

import (
	"bufio"
	"compress/gzip"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"filet/internal/auth"
)

// recoverer turns panics into 500s (http.ErrAbortHandler is re-raised so
// the connection is dropped silently).
func recoverer(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				if p == http.ErrAbortHandler { //nolint:errorlint // sentinel comparison as documented
					panic(p)
				}
				log.Error("panic in handler", "method", r.Method, "path", r.URL.Path, "panic", p)
				http.Error(w, `{"error":{"code":"internal","message":"Internal error"}}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(p []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(p)
	s.bytes += int64(n)
	return n, err
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// requestLog logs method, path, status, duration and bytes. Paths are only
// logged at debug level.
func requestLog(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.status == 0 {
			sw.status = http.StatusOK
		}
		attrs := []any{"method", r.Method, "status", sw.status, "dur", time.Since(start).Round(time.Microsecond), "bytes", sw.bytes}
		if log.Enabled(r.Context(), slog.LevelDebug) {
			attrs = append(attrs, "path", r.URL.Path)
			log.Debug("request", attrs...)
			return
		}
		if sw.status >= 500 {
			log.Warn("request", attrs...)
		}
	})
}

// securityHeaders sets the app response headers.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func authExempt(path string) bool {
	return path == "/api/auth/login" || path == "/api/auth/session" || strings.HasPrefix(path, "/api/public/")
}

// requireAuth checks the session cookie on /api/* (except the exemptions)
// and slides the session expiry.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || authExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie(auth.SessionCookie)
		if err != nil {
			http.SetCookie(w, s.Auth.Cookie("", 0))
			writeError(w, r, s.Log, errUnauth)
			return
		}
		_, renewed, err := s.Auth.Authenticate(r.Context(), c.Value)
		if err != nil {
			http.SetCookie(w, s.Auth.Cookie("", 0))
			writeError(w, r, s.Log, err)
			return
		}
		if renewed {
			http.SetCookie(w, s.Auth.Cookie(c.Value, auth.SessionMaxAge))
		}
		next.ServeHTTP(w, r)
	})
}

// gzipEligible reports whether a path produces JSON/NDJSON only.
func gzipEligible(path string) bool {
	if !strings.HasPrefix(path, "/api/") || path == "/api/events" {
		return false
	}
	for _, seg := range []string{"/raw/", "/thumb/", "/render/", "/media/remux/", "/media/subtitle/", "/archive/entry/", "/chunks/"} {
		if strings.Contains(path, seg) {
			return false
		}
	}
	return !strings.HasSuffix(path, "/zip")
}

// gzipJSON compresses JSON and NDJSON responses over 1 KiB.
func gzipJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !gzipEligible(r.URL.Path) || !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipWriter{ResponseWriter: w}
		defer gw.close()
		next.ServeHTTP(gw, r)
	})
}

type gzipWriter struct {
	http.ResponseWriter
	status  int
	decided bool
	gz      *gzip.Writer
	buf     []byte
}

func (g *gzipWriter) WriteHeader(code int) {
	if g.status == 0 {
		g.status = code
	}
	if code == http.StatusNoContent || code == http.StatusNotModified || code < 200 {
		g.decide(false)
	}
}

func (g *gzipWriter) Write(p []byte) (int, error) {
	if g.status == 0 {
		g.status = http.StatusOK
	}
	if g.decided {
		if g.gz != nil {
			return g.gz.Write(p)
		}
		return g.ResponseWriter.Write(p)
	}
	g.buf = append(g.buf, p...)
	if len(g.buf) > 1024 {
		g.decide(true)
	}
	return len(p), nil
}

func (g *gzipWriter) decide(big bool) {
	if g.decided {
		return
	}
	g.decided = true
	if g.status == 0 {
		g.status = http.StatusOK
	}
	ct := g.Header().Get("Content-Type")
	compress := big && g.Header().Get("Content-Encoding") == "" &&
		(strings.HasPrefix(ct, "application/json") || strings.HasPrefix(ct, "application/x-ndjson"))
	if compress {
		g.Header().Set("Content-Encoding", "gzip")
		g.Header().Add("Vary", "Accept-Encoding")
		g.Header().Del("Content-Length")
		g.ResponseWriter.WriteHeader(g.status)
		g.gz, _ = gzip.NewWriterLevel(g.ResponseWriter, gzip.BestSpeed)
		_, _ = g.gz.Write(g.buf)
	} else {
		g.ResponseWriter.WriteHeader(g.status)
		if len(g.buf) > 0 {
			_, _ = g.ResponseWriter.Write(g.buf) //nolint:gosec // pass-through of the handler's own output
		}
	}
	g.buf = nil
}

func (g *gzipWriter) Flush() {
	g.decide(len(g.buf) > 1024)
	if g.gz != nil {
		_ = g.gz.Flush()
	}
	_ = http.NewResponseController(g.ResponseWriter).Flush()
}

func (g *gzipWriter) close() {
	if !g.decided {
		if g.status == 0 && len(g.buf) == 0 {
			return // handler wrote nothing (e.g. aborted)
		}
		g.decide(len(g.buf) > 1024)
	}
	if g.gz != nil {
		_ = g.gz.Close()
	}
}

func (g *gzipWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

// Hijack supports the test fault middleware through the wrapper chain.
func (g *gzipWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := g.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("hijack not supported")
}
