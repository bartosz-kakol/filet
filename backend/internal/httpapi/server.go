// Package httpapi is the HTTP layer: router, middleware and handlers for the
// JSON API, the SSE stream, public share/stream endpoints and the SPA.
package httpapi

import (
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"filet/internal/apierr"
	"filet/internal/archive"
	"filet/internal/auth"
	"filet/internal/config"
	"filet/internal/db"
	"filet/internal/events"
	"filet/internal/fsops"
	"filet/internal/jobs"
	"filet/internal/media"
	"filet/internal/places"
	"filet/internal/share"
	"filet/internal/smart"
	"filet/internal/textview"
	"filet/internal/thumbs"
	"filet/internal/tools"
	"filet/internal/uploads"
)

var errUnauth = apierr.ErrUnauthenticated

// Server holds every service the handlers need.
type Server struct {
	Cfg     *config.Config
	Version string
	DB      *db.DB
	Auth    *auth.Service
	Reg     *places.Registry
	Hub     *events.Hub
	Ops     *fsops.Ops
	Jobs    *jobs.Runner
	Uploads *uploads.Service
	Thumbs  *thumbs.Cache
	Media   *media.Service
	Archive *archive.Service
	Text    *textview.Service
	Share   *share.Service
	Smart   *smart.Service
	Tools   *tools.Runner
	Log     *slog.Logger
	SPA     fs.FS
}

// Handler builds the complete middleware chain and router.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	var h http.Handler = mux
	h = gzipJSON(h)
	h = s.requireAuth(h)
	h = faults(h) // no-op unless built with -tags filettest
	cop := http.NewCrossOriginProtection()
	for _, o := range s.Cfg.Auth.TrustedOrigins {
		_ = cop.AddTrustedOrigin(o)
	}
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, s.Log, apierr.New(http.StatusForbidden, "cross_origin", "Cross-origin request rejected"))
	}))
	h = cop.Handler(h)
	h = securityHeaders(h)
	h = requestLog(s.Log, h)
	return recoverer(s.Log, h)
}

func (s *Server) routes(m *http.ServeMux) {
	// auth & meta
	m.HandleFunc("POST /api/auth/login", s.login)
	m.HandleFunc("POST /api/auth/logout", s.logout)
	m.HandleFunc("GET /api/auth/session", s.session)
	m.HandleFunc("GET /api/settings", s.settings)
	m.HandleFunc("GET /healthz", s.healthz)
	m.HandleFunc("GET /api/events", s.events)

	// places & content
	m.HandleFunc("GET /api/places", s.listPlaces)
	m.HandleFunc("GET /api/places/{place}/list", s.withPlace(s.list))
	m.HandleFunc("GET /api/places/{place}/stat", s.withPlace(s.stat))
	m.HandleFunc("GET /api/places/{place}/search", s.search)
	m.HandleFunc("GET /api/places/{place}/raw/{path...}", s.withPlacePath(s.raw))
	m.HandleFunc("POST /api/places/{place}/zip", s.withPlace(s.zip))
	m.HandleFunc("GET /api/places/{place}/thumb/{path...}", s.withPlacePath(s.thumb))
	m.HandleFunc("GET /api/places/{place}/render/{path...}", s.withPlacePath(s.render))
	m.HandleFunc("GET /api/places/{place}/media/info/{path...}", s.withPlacePath(s.mediaInfo))
	m.HandleFunc("GET /api/places/{place}/media/remux/{path...}", s.withPlacePath(s.remux))
	m.HandleFunc("GET /api/places/{place}/media/subtitle/{path...}", s.withPlacePath(s.subtitle))
	m.HandleFunc("GET /api/places/{place}/text-index/{path...}", s.withPlacePath(s.textIndex))
	m.HandleFunc("GET /api/places/{place}/archive/list/{path...}", s.withPlacePath(s.archiveList))
	m.HandleFunc("GET /api/places/{place}/archive/entry/{path...}", s.withPlacePath(s.archiveEntry))
	m.HandleFunc("GET /api/places/{place}/smart", s.smartDetail)
	m.HandleFunc("POST /api/places/{place}/smart/refresh", s.smartRefresh)

	// mutations
	m.HandleFunc("POST /api/places/{place}/mkdir", s.mkdir)
	m.HandleFunc("POST /api/places/{place}/rename", s.rename)

	// trash
	m.HandleFunc("GET /api/places/{place}/trash", s.trashList)
	m.HandleFunc("POST /api/places/{place}/trash/restore", s.trashRestore)
	m.HandleFunc("POST /api/places/{place}/trash/purge", s.trashPurge)

	// uploads
	m.HandleFunc("POST /api/uploads/preflight", s.preflight)
	m.HandleFunc("POST /api/uploads", s.register)
	m.HandleFunc("PUT /api/uploads/{id}/chunks/{index}", s.putChunk)
	m.HandleFunc("GET /api/uploads/leftovers", s.leftovers)
	m.HandleFunc("GET /api/uploads/leftovers/count", s.leftoverCount)
	m.HandleFunc("DELETE /api/uploads/leftovers", s.removeLeftovers)
	m.HandleFunc("GET /api/uploads/{id}", s.getUpload)
	m.HandleFunc("DELETE /api/uploads/{id}", s.cancelUpload)

	// jobs
	m.HandleFunc("POST /api/jobs", s.submitJob)
	m.HandleFunc("GET /api/jobs", s.listJobs)
	m.HandleFunc("GET /api/jobs/{id}", s.getJob)
	m.HandleFunc("POST /api/jobs/{id}/cancel", s.cancelJob)
	m.HandleFunc("POST /api/jobs/{id}/resolve", s.resolveJob)
	m.HandleFunc("DELETE /api/jobs/{id}", s.dismissJob)

	// links
	m.HandleFunc("POST /api/stream-links", s.createStreamLink)
	m.HandleFunc("GET /stream/{token}/{name}", s.stream)
	m.HandleFunc("GET /api/share-links", s.listShareLinks)
	m.HandleFunc("POST /api/share-links", s.createShareLink)
	m.HandleFunc("DELETE /api/share-links/{id}", s.deleteShareLink)

	// public share API
	m.HandleFunc("GET /api/public/share/{token}", s.publicInfo)
	m.HandleFunc("POST /api/public/share/{token}/unlock", s.publicUnlock)
	m.HandleFunc("GET /api/public/share/{token}/list", s.public(s.list, false))
	m.HandleFunc("GET /api/public/share/{token}/raw/{path...}", s.public(s.raw, true))
	m.HandleFunc("GET /api/public/share/{token}/thumb/{path...}", s.public(s.thumb, true))
	m.HandleFunc("GET /api/public/share/{token}/render/{path...}", s.public(s.render, true))
	m.HandleFunc("GET /api/public/share/{token}/media/info/{path...}", s.public(s.mediaInfo, true))
	m.HandleFunc("GET /api/public/share/{token}/media/remux/{path...}", s.public(s.remux, true))
	m.HandleFunc("GET /api/public/share/{token}/media/subtitle/{path...}", s.public(s.subtitle, true))
	m.HandleFunc("GET /api/public/share/{token}/text-index/{path...}", s.public(s.textIndex, true))
	m.HandleFunc("GET /api/public/share/{token}/archive/list/{path...}", s.public(s.archiveList, true))
	m.HandleFunc("GET /api/public/share/{token}/archive/entry/{path...}", s.public(s.archiveEntry, true))
	m.HandleFunc("POST /api/public/share/{token}/zip", s.public(s.zip, false))

	testRoutes(m) // filettest build only

	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, s.Log, apierr.New(http.StatusNotFound, "not_found", "Unknown API endpoint"))
	})
	m.HandleFunc("POST /share-target", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/share?error=no-sw", http.StatusSeeOther)
	})
	m.HandleFunc("/", s.spa)
}

// --- SPA ---

func (s *Server) spa(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/stream/") || strings.HasPrefix(r.URL.Path, "/share-target") {
		http.NotFound(w, r)
		return
	}
	if s.SPA == nil {
		http.Error(w, "frontend not built; run make frontend", http.StatusServiceUnavailable)
		return
	}
	if _, err := fs.Stat(s.SPA, "index.html"); err != nil {
		http.Error(w, "frontend not built; run make frontend", http.StatusServiceUnavailable)
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name != "" && name != "index.html" {
		if fi, err := fs.Stat(s.SPA, name); err == nil && !fi.IsDir() {
			switch {
			case strings.HasPrefix(name, "_app/immutable/"):
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			case name == "service-worker.js" || name == "sw.js" || name == "manifest.webmanifest":
				w.Header().Set("Cache-Control", "no-cache")
			}
			if strings.HasSuffix(name, ".webmanifest") {
				w.Header().Set("Content-Type", "application/manifest+json")
			}
			http.ServeFileFS(w, r, s.SPA, name) //nolint:gosec // name is path.Clean'ed and served from the embedded FS
			return
		}
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data, err := fs.ReadFile(s.SPA, "index.html")
	if err != nil {
		http.Error(w, "frontend not built; run make frontend", http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write(data) //nolint:gosec // the embedded index.html, not user input
}

// --- meta ---

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if err := s.DB.Ping(r.Context()); err != nil {
		http.Error(w, "db unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) settings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":              s.Version,
		"shareDefaultExpiryMs": s.Cfg.ShareDefaultExpiry().Milliseconds(),
		"streamLinkTtlMs":      s.Cfg.StreamLinks.TTL.D().Milliseconds(),
		"smartEnabled":         s.Smart.Enabled(),
		"capabilities":         s.Tools.Caps.JSON(s.Smart.Enabled()),
	})
}

// events streams Server-Sent Events.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	rc := http.NewResponseController(w)
	ch := s.Hub.Subscribe(r.Context())
	_, _ = w.Write([]byte("retry: 3000\n\n"))
	_, _ = w.Write(events.Frame(events.Event{Type: "hello", Data: map[string]any{"version": s.Version, "serverTime": time.Now().UnixMilli()}}))
	_ = rc.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			_ = rc.Flush()
		case e, ok := <-ch:
			if !ok {
				return
			}
			if _, err := w.Write(events.Frame(e)); err != nil {
				return
			}
			_ = rc.Flush()
		}
	}
}
