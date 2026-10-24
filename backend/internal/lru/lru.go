// Package lru is a small thread-safe generic LRU cache.
package lru

import (
	"container/list"
	"sync"
)

// Cache is an LRU cache with a fixed capacity.
type Cache[K comparable, V any] struct {
	mu    sync.Mutex
	cap   int
	ll    *list.List
	items map[K]*list.Element
	// OnEvict, if set, is called (without the lock held) for evicted values.
	OnEvict func(K, V)
}

type entry[K comparable, V any] struct {
	k K
	v V
}

// New returns a cache holding up to n entries.
func New[K comparable, V any](n int) *Cache[K, V] {
	return &Cache[K, V]{cap: n, ll: list.New(), items: map[K]*list.Element{}}
}

// Get returns the value for k and marks it recently used.
func (c *Cache[K, V]) Get(k K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[k]; ok {
		c.ll.MoveToFront(e)
		return e.Value.(*entry[K, V]).v, true
	}
	var zero V
	return zero, false
}

// Add inserts or replaces k.
func (c *Cache[K, V]) Add(k K, v V) {
	c.mu.Lock()
	if e, ok := c.items[k]; ok {
		c.ll.MoveToFront(e)
		e.Value.(*entry[K, V]).v = v
		c.mu.Unlock()
		return
	}
	c.items[k] = c.ll.PushFront(&entry[K, V]{k, v})
	var evicted []*entry[K, V]
	for c.ll.Len() > c.cap {
		last := c.ll.Back()
		c.ll.Remove(last)
		en := last.Value.(*entry[K, V])
		delete(c.items, en.k)
		evicted = append(evicted, en)
	}
	cb := c.OnEvict
	c.mu.Unlock()
	if cb != nil {
		for _, en := range evicted {
			cb(en.k, en.v)
		}
	}
}

// Remove deletes k.
func (c *Cache[K, V]) Remove(k K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[k]; ok {
		c.ll.Remove(e)
		delete(c.items, k)
	}
}

// Len returns the number of entries.
func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
