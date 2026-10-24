package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeebo/blake3"

	"filet/internal/archive"
	"filet/internal/auth"
	"filet/internal/clock"
	"filet/internal/config"
	"filet/internal/db"
	"filet/internal/events"
	"filet/internal/fsops"
	"filet/internal/httpapi"
	"filet/internal/ids"
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

const (
	testUser = "admin"
	testPass = "correct horse battery"
)

// env is a running server around real services, a temp SQLite file and
// temp places.
type env struct {
	t       *testing.T
	dir     string
	cfg     *config.Config
	srv     *httptest.Server
	client  *http.Client
	db      *db.DB
	auth    *auth.Service
	reg     *places.Registry
	uploads *uploads.Service
	jobs    *jobs.Runner
	ops     *fsops.Ops
	places  map[string]string // id → path
	log     *slog.Logger
	stopFns []func()
}

type envOpts struct {
	extraYAML string
	readonly  bool // add a read-only place "ro"
	second    bool // add a second writable place "other"
	user      string
	pass      string
	dbDir     string
}

func newEnv(t *testing.T, o envOpts) *env {
	t.Helper()
	clock.Reset()
	dir := t.TempDir()
	if o.dbDir == "" {
		o.dbDir = filepath.Join(dir, "data")
	}
	mustMkdir(t, o.dbDir)
	pl := map[string]string{"media": filepath.Join(dir, "media")}
	mustMkdir(t, pl["media"])
	y := fmt.Sprintf("database: %s/filet.db\nplaces:\n  - name: Media\n    path: %s\n", o.dbDir, pl["media"])
	if o.second {
		pl["other"] = filepath.Join(dir, "other")
		mustMkdir(t, pl["other"])
		y += fmt.Sprintf("  - name: Other\n    path: %s\n", pl["other"])
	}
	if o.readonly {
		pl["ro"] = filepath.Join(dir, "ro")
		mustMkdir(t, pl["ro"])
		y += fmt.Sprintf("  - name: RO\n    path: %s\n    readonly: true\n", pl["ro"])
	}
	y += "auth:\n  secure_cookies: false\n" + o.extraYAML
	cfg, err := config.Parse([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	for id, p := range pl {
		rp, _ := filepath.EvalSymlinks(p)
		pl[id] = rp
	}
	if o.user == "" {
		o.user, o.pass = testUser, testPass
	}
	cfg.Username, cfg.Password = o.user, o.pass
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e := &env{t: t, dir: dir, cfg: cfg, places: pl, log: log}
	e.start()
	t.Cleanup(e.stop)
	return e
}

func (e *env) start() {
	t := e.t
	ctx := context.Background()
	d, err := db.Open(ctx, e.cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	a := auth.New(d, auth.Options{
		Username: e.cfg.Username, Password: e.cfg.Password, LockoutEnabled: e.cfg.LockoutEnabled(),
		LockoutDuration: e.cfg.Auth.LoginLockout.Duration.D(), SecureCookies: false,
	}, e.log)
	if err := a.Init(ctx); err != nil {
		t.Fatal(err)
	}
	runner := tools.New(tools.Names{FFmpeg: "ffmpeg", FFprobe: "ffprobe", VipsThumbnail: "vipsthumbnail", Vips: "vips", Smartctl: "smartctl-missing"}, e.log)
	runner.Probe(ctx)
	reg, err := places.NewRegistry(e.cfg.Places, filepath.Join(filepath.Dir(e.cfg.Database), "cache"), &runner.Caps, e.log)
	if err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub()
	ms := media.New(runner)
	th := thumbs.New(runner, ms, e.log)
	th.Start(2)
	ops := &fsops.Ops{DB: d, Reg: reg, Hub: hub, Thumbs: th, Log: e.log}
	upl := uploads.New(d, reg, hub, ops, th, e.log)
	ops.Uploads = upl
	jr := jobs.New(d, reg, hub, ops, th, e.log)
	if err := upl.Reconcile(ctx, e.log); err != nil {
		t.Fatal(err)
	}
	if err := jr.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if err := ops.ReconcileTrash(ctx, e.log); err != nil {
		t.Fatal(err)
	}
	sm := smart.New(d, reg, runner, hub, e.log, time.Hour, false)
	s := &httpapi.Server{
		Cfg: e.cfg, Version: "test", DB: d, Auth: a, Reg: reg, Hub: hub, Ops: ops, Jobs: jr, Uploads: upl,
		Thumbs: th, Media: ms, Archive: archive.New(), Text: textview.New(), Share: share.New(d, reg, a),
		Smart: sm, Tools: runner, Log: e.log,
	}
	e.srv = httptest.NewServer(s.Handler())
	jar, _ := cookiejar.New(nil)
	e.client = &http.Client{Jar: jar, Timeout: 60 * time.Second}
	e.db, e.auth, e.reg, e.uploads, e.jobs, e.ops = d, a, reg, upl, jr, ops
	stopThumbs := th.Stop
	e.stopFns = []func(){func() { e.srv.Close() }, jr.Shutdown, stopThumbs, upl.Close, reg.Close, func() { _ = d.Close() }}
}

func (e *env) stop() {
	for _, f := range e.stopFns {
		f()
	}
	e.stopFns = nil
}

// restart simulates a server restart against the same config and DB.
func (e *env) restart() {
	e.stop()
	e.start()
	e.login()
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func (e *env) path(place, rel string) string {
	return filepath.Join(e.places[place], filepath.FromSlash(strings.TrimPrefix(rel, "/")))
}

func (e *env) write(place, rel, content string) {
	e.t.Helper()
	p := e.path(place, rel)
	mustMkdir(e.t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) exists(place, rel string) bool {
	_, err := os.Lstat(e.path(place, rel))
	return err == nil
}

func (e *env) read(place, rel string) string {
	e.t.Helper()
	b, err := os.ReadFile(e.path(place, rel))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

type resp struct {
	status int
	header http.Header
	body   []byte
}

func (r resp) json(v any) {
	_ = json.Unmarshal(r.body, v)
}

func (r resp) code() string {
	var b struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(r.body, &b)
	return b.Error.Code
}

func (r resp) details() map[string]any {
	var b struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(r.body, &b)
	return b.Error.Details
}

func (e *env) do(method, path string, body any, hdr ...string) resp {
	e.t.Helper()
	var rd io.Reader
	ct := ""
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
		ct = "application/octet-stream"
	case string:
		rd = strings.NewReader(b)
		ct = "application/x-www-form-urlencoded"
	default:
		data, _ := json.Marshal(b)
		rd = bytes.NewReader(data)
		ct = "application/json"
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := e.client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return resp{status: res.StatusCode, header: res.Header, body: data}
}

func (e *env) login() {
	e.t.Helper()
	r := e.do("POST", "/api/auth/login", map[string]string{"username": e.cfg.Username, "password": e.cfg.Password})
	if r.status != 204 {
		e.t.Fatalf("login: %d %s", r.status, r.body)
	}
}

func expect(t *testing.T, r resp, status int, code string) {
	t.Helper()
	if r.status != status || (code != "" && r.code() != code) {
		t.Fatalf("expected %d %s, got %d %s", status, code, r.status, r.body)
	}
}

// --- upload helpers ---

func chunkHashes(data []byte) []byte {
	var out []byte
	for off := 0; off < len(data); off += uploads.ChunkSize {
		end := min(off+uploads.ChunkSize, len(data))
		h := blake3.Sum256(data[off:end])
		out = append(out, h[:]...)
	}
	return out
}

func chunk(data []byte, i int) []byte {
	off := i * uploads.ChunkSize
	return data[off:min(off+uploads.ChunkSize, len(data))]
}

type regResp struct {
	ID         string     `json:"id"`
	Status     string     `json:"status"`
	ChunkCount int64      `json:"chunkCount"`
	Missing    [][2]int64 `json:"missing"`
	Renamed    bool       `json:"renamed"`
}

func (e *env) register(place, path string, data []byte, mtime int64, conflict string) (regResp, resp) {
	e.t.Helper()
	body := map[string]any{
		"place": place, "path": path, "size": len(data), "mtime": mtime, "chunkSize": uploads.ChunkSize,
		"hashes": ids.B64(chunkHashes(data)), "conflict": conflict,
	}
	r := e.do("POST", "/api/uploads", body)
	var rr regResp
	r.json(&rr)
	return rr, r
}

func (e *env) put(id string, i int, body []byte, final bool) resp {
	e.t.Helper()
	var hdr []string
	if final {
		hdr = []string{"Filet-Final", "1"}
	}
	return e.do("PUT", fmt.Sprintf("/api/uploads/%s/chunks/%d", id, i), body, hdr...)
}

func pattern(n int, seed byte) []byte {
	b := make([]byte, n)
	x := uint32(seed) + 1
	for i := range b {
		x = x*1664525 + 1013904223
		b[i] = byte(x >> 24)
	}
	return b
}

func newJar() http.CookieJar {
	jar, _ := cookiejar.New(nil)
	return jar
}

type jobJSON struct {
	ID              string
	Status          string
	ItemsDone       int64
	ItemsTotal      int64
	BytesDone       int64
	PendingConflict *struct {
		ID               string
		SameItem         bool
		UploadInProgress bool
		SuggestedName    string
	}
	Result struct {
		Skipped int
		Renamed []struct{ From, To string }
		Errors  []struct{ Path, Code string }
	}
	Error *struct{ Code string }
}

// submit posts a job and waits until it is terminal or awaiting input.
func (e *env) submit(spec map[string]any) jobJSON {
	e.t.Helper()
	r := e.do("POST", "/api/jobs", spec)
	if r.status != 201 {
		e.t.Fatalf("submit: %d %s", r.status, r.body)
	}
	var j jobJSON
	r.json(&j)
	return e.waitJob(j.ID)
}

func (e *env) waitJob(id string) jobJSON {
	e.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var j jobJSON
		e.do("GET", "/api/jobs/"+id, nil).json(&j)
		switch j.Status {
		case "completed", "failed", "cancelled", "interrupted", "awaiting_input":
			return j
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatalf("job %s did not settle", id)
	return jobJSON{}
}

func newReq(ctx context.Context, url string) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
}
