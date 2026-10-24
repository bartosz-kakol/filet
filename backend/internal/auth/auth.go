// Package auth implements the single-user login, sessions, the login
// lockout, the credential fingerprint and the HMAC-signed stream and share
// tokens.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"filet/internal/apierr"
	"filet/internal/clock"
	"filet/internal/db"
	"filet/internal/db/gen"
	"filet/internal/ids"
)

const (
	// SessionCookie is the session cookie name.
	SessionCookie = "filet_session"
	sessionTTL    = 30 * 24 * time.Hour
	renewBelow    = 29 * 24 * time.Hour
	cacheTTL      = 60 * time.Second
)

// Options configures the service.
type Options struct {
	Username, Password string
	LockoutEnabled     bool
	LockoutDuration    time.Duration
	SecureCookies      bool
}

// Service is the auth service.
type Service struct {
	db   *db.DB
	opt  Options
	log  *slog.Logger
	user [32]byte
	pass [32]byte

	fingerprint  []byte
	kStream      []byte
	kShare       []byte
	kShareCookie []byte

	login *Lockout

	cacheMu sync.Mutex
	cache   map[[32]byte]cacheEntry

	shareLockMu sync.Mutex
	shareLocks  map[string]*Lockout
}

type cacheEntry struct {
	expiresAt int64 // session expiry (ms)
	until     time.Time
}

// New creates the service. Call Init before use.
func New(d *db.DB, opt Options, log *slog.Logger) *Service {
	return &Service{
		db: d, opt: opt, log: log,
		user:       sha256.Sum256([]byte(opt.Username)),
		pass:       sha256.Sum256([]byte(opt.Password)),
		login:      NewLockout(opt.LockoutEnabled, opt.LockoutDuration),
		cache:      map[[32]byte]cacheEntry{},
		shareLocks: map[string]*Lockout{},
	}
}

// Argon2 hashes secret with Filet's argon2id parameters.
func Argon2(secret, salt []byte) []byte {
	return argon2.IDKey(secret, salt, 2, 64*1024, 2, 32)
}

// Init computes the credential fingerprint, invalidates sessions if it
// changed, and loads or creates the HMAC master secret.
func (s *Service) Init(ctx context.Context) error {
	salt, err := s.setting(ctx, "credential_salt", func() []byte { return ids.RandomBytes(16) })
	if err != nil {
		return err
	}
	fp := Argon2([]byte(s.opt.Username+"\x00"+s.opt.Password), salt)
	stored, err := s.db.W.GetSetting(ctx, "credential_fingerprint")
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if !hmac.Equal(stored, fp) {
		if err := s.db.W.DeleteAllSessions(ctx); err != nil {
			return err
		}
		if err := s.db.W.SetSetting(ctx, gen.SetSettingParams{Key: "credential_fingerprint", Value: fp}); err != nil {
			return err
		}
		if stored != nil {
			s.log.Warn("credentials changed; all sessions invalidated")
		}
		s.purgeCache()
	}
	s.fingerprint = fp
	master, err := s.setting(ctx, "hmac_master", func() []byte { return ids.RandomBytes(32) })
	if err != nil {
		return err
	}
	s.kStream = mac(master, append([]byte("filet/stream/v1"), fp...))
	s.kShare = mac(master, []byte("filet/share/v1"))
	s.kShareCookie = mac(master, []byte("filet/share-cookie/v1"))
	return nil
}

func (s *Service) setting(ctx context.Context, key string, gen0 func() []byte) ([]byte, error) {
	v, err := s.db.W.GetSetting(ctx, key)
	if err == nil {
		return v, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	v = gen0()
	return v, s.db.W.SetSetting(ctx, gen.SetSettingParams{Key: key, Value: v})
}

func mac(key, msg []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(msg)
	return h.Sum(nil)
}

// --- login ---

// Lockout is a "fail → locked for duration" gate.
type Lockout struct {
	enabled bool
	d       time.Duration
	mu      sync.Mutex
	until   time.Time
}

// NewLockout creates a lockout.
func NewLockout(enabled bool, d time.Duration) *Lockout { return &Lockout{enabled: enabled, d: d} }

// Locked returns the remaining lock time, or 0.
func (l *Lockout) Locked() time.Duration {
	if !l.enabled {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if rem := time.Until(l.until); rem > 0 {
		return rem
	}
	return 0
}

// Fail records an evaluated failure.
func (l *Lockout) Fail() {
	if !l.enabled {
		return
	}
	l.mu.Lock()
	l.until = time.Now().Add(l.d)
	l.mu.Unlock()
}

// LockedError builds the 429 login_locked error.
func LockedError(rem time.Duration) *apierr.Error {
	e := apierr.New(http.StatusTooManyRequests, "login_locked", "Too many attempts; try again shortly").
		With("retryAfterMs", rem.Milliseconds())
	e.RetryAfter = int(math.Ceil(rem.Seconds()))
	return e
}

// Login checks credentials and creates a session, returning the raw token.
func (s *Service) Login(ctx context.Context, username, password, ua, remote string) (string, error) {
	if rem := s.login.Locked(); rem > 0 {
		return "", LockedError(rem)
	}
	u := sha256.Sum256([]byte(username))
	p := sha256.Sum256([]byte(password))
	okU := subtle.ConstantTimeCompare(u[:], s.user[:])
	okP := subtle.ConstantTimeCompare(p[:], s.pass[:])
	if okU&okP != 1 {
		s.login.Fail()
		return "", apierr.New(http.StatusUnauthorized, "invalid_credentials", "Incorrect username or password")
	}
	tok := ids.RandomBytes(32)
	h := sha256.Sum256(tok)
	now := time.Now()
	if len(ua) > 512 {
		ua = ua[:512]
	}
	err := s.db.W.CreateSession(ctx, gen.CreateSessionParams{
		TokenHash: h[:], CreatedAt: now.UnixMilli(), ExpiresAt: now.Add(sessionTTL).UnixMilli(),
		LastSeenAt: now.UnixMilli(), UserAgent: ua, RemoteAddr: remote,
	})
	if err != nil {
		return "", err
	}
	return ids.B64(tok), nil
}

// Authenticate validates a session token. renewed is true when the session
// was extended and the cookie should be re-sent.
func (s *Service) Authenticate(ctx context.Context, token string) (expiresAt int64, renewed bool, err error) {
	raw, err := ids.FromB64(token)
	if err != nil || len(raw) != 32 {
		return 0, false, apierr.ErrUnauthenticated
	}
	h := sha256.Sum256(raw)
	now := time.Now()
	s.cacheMu.Lock()
	ce, ok := s.cache[h]
	s.cacheMu.Unlock()
	if !ok || now.After(ce.until) || ce.expiresAt <= now.UnixMilli() {
		row, err := s.db.R.GetSession(ctx, gen.GetSessionParams{TokenHash: h[:], ExpiresAt: now.UnixMilli()})
		if errors.Is(err, sql.ErrNoRows) {
			s.dropCache(h)
			return 0, false, apierr.ErrUnauthenticated
		}
		if err != nil {
			return 0, false, err
		}
		ce = cacheEntry{expiresAt: row.ExpiresAt, until: now.Add(cacheTTL)}
	}
	if time.UnixMilli(ce.expiresAt).Sub(now) <= renewBelow {
		exp := now.Add(sessionTTL).UnixMilli()
		if err := s.db.W.RenewSession(ctx, gen.RenewSessionParams{ExpiresAt: exp, LastSeenAt: now.UnixMilli(), TokenHash: h[:]}); err != nil {
			return 0, false, err
		}
		ce.expiresAt = exp
		renewed = true
	}
	s.cacheMu.Lock()
	s.cache[h] = ce
	if len(s.cache) > 10000 {
		for k, v := range s.cache {
			if now.After(v.until) {
				delete(s.cache, k)
			}
		}
	}
	s.cacheMu.Unlock()
	return ce.expiresAt, renewed, nil
}

// Logout deletes the session.
func (s *Service) Logout(ctx context.Context, token string) error {
	raw, err := ids.FromB64(token)
	if err != nil {
		return nil
	}
	h := sha256.Sum256(raw)
	s.dropCache(h)
	return s.db.W.DeleteSession(ctx, h[:])
}

func (s *Service) dropCache(h [32]byte) {
	s.cacheMu.Lock()
	delete(s.cache, h)
	s.cacheMu.Unlock()
}

func (s *Service) purgeCache() {
	s.cacheMu.Lock()
	s.cache = map[[32]byte]cacheEntry{}
	s.cacheMu.Unlock()
}

// Cookie builds the session cookie; maxAge 0 clears it.
func (s *Service) Cookie(token string, maxAge int) *http.Cookie {
	c := &http.Cookie{ //nolint:gosec // Secure is the auth.secure_cookies setting (off only for plain-HTTP testing)
		Name: SessionCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: s.opt.SecureCookies, MaxAge: maxAge,
	}
	if maxAge == 0 {
		c.MaxAge = -1
		c.Value = ""
	}
	return c
}

// SessionMaxAge is the cookie Max-Age in seconds.
const SessionMaxAge = int(sessionTTL / time.Second)

// --- stream tokens ---

type streamPayload struct {
	P string `json:"p"`
	F string `json:"f"`
	E int64  `json:"e"`
}

// StreamToken signs a stream token for placeID/rel valid until exp.
func (s *Service) StreamToken(placeID, rel string, exp time.Time) string {
	b, _ := json.Marshal(streamPayload{P: placeID, F: rel, E: exp.Unix()})
	payload := ids.B64(b)
	return payload + "." + ids.B64(mac(s.kStream, []byte(payload))[:16])
}

// ParseStreamToken verifies a stream token; ok is false on any problem.
func (s *Service) ParseStreamToken(tok string) (placeID, rel string, ok bool) {
	payload, sig, found := strings.Cut(tok, ".")
	if !found {
		return "", "", false
	}
	got, err := ids.FromB64(sig)
	if err != nil || !hmac.Equal(got, mac(s.kStream, []byte(payload))[:16]) {
		return "", "", false
	}
	b, err := ids.FromB64(payload)
	if err != nil {
		return "", "", false
	}
	var p streamPayload
	if json.Unmarshal(b, &p) != nil || p.E <= clock.Now().Unix() {
		return "", "", false
	}
	return p.P, p.F, true
}

// --- share tokens ---

// NewShareID returns a fresh share-link id (base64url of 12 random bytes).
func NewShareID() string { return ids.B64(ids.RandomBytes(12)) }

// ShareToken derives the public token for a share id.
func (s *Service) ShareToken(id string) string {
	raw, err := ids.FromB64(id)
	if err != nil {
		return ""
	}
	return ids.B64(append(raw, mac(s.kShare, raw)[:12]...))
}

// ParseShareToken verifies the MAC and returns the share id. It never
// touches the database.
func (s *Service) ParseShareToken(tok string) (string, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil || len(raw) != 24 {
		return "", false
	}
	id, sig := raw[:12], raw[12:]
	if !hmac.Equal(sig, mac(s.kShare, id)[:12]) {
		return "", false
	}
	return ids.B64(id), true
}

// ShareCookieName returns the unlock cookie name for a share id.
func ShareCookieName(id string) string { return "filet_share_" + id }

// ShareCookieValue returns "<exp>.<mac>" for an unlocked share.
func (s *Service) ShareCookieValue(id string, exp time.Time) string {
	e := strconv.FormatInt(exp.Unix(), 10)
	return e + "." + ids.B64(mac(s.kShareCookie, []byte(id+e)))
}

// VerifyShareCookie checks an unlock cookie value.
func (s *Service) VerifyShareCookie(id, value string) bool {
	e, sig, ok := strings.Cut(value, ".")
	if !ok {
		return false
	}
	got, err := ids.FromB64(sig)
	if err != nil || !hmac.Equal(got, mac(s.kShareCookie, []byte(id+e))) {
		return false
	}
	exp, err := strconv.ParseInt(e, 10, 64)
	return err == nil && exp > clock.Now().Unix()
}

// ShareLockout returns the per-share-link unlock lockout.
func (s *Service) ShareLockout(id string) *Lockout {
	s.shareLockMu.Lock()
	defer s.shareLockMu.Unlock()
	l, ok := s.shareLocks[id]
	if !ok {
		l = NewLockout(s.opt.LockoutEnabled, s.opt.LockoutDuration)
		s.shareLocks[id] = l
	}
	return l
}

// SecureCookies reports whether cookies get the Secure attribute.
func (s *Service) SecureCookies() bool { return s.opt.SecureCookies }
