package uploads

import (
	"os"
	"sync"
	"time"

	"filet/internal/fsx"
)

const (
	maxOpenFiles = 64
	fileIdle     = 60 * time.Second
)

// openFile is a cached partial-file descriptor with a sync coalescer: many
// parallel chunk writers share as few fdatasync calls as possible, and each
// writer returns only once a sync that started after its write completed.
type openFile struct {
	f       *os.File
	refs    int
	lastUse time.Time

	mu        sync.Mutex
	cond      *sync.Cond
	writeGen  uint64
	syncedGen uint64
	syncing   bool
	sync      func(*os.File) error
	syncs     int // number of sync calls (tests)
}

func newOpenFile(f *os.File, syncFn func(*os.File) error) *openFile {
	of := &openFile{f: f, sync: syncFn, lastUse: time.Now()}
	of.cond = sync.NewCond(&of.mu)
	return of
}

// writeDurable writes buf at off and returns once the bytes are on stable
// storage.
func (of *openFile) writeDurable(buf []byte, off int64) error {
	if _, err := of.f.WriteAt(buf, off); err != nil {
		return err
	}
	of.mu.Lock()
	of.writeGen++
	gen := of.writeGen
	of.mu.Unlock()
	return of.durable(gen)
}

func (of *openFile) durable(gen uint64) error {
	of.mu.Lock()
	defer of.mu.Unlock()
	for of.syncedGen < gen {
		if of.syncing {
			of.cond.Wait()
			continue
		}
		of.syncing = true
		start := of.writeGen
		of.syncs++
		of.mu.Unlock()
		err := of.sync(of.f)
		of.mu.Lock()
		of.syncing = false
		if err == nil && start > of.syncedGen {
			of.syncedGen = start
		}
		of.cond.Broadcast()
		if err != nil {
			return err
		}
	}
	return nil
}

// fileCache is the LRU of open partial files, keyed by upload id.
type fileCache struct {
	mu   sync.Mutex
	m    map[string]*openFile
	sync func(*os.File) error
	stop chan struct{}
}

func newFileCache(syncFn func(*os.File) error) *fileCache {
	c := &fileCache{m: map[string]*openFile{}, sync: syncFn, stop: make(chan struct{})}
	go c.janitor()
	return c
}

// acquire returns the open file for id, opening it with open if needed.
func (c *fileCache) acquire(id string, open func() (*os.File, error)) (*openFile, error) {
	c.mu.Lock()
	of, ok := c.m[id]
	if ok {
		of.refs++
		c.mu.Unlock()
		return of, nil
	}
	c.mu.Unlock()
	f, err := open()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if cur, ok := c.m[id]; ok { // lost a race
		_ = f.Close()
		cur.refs++
		return cur, nil
	}
	of = newOpenFile(f, c.sync)
	of.refs = 1
	c.m[id] = of
	c.evictLocked()
	return of, nil
}

// put caches a freshly created file (registration keeps the fd).
func (c *fileCache) put(id string, f *os.File) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.m[id]; ok {
		_ = old.f.Close()
	}
	c.m[id] = newOpenFile(f, c.sync)
	c.evictLocked()
}

func (c *fileCache) release(of *openFile) {
	c.mu.Lock()
	of.refs--
	of.lastUse = time.Now()
	c.mu.Unlock()
}

// close closes the fd for id (after a final sync) and forgets it.
func (c *fileCache) close(id string) {
	c.mu.Lock()
	of, ok := c.m[id]
	delete(c.m, id)
	c.mu.Unlock()
	if ok {
		_ = fsx.Datasync(of.f)
		_ = of.f.Close()
	}
}

func (c *fileCache) evictLocked() {
	for len(c.m) > maxOpenFiles {
		var oldestID string
		var oldest *openFile
		for id, of := range c.m {
			if of.refs == 0 && (oldest == nil || of.lastUse.Before(oldest.lastUse)) {
				oldestID, oldest = id, of
			}
		}
		if oldest == nil {
			return // everything is in use; allow a temporary overshoot
		}
		delete(c.m, oldestID)
		_ = oldest.f.Close()
	}
}

func (c *fileCache) janitor() {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-t.C:
			c.mu.Lock()
			for id, of := range c.m {
				if of.refs == 0 && time.Since(of.lastUse) > fileIdle {
					delete(c.m, id)
					_ = of.f.Close()
				}
			}
			c.mu.Unlock()
		}
	}
}

// closeAll syncs and closes every fd (shutdown).
func (c *fileCache) closeAll() {
	close(c.stop)
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, of := range c.m {
		_ = fsx.Datasync(of.f)
		_ = of.f.Close()
		delete(c.m, id)
	}
}

// keyedMutex is a mutex per string key.
type keyedMutex struct {
	mu sync.Mutex
	m  map[string]*keyedEntry
}

type keyedEntry struct {
	mu   sync.Mutex
	refs int
}

func (k *keyedMutex) lock(key string) func() {
	k.mu.Lock()
	if k.m == nil {
		k.m = map[string]*keyedEntry{}
	}
	e, ok := k.m[key]
	if !ok {
		e = &keyedEntry{}
		k.m[key] = e
	}
	e.refs++
	k.mu.Unlock()
	e.mu.Lock()
	return func() {
		e.mu.Unlock()
		k.mu.Lock()
		e.refs--
		if e.refs == 0 {
			delete(k.m, key)
		}
		k.mu.Unlock()
	}
}
