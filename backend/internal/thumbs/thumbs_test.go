package thumbs

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"filet/internal/config"
	"filet/internal/fsx"
	"filet/internal/places"
	"filet/internal/tools"
)

func setup(t *testing.T) (*Cache, *places.Place, string) {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	runner := tools.New(tools.Names{}, log)
	runner.Caps = tools.Caps{VipsThumbnail: true, Vips: true}
	reg, err := places.NewRegistry([]config.PlaceConfig{{ID: "p", Name: "P", ResolvedPath: dir}}, t.TempDir(), &runner.Caps, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reg.Close)
	p, _ := reg.Get("p")
	return New(runner, nil, log), p, dir
}

func ident(t *testing.T, path string) fsx.Identity {
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fsx.IdentityOf(fi)
}

func TestKeyStableAcrossRenameAndChangesWithContent(t *testing.T) {
	_, _, dir := setup(t)
	a := filepath.Join(dir, "a.jpg")
	_ = os.WriteFile(a, []byte("img"), 0o644)
	k1 := Key(ident(t, a))
	b := filepath.Join(dir, "sub")
	_ = os.Mkdir(b, 0o755)
	_ = os.Rename(a, filepath.Join(b, "renamed.jpg"))
	if Key(ident(t, filepath.Join(b, "renamed.jpg"))) != k1 {
		t.Fatal("key changed after rename")
	}
	_ = os.Chtimes(filepath.Join(b, "renamed.jpg"), time.Now(), time.Now().Add(time.Hour))
	if Key(ident(t, filepath.Join(b, "renamed.jpg"))) == k1 {
		t.Fatal("key did not change with mtime")
	}
	if len(k1) != 32 {
		t.Fatalf("key length %d", len(k1))
	}
}

func TestFailMarkerAndSingleflight(t *testing.T) {
	c, p, dir := setup(t)
	_ = os.WriteFile(filepath.Join(dir, "x.jpg"), []byte("img"), 0o644)
	var calls atomic.Int32
	release := make(chan struct{})
	c.generate = func(ctx context.Context, tk *task) error {
		calls.Add(1)
		<-release
		_ = os.MkdirAll(filepath.Dir(tk.file), 0o700)
		return os.WriteFile(tk.file, []byte("webp"), 0o600)
	}
	c.Start(4)
	defer c.Stop()
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.Thumbnail(context.Background(), p.FullView(), "/x.jpg", Interactive)
			errs <- err
		}()
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("generator ran %d times", calls.Load())
	}

	// A failure writes the marker; no retry until the file changes.
	_ = os.WriteFile(filepath.Join(dir, "bad.jpg"), []byte("bad"), 0o644)
	calls.Store(0)
	c.generate = func(ctx context.Context, tk *task) error { calls.Add(1); return io.ErrUnexpectedEOF }
	for range 3 {
		if _, err := c.Thumbnail(context.Background(), p.FullView(), "/bad.jpg", Interactive); err != ErrNoThumbnail {
			t.Fatalf("want ErrNoThumbnail, got %v", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("failed thumbnail retried %d times", calls.Load())
	}
	if _, err := os.Stat(CachePath(p, Key(ident(t, filepath.Join(dir, "bad.jpg"))), SuffixFail)); err != nil {
		t.Fatal("no .fail marker")
	}
}

func TestPriorityInteractiveLIFOBeatsPrefetch(t *testing.T) {
	c, p, dir := setup(t)
	var mu sync.Mutex
	var order []string
	gate := make(chan struct{})
	c.generate = func(ctx context.Context, tk *task) error {
		if tk.name == "blocker.jpg" {
			<-gate
		}
		mu.Lock()
		order = append(order, tk.name)
		mu.Unlock()
		_ = os.MkdirAll(filepath.Dir(tk.file), 0o700)
		return os.WriteFile(tk.file, []byte("w"), 0o600)
	}
	for _, n := range []string{"blocker.jpg", "pre1.jpg", "pre2.jpg", "int1.jpg", "int2.jpg"} {
		_ = os.WriteFile(filepath.Join(dir, n), []byte(n), 0o644)
	}
	c.Start(1)
	defer c.Stop()
	v := p.FullView()
	c.Enqueue(v, "/blocker.jpg", Prefetch)
	time.Sleep(50 * time.Millisecond) // the single worker is now busy
	c.Enqueue(v, "/pre1.jpg", Prefetch)
	c.Enqueue(v, "/pre2.jpg", Prefetch)
	var wg sync.WaitGroup
	for _, n := range []string{"/int1.jpg", "/int2.jpg"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.Thumbnail(context.Background(), v, n, Interactive)
		}()
		time.Sleep(20 * time.Millisecond)
	}
	close(gate)
	wg.Wait()
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	want := []string{"blocker.jpg", "int2.jpg", "int1.jpg", "pre1.jpg", "pre2.jpg"}
	for i := range want {
		if i >= len(order) || order[i] != want[i] {
			t.Fatalf("order %v want %v", order, want)
		}
	}
}
