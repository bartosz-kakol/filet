package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"filet/internal/apierr"
	"filet/internal/archive"
	"filet/internal/fsops"
	"filet/internal/fsx"
	"filet/internal/places"
	"filet/internal/smart"
	"filet/internal/thumbs"
	"filet/internal/uploads"
)

type viewHandler func(w http.ResponseWriter, r *http.Request, v *places.View, rel string)

func (s *Server) place(w http.ResponseWriter, r *http.Request) (*places.Place, bool) {
	p, err := s.Reg.Get(r.PathValue("place"))
	if err != nil {
		writeError(w, r, s.Log, err)
		return nil, false
	}
	return p, true
}

// withPlace serves a whole-place view with the path from ?path=.
func (s *Server) withPlace(h viewHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if p, ok := s.place(w, r); ok {
			h(w, r, p.FullView(), queryPath(r))
		}
	}
}

// withPlacePath serves a whole-place view with the path from {path...}.
func (s *Server) withPlacePath(h viewHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if p, ok := s.place(w, r); ok {
			h(w, r, p.FullView(), pathParam(r))
		}
	}
}

// --- places ---

type placeJSON struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	ReadOnly       bool           `json:"readonly"`
	TrashAvailable bool           `json:"trashAvailable"`
	Disk           *fsx.DiskUsage `json:"disk"`
	Smart          *smart.Summary `json:"smart"`
}

func (s *Server) listPlaces(w http.ResponseWriter, r *http.Request) {
	out := []placeJSON{}
	for _, p := range s.Reg.All() {
		pj := placeJSON{ID: p.ID, Name: p.Name, ReadOnly: p.ReadOnly, TrashAvailable: p.HasTrash}
		if du, err := p.DiskUsage(); err == nil {
			pj.Disk = &du
		}
		pj.Smart = s.Smart.PlaceSummary(r.Context(), p.ID)
		out = append(out, pj)
	}
	writeJSON(w, http.StatusOK, out)
}

type listJSON struct {
	*places.Listing
	Uploads []uploads.Placeholder `json:"uploads"`
}

func (s *Server) list(w http.ResponseWriter, r *http.Request, v *places.View, rel string) {
	rel, err := v.Check(rel)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	if v.OnlyName != "" {
		writeError(w, r, s.Log, apierr.ErrNotADirectory)
		return
	}
	l, err := v.List(r.Context(), rel)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	out := listJSON{Listing: l, Uploads: []uploads.Placeholder{}}
	if !v.Public {
		if out.Uploads, err = s.Uploads.InDir(r.Context(), v.Place.ID, rel); err != nil {
			writeError(w, r, s.Log, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, out)
	s.Thumbs.EnqueueListing(v, rel, l.Entries)
}

func (s *Server) stat(w http.ResponseWriter, r *http.Request, v *places.View, rel string) {
	rel, err := v.Check(rel)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	e, err := v.StatEntry(rel)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// --- raw files ---

func etag(id fsx.Identity) string {
	return fmt.Sprintf(`"%x-%x-%x"`, id.Ino, id.Size, id.MtimeNs)
}

func (s *Server) raw(w http.ResponseWriter, r *http.Request, v *places.View, rel string) {
	rel, err := v.Check(rel)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	f, fi, err := v.OpenRegular(rel)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	defer f.Close()
	_, name := places.Split(v.PlacePath(rel))
	setUserFileHeaders(w, name, boolQuery(r, "download"))
	w.Header().Set("ETag", etag(fsx.IdentityOf(fi)))
	w.Header().Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

func (s *Server) zip(w http.ResponseWriter, r *http.Request, v *places.View, _ string) {
	r.Body = http.MaxBytesReader(w, r.Body, limitJSON)
	if err := r.ParseForm(); err != nil {
		writeError(w, r, s.Log, apierr.Invalid("dir", "Malformed form"))
		return
	}
	dir := r.PostForm.Get("dir")
	if dir == "" {
		dir = "/"
	}
	dir, names, err := fsops.ValidateZip(v, dir, r.PostForm["name"])
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", contentDisposition("attachment", fsops.ZipName(v, dir, names)))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if err := fsops.WriteZip(r.Context(), w, v, dir, names); err != nil && r.Context().Err() == nil {
		s.Log.Warn("zip stream failed", "err", err)
		panic(http.ErrAbortHandler) // truncate the download visibly
	}
}

// --- search ---

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	p, ok := s.place(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	terms, err := fsops.SearchTerms(q.Get("q"))
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	limit := fsops.SearchDefaultLimit
	if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 {
		limit = min(l, fsops.SearchMaxLimit)
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	var mu sync.Mutex
	pending := 0
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	flush := func() {
		if pending > 0 {
			_ = rc.Flush()
			pending = 0
		}
	}
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				mu.Lock()
				flush()
				mu.Unlock()
			}
		}
	}()
	done := fsops.Search(r.Context(), p, terms, boolQuery(r, "hidden"), limit, func(hit fsops.SearchHit) {
		mu.Lock()
		defer mu.Unlock()
		_ = enc.Encode(hit)
		pending++
		if pending >= 50 {
			flush()
		}
	})
	close(stop)
	mu.Lock()
	defer mu.Unlock()
	_ = enc.Encode(done)
	_ = rc.Flush()
}

// --- thumbnails & renditions ---

func (s *Server) thumb(w http.ResponseWriter, r *http.Request, v *places.View, rel string) {
	rel, err := v.Check(rel)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	file, err := s.Thumbs.Thumbnail(r.Context(), v, rel, thumbs.Interactive)
	if err != nil {
		if apierr.Is(err, "no_thumbnail") {
			w.Header().Set("Cache-Control", "private, max-age=86400")
		}
		writeError(w, r, s.Log, err)
		return
	}
	s.serveCached(w, r, file, "image/webp", thumbs.SuffixThumb, v.Public)
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, v *places.View, rel string) {
	rel, err := v.Check(rel)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	file, err := s.Thumbs.Rendition(r.Context(), v, rel)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	s.serveCached(w, r, file, "image/jpeg", thumbs.SuffixRendition, v.Public)
}

func (s *Server) serveCached(w http.ResponseWriter, r *http.Request, file, ctype, suffix string, public bool) {
	f, err := openCache(file)
	if err != nil {
		writeError(w, r, s.Log, apierr.ErrNoThumbnail)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Set("ETag", `"`+strings.TrimSuffix(filepath.Base(file), suffix)+`"`)
	if public {
		h.Set("Cache-Control", "private, max-age=3600")
	} else {
		h.Set("Cache-Control", "private, max-age=31536000, immutable")
	}
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

// --- media ---

func (s *Server) mediaInfo(w http.ResponseWriter, r *http.Request, v *places.View, rel string) {
	rel, err := v.Check(rel)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	info, err := s.Media.Info(r.Context(), v, rel)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func intQuery(r *http.Request, key string, def int) int {
	if n, err := strconv.Atoi(r.URL.Query().Get(key)); err == nil {
		return n
	}
	return def
}

func (s *Server) remux(w http.ResponseWriter, r *http.Request, v *places.View, rel string) {
	rel, err := v.Check(rel)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	start, _ := strconv.ParseFloat(r.URL.Query().Get("start"), 64)
	if start < 0 {
		start = 0
	}
	video, audio := intQuery(r, "video", -1), intQuery(r, "audio", -1)
	rc := http.NewResponseController(w)
	err = s.Media.Remux(r.Context(), v, rel, start, video, audio, w, func() {
		h := w.Header()
		if video < 0 {
			h.Set("Content-Type", "audio/mp4")
		} else {
			h.Set("Content-Type", "video/mp4")
		}
		h.Set("Accept-Ranges", "none")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
	}, func() { _ = rc.Flush() })
	if err != nil {
		writeError(w, r, s.Log, err)
	}
}

func (s *Server) subtitle(w http.ResponseWriter, r *http.Request, v *places.View, rel string) {
	rel, err := v.Check(rel)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	q := r.URL.Query()
	stream := -1
	if q.Has("stream") {
		if stream, err = strconv.Atoi(q.Get("stream")); err != nil {
			writeError(w, r, s.Log, apierr.Invalid("stream", "Invalid stream index"))
			return
		}
	}
	sidecar := q.Get("sidecar")
	if stream < 0 && sidecar == "" {
		writeError(w, r, s.Log, apierr.Invalid("stream", "stream or sidecar is required"))
		return
	}
	if v.Public && strings.HasPrefix(sidecar, ".") {
		writeError(w, r, s.Log, apierr.ErrNotFound)
		return
	}
	data, err := s.Media.Subtitle(r.Context(), v, rel, stream, sidecar)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-cache")
	_, _ = w.Write(data) //nolint:gosec // served as text/vtt with nosniff, never rendered as HTML
}

// --- text & archives ---

func (s *Server) textIndex(w http.ResponseWriter, r *http.Request, v *places.View, rel string) {
	rel, err := v.Check(rel)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	ix, err := s.Text.Index(r.Context(), v, rel)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, ix)
}

func (s *Server) archiveList(w http.ResponseWriter, r *http.Request, v *places.View, rel string) {
	rel, err := v.Check(rel)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	l, err := s.Archive.List(r.Context(), v, rel)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) archiveEntry(w http.ResponseWriter, r *http.Request, v *places.View, rel string) {
	rel, err := v.Check(rel)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	entry := r.URL.Query().Get("entry")
	if entry == "" {
		writeError(w, r, s.Log, apierr.Invalid("entry", "entry is required"))
		return
	}
	download := boolQuery(r, "download")
	rc, size, err := s.Archive.OpenEntry(r.Context(), v, rel, entry)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	defer rc.Close()
	if !download && size > archive.MaxInline {
		writeError(w, r, s.Log, apierr.ErrTooLarge.With("maxInline", archive.MaxInline))
		return
	}
	name := entry
	if i := strings.LastIndexByte(entry, '/'); i >= 0 {
		name = entry[i+1:]
	}
	setUserFileHeaders(w, name, download)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Cache-Control", "private, no-cache")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, rc); err != nil && r.Context().Err() == nil {
		if apierr.Is(err, "entry_encrypted") {
			s.Log.Debug("encrypted 7z entry", "entry", entry)
		}
		panic(http.ErrAbortHandler)
	}
}

// --- SMART ---

func (s *Server) smartDetail(w http.ResponseWriter, r *http.Request) {
	p, ok := s.place(w, r)
	if !ok {
		return
	}
	if !s.Smart.Enabled() {
		writeError(w, r, s.Log, apierr.ToolUnavailable("smartctl"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": s.Smart.Detail(r.Context(), p.ID), "summary": s.Smart.PlaceSummary(r.Context(), p.ID)})
}

func (s *Server) smartRefresh(w http.ResponseWriter, r *http.Request) {
	p, ok := s.place(w, r)
	if !ok {
		return
	}
	if err := s.Smart.Refresh(p.ID); err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
