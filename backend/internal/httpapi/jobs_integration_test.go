package httpapi_test

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"filet/internal/fsops"
	"filet/internal/fsx"
	"filet/internal/thumbs"
)

func copySpec(srcPlace string, paths []string, dstPlace, dir string, extra ...string) map[string]any {
	s := map[string]any{"type": "copy", "src": map[string]any{"place": srcPlace, "paths": paths}, "dst": map[string]any{"place": dstPlace, "dir": dir}}
	if len(extra) > 0 {
		s["conflict"] = extra[0]
	}
	return s
}

func moveSpec(srcPlace string, paths []string, dstPlace, dir string) map[string]any {
	s := copySpec(srcPlace, paths, dstPlace, dir)
	s["type"] = "move"
	return s
}

func TestCopyPreservesModeAndMtime(t *testing.T) {
	e := newEnv(t, envOpts{second: true})
	e.login()
	e.write("media", "/src/a.sh", "#!/bin/sh")
	_ = os.Chmod(e.path("media", "/src/a.sh"), 0o750)
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	_ = os.Chtimes(e.path("media", "/src/a.sh"), old, old)
	e.write("media", "/src/empty/.keep", "")
	_ = os.Chtimes(e.path("media", "/src"), old, old)
	j := e.submit(copySpec("media", []string{"/src"}, "other", "/"))
	if j.Status != "completed" || len(j.Result.Errors) != 0 {
		t.Fatalf("job: %+v", j)
	}
	fi, _ := os.Stat(e.path("other", "/src/a.sh"))
	if fi.Mode().Perm() != 0o750 || !fi.ModTime().Equal(old) {
		t.Fatalf("mode %v mtime %v", fi.Mode(), fi.ModTime())
	}
	di, _ := os.Stat(e.path("other", "/src"))
	if !di.ModTime().Equal(old) {
		t.Fatalf("dir mtime %v", di.ModTime())
	}
	if !e.exists("other", "/src/empty/.keep") {
		t.Fatal("nested content missing")
	}
	// Copying a place root never copies its .filet directory.
	j = e.submit(copySpec("media", []string{"/"}, "other", "/"))
	if j.Status != "completed" || e.exists("other", "/Media/.filet") || !e.exists("other", "/Media/src/a.sh") {
		t.Fatalf("root copy: %+v", j)
	}
}

func TestCopyCancelAndRestartCleanup(t *testing.T) {
	e := newEnv(t, envOpts{second: true})
	e.login()
	for i := range 10 {
		e.write("media", "/big/f"+string(rune('a'+i))+".bin", strings.Repeat("x", 1<<20))
	}
	fsops.TestSegmentDelay.Store(int64(100 * time.Millisecond))
	defer fsops.TestSegmentDelay.Store(0)
	r := e.do("POST", "/api/jobs", copySpec("media", []string{"/big"}, "other", "/"))
	var j jobJSON
	r.json(&j)
	time.Sleep(250 * time.Millisecond)
	expect(t, e.do("POST", "/api/jobs/"+j.ID+"/cancel", nil), 200, "")
	j = e.waitJob(j.ID)
	if j.Status != "cancelled" || j.ItemsDone == 0 || j.ItemsDone >= j.ItemsTotal {
		t.Fatalf("cancel: %+v", j)
	}
	if temps(t, e.places["other"]) != 0 {
		t.Fatal("temp files left after cancel")
	}

	// A crash mid-copy leaves the row "running" with a temp file recorded;
	// startup marks it interrupted and removes the file. Recreate that state.
	tmp := e.path("other", "/big/.filet-copy-"+j.ID+"-99")
	_ = os.WriteFile(tmp, []byte("partial"), 0o644)
	_, _ = e.db.Writer.Exec("UPDATE jobs SET temp_place_id='other', temp_path='/big/.filet-copy-"+j.ID+"-99', status='running' WHERE id=?", j.ID)
	e.restart()
	var after jobJSON
	e.do("GET", "/api/jobs/"+j.ID, nil).json(&after)
	if after.Status != "interrupted" || after.Error == nil || after.Error.Code != "server_restarted" {
		t.Fatalf("after restart: %+v", after)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatal("temp file not removed at startup")
	}
}

func temps(t *testing.T, root string) int {
	n := 0
	_ = filepath.Walk(root, func(p string, _ os.FileInfo, _ error) error {
		if strings.Contains(filepath.Base(p), ".filet-copy-") {
			n++
		}
		return nil
	})
	return n
}

func TestMoveVariants(t *testing.T) {
	e := newEnv(t, envOpts{second: true})
	e.login()
	// Same place rename.
	e.write("media", "/a/x.txt", "x")
	e.write("media", "/b/keep.txt", "k")
	if j := e.submit(moveSpec("media", []string{"/a/x.txt"}, "media", "/b")); j.Status != "completed" || !e.exists("media", "/b/x.txt") || e.exists("media", "/a/x.txt") {
		t.Fatalf("same place: %+v", j)
	}
	// Cross place on the same device (renameat2 between roots).
	if j := e.submit(moveSpec("media", []string{"/b/x.txt"}, "other", "/")); j.Status != "completed" || !e.exists("other", "/x.txt") {
		t.Fatalf("cross place: %+v", j)
	}
	// Directory merge (A4).
	e.write("media", "/m/inner/one.txt", "1")
	e.write("other", "/m/inner/two.txt", "2")
	if j := e.submit(moveSpec("media", []string{"/m"}, "other", "/")); j.Status != "completed" || !e.exists("other", "/m/inner/one.txt") || !e.exists("other", "/m/inner/two.txt") || e.exists("media", "/m") {
		t.Fatalf("merge: %+v", j)
	}
	// Cross device: copy then delete (EXDEV simulated).
	fsx.SetRenameHookForTest(func(o, n string) error {
		if !strings.HasPrefix(o, ".filet-copy-") {
			return syscall.EXDEV
		}
		return nil
	})
	defer fsx.SetRenameHookForTest(nil)
	e.write("media", "/xd/deep/f.txt", "cross")
	j := e.submit(moveSpec("media", []string{"/xd"}, "other", "/"))
	if j.Status != "completed" || e.read("other", "/xd/deep/f.txt") != "cross" || e.exists("media", "/xd") {
		t.Fatalf("cross device: %+v", j)
	}
	// Moving into itself is refused synchronously.
	e.write("media", "/self/a.txt", "a")
	expect(t, e.do("POST", "/api/jobs", moveSpec("media", []string{"/self"}, "media", "/self")), 400, "invalid_destination")
}

func TestConflictFlow(t *testing.T) {
	e := newEnv(t, envOpts{second: true})
	e.login()
	for _, n := range []string{"r1.txt", "r2.txt", "r3.txt", "report.pdf", ".env", "dir"} {
		if n == "dir" {
			e.write("media", "/src/dir/in.txt", "new")
			e.write("other", "/dir", "a file named dir")
			continue
		}
		e.write("media", "/src/"+n, "new "+n)
		e.write("other", "/"+n, "old "+n)
	}
	paths := []string{"/src/r1.txt", "/src/r2.txt", "/src/r3.txt"}
	j := e.submit(copySpec("media", paths, "other", "/"))
	if j.Status != "awaiting_input" || j.PendingConflict == nil || j.PendingConflict.SuggestedName != "r1 (1).txt" {
		t.Fatalf("expected a prompt: %+v", j)
	}
	// A stale id is rejected.
	expect(t, e.do("POST", "/api/jobs/"+j.ID+"/resolve", map[string]any{"conflictId": "nope", "action": "skip"}), 409, "stale_conflict")
	// Overwrite the first; the old file goes to trash.
	expect(t, e.do("POST", "/api/jobs/"+j.ID+"/resolve", map[string]any{"conflictId": j.PendingConflict.ID, "action": "overwrite"}), 200, "")
	j = e.waitJob(j.ID)
	if j.Status != "awaiting_input" || e.read("other", "/r1.txt") != "new r1.txt" {
		t.Fatalf("after overwrite: %+v", j)
	}
	var trash []struct{ OriginalPath string }
	e.do("GET", "/api/places/other/trash", nil).json(&trash)
	if len(trash) != 1 || trash[0].OriginalPath != "/r1.txt" {
		t.Fatalf("trash: %+v", trash)
	}
	// Keep both, applied to all remaining conflicts.
	expect(t, e.do("POST", "/api/jobs/"+j.ID+"/resolve", map[string]any{"conflictId": j.PendingConflict.ID, "action": "rename", "applyToAll": true}), 200, "")
	j = e.waitJob(j.ID)
	if j.Status != "completed" || e.read("other", "/r2 (1).txt") != "new r2.txt" || e.read("other", "/r3 (1).txt") != "new r3.txt" || len(j.Result.Renamed) != 2 {
		t.Fatalf("apply to all: %+v", j)
	}
	// Preset conflict policies and naming of dotfiles and directories.
	j = e.submit(copySpec("media", []string{"/src/.env", "/src/report.pdf", "/src/dir"}, "other", "/", "rename"))
	if j.Status != "completed" || !e.exists("other", "/.env (1)") || !e.exists("other", "/report (1).pdf") || !e.exists("other", "/dir (1)/in.txt") {
		t.Fatalf("keep-both naming: %+v", j)
	}
	j = e.submit(copySpec("media", []string{"/src/report.pdf"}, "other", "/", "skip"))
	if j.Status != "completed" || j.Result.Skipped != 1 {
		t.Fatalf("skip: %+v", j)
	}
	// Copying a file onto itself offers no overwrite.
	j = e.submit(copySpec("other", []string{"/report.pdf"}, "other", "/"))
	if j.PendingConflict == nil || !j.PendingConflict.SameItem {
		t.Fatalf("same item: %+v", j)
	}
	expect(t, e.do("POST", "/api/jobs/"+j.ID+"/resolve", map[string]any{"conflictId": j.PendingConflict.ID, "action": "overwrite"}), 400, "invalid_request")
	expect(t, e.do("POST", "/api/jobs/"+j.ID+"/cancel", nil), 200, "")
	if j = e.waitJob(j.ID); j.Status != "cancelled" {
		t.Fatalf("cancel while waiting: %+v", j)
	}
	// Terminal jobs can be dismissed.
	expect(t, e.do("DELETE", "/api/jobs/"+j.ID, nil), 204, "")
	expect(t, e.do("GET", "/api/jobs/"+j.ID, nil), 404, "job_not_found")
}

func TestTrashRestorePurge(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	e.write("media", "/docs/2024/report.pdf", "pdf")
	e.write("media", "/folder/a.txt", "a")
	j := e.submit(map[string]any{"type": "trash", "place": "media", "paths": []string{"/docs/2024/report.pdf", "/folder"}})
	if j.Status != "completed" || e.exists("media", "/docs/2024/report.pdf") {
		t.Fatalf("trash: %+v", j)
	}
	_ = os.RemoveAll(e.path("media", "/docs")) // the parent is recreated on restore
	var items []struct {
		ID, OriginalPath string
		Size             *int64
	}
	time.Sleep(100 * time.Millisecond)
	e.do("GET", "/api/places/media/trash", nil).json(&items)
	if len(items) != 2 {
		t.Fatalf("trash list: %+v", items)
	}
	var reportID, folderID string
	for _, it := range items {
		if it.OriginalPath == "/folder" {
			folderID = it.ID
			if it.Size == nil || *it.Size != 1 {
				t.Errorf("directory size: %v", it.Size)
			}
		} else {
			reportID = it.ID
		}
	}
	j = e.submit(map[string]any{"type": "restore", "place": "media", "ids": []string{reportID}})
	if j.Status != "completed" || e.read("media", "/docs/2024/report.pdf") != "pdf" {
		t.Fatalf("restore: %+v", j)
	}
	j = e.submit(map[string]any{"type": "purge", "place": "media", "all": true})
	if j.Status != "completed" {
		t.Fatalf("purge: %+v", j)
	}
	if entries, _ := os.ReadDir(e.path("media", "/.filet/trash")); len(entries) != 0 {
		t.Fatalf("trash dir not empty: %v", entries)
	}
	_ = folderID
	// EXDEV fallback: the item is copied into the trash and removed.
	fsx.SetRenameHookForTest(func(o, n string) error {
		if o == "mounted.bin" {
			return syscall.EXDEV
		}
		return nil
	})
	defer fsx.SetRenameHookForTest(nil)
	e.write("media", "/mounted.bin", "on another fs")
	j = e.submit(map[string]any{"type": "trash", "place": "media", "paths": []string{"/mounted.bin"}})
	if j.Status != "completed" || e.exists("media", "/mounted.bin") {
		t.Fatalf("exdev trash: %+v", j)
	}
	e.do("GET", "/api/places/media/trash", nil).json(&items)
	if len(items) != 1 {
		t.Fatalf("exdev trash list: %+v", items)
	}
}

func TestTrashReconciliation(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	trash := e.path("media", "/.filet/trash")
	_ = os.MkdirAll(filepath.Join(trash, "orphan1"), 0o700)
	_ = os.WriteFile(filepath.Join(trash, "orphan1", "lost.txt"), []byte("x"), 0o644)
	_ = os.MkdirAll(filepath.Join(trash, "empty1"), 0o700)
	_, _ = e.db.Writer.Exec("INSERT INTO trash_items (id, place_id, original_path, name, is_dir, deleted_at) VALUES ('gone1','media','/g','g',0,1)")
	e.restart()
	var items []struct {
		ID, OriginalPath string
		OriginUnknown    bool
	}
	e.do("GET", "/api/places/media/trash", nil).json(&items)
	if len(items) != 1 || items[0].ID != "orphan1" || items[0].OriginalPath != "/lost.txt" || !items[0].OriginUnknown {
		t.Fatalf("reconciled: %+v", items)
	}
	if _, err := os.Stat(filepath.Join(trash, "empty1")); !os.IsNotExist(err) {
		t.Fatal("empty trash dir not removed")
	}
}

func TestZipContents(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	e.write("media", "/z/a.txt", "a")
	e.write("media", "/z/.dot", "d")
	_ = os.MkdirAll(e.path("media", "/z/empty"), 0o755)
	e.write("media", "/z/.filet-part-x", "hidden")
	r := e.do("POST", "/api/places/media/zip", "dir=/&name=z")
	expect(t, r, 200, "")
	if r.header.Get("Content-Disposition") != "attachment; filename*=UTF-8''z.zip" {
		t.Fatalf("disposition %q", r.header.Get("Content-Disposition"))
	}
	zr, err := zip.NewReader(bytes.NewReader(r.body), int64(len(r.body)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]uint16{}
	for _, f := range zr.File {
		names[f.Name] = f.Method
	}
	for _, want := range []string{"z/", "z/a.txt", "z/.dot", "z/empty/"} {
		if _, ok := names[want]; !ok {
			t.Errorf("missing %s in %v", want, names)
		}
	}
	if names["z/a.txt"] != zip.Store {
		t.Error("not stored")
	}
	if _, ok := names["z/.filet-part-x"]; ok {
		t.Error("reserved name in zip")
	}
	if _, ok := names["_filet_skipped.txt"]; ok {
		t.Error("skipped list without skips")
	}
	if os.Geteuid() != 0 {
		_ = os.Chmod(e.path("media", "/z/a.txt"), 0)
		r = e.do("POST", "/api/places/media/zip", "dir=/&name=z")
		zr, _ = zip.NewReader(bytes.NewReader(r.body), int64(len(r.body)))
		found := false
		for _, f := range zr.File {
			if f.Name == "_filet_skipped.txt" {
				rc, _ := f.Open()
				b, _ := io.ReadAll(rc)
				found = strings.Contains(string(b), "z/a.txt")
			}
		}
		_ = os.Chmod(e.path("media", "/z/a.txt"), 0o644)
		if !found {
			t.Fatal("unreadable file not listed in _filet_skipped.txt")
		}
	}
}

func TestZip64LargeSparseFile(t *testing.T) {
	if testing.Short() {
		t.Skip("streams 4 GiB")
	}
	e := newEnv(t, envOpts{})
	e.login()
	f, _ := os.Create(e.path("media", "/huge.bin"))
	_ = f.Truncate(4<<30 + 1024)
	_ = f.Close()
	res, err := e.client.Post(e.srv.URL+"/api/places/media/zip", "application/x-www-form-urlencoded", strings.NewReader("dir=/&name=huge.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	tmp, _ := os.CreateTemp(t.TempDir(), "z")
	n, err := io.Copy(tmp, res.Body)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(tmp, n)
	if err != nil {
		t.Fatal(err)
	}
	if zr.File[0].UncompressedSize64 != 4<<30+1024 {
		t.Fatalf("size %d", zr.File[0].UncompressedSize64)
	}
}

func TestSearch(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	e.write("media", "/Photos/Summer Trip.JPG", "x")
	e.write("media", "/Photos/winter trip.jpg", "x")
	e.write("media", "/docs/trip-notes.txt", "x")
	e.write("media", "/.hidden/trip-secret.txt", "x")
	e.write("media", "/docs/.trip-dot", "x")
	search := func(q string, hidden bool, limit int) ([]string, map[string]any) {
		url := "/api/places/media/search?q=" + strings.ReplaceAll(q, " ", "%20")
		if hidden {
			url += "&hidden=1"
		}
		if limit > 0 {
			url += "&limit=" + string(rune('0'+limit))
		}
		r := e.do("GET", url, nil)
		var paths []string
		var done map[string]any
		sc := bufio.NewScanner(bytes.NewReader(r.body))
		for sc.Scan() {
			var m map[string]any
			_ = json.Unmarshal(sc.Bytes(), &m)
			if m["done"] == true {
				done = m
			} else {
				paths = append(paths, m["path"].(string))
			}
		}
		return paths, done
	}
	if p, _ := search("TRIP jpg", false, 0); len(p) != 2 {
		t.Fatalf("multi-term: %v", p)
	}
	if p, _ := search("trip", false, 0); len(p) != 3 {
		t.Fatalf("hidden excluded: %v", p)
	}
	if p, _ := search("trip", true, 0); len(p) != 5 {
		t.Fatalf("hidden included: %v", p)
	}
	if p, d := search("trip", false, 2); len(p) != 2 || d["truncated"] != true {
		t.Fatalf("limit: %v %v", p, d)
	}
	expect(t, e.do("GET", "/api/places/media/search?q=a", nil), 400, "query_too_short")
	// Aborting the request cancels the walk.
	ctx, cancel := context.WithCancel(context.Background())
	req, err := newReq(ctx, e.srv.URL+"/api/places/media/search?q=trip")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if res, err := e.client.Do(req); err == nil {
		_ = res.Body.Close()
	}
}

// Target: a cached thumbnail is served in < 10 ms server time.
func TestCachedThumbnailFast(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.login()
	pl, _ := e.reg.Get("media")
	// Pretend vipsthumbnail exists and a thumbnail is already cached.
	e.reg.Caps.VipsThumbnail = true
	e.write("media", "/a.jpg", "jpeg bytes")
	fi, _ := os.Stat(e.path("media", "/a.jpg"))
	cache := thumbs.CachePath(pl, thumbs.Key(fsx.IdentityOf(fi)), thumbs.SuffixThumb)
	_ = os.MkdirAll(filepath.Dir(cache), 0o700)
	_ = os.WriteFile(cache, bytes.Repeat([]byte("w"), 20000), 0o600)
	r := e.do("GET", "/api/places/media/thumb/a.jpg?v=1", nil)
	expect(t, r, 200, "")
	if r.header.Get("Content-Type") != "image/webp" || !strings.Contains(r.header.Get("Cache-Control"), "immutable") {
		t.Fatalf("headers: %v", r.header)
	}
	best := time.Hour
	for range 20 {
		start := time.Now()
		r = e.do("GET", "/api/places/media/thumb/a.jpg?v=1", nil)
		best = min(best, time.Since(start))
	}
	t.Logf("cached thumbnail round trip %v", best)
	if best > 10*time.Millisecond {
		t.Fatalf("cached thumbnail took %v", best)
	}
	// Non-thumbable files: 404 no_thumbnail with a day of caching.
	e.write("media", "/x.txt", "x")
	r = e.do("GET", "/api/places/media/thumb/x.txt", nil)
	expect(t, r, 404, "no_thumbnail")
	if r.header.Get("Cache-Control") != "private, max-age=86400" {
		t.Fatalf("cache-control %q", r.header.Get("Cache-Control"))
	}
}
