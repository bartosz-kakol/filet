package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"filet/internal/apierr"
	"filet/internal/auth"
	"filet/internal/clock"
	"filet/internal/jobs"
	"filet/internal/media"
	"filet/internal/places"
	"filet/internal/share"
	"filet/internal/uploads"
)

func openCache(path string) (*os.File, error) { return os.Open(path) } //nolint:gosec // cache path derived from a hash

// --- auth ---

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return ip
	}
	return r.RemoteAddr
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, limitJSON, &body); err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	tok, err := s.Auth.Login(r.Context(), body.Username, body.Password, r.UserAgent(), clientIP(r))
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	http.SetCookie(w, s.Auth.Cookie(tok, auth.SessionMaxAge))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.SessionCookie); err == nil {
		_ = s.Auth.Logout(r.Context(), c.Value)
	}
	http.SetCookie(w, s.Auth.Cookie("", 0))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"authenticated": false}
	if c, err := r.Cookie(auth.SessionCookie); err == nil {
		if exp, renewed, err := s.Auth.Authenticate(r.Context(), c.Value); err == nil {
			out["authenticated"] = true
			out["expiresAt"] = exp
			if renewed {
				http.SetCookie(w, s.Auth.Cookie(c.Value, auth.SessionMaxAge))
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

// --- mkdir / rename ---

func (s *Server) mkdir(w http.ResponseWriter, r *http.Request) {
	p, ok := s.place(w, r)
	if !ok {
		return
	}
	var body struct {
		Path    string `json:"path"`
		Parents bool   `json:"parents"`
	}
	if err := decodeJSON(w, r, limitJSON, &body); err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	e, err := s.Ops.Mkdir(r.Context(), p, body.Path, body.Parents)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	writeJSON(w, http.StatusCreated, e)
}

func (s *Server) rename(w http.ResponseWriter, r *http.Request) {
	p, ok := s.place(w, r)
	if !ok {
		return
	}
	var body struct {
		Path    string `json:"path"`
		NewName string `json:"newName"`
	}
	if err := decodeJSON(w, r, limitJSON, &body); err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	e, err := s.Ops.Rename(r.Context(), p, body.Path, body.NewName)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// --- trash ---

type trashJSON struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	OriginalPath  string `json:"originalPath"`
	IsDir         bool   `json:"isDir"`
	Size          *int64 `json:"size"`
	DeletedAt     int64  `json:"deletedAt"`
	OriginUnknown bool   `json:"originUnknown"`
}

func (s *Server) trashList(w http.ResponseWriter, r *http.Request) {
	p, ok := s.place(w, r)
	if !ok {
		return
	}
	if err := p.Writable(); err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	rows, err := s.DB.R.ListTrashItems(r.Context(), p.ID)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	out := make([]trashJSON, len(rows))
	for i, row := range rows {
		out[i] = trashJSON{
			ID: row.ID, Name: row.Name, OriginalPath: row.OriginalPath, IsDir: row.IsDir == 1,
			DeletedAt: row.DeletedAt, OriginUnknown: row.OriginUnknown == 1,
		}
		if row.Size.Valid {
			out[i].Size = &row.Size.Int64
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) trashJob(w http.ResponseWriter, r *http.Request, typ string) {
	p, ok := s.place(w, r)
	if !ok {
		return
	}
	var body struct {
		IDs []string `json:"ids"`
		All bool     `json:"all"`
	}
	if err := decodeJSON(w, r, limitJSON, &body); err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	j, err := s.Jobs.Submit(r.Context(), jobs.Spec{Type: typ, Place: p.ID, IDs: body.IDs, All: body.All && typ == "purge"})
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	writeJSON(w, http.StatusCreated, j)
}

func (s *Server) trashRestore(w http.ResponseWriter, r *http.Request) { s.trashJob(w, r, "restore") }
func (s *Server) trashPurge(w http.ResponseWriter, r *http.Request)   { s.trashJob(w, r, "purge") }

// --- uploads ---

func (s *Server) preflight(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Items []uploads.PreflightItem `json:"items"`
	}
	if err := decodeJSON(w, r, limitPreflight, &body); err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	if len(body.Items) > 10000 {
		writeError(w, r, s.Log, apierr.Invalid("items", "At most 10,000 items per request"))
		return
	}
	res, err := s.Uploads.Preflight(r.Context(), body.Items)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": res})
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var req uploads.RegisterRequest
	if err := decodeJSON(w, r, limitRegister, &req); err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	res, err := s.Uploads.Register(r.Context(), req)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	writeJSON(w, res.HTTPStatus(), res)
}

func (s *Server) putChunk(w http.ResponseWriter, r *http.Request) {
	index, err := strconv.ParseInt(r.PathValue("index"), 10, 64)
	if err != nil {
		writeError(w, r, s.Log, uploads.ErrChunkIndex)
		return
	}
	final := r.Header.Get("Filet-Final") == "1"
	res, err := s.Uploads.PutChunk(r.Context(), r.PathValue("id"), index, final, r.Body, r.ContentLength)
	if errors.Is(err, uploads.ErrClientGone) {
		panic(http.ErrAbortHandler)
	}
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) getUpload(w http.ResponseWriter, r *http.Request) {
	info, err := s.Uploads.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) cancelUpload(w http.ResponseWriter, r *http.Request) {
	if err := s.Uploads.Cancel(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) leftovers(w http.ResponseWriter, r *http.Request) {
	l, err := s.Uploads.Leftovers(r.Context())
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) leftoverCount(w http.ResponseWriter, r *http.Request) {
	n, err := s.Uploads.LeftoverCount(r.Context())
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"count": n})
}

func (s *Server) removeLeftovers(w http.ResponseWriter, r *http.Request) {
	n, err := s.Uploads.RemoveLeftovers(r.Context())
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"removed": n})
}

// --- jobs ---

func (s *Server) submitJob(w http.ResponseWriter, r *http.Request) {
	var spec jobs.Spec
	if err := decodeJSON(w, r, limitJSON, &spec); err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	j, err := s.Jobs.Submit(r.Context(), spec)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	writeJSON(w, http.StatusCreated, j)
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	l, err := s.Jobs.List(r.Context())
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.Jobs.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.Jobs.Cancel(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) resolveJob(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ConflictID string `json:"conflictId"`
		Action     string `json:"action"`
		ApplyToAll bool   `json:"applyToAll"`
	}
	if err := decodeJSON(w, r, limitJSON, &body); err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	j, err := s.Jobs.Resolve(r.Context(), r.PathValue("id"), body.ConflictID, body.Action, body.ApplyToAll)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) dismissJob(w http.ResponseWriter, r *http.Request) {
	if err := s.Jobs.Dismiss(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- stream links ---

func (s *Server) createStreamLink(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Place string `json:"place"`
		Path  string `json:"path"`
	}
	if err := decodeJSON(w, r, limitJSON, &body); err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	p, err := s.Reg.Get(body.Place)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	rel, err := places.ValidateRelPath(body.Path)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	_, name := places.Split(rel)
	if !media.IsMedia(name) {
		writeError(w, r, s.Log, apierr.ErrNotMedia)
		return
	}
	f, _, err := p.FullView().OpenRegular(rel)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	_ = f.Close()
	exp := clock.Now().Add(s.Cfg.StreamLinks.TTL.D())
	tok := s.Auth.StreamToken(p.ID, rel, exp)
	writeJSON(w, http.StatusCreated, map[string]any{
		"url": "/stream/" + tok + "/" + url.PathEscape(name), "expiresAt": exp.UnixMilli(),
	})
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	notFound := func() { w.WriteHeader(http.StatusNotFound) }
	placeID, rel, ok := s.Auth.ParseStreamToken(r.PathValue("token"))
	if !ok {
		notFound()
		return
	}
	p, err := s.Reg.Get(placeID)
	if err != nil {
		notFound()
		return
	}
	if rel, err = places.ValidateRelPath(rel); err != nil {
		notFound()
		return
	}
	f, fi, err := p.FullView().OpenRegular(rel)
	if err != nil {
		notFound()
		return
	}
	defer f.Close()
	_, name := places.Split(rel)
	setUserFileHeaders(w, name, false)
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

// --- share links ---

func (s *Server) listShareLinks(w http.ResponseWriter, r *http.Request) {
	l, err := s.Share.List(r.Context())
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) createShareLink(w http.ResponseWriter, r *http.Request) {
	var req share.CreateRequest
	if err := decodeJSON(w, r, limitJSON, &req); err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	l, err := s.Share.Create(r.Context(), req)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	writeJSON(w, http.StatusCreated, l)
}

func (s *Server) deleteShareLink(w http.ResponseWriter, r *http.Request) {
	if err := s.Share.Delete(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- public share API ---

func (s *Server) public(h viewHandler, fromPathParam bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, err := s.Share.Resolve(r.Context(), r.PathValue("token"), r)
		if err != nil {
			writeError(w, r, s.Log, err)
			return
		}
		defer a.Close()
		if a.NeedsPassword() {
			writeError(w, r, s.Log, share.ErrLocked)
			return
		}
		rel := queryPath(r)
		if fromPathParam {
			rel = pathParam(r)
		}
		h(w, r, a.View, rel)
	}
}

func (s *Server) publicInfo(w http.ResponseWriter, r *http.Request) {
	a, err := s.Share.Resolve(r.Context(), r.PathValue("token"), r)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	defer a.Close()
	s.Share.Touch(r.Context(), a.Link.ID)
	fi, err := a.Place.Root.Stat(places.RootRel(a.Link.Path))
	if err != nil {
		writeError(w, r, s.Log, share.ErrInvalidLink)
		return
	}
	out := map[string]any{
		"name": a.Name, "isDir": a.Link.IsDir == 1, "size": fi.Size(), "mtime": fi.ModTime().UnixMilli(),
		"kind": places.KindDir, "expiresAt": nil, "passwordRequired": a.Link.PasswordHash != nil, "unlocked": a.Unlocked,
	}
	if a.Link.IsDir == 1 {
		out["size"] = 0
	} else {
		out["kind"] = places.EffectiveKind(a.Name, &s.Tools.Caps)
		out["mime"] = places.MimeOf(a.Name)
		out["thumbable"] = places.Thumbable(a.Name, &s.Tools.Caps)
	}
	if a.Link.ExpiresAt.Valid {
		out["expiresAt"] = a.Link.ExpiresAt.Int64
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) publicUnlock(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, limitJSON, &body); err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	a, err := s.Share.Resolve(r.Context(), r.PathValue("token"), r)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	defer a.Close()
	c, err := s.Share.Unlock(a, body.Password)
	if err != nil {
		writeError(w, r, s.Log, err)
		return
	}
	http.SetCookie(w, c)
	w.WriteHeader(http.StatusNoContent)
}
