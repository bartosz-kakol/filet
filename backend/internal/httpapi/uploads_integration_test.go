package httpapi_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"filet/internal/clock"
	"filet/internal/fsx"
	"filet/internal/uploads"
)

const mib = 1 << 20

func (e *env) sessionCount() int {
	rows, err := e.db.R.ListUploadSessions(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return len(rows)
}

func (e *env) chunkRowCount(id string) int {
	rows, err := e.db.R.ListChunks(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return len(rows)
}

func partials(t *testing.T, dir string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, ".filet-part-*"))
	return m
}

func TestUploadHappyPathParallelOutOfOrder(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	data := pattern(20*mib+123, 1)
	mtime := time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC).UnixMilli()
	rr, r := e.register("media", "/Photos/2024/big.bin", data, mtime, "fail")
	expect(t, r, 201, "")
	if rr.Status != "new" || rr.ChunkCount != 3 || len(rr.Missing) != 1 || rr.Missing[0] != [2]int64{0, 2} {
		t.Fatalf("unexpected registration %+v", rr)
	}
	// The placeholder appears in the listing, the partial does not.
	var l struct {
		Entries []struct{ Name string }
		Uploads []struct {
			Name   string
			Active bool
		}
	}
	e.do("GET", "/api/places/media/list?path=/Photos/2024", nil).json(&l)
	if len(l.Entries) != 0 || len(l.Uploads) != 1 || l.Uploads[0].Name != "big.bin" || !l.Uploads[0].Active {
		t.Fatalf("listing during upload: %+v", l)
	}
	// Chunks 1 and 0 in parallel, then the final chunk.
	var wg sync.WaitGroup
	for _, i := range []int{1, 0} {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if r := e.put(rr.ID, i, chunk(data, i), false); r.status != 200 {
				t.Errorf("chunk %d: %d %s", i, r.status, r.body)
			}
		}(i)
	}
	wg.Wait()
	r = e.put(rr.ID, 2, chunk(data, 2), true)
	expect(t, r, 200, "")
	var done struct {
		Status string
		Entry  struct{ Name string }
	}
	r.json(&done)
	if done.Status != "complete" || done.Entry.Name != "big.bin" {
		t.Fatalf("final: %s", r.body)
	}
	got, _ := os.ReadFile(e.path("media", "/Photos/2024/big.bin"))
	if !bytes.Equal(got, data) {
		t.Fatal("content differs")
	}
	fi, _ := os.Stat(e.path("media", "/Photos/2024/big.bin"))
	if fi.ModTime().UnixMilli() != mtime {
		t.Fatalf("mtime %v want %v", fi.ModTime().UnixMilli(), mtime)
	}
	if e.sessionCount() != 0 || e.chunkRowCount(rr.ID) != 0 {
		t.Fatal("session or chunk rows left behind")
	}
	if p := partials(t, e.path("media", "/Photos/2024")); len(p) != 0 {
		t.Fatalf("partials left: %v", p)
	}
}

func TestUploadEmptyFile(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	rr, r := e.register("media", "/empty.txt", nil, 1700000000000, "fail")
	expect(t, r, 200, "")
	if rr.Status != "complete" || !e.exists("media", "/empty.txt") || e.sessionCount() != 0 {
		t.Fatalf("empty upload: %s", r.body)
	}
	_, r = e.register("media", "/empty.txt", nil, 0, "fail")
	expect(t, r, 409, "already_exists")
}

func TestUploadHashMismatchAndCorruption(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	data := pattern(2*mib, 2)
	rr, r := e.register("media", "/c.bin", data, 0, "fail")
	expect(t, r, 201, "")
	bad := bytes.Clone(data)
	bad[100] ^= 0xff
	for attempt := 1; attempt <= 2; attempt++ {
		r = e.put(rr.ID, 0, bad, true)
		expect(t, r, 422, "chunk_hash_mismatch")
		if d := r.details(); d["attempt"] != float64(attempt) || d["maxAttempts"] != float64(3) {
			t.Fatalf("details %v", d)
		}
		// Unverified bytes are never written: the partial is still zeros.
		part := partials(t, e.places["media"])
		if len(part) != 1 {
			t.Fatalf("partials: %v", part)
		}
		got, _ := os.ReadFile(part[0])
		if !bytes.Equal(got[:200], make([]byte, 200)) {
			t.Fatal("unverified bytes were written")
		}
	}
	r = e.put(rr.ID, 0, bad, true)
	expect(t, r, 422, "upload_corrupted")
	if len(partials(t, e.places["media"])) != 0 || e.sessionCount() != 0 || e.chunkRowCount(rr.ID) != 0 {
		t.Fatal("corrupted upload left state behind")
	}
	expect(t, e.put(rr.ID, 0, data, true), 410, "upload_gone")
}

func TestUploadResumeExactMissingRanges(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	data := pattern(5*uploads.ChunkSize+7, 3)
	rr, _ := e.register("media", "/r.bin", data, 0, "fail")
	for _, i := range []int{0, 2, 3} {
		expect(t, e.put(rr.ID, i, chunk(data, i), false), 200, "")
	}
	rr2, r := e.register("media", "/r.bin", data, 0, "fail")
	expect(t, r, 200, "")
	if rr2.Status != "resumed" || rr2.ID != rr.ID {
		t.Fatalf("resume: %s", r.body)
	}
	want := [][2]int64{{1, 1}, {4, 5}}
	if len(rr2.Missing) != 2 || rr2.Missing[0] != want[0] || rr2.Missing[1] != want[1] {
		t.Fatalf("missing %v want %v", rr2.Missing, want)
	}
	expect(t, e.put(rr.ID, 1, chunk(data, 1), false), 200, "")
	expect(t, e.put(rr.ID, 4, chunk(data, 4), false), 200, "")
	expect(t, e.put(rr.ID, 5, chunk(data, 5), true), 200, "")
	if e.read("media", "/r.bin") != string(data) {
		t.Fatal("content differs after resume")
	}
}

func TestUploadResumeMismatchStartsFresh(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	data := pattern(3*mib, 4)
	rr, _ := e.register("media", "/m.bin", data, 0, "fail")
	expect(t, e.put(rr.ID, 0, chunk(data, 0), false), 200, "")
	clock.Advance(3 * time.Minute) // becomes a leftover
	defer clock.Reset()
	other := pattern(3*mib, 5)
	rr2, r := e.register("media", "/m.bin", other, 0, "fail")
	expect(t, r, 201, "")
	if rr2.ID == rr.ID || rr2.Status != "new" {
		t.Fatalf("expected a new session: %s", r.body)
	}
	if p := partials(t, e.places["media"]); len(p) != 1 || !strings.HasSuffix(p[0], rr2.ID) {
		t.Fatalf("old partial not discarded: %v", p)
	}
	// Different size also starts fresh.
	clock.Advance(3 * time.Minute)
	rr3, r := e.register("media", "/m.bin", pattern(4*mib, 6), 0, "fail")
	expect(t, r, 201, "")
	if rr3.ID == rr2.ID {
		t.Fatal("size change must start fresh")
	}
}

func TestUploadActiveConflictAndOverwrite(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	a := pattern(2*mib, 7)
	rrA, _ := e.register("media", "/x.bin", a, 0, "fail")
	b := pattern(2*mib, 8)
	_, r := e.register("media", "/x.bin", b, 0, "fail")
	expect(t, r, 409, "upload_conflict")
	if r.details()["active"] != true {
		t.Fatalf("details: %s", r.body)
	}
	rrB, r := e.register("media", "/x.bin", b, 0, "overwrite")
	expect(t, r, 201, "")
	expect(t, e.put(rrA.ID, 0, a, true), 410, "upload_gone")
	expect(t, e.put(rrB.ID, 0, b, true), 200, "")
	if e.read("media", "/x.bin") != string(b) {
		t.Fatal("wrong content")
	}
}

func TestUploadFreeSpace(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	e.uploads.Statfs = func(*os.File) (fsx.DiskUsage, error) {
		return fsx.DiskUsage{Free: 10 * mib, Total: 100 * mib, Fsid: "fake"}, nil
	}
	items := []map[string]any{
		{"key": "a", "place": "media", "path": "/a.bin", "size": 4 * mib},
		{"key": "b", "place": "media", "path": "/b.bin", "size": 4 * mib},
		{"key": "c", "place": "media", "path": "/c.bin", "size": 4 * mib},
		{"key": "d", "place": "media", "path": "/d.bin", "size": 1 * mib},
	}
	var out struct {
		Items []struct {
			Key       string
			Status    string
			Required  int64
			Available int64
		}
	}
	r := e.do("POST", "/api/uploads/preflight", map[string]any{"items": items})
	expect(t, r, 200, "")
	r.json(&out)
	got := []string{}
	for _, it := range out.Items {
		got = append(got, it.Key+"="+it.Status)
	}
	if strings.Join(got, ",") != "a=ok,b=ok,c=insufficient_space,d=ok" {
		t.Fatalf("preflight %v", got)
	}
	if out.Items[2].Required != 4*mib || out.Items[2].Available != 2*mib {
		t.Fatalf("numbers: %+v", out.Items[2])
	}
	_, r = e.register("media", "/huge.bin", pattern(11*mib, 1), 0, "fail")
	expect(t, r, 507, "insufficient_storage")
	if d := r.details(); d["required"] != float64(11*mib) || d["available"] != float64(10*mib) {
		t.Fatalf("details %v", d)
	}
}

func TestUploadFallocateFallback(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	called := false
	restore := fsx.SetFallocateForTest(func(*os.File, int64) error { called = true; return syscall.EOPNOTSUPP })
	defer restore()
	data := pattern(mib+5, 9)
	rr, r := e.register("media", "/sparse.bin", data, 0, "fail")
	expect(t, r, 201, "")
	part := partials(t, e.places["media"])
	fi, _ := os.Stat(part[0])
	if !called || fi.Size() != int64(len(data)) {
		t.Fatalf("fallback not applied: called=%v size=%d", called, fi.Size())
	}
	expect(t, e.put(rr.ID, 0, data, true), 200, "")
}

func TestUploadOverwriteTrashesAtFinalization(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	e.write("media", "/doc.txt", "original")
	data := pattern(mib, 10)
	rr, r := e.register("media", "/doc.txt", data, 0, "overwrite")
	expect(t, r, 201, "")
	if e.read("media", "/doc.txt") != "original" {
		t.Fatal("original touched at registration")
	}
	// Abandoned overwrite leaves the original intact.
	expect(t, e.do("DELETE", "/api/uploads/"+rr.ID, nil), 204, "")
	if e.read("media", "/doc.txt") != "original" {
		t.Fatal("abandoned overwrite destroyed the original")
	}
	rr, _ = e.register("media", "/doc.txt", data, 0, "overwrite")
	expect(t, e.put(rr.ID, 0, data, true), 200, "")
	if e.read("media", "/doc.txt") != string(data) {
		t.Fatal("not overwritten")
	}
	var trash []struct{ OriginalPath string }
	e.do("GET", "/api/places/media/trash", nil).json(&trash)
	if len(trash) != 1 || trash[0].OriginalPath != "/doc.txt" {
		t.Fatalf("original not in trash: %+v", trash)
	}
}

func TestUploadTargetAppearedAutoKeepBoth(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	data := pattern(mib, 11)
	rr, _ := e.register("media", "/photo.jpg", data, 0, "fail")
	e.write("media", "/photo.jpg", "someone else")
	r := e.put(rr.ID, 0, data, true)
	expect(t, r, 200, "")
	var res struct {
		Renamed bool
		Entry   struct{ Name string }
	}
	r.json(&res)
	if !res.Renamed || res.Entry.Name != "photo (1).jpg" || e.read("media", "/photo.jpg") != "someone else" {
		t.Fatalf("keep-both: %s", r.body)
	}
}

func TestUploadFinalTooEarlyAndResendLast(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	data := pattern(3*uploads.ChunkSize, 12)
	rr, _ := e.register("media", "/f.bin", data, 0, "fail")
	expect(t, e.put(rr.ID, 0, chunk(data, 0), false), 200, "")
	r := e.put(rr.ID, 2, chunk(data, 2), true)
	expect(t, r, 409, "chunks_missing")
	if m, _ := r.details()["missing"].([]any); len(m) != 1 {
		t.Fatalf("missing: %s", r.body)
	}
	expect(t, e.put(rr.ID, 1, chunk(data, 1), false), 200, "")
	// Every chunk is on the server now; re-sending the last one with the
	// final flag completes the upload.
	rr2, _ := e.register("media", "/f.bin", data, 0, "fail")
	if len(rr2.Missing) != 0 {
		t.Fatalf("expected nothing missing: %+v", rr2)
	}
	expect(t, e.put(rr.ID, 2, chunk(data, 2), true), 200, "")
	if e.read("media", "/f.bin") != string(data) {
		t.Fatal("content")
	}
}

func TestUploadDurabilityOrderingAndCoalescing(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	var mu sync.Mutex
	var events []string
	var syncs atomic.Int32
	e.uploads.SetSyncForTest(func(f *os.File) error {
		syncs.Add(1)
		time.Sleep(30 * time.Millisecond) // widen the coalescing window
		mu.Lock()
		events = append(events, "sync")
		mu.Unlock()
		return f.Sync()
	})
	e.uploads.BeforeMark = func(string, int) {
		mu.Lock()
		events = append(events, "mark")
		mu.Unlock()
	}
	data := pattern(9*mib, 13) // 2 chunks
	rr, _ := e.register("media", "/d.bin", data, 0, "fail")
	expect(t, e.put(rr.ID, 0, chunk(data, 0), false), 200, "")
	mu.Lock()
	if len(events) < 2 || events[0] != "sync" || events[len(events)-1] != "mark" {
		t.Fatalf("sync must precede the received mark: %v", events)
	}
	mu.Unlock()

	// Coalescing: 8 concurrent chunk writes → fewer than 8 syncs.
	big := pattern(8*uploads.ChunkSize, 14)
	rr2, _ := e.register("media", "/coalesce.bin", big, 0, "fail")
	syncs.Store(0)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r := e.put(rr2.ID, i, chunk(big, i), false); r.status != 200 {
				t.Errorf("chunk %d: %d", i, r.status)
			}
		}()
	}
	wg.Wait()
	if n := syncs.Load(); n >= 8 {
		t.Fatalf("expected coalesced syncs, got %d", n)
	}
}

func TestUploadConcurrentDuplicateChunk(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	data := pattern(2*mib, 15)
	rr, _ := e.register("media", "/dup.bin", data, 0, "fail")
	// The first request sends half its body and stalls, holding the chunk.
	pr, pw := io.Pipe()
	req, _ := http.NewRequest("PUT", e.srv.URL+"/api/uploads/"+rr.ID+"/chunks/0", pr)
	req.ContentLength = int64(len(data))
	first := make(chan int, 1)
	go func() {
		res, err := e.client.Do(req)
		if err != nil {
			first <- 0
			return
		}
		res.Body.Close()
		first <- res.StatusCode
	}()
	if _, err := pw.Write(data[:mib]); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	expect(t, e.put(rr.ID, 0, data, false), 409, "chunk_in_progress")
	if _, err := pw.Write(data[mib:]); err != nil {
		t.Fatal(err)
	}
	_ = pw.Close()
	if code := <-first; code != 200 {
		t.Fatalf("first request: %d", code)
	}
}

func TestUploadPartialsHiddenEverywhere(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	data := pattern(mib, 16)
	e.write("media", "/dir/visible.txt", "v")
	_, r := e.register("media", "/dir/hidden-upload.bin", data, 0, "fail")
	expect(t, r, 201, "")
	var l struct {
		Entries []struct{ Name string }
		Uploads []struct{ Name string }
	}
	e.do("GET", "/api/places/media/list?path=/dir", nil).json(&l)
	if len(l.Entries) != 1 || len(l.Uploads) != 1 {
		t.Fatalf("listing %+v", l)
	}
	s := e.do("GET", "/api/places/media/search?q=filet", nil)
	if strings.Contains(string(s.body), "filet-part") {
		t.Fatal("partial in search")
	}
	z := e.do("POST", "/api/places/media/zip", "dir=/&name=dir")
	expect(t, z, 200, "")
	if bytes.Contains(z.body, []byte(".filet-part-")) {
		t.Fatal("partial in zip")
	}
}

func TestUploadAncestorProtection(t *testing.T) {
	e := newEnv(t, envOpts{second: true})
	e.login()
	_, r := e.register("media", "/a/b/c.bin", pattern(mib, 17), 0, "fail")
	expect(t, r, 201, "")
	expect(t, e.do("POST", "/api/places/media/rename", map[string]string{"path": "/a", "newName": "z"}), 409, "upload_in_progress")
	expect(t, e.do("POST", "/api/jobs", map[string]any{"type": "trash", "place": "media", "paths": []string{"/a"}}), 409, "upload_in_progress")
	expect(t, e.do("POST", "/api/jobs", map[string]any{"type": "move", "src": map[string]any{"place": "media", "paths": []string{"/a/b"}},
		"dst": map[string]any{"place": "other", "dir": "/"}}), 409, "upload_in_progress")
	expect(t, e.do("POST", "/api/places/media/mkdir", map[string]any{"path": "/a/b/c.bin"}), 409, "upload_in_progress")
}

func TestUploadReconciliation(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	// (2) partial present with the right size → kept.
	keep := pattern(mib, 20)
	rrKeep, _ := e.register("media", "/keep.bin", keep, 0, "fail")
	// (3) completed file, partial gone, mtime matches → rows deleted.
	done := pattern(mib, 21)
	mt := time.Date(2023, 1, 2, 3, 4, 5, 0, time.UTC).UnixMilli()
	rrDone, _ := e.register("media", "/done.bin", done, mt, "fail")
	// (4) partial missing, no target → rows deleted.
	gone := pattern(mib, 22)
	rrGone, _ := e.register("media", "/gone.bin", gone, 0, "fail")
	// Simulate a crash after the final rename for rrDone, and a lost partial for rrGone.
	partDir := e.places["media"]
	if err := os.Rename(filepath.Join(partDir, ".filet-part-"+rrDone.ID), filepath.Join(partDir, "done.bin")); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(filepath.Join(partDir, "done.bin"), time.Now(), time.UnixMilli(mt))
	_ = os.Remove(filepath.Join(partDir, ".filet-part-"+rrGone.ID))
	// (4b) wrong-size partial → partial and rows deleted.
	bad := pattern(mib, 23)
	rrBad, _ := e.register("media", "/bad.bin", bad, 0, "fail")
	_ = os.Truncate(filepath.Join(partDir, ".filet-part-"+rrBad.ID), 10)
	e.restart()
	rows, _ := e.db.R.ListUploadSessions(context.Background())
	if len(rows) != 1 || rows[0].ID != rrKeep.ID {
		t.Fatalf("after reconcile: %+v", rows)
	}
	if _, err := os.Stat(filepath.Join(partDir, ".filet-part-"+rrBad.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("wrong-size partial not removed")
	}
	// The kept session resumes after the restart.
	expect(t, e.put(rrKeep.ID, 0, keep, true), 200, "")
}

func TestUploadReconcilePlaceMissing(t *testing.T) {
	e := newEnv(t, envOpts{second: true})
	e.login()
	_, r := e.register("other", "/x.bin", pattern(mib, 24), 0, "fail")
	expect(t, r, 201, "")
	// Drop the place from the config and restart.
	e.cfg.Places = e.cfg.Places[:1]
	e.restart()
	clock.Advance(3 * time.Minute)
	defer clock.Reset()
	var l []struct {
		ID           string
		PlaceMissing bool
	}
	e.do("GET", "/api/uploads/leftovers", nil).json(&l)
	if len(l) != 1 || !l[0].PlaceMissing {
		t.Fatalf("leftovers: %+v", l)
	}
	var removed struct{ Removed int }
	e.do("DELETE", "/api/uploads/leftovers", nil).json(&removed)
	if removed.Removed != 1 || e.sessionCount() != 0 {
		t.Fatal("placeMissing leftover not removed")
	}
}

func TestLeftoversCountAndRemoveOnlyLeftovers(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	e.register("media", "/old.bin", pattern(mib, 25), 0, "fail")
	clock.Advance(3 * time.Minute)
	defer clock.Reset()
	e.register("media", "/new.bin", pattern(mib, 26), 0, "fail")
	var c struct{ Count int }
	e.do("GET", "/api/uploads/leftovers/count", nil).json(&c)
	if c.Count != 1 {
		t.Fatalf("count %d", c.Count)
	}
	var removed struct{ Removed int }
	e.do("DELETE", "/api/uploads/leftovers", nil).json(&removed)
	if removed.Removed != 1 || e.sessionCount() != 1 {
		t.Fatal("must remove only leftovers")
	}
}

func TestPreflightStatuses(t *testing.T) {
	e := newEnv(t, envOpts{readonly: true})
	e.login()
	e.write("media", "/exists.txt", "x")
	e.write("media", "/file", "x")
	resumable := pattern(mib, 27)
	e.register("media", "/left.bin", resumable, 0, "fail")
	e.register("media", "/active.bin", pattern(mib, 28), 0, "fail")
	clock.Advance(3 * time.Minute)
	defer clock.Reset()
	e.register("media", "/active.bin", pattern(mib, 28), 0, "fail") // refresh activity
	items := []map[string]any{
		{"key": "1", "place": "ro", "path": "/a", "size": 1},
		{"key": "2", "place": "media", "path": "/../x", "size": 1},
		{"key": "3", "place": "media", "path": "/.filet/x", "size": 1},
		{"key": "4", "place": "media", "path": "/file/x", "size": 1},
		{"key": "5", "place": "media", "path": "/active.bin", "size": mib},
		{"key": "6", "place": "media", "path": "/exists.txt", "size": 1},
		{"key": "7", "place": "media", "path": "/left.bin", "size": mib},
		{"key": "8", "place": "media", "path": "/left.bin", "size": 5},
		{"key": "9", "place": "media", "path": "/new/dir/n.txt", "size": 1},
	}
	var out struct {
		Items []struct {
			Key, Status, SuggestedName string
			ReplacesLeftover           bool
			ReceivedBytes              int64
		}
	}
	e.do("POST", "/api/uploads/preflight", map[string]any{"items": items}).json(&out)
	want := []string{"readonly", "invalid_path", "reserved_name", "parent_not_directory", "uploading", "conflict", "resumable", "ok", "ok"}
	for i, it := range out.Items {
		if it.Status != want[i] {
			t.Errorf("item %s: %s want %s", it.Key, it.Status, want[i])
		}
	}
	if out.Items[5].SuggestedName != "exists (1).txt" || !out.Items[7].ReplacesLeftover {
		t.Fatalf("details: %+v", out.Items)
	}
}
