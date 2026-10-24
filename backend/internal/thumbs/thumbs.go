// Package thumbs generates and caches thumbnails (WebP, ≤400×400) and
// browser-viewable renditions (JPEG, ≤3840 px) in each place's thumbnail
// directory, using a priority worker pool with singleflight per cache key.
package thumbs

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/zeebo/blake3"

	"filet/internal/apierr"
	"filet/internal/fsx"
	"filet/internal/ids"
	"filet/internal/places"
	"filet/internal/tools"
)

// Priority orders work in the pool.
type Priority int

// Priorities, lowest first.
const (
	Background Priority = iota
	Prefetch
	Interactive
)

const (
	queueCap         = 50000
	thumbTimeout     = 30 * time.Second
	renditionTimeout = 120 * time.Second
)

// Cache file suffixes.
const (
	SuffixThumb     = ".t.webp"
	SuffixRendition = ".r.jpg"
	SuffixFail      = ".fail"
)

// ErrNoThumbnail means no thumbnail exists or can be generated.
var ErrNoThumbnail = apierr.ErrNoThumbnail

var errDropped = errors.New("thumbnail task dropped")

// DurationProber returns a media file's duration in seconds (0 if unknown).
type DurationProber interface {
	Duration(ctx context.Context, realPath string, id fsx.Identity) (float64, error)
}

// Key computes the identity-based cache key (32 hex chars).
func Key(id fsx.Identity) string {
	var b [13 + 32]byte
	copy(b[:], "filet-thumb-v1")
	binary.BigEndian.PutUint64(b[13:], id.Dev)
	binary.BigEndian.PutUint64(b[21:], id.Ino)
	binary.BigEndian.PutUint64(b[29:], uint64(id.Size))    //nolint:gosec // bit pattern
	binary.BigEndian.PutUint64(b[37:], uint64(id.MtimeNs)) //nolint:gosec // bit pattern
	sum := blake3.Sum256(b[:])
	return hex.EncodeToString(sum[:16])
}

// CachePath returns the cache file path for key with suffix in a place.
func CachePath(p *places.Place, key, suffix string) string {
	return filepath.Join(p.ThumbDir, key[0:2], key[2:4], key+suffix)
}

type task struct {
	file      string // final cache file
	kind      string
	name      string
	view      *places.View
	rel       string
	ident     fsx.Identity
	rendition bool

	prio    Priority
	state   int // 0 queued, 1 running, 2 done
	waiters int
	done    chan struct{}
	err     error
}

// Cache is the thumbnail and rendition cache.
type Cache struct {
	runner *tools.Runner
	prober DurationProber
	log    *slog.Logger

	mu          sync.Mutex
	cond        *sync.Cond
	tasks       map[string]*task
	interactive []*task
	prefetch    fifo
	background  fifo
	closed      bool
	failedRend  map[string]bool
	wg          sync.WaitGroup

	// generate is replaceable in tests.
	generate func(ctx context.Context, t *task) error
}

// New creates a cache with n workers. Call Start to run them.
func New(runner *tools.Runner, prober DurationProber, log *slog.Logger) *Cache {
	c := &Cache{runner: runner, prober: prober, log: log, tasks: map[string]*task{}, failedRend: map[string]bool{}}
	c.cond = sync.NewCond(&c.mu)
	c.generate = c.run
	return c
}

// Start launches n workers.
func (c *Cache) Start(n int) {
	for range n {
		c.wg.Add(1)
		go c.worker()
	}
}

// Stop stops the workers after their current task.
func (c *Cache) Stop() {
	c.mu.Lock()
	c.closed = true
	c.cond.Broadcast()
	c.mu.Unlock()
	c.wg.Wait()
}

func (c *Cache) caps() *tools.Caps { return &c.runner.Caps }

// Thumbnail returns the path of rel's cached thumbnail, generating it at
// prio if needed.
func (c *Cache) Thumbnail(ctx context.Context, v *places.View, rel string, prio Priority) (string, error) {
	fi, err := v.Stat(rel)
	if err != nil {
		return "", apierr.FromFS(err)
	}
	_, name := places.Split(v.PlacePath(rel))
	if !fi.Mode().IsRegular() || !places.Thumbable(name, c.caps()) {
		return "", ErrNoThumbnail
	}
	id := fsx.IdentityOf(fi)
	key := Key(id)
	file := CachePath(v.Place, key, SuffixThumb)
	if exists(file) {
		return file, nil
	}
	if exists(CachePath(v.Place, key, SuffixFail)) {
		return "", ErrNoThumbnail
	}
	t := &task{file: file, kind: places.EffectiveKind(name, c.caps()), name: name, view: v, rel: rel, ident: id}
	if err := c.wait(ctx, t, prio); err != nil {
		return "", err
	}
	return file, nil
}

// Rendition returns the path of rel's cached JPEG rendition.
func (c *Cache) Rendition(ctx context.Context, v *places.View, rel string) (string, error) {
	fi, err := v.Stat(rel)
	if err != nil {
		return "", apierr.FromFS(err)
	}
	_, name := places.Split(v.PlacePath(rel))
	if !fi.Mode().IsRegular() || places.EffectiveKind(name, c.caps()) != places.KindImageRender {
		return "", apierr.ErrUnsupportedFormat
	}
	if !c.caps().Vips {
		return "", apierr.ToolUnavailable("vips")
	}
	id := fsx.IdentityOf(fi)
	key := Key(id)
	file := CachePath(v.Place, key, SuffixRendition)
	if exists(file) {
		return file, nil
	}
	c.mu.Lock()
	failed := c.failedRend[file]
	c.mu.Unlock()
	if failed {
		return "", apierr.New(http.StatusUnprocessableEntity, "render_failed", "Could not render this image")
	}
	t := &task{file: file, kind: places.KindImageRender, name: name, view: v, rel: rel, ident: id, rendition: true}
	if err := c.wait(ctx, t, Interactive); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			return "", apierr.New(http.StatusUnprocessableEntity, "render_failed", "Could not render this image")
		}
		return "", err
	}
	return file, nil
}

// Enqueue schedules thumbnail generation without waiting.
func (c *Cache) Enqueue(v *places.View, rel string, prio Priority) {
	fi, err := v.Stat(rel)
	if err != nil || !fi.Mode().IsRegular() {
		return
	}
	_, name := places.Split(v.PlacePath(rel))
	c.EnqueueIdent(v, rel, name, fsx.IdentityOf(fi), prio)
}

// EnqueueIdent schedules generation for a file whose identity is known
// (from a listing), skipping it if a cache file or failure marker exists.
func (c *Cache) EnqueueIdent(v *places.View, rel, name string, id fsx.Identity, prio Priority) {
	if !places.Thumbable(name, c.caps()) {
		return
	}
	key := Key(id)
	file := CachePath(v.Place, key, SuffixThumb)
	if exists(file) || exists(CachePath(v.Place, key, SuffixFail)) {
		return
	}
	t := &task{file: file, kind: places.EffectiveKind(name, c.caps()), name: name, view: v, rel: rel, ident: id}
	c.mu.Lock()
	c.submitLocked(t, prio)
	c.mu.Unlock()
}

// EnqueueListing schedules prefetch for every thumbable entry of a listing.
func (c *Cache) EnqueueListing(v *places.View, dir string, entries []places.Entry) {
	go func() {
		for i := range entries {
			e := &entries[i]
			if e.Type == "file" && e.Thumbable {
				c.EnqueueIdent(v, places.Join(dir, e.Name), e.Name, e.Ident, Prefetch)
			}
		}
	}()
}

// Delete removes every cache file for a file identity (purge/overwrite).
func (c *Cache) Delete(p *places.Place, id fsx.Identity) {
	key := Key(id)
	for _, s := range []string{SuffixThumb, SuffixRendition, SuffixFail} {
		_ = os.Remove(CachePath(p, key, s))
	}
	if m, _ := filepath.Glob(CachePath(p, key, ".s*.vtt")); m != nil {
		for _, f := range m {
			_ = os.Remove(f)
		}
	}
}

// submitLocked adds or upgrades a task and returns the live task.
func (c *Cache) submitLocked(t *task, prio Priority) *task {
	if cur, ok := c.tasks[t.file]; ok {
		if cur.state == 0 && prio > cur.prio {
			cur.prio = prio
			c.pushLocked(cur)
		}
		return cur
	}
	t.prio = prio
	t.done = make(chan struct{})
	c.tasks[t.file] = t
	c.pushLocked(t)
	return t
}

func (c *Cache) pushLocked(t *task) {
	switch t.prio {
	case Interactive:
		c.interactive = append(c.interactive, t)
	case Prefetch:
		if dropped := c.prefetch.push(t); dropped != nil {
			c.dropLocked(dropped, Prefetch)
		}
	default:
		if dropped := c.background.push(t); dropped != nil {
			c.dropLocked(dropped, Background)
		}
	}
	c.cond.Signal()
}

func (c *Cache) dropLocked(t *task, prio Priority) {
	if t.state == 0 && t.prio == prio && t.waiters == 0 {
		t.state = 2
		t.err = errDropped
		delete(c.tasks, t.file)
		close(t.done)
	}
}

func (c *Cache) wait(ctx context.Context, t *task, prio Priority) error {
	c.mu.Lock()
	t = c.submitLocked(t, prio)
	t.waiters++
	c.mu.Unlock()
	select {
	case <-t.done:
		c.mu.Lock()
		t.waiters--
		c.mu.Unlock()
		if t.err != nil {
			if errors.Is(t.err, tools.ErrUnavailable) || errors.Is(t.err, errNoGenerator) {
				return ErrNoThumbnail
			}
			if !t.rendition {
				return ErrNoThumbnail
			}
			return t.err
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		t.waiters--
		if t.waiters == 0 && t.state == 0 && t.prio == Interactive {
			// The tile scrolled away; it will probably be needed soon.
			t.prio = Prefetch
			c.pushLocked(t)
		}
		c.mu.Unlock()
		return ctx.Err()
	}
}

func (c *Cache) popLocked() *task {
	for len(c.interactive) > 0 {
		t := c.interactive[len(c.interactive)-1]
		c.interactive = c.interactive[:len(c.interactive)-1]
		if t.state == 0 && t.prio == Interactive {
			return t
		}
	}
	for c.prefetch.len() > 0 {
		if t := c.prefetch.pop(); t.state == 0 && t.prio == Prefetch {
			return t
		}
	}
	for c.background.len() > 0 {
		if t := c.background.pop(); t.state == 0 && t.prio == Background {
			return t
		}
	}
	return nil
}

func (c *Cache) worker() {
	defer c.wg.Done()
	for {
		c.mu.Lock()
		var t *task
		for {
			if c.closed {
				c.mu.Unlock()
				return
			}
			if t = c.popLocked(); t != nil {
				break
			}
			c.cond.Wait()
		}
		t.state = 1
		c.mu.Unlock()

		timeout := thumbTimeout
		if t.rendition {
			timeout = renditionTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		err := c.generate(ctx, t)
		cancel()
		if err != nil && !errors.Is(err, tools.ErrUnavailable) && !errors.Is(err, errNoGenerator) {
			c.log.Debug("thumbnail generation failed", "file", t.rel, "err", err)
			if t.rendition {
				c.mu.Lock()
				c.failedRend[t.file] = true
				c.mu.Unlock()
			} else {
				writeFail(strings0(t.file, SuffixThumb) + SuffixFail)
			}
		}

		c.mu.Lock()
		t.err = err
		t.state = 2
		delete(c.tasks, t.file)
		close(t.done)
		c.mu.Unlock()
	}
}

func strings0(s, suffix string) string { return s[:len(s)-len(suffix)] }

func writeFail(path string) {
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600); err == nil { //nolint:gosec // cache path
		_ = f.Close()
	}
}

var errNoGenerator = errors.New("no generator for kind")

// run generates the cache file for t with the external tools.
func (c *Cache) run(ctx context.Context, t *task) error {
	src, err := t.view.RealPath(t.rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(t.file), 0o700); err != nil {
		return err
	}
	tmp := t.file + ".tmp-" + ids.New()[:8]
	opts := tools.Opts{Nice: t.prio == Background}
	names := c.runner.Names
	var outFile string
	switch {
	case t.rendition:
		outFile = tmp + ".jpg"
		_, err = c.runner.Run(ctx, opts, names.Vips, "thumbnail", src, outFile+"[Q=88,strip]", "3840", "--height", "3840", "--size", "down")
	case t.kind == places.KindImage || t.kind == places.KindImageRender || t.kind == places.KindPDF:
		if !c.caps().VipsThumbnail {
			return tools.ErrUnavailable
		}
		outFile = tmp + ".webp"
		_, err = c.runner.Run(ctx, opts, names.VipsThumbnail, src, "--size", "400x400>", "-o", outFile+"[Q=75,effort=2,strip]")
	case t.kind == places.KindVideo:
		outFile = tmp
		at := 1.0
		if c.prober != nil {
			if d, perr := c.prober.Duration(ctx, src, t.ident); perr == nil && d > 0 {
				at = math.Max(0, math.Min(d*0.1, d-0.5))
			}
		}
		err = c.ffmpegFrame(ctx, opts, src, outFile, at)
		if err != nil && at != 0 && ctx.Err() == nil {
			err = c.ffmpegFrame(ctx, opts, src, outFile, 0)
		}
	case t.kind == places.KindAudio:
		outFile = tmp
		_, err = c.runner.Run(ctx, opts, names.FFmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-i", src,
			"-map", "0:v:0", "-frames:v", "1", "-vf", "scale=400:400:force_original_aspect_ratio=decrease",
			"-c:v", "libwebp", "-quality", "75", "-f", "webp", outFile)
	default:
		return errNoGenerator
	}
	if err != nil {
		_ = os.Remove(outFile)
		return err
	}
	if fi, serr := os.Stat(outFile); serr != nil || fi.Size() == 0 {
		_ = os.Remove(outFile)
		return fmt.Errorf("no output produced")
	}
	return os.Rename(outFile, t.file)
}

func (c *Cache) ffmpegFrame(ctx context.Context, opts tools.Opts, src, out string, at float64) error {
	_, err := c.runner.Run(ctx, opts, c.runner.Names.FFmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-ss", strconv.FormatFloat(at, 'f', 3, 64), "-i", src, "-frames:v", "1",
		"-vf", "scale=400:400:force_original_aspect_ratio=decrease", "-c:v", "libwebp", "-quality", "75", "-f", "webp", out)
	return err
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil || !errors.Is(err, fs.ErrNotExist)
}

// fifo is a bounded FIFO queue of tasks.
type fifo struct {
	items []*task
	head  int
}

func (q *fifo) len() int { return len(q.items) - q.head }

func (q *fifo) push(t *task) (dropped *task) {
	if q.len() >= queueCap {
		dropped = q.pop()
	}
	q.items = append(q.items, t)
	return dropped
}

func (q *fifo) pop() *task {
	t := q.items[q.head]
	q.items[q.head] = nil
	q.head++
	if q.head > 1024 && q.head*2 > len(q.items) {
		q.items = append([]*task(nil), q.items[q.head:]...)
		q.head = 0
	}
	return t
}
