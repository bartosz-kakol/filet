package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filet/internal/auth"
	"filet/internal/clock"
)

func TestLoginLockout(t *testing.T) {
	e := newEnv(t, envOpts{extraYAML: "  login_lockout:\n    duration: 400ms\n"})
	bad := map[string]string{"username": testUser, "password": "nope"}
	good := map[string]string{"username": testUser, "password": testPass}
	expect(t, e.do("POST", "/api/auth/login", bad), 401, "invalid_credentials")
	r := e.do("POST", "/api/auth/login", good)
	expect(t, r, 429, "login_locked")
	if r.header.Get("Retry-After") != "1" || r.details()["retryAfterMs"] == nil {
		t.Fatalf("lock headers: %v %s", r.header, r.body)
	}
	time.Sleep(450 * time.Millisecond)
	expect(t, e.do("POST", "/api/auth/login", good), 204, "")
}

func TestLoginLockoutDisabled(t *testing.T) {
	e := newEnv(t, envOpts{extraYAML: "  login_lockout:\n    enabled: false\n"})
	bad := map[string]string{"username": testUser, "password": "nope"}
	for range 3 {
		expect(t, e.do("POST", "/api/auth/login", bad), 401, "invalid_credentials")
	}
	expect(t, e.do("POST", "/api/auth/login", map[string]string{"username": testUser, "password": testPass}), 204, "")
}

func TestSessionCookieRenewalAndLogout(t *testing.T) {
	e := newEnv(t, envOpts{})
	r := e.do("GET", "/api/places", nil)
	expect(t, r, 401, "unauthenticated")
	r = e.do("POST", "/api/auth/login", map[string]string{"username": testUser, "password": testPass})
	sc := r.header.Get("Set-Cookie")
	for _, want := range []string{"filet_session=", "Path=/", "HttpOnly", "SameSite=Lax", "Max-Age=2592000"} {
		if !strings.Contains(sc, want) {
			t.Fatalf("cookie %q missing %q", sc, want)
		}
	}
	// No renewal while more than 29 days remain.
	r = e.do("GET", "/api/places", nil)
	expect(t, r, 200, "")
	if r.header.Get("Set-Cookie") != "" {
		t.Fatal("renewed too early")
	}
	// Age the session by two days: the next request renews it.
	_, err := e.db.Writer.Exec("UPDATE sessions SET expires_at = expires_at - ?", (2 * 24 * time.Hour).Milliseconds())
	if err != nil {
		t.Fatal(err)
	}
	e.restartAuthCache()
	r = e.do("GET", "/api/places", nil)
	if !strings.Contains(r.header.Get("Set-Cookie"), "Max-Age=2592000") {
		t.Fatalf("expected renewal, got %q", r.header.Get("Set-Cookie"))
	}
	expect(t, e.do("POST", "/api/auth/logout", nil), 204, "")
	expect(t, e.do("GET", "/api/places", nil), 401, "unauthenticated")
	var s struct{ Authenticated bool }
	e.do("GET", "/api/auth/session", nil).json(&s)
	if s.Authenticated {
		t.Fatal("still authenticated")
	}
}

// restartAuthCache restarts the server process state so the 60 s session
// cache does not hide DB changes.
func (e *env) restartAuthCache() {
	jar := e.client.Jar
	e.stop()
	e.start()
	e.client.Jar = jar
}

func TestCredentialChangeInvalidatesSessions(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	jar := e.client.Jar
	e.stop()
	e.cfg.Password = "a brand new password"
	e.start()
	e.client.Jar = jar
	expect(t, e.do("GET", "/api/places", nil), 401, "unauthenticated")
}

func TestCSRF(t *testing.T) {
	e := newEnv(t, envOpts{extraYAML: "  trusted_origins: [\"https://files.example.com\"]\n"})
	e.login()
	body := map[string]any{"path": "/csrf"}
	r := e.do("POST", "/api/places/media/mkdir", body, "Sec-Fetch-Site", "cross-site", "Origin", "https://evil.example")
	if r.status != 403 {
		t.Fatalf("cross-origin POST allowed: %d", r.status)
	}
	expect(t, e.do("POST", "/api/places/media/mkdir", body, "Sec-Fetch-Site", "same-origin"), 201, "")
	body = map[string]any{"path": "/csrf2"}
	expect(t, e.do("POST", "/api/places/media/mkdir", body, "Origin", "https://files.example.com", "Sec-Fetch-Site", "cross-site"), 201, "")
}

func TestPathValidation(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	cases := map[string]string{
		"/../etc":                      "invalid_path",
		"/a/./b":                       "invalid_path",
		"/a//b":                        "invalid_path",
		"a":                            "invalid_path",
		"/a\x00b":                      "invalid_path",
		"/" + strings.Repeat("x", 256): "invalid_path",
		"/.filet":                      "reserved_name",
		"/x/.filet/thumbs":             "reserved_name",
		"/a/.filet-part-abc":           "reserved_name",
		"/.filet-copy-1-2":             "reserved_name",
	}
	for p, code := range cases {
		r := e.do("POST", "/api/places/media/mkdir", map[string]any{"path": p})
		if r.code() != code {
			t.Errorf("%q: got %d %s want %s", p, r.status, r.code(), code)
		}
	}
}

func TestSymlinks(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	e.write("media", "/real/file.txt", "inside")
	outside := filepath.Join(e.dir, "outside.txt")
	_ = os.WriteFile(outside, []byte("secret"), 0o644)
	root := e.places["media"]
	_ = os.Symlink("real/file.txt", filepath.Join(root, "ok-link"))
	_ = os.Symlink("../outside.txt", filepath.Join(root, "out-link"))
	_ = os.Symlink("missing.txt", filepath.Join(root, "broken-link"))
	_ = os.Symlink(".", filepath.Join(root, "real", "loop"))
	var l struct {
		Entries []struct {
			Name    string
			Type    string
			Symlink *struct{ Status string }
		}
	}
	e.do("GET", "/api/places/media/list?path=/", nil).json(&l)
	got := map[string]string{}
	for _, en := range l.Entries {
		if en.Symlink != nil {
			got[en.Name] = en.Type + ":" + en.Symlink.Status
		}
	}
	if got["ok-link"] != "file:ok" || got["out-link"] != "symlink:outside" || got["broken-link"] != "symlink:broken" {
		t.Fatalf("symlink statuses: %v", got)
	}
	r := e.do("GET", "/api/places/media/raw/ok-link", nil)
	if r.status != 200 || string(r.body) != "inside" {
		t.Fatalf("ok-link: %d %s", r.status, r.body)
	}
	if r := e.do("GET", "/api/places/media/raw/out-link", nil); r.status == 200 || strings.Contains(string(r.body), "secret") {
		t.Fatal("followed a link outside the place")
	}
	// Loops are not descended in zip, search or copy.
	z := e.do("POST", "/api/places/media/zip", "dir=/&name=real")
	expect(t, z, 200, "")
	s := e.do("GET", "/api/places/media/search?q=file", nil)
	if strings.Contains(string(s.body), "loop/") {
		t.Fatalf("search descended a loop: %s", s.body)
	}
	j := e.submit(map[string]any{"type": "copy", "src": map[string]any{"place": "media", "paths": []string{"/real"}},
		"dst": map[string]any{"place": "media", "dir": "/"}, "conflict": "rename"})
	if j.Status != "completed" {
		t.Fatalf("copy: %+v", j)
	}
}

func TestStreamTokens(t *testing.T) {
	e := newEnv(t, envOpts{extraYAML: "stream_links:\n  ttl: 1m\n"})
	e.login()
	e.write("media", "/v/movie.mp4", strings.Repeat("0123456789", 100))
	e.write("media", "/v/notes.txt", "x")
	expect(t, e.do("POST", "/api/stream-links", map[string]string{"place": "media", "path": "/v/notes.txt"}), 400, "not_media")
	var sl struct {
		URL       string
		ExpiresAt int64
	}
	r := e.do("POST", "/api/stream-links", map[string]string{"place": "media", "path": "/v/movie.mp4"})
	expect(t, r, 201, "")
	r.json(&sl)
	// No cookie needed; Range works.
	anon := &http.Client{}
	req, _ := http.NewRequest("GET", e.srv.URL+sl.URL, nil)
	req.Header.Set("Range", "bytes=0-99")
	res, err := anon.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 206 || len(b) != 100 {
		t.Fatalf("range: %d %d", res.StatusCode, len(b))
	}
	tampered := strings.Replace(sl.URL, "/stream/", "/stream/x", 1)
	if res, _ := anon.Get(e.srv.URL + tampered); res.StatusCode != 404 {
		t.Fatal("tampered token accepted")
	}
	// A token for another path does not open this one.
	tok := e.auth.StreamToken("media", "/v/../v/movie.mp4", time.Now().Add(time.Hour))
	if res, _ := anon.Get(e.srv.URL + "/stream/" + tok + "/movie.mp4"); res.StatusCode != 404 {
		t.Fatal("invalid path accepted")
	}
	clock.Advance(2 * time.Minute)
	defer clock.Reset()
	if res, _ := anon.Get(e.srv.URL + sl.URL); res.StatusCode != 404 {
		t.Fatal("expired token accepted")
	}
	clock.Reset()
	// Credential change revokes outstanding stream links.
	e.stop()
	e.cfg.Password = "changed password!"
	e.start()
	if res, _ := anon.Get(e.srv.URL + sl.URL); res.StatusCode != 404 {
		t.Fatal("token survived a credential change")
	}
}

func TestShareTokenMACCheckedBeforeDB(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	e.write("media", "/shared/a.txt", "A")
	var link struct{ ID, URL string }
	r := e.do("POST", "/api/share-links", map[string]any{"place": "media", "path": "/shared", "expiresAt": nil})
	expect(t, r, 201, "")
	r.json(&link)
	token := strings.TrimPrefix(link.URL, "/s/")
	before := e.db.QueryCount()
	bad := token[:len(token)-2] + "AA"
	if bad == token {
		bad = token[:len(token)-2] + "BB"
	}
	expect(t, e.do("GET", "/api/public/share/"+bad, nil), 404, "")
	if e.db.QueryCount() != before {
		t.Fatal("a tampered token reached the database")
	}
	expect(t, e.do("GET", "/api/public/share/"+token, nil), 200, "")
	expect(t, e.do("DELETE", "/api/share-links/"+link.ID, nil), 204, "")
	expect(t, e.do("GET", "/api/public/share/"+token, nil), 404, "")
}

func TestSharePasswordAndExpiry(t *testing.T) {
	e := newEnv(t, envOpts{extraYAML: "  login_lockout:\n    duration: 300ms\n"})
	e.login()
	e.write("media", "/shared/a.txt", "A")
	e.write("media", "/shared/.hidden", "H")
	e.write("media", "/secret.txt", "S")
	exp := time.Now().Add(2 * time.Hour).UnixMilli()
	var link struct{ ID, URL string }
	e.do("POST", "/api/share-links", map[string]any{"place": "media", "path": "/shared", "expiresAt": exp, "password": "pw"}).json(&link)
	token := strings.TrimPrefix(link.URL, "/s/")
	// A fresh client without a session.
	pub := newPublicClient(e)
	var info struct {
		PasswordRequired, Unlocked bool
	}
	pub.do("GET", "/api/public/share/"+token, nil).json(&info)
	if !info.PasswordRequired || info.Unlocked {
		t.Fatalf("info %+v", info)
	}
	expect(t, pub.do("GET", "/api/public/share/"+token+"/list?path=/", nil), 401, "password_required")
	expect(t, pub.do("POST", "/api/public/share/"+token+"/unlock", map[string]string{"password": "wrong"}), 401, "invalid_password")
	expect(t, pub.do("POST", "/api/public/share/"+token+"/unlock", map[string]string{"password": "pw"}), 429, "login_locked")
	time.Sleep(350 * time.Millisecond)
	expect(t, pub.do("POST", "/api/public/share/"+token+"/unlock", map[string]string{"password": "pw"}), 204, "")
	var l struct{ Entries []struct{ Name string } }
	pub.do("GET", "/api/public/share/"+token+"/list?path=/", nil).json(&l)
	if len(l.Entries) != 1 || l.Entries[0].Name != "a.txt" {
		t.Fatalf("public list (dotfiles must be hidden): %+v", l)
	}
	if r := pub.do("GET", "/api/public/share/"+token+"/raw/a.txt", nil); string(r.body) != "A" {
		t.Fatal("raw")
	}
	expect(t, pub.do("GET", "/api/public/share/"+token+"/raw/..%2Fsecret.txt", nil), 400, "")
	// The lockout is per link: another link is unaffected.
	clock.Advance(3 * time.Hour)
	defer clock.Reset()
	expect(t, pub.do("GET", "/api/public/share/"+token, nil), 404, "")
}

func newPublicClient(e *env) *env {
	c := *e
	c.client = &http.Client{Jar: newJar()}
	return &c
}

func TestReadOnlyPlace(t *testing.T) {
	e := newEnv(t, envOpts{readonly: true})
	e.login()
	_ = os.WriteFile(e.path("ro", "/r.txt"), []byte("ro"), 0o644)
	ro := func(r resp) { t.Helper(); expect(t, r, 403, "place_readonly") }
	ro(e.do("POST", "/api/uploads/preflight", map[string]any{"items": []map[string]any{{"key": "1", "place": "ro", "path": "/x", "size": 1}}}).withStatus(func(r resp) resp {
		var out struct{ Items []struct{ Status string } }
		r.json(&out)
		if out.Items[0].Status != "readonly" {
			t.Fatal("preflight not readonly")
		}
		return resp{status: 403, body: []byte(`{"error":{"code":"place_readonly"}}`)}
	}))
	_, r := e.register("ro", "/x.bin", []byte("abc"), 0, "fail")
	ro(r)
	ro(e.do("POST", "/api/places/ro/mkdir", map[string]any{"path": "/d"}))
	ro(e.do("POST", "/api/places/ro/rename", map[string]any{"path": "/r.txt", "newName": "s.txt"}))
	ro(e.do("POST", "/api/jobs", map[string]any{"type": "trash", "place": "ro", "paths": []string{"/r.txt"}}))
	ro(e.do("POST", "/api/places/ro/trash/restore", map[string]any{"ids": []string{"x"}}))
	ro(e.do("POST", "/api/places/ro/trash/purge", map[string]any{"all": true}))
	ro(e.do("POST", "/api/jobs", map[string]any{"type": "move", "src": map[string]any{"place": "ro", "paths": []string{"/r.txt"}}, "dst": map[string]any{"place": "media", "dir": "/"}}))
	ro(e.do("POST", "/api/jobs", map[string]any{"type": "move", "src": map[string]any{"place": "media", "paths": []string{"/"}}, "dst": map[string]any{"place": "ro", "dir": "/"}}))
	e.write("media", "/m.txt", "m")
	ro(e.do("POST", "/api/jobs", map[string]any{"type": "copy", "src": map[string]any{"place": "media", "paths": []string{"/m.txt"}}, "dst": map[string]any{"place": "ro", "dir": "/"}}))
	// Copying out works.
	j := e.submit(map[string]any{"type": "copy", "src": map[string]any{"place": "ro", "paths": []string{"/r.txt"}}, "dst": map[string]any{"place": "media", "dir": "/"}})
	if j.Status != "completed" || e.read("media", "/r.txt") != "ro" {
		t.Fatalf("copy out: %+v", j)
	}
}

func (r resp) withStatus(f func(resp) resp) resp { return f(r) }

func TestUserFileContentTypes(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	e.write("media", "/page.html", "<script>alert(1)</script>")
	e.write("media", "/img.svg", "<svg xmlns='http://www.w3.org/2000/svg'/>")
	r := e.do("GET", "/api/places/media/raw/page.html", nil)
	if r.header.Get("Content-Type") != "text/plain; charset=utf-8" || r.header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("html served as %q", r.header.Get("Content-Type"))
	}
	r = e.do("GET", "/api/places/media/raw/img.svg", nil)
	if !strings.HasPrefix(r.header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatal("svg without sandbox CSP")
	}
	r = e.do("GET", "/api/places/media/raw/page.html?download=1", nil)
	if r.header.Get("Content-Disposition") != "attachment; filename*=UTF-8''page.html" {
		t.Fatalf("disposition %q", r.header.Get("Content-Disposition"))
	}
}

func TestSessionsSweptByFingerprintOnly(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	// Restart with the same credentials keeps the session.
	jar := e.client.Jar
	e.stop()
	e.start()
	e.client.Jar = jar
	expect(t, e.do("GET", "/api/places", nil), 200, "")
	rows, _ := e.db.Reader.QueryContext(context.Background(), "SELECT COUNT(*) FROM sessions")
	defer rows.Close()
	_ = auth.SessionCookie
}
