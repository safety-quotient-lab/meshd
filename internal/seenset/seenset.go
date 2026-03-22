// Package seenset provides a generic, thread-safe seen-set with TTL-based
// pruning. Used by transport.Watcher, transport.Fetcher, and events.Queue
// to deduplicate items within a time window.
package seenset

import (
	"sync"
	"time"
)

// SeenSet tracks items by key with timestamps, supporting TTL-based pruning.
// T constrains the key type (typically string).
type SeenSet[T comparable] struct {
	mu    sync.Mutex
	items map[T]time.Time
}

// New constructs an empty SeenSet.
func New[T comparable]() *SeenSet[T] {
	return &SeenSet[T]{
		items: make(map[T]time.Time),
	}
}

// Contains reports whether the set already holds the given key.
func (s *SeenSet[T]) Contains(key T) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, found := s.items[key]
	return found
}

// Add records a key with the current timestamp.
func (s *SeenSet[T]) Add(key T) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[key] = time.Now()
}

// AddAt records a key with a specific timestamp.
func (s *SeenSet[T]) AddAt(key T, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[key] = at
}

// Prune removes entries older than the given TTL and returns the count removed.
func (s *SeenSet[T]) Prune(ttl time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-ttl)
	pruned := 0
	for key, ts := range s.items {
		if ts.Before(cutoff) {
			delete(s.items, key)
			pruned++
		}
	}
	return pruned
}

// Len returns the number of entries in the set.
func (s *SeenSet[T]) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}

// Snapshot returns a copy of all entries. Useful for persistence.
func (s *SeenSet[T]) Snapshot() map[T]time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[T]time.Time, len(s.items))
	for k, v := range s.items {
		out[k] = v
	}
	return out
}

// LoadFiltered populates the set from a map, excluding entries older than
// the given TTL. Returns the count of entries loaded.
func (s *SeenSet[T]) LoadFiltered(entries map[T]time.Time, ttl time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-ttl)
	loaded := 0
	for k, v := range entries {
		if v.After(cutoff) {
			s.items[k] = v
			loaded++
		}
	}
	return loaded
}
