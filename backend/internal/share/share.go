// Package share manages expiring, optionally password-protected read-only
// share links and resolves public share requests to confined views.
package share

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"time"

	"filet/internal/apierr"
	"filet/internal/auth"
	"filet/internal/clock"
	"filet/internal/db"
	"filet/internal/db/gen"
	"filet/internal/ids"
	"filet/internal/places"
)

const maxExpiry = 3650 * 24 * time.Hour

// Errors.
var (
	ErrInvalidLink     = apierr.New(http.StatusNotFound, "not_found", "This link is invalid or has expired.")
	ErrInvalidPassword = apierr.New(http.StatusUnauthorized, "invalid_password", "Incorrect password")
	ErrLocked          = apierr.New(http.StatusUnauthorized, "password_required", "This link is password protected")
)

// Service is the share-link service.
type Service struct {
	db   *db.DB
	reg  *places.Registry
	auth *auth.Service
}

// New creates the service.
func New(d *db.DB, reg *places.Registry, a *auth.Service) *Service {
	return &Service{db: d, reg: reg, auth: a}
}

// Link is the JSON share-link object.
type Link struct {
	ID             string `json:"id"`
	URL            string `json:"url"`
	Place          string `json:"place"`
	PlaceName      string `json:"placeName"`
	PlaceMissing   bool   `json:"placeMissing"`
	Path           string `json:"path"`
	IsDir          bool   `json:"isDir"`
	ExpiresAt      *int64 `json:"expiresAt"`
	HasPassword    bool   `json:"hasPassword"`
	CreatedAt      int64  `json:"createdAt"`
	LastAccessedAt *int64 `json:"lastAccessedAt"`
	AccessCount    int64  `json:"accessCount"`
	TargetExists   bool   `json:"targetExists"`
}

func (s *Service) toLink(row gen.ShareLink, checkTarget bool) Link {
	l := Link{
		ID: row.ID, URL: "/s/" + s.auth.ShareToken(row.ID), Place: row.PlaceID, Path: row.Path, IsDir: row.IsDir == 1,
		HasPassword: row.PasswordHash != nil, CreatedAt: row.CreatedAt, AccessCount: row.AccessCount,
	}
	if row.ExpiresAt.Valid {
		l.ExpiresAt = &row.ExpiresAt.Int64
	}
	if row.LastAccessedAt.Valid {
		l.LastAccessedAt = &row.LastAccessedAt.Int64
	}
	p, err := s.reg.Get(row.PlaceID)
	if err != nil {
		l.PlaceMissing = true
		return l
	}
	l.PlaceName = p.Name
	if checkTarget {
		fi, err := p.Root.Stat(places.RootRel(row.Path))
		l.TargetExists = err == nil && fi.IsDir() == l.IsDir
	}
	return l
}

// CreateRequest is the body of POST /api/share-links.
type CreateRequest struct {
	Place     string  `json:"place"`
	Path      string  `json:"path"`
	ExpiresAt *int64  `json:"expiresAt"`
	Password  *string `json:"password"`
}

// Create creates a share link.
func (s *Service) Create(ctx context.Context, req CreateRequest) (Link, error) {
	p, err := s.reg.Get(req.Place)
	if err != nil {
		return Link{}, err
	}
	rel, err := places.ValidateRelPath(req.Path)
	if err != nil {
		return Link{}, err
	}
	v := p.FullView()
	e, err := v.StatEntry(rel)
	if err != nil {
		return Link{}, err
	}
	if !e.IsOpenable() || (e.Type != "file" && e.Type != "dir") {
		return Link{}, apierr.Invalid("path", "This item cannot be shared")
	}
	now := clock.Now()
	var exp sql.NullInt64
	if req.ExpiresAt != nil {
		t := time.UnixMilli(*req.ExpiresAt)
		if !t.After(now) || t.Sub(now) > maxExpiry {
			return Link{}, apierr.Invalid("expiresAt", "Expiry must be in the future and at most 10 years ahead")
		}
		exp = sql.NullInt64{Int64: *req.ExpiresAt, Valid: true}
	}
	var hash, salt []byte
	if req.Password != nil {
		n := len([]rune(*req.Password))
		if n < 1 || n > 256 {
			return Link{}, apierr.Invalid("password", "Password must be 1–256 characters")
		}
		salt = ids.RandomBytes(16)
		hash = auth.Argon2([]byte(*req.Password), salt)
	}
	isDir := int64(0)
	if e.Type == "dir" {
		isDir = 1
	}
	row := gen.ShareLink{
		ID: auth.NewShareID(), PlaceID: p.ID, Path: rel, IsDir: isDir, PasswordHash: hash, PasswordSalt: salt,
		ExpiresAt: exp, CreatedAt: now.UnixMilli(),
	}
	if err := s.db.W.InsertShareLink(ctx, gen.InsertShareLinkParams{
		ID: row.ID, PlaceID: row.PlaceID, Path: row.Path, IsDir: row.IsDir, PasswordHash: row.PasswordHash,
		PasswordSalt: row.PasswordSalt, ExpiresAt: row.ExpiresAt, CreatedAt: row.CreatedAt,
	}); err != nil {
		return Link{}, err
	}
	l := s.toLink(row, false)
	l.TargetExists = true
	return l, nil
}

// List returns all non-expired links, newest first.
func (s *Service) List(ctx context.Context) ([]Link, error) {
	rows, err := s.db.R.ListShareLinks(ctx, sql.NullInt64{Int64: clock.NowMs(), Valid: true})
	if err != nil {
		return nil, err
	}
	out := make([]Link, len(rows))
	for i, r := range rows {
		out[i] = s.toLink(r, true)
	}
	return out, nil
}

// Delete revokes a link.
func (s *Service) Delete(ctx context.Context, id string) error {
	n, err := s.db.W.DeleteShareLink(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return apierr.ErrNotFound
	}
	return nil
}

// Sweep deletes expired links.
func (s *Service) Sweep(ctx context.Context) {
	_, _ = s.db.W.DeleteExpiredShareLinks(ctx, sql.NullInt64{Int64: clock.NowMs(), Valid: true})
}

// Access is a resolved public share request.
type Access struct {
	Link     gen.ShareLink
	Place    *places.Place
	View     *places.View
	Unlocked bool
	Name     string
	root     *os.Root
}

// Close releases the share sub-root.
func (a *Access) Close() {
	if a.root != nil {
		_ = a.root.Close()
	}
}

// NeedsPassword reports whether the request still has to unlock the link.
func (a *Access) NeedsPassword() bool { return a.Link.PasswordHash != nil && !a.Unlocked }

// Resolve checks token MAC → row → expiry → unlock cookie → target and
// opens a confined view. The MAC is verified before touching SQLite.
func (s *Service) Resolve(ctx context.Context, token string, r *http.Request) (*Access, error) {
	id, ok := s.auth.ParseShareToken(token)
	if !ok {
		return nil, ErrInvalidLink
	}
	row, err := s.db.R.GetShareLink(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidLink
	}
	if err != nil {
		return nil, err
	}
	if row.ExpiresAt.Valid && row.ExpiresAt.Int64 <= clock.NowMs() {
		return nil, ErrInvalidLink
	}
	p, err := s.reg.Get(row.PlaceID)
	if err != nil {
		return nil, ErrInvalidLink
	}
	a := &Access{Link: row, Place: p}
	if row.PasswordHash != nil && r != nil {
		if c, err := r.Cookie(auth.ShareCookieName(row.ID)); err == nil && s.auth.VerifyShareCookie(row.ID, c.Value) {
			a.Unlocked = true
		}
	}
	fi, err := p.Root.Stat(places.RootRel(row.Path))
	if err != nil || fi.IsDir() != (row.IsDir == 1) {
		return nil, ErrInvalidLink
	}
	if row.IsDir == 1 {
		root, err := p.Root.OpenRoot(places.RootRel(row.Path))
		if err != nil {
			return nil, ErrInvalidLink
		}
		a.root = root
		a.View = &places.View{Place: p, Root: root, Base: row.Path, Public: true}
		a.Name = fi.Name()
		if row.Path == "/" {
			a.Name = p.Name
		}
	} else {
		dir, name := places.Split(row.Path)
		root, err := p.Root.OpenRoot(places.RootRel(dir))
		if err != nil {
			return nil, ErrInvalidLink
		}
		a.root = root
		a.View = &places.View{Place: p, Root: root, Base: dir, Public: true, OnlyName: name}
		a.Name = name
	}
	return a, nil
}

// Touch records an access.
func (s *Service) Touch(ctx context.Context, id string) {
	_ = s.db.W.TouchShareLink(ctx, gen.TouchShareLinkParams{LastAccessedAt: sql.NullInt64{Int64: clock.NowMs(), Valid: true}, ID: id})
}

// Unlock checks a password and returns the unlock cookie.
func (s *Service) Unlock(a *Access, password string) (*http.Cookie, error) {
	lock := s.auth.ShareLockout(a.Link.ID)
	if rem := lock.Locked(); rem > 0 {
		return nil, auth.LockedError(rem)
	}
	if a.Link.PasswordHash == nil {
		return nil, apierr.Invalid("password", "This link has no password")
	}
	got := auth.Argon2([]byte(password), a.Link.PasswordSalt)
	if subtle.ConstantTimeCompare(got, a.Link.PasswordHash) != 1 {
		lock.Fail()
		return nil, ErrInvalidPassword
	}
	exp := clock.Now().Add(24 * time.Hour)
	if a.Link.ExpiresAt.Valid && time.UnixMilli(a.Link.ExpiresAt.Int64).Before(exp) {
		exp = time.UnixMilli(a.Link.ExpiresAt.Int64)
	}
	return &http.Cookie{ //nolint:gosec // Secure follows auth.secure_cookies, as for the session cookie
		Name: auth.ShareCookieName(a.Link.ID), Value: s.auth.ShareCookieValue(a.Link.ID, exp), Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.auth.SecureCookies(), Expires: exp,
	}, nil
}
