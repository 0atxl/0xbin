// Package ratelimit provides bounded in-memory token buckets for 0xbin.
package ratelimit

import (
	"fmt"
	"sync"
	"time"

	"github.com/0atxl/0xbin/internal/config"
)

type Category string

const (
	Create  Category = "create"
	Read    Category = "read"
	Miss    Category = "miss"
	Consume Category = "consume"
)

const consecutiveMissThreshold = 5

// Registry holds token buckets and bounded per-IP miss streaks.
type Registry struct {
	mu         sync.Mutex
	rates      map[Category]config.Rate
	buckets    map[key]bucket
	misses     map[string]missState
	maxEntries int
	staleAfter time.Duration
	now        func() time.Time
}

type key struct {
	category Category
	identity string
}

type bucket struct {
	tokens float64
	seen   time.Time
}

type missState struct {
	consecutive int
	seen        time.Time
}

// NewRegistry creates a bounded registry. Entries inactive for staleAfter are
// evicted while requests are processed.
func NewRegistry(rates map[Category]config.Rate, maxEntries int, staleAfter time.Duration, now func() time.Time) (*Registry, error) {
	if maxEntries < 1 || staleAfter <= 0 || now == nil {
		return nil, fmt.Errorf("positive maximum entries, stale duration, and clock are required")
	}
	copy := make(map[Category]config.Rate, len(rates))
	for category, rate := range rates {
		if rate.Count < 1 || rate.Window <= 0 {
			return nil, fmt.Errorf("rate for %q must be positive", category)
		}
		copy[category] = rate
	}
	return &Registry{rates: copy, buckets: make(map[key]bucket), misses: make(map[string]missState), maxEntries: maxEntries, staleAfter: staleAfter, now: now}, nil
}

// Allow consumes cost tokens for an identity/category. retryAfter is nonzero
// only when the request is denied.
func (r *Registry) Allow(category Category, identity string, cost int) (allowed bool, retryAfter time.Duration) {
	if cost < 1 {
		cost = 1
	}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evict(now)
	rate, ok := r.rates[category]
	if !ok {
		return true, 0
	}
	key := key{category: category, identity: identity}
	state, exists := r.buckets[key]
	if !exists {
		state = bucket{tokens: float64(rate.Count), seen: now}
	} else {
		elapsed := now.Sub(state.seen)
		state.tokens = min(float64(rate.Count), state.tokens+elapsed.Seconds()*float64(rate.Count)/rate.Window.Seconds())
		state.seen = now
	}
	if state.tokens >= float64(cost) {
		state.tokens -= float64(cost)
		r.buckets[key] = state
		return true, 0
	}
	r.buckets[key] = state
	missing := float64(cost) - state.tokens
	retryAfter = time.Duration(missing / float64(rate.Count) * float64(rate.Window))
	if retryAfter < time.Second {
		retryAfter = time.Second
	}
	return false, retryAfter.Round(time.Second)
}

// RecordMiss increments the identity's miss streak and returns its next miss
// cost. Successful reads must call RecordSuccess to reset that streak.
func (r *Registry) RecordMiss(identity string) int {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evict(now)
	state := r.misses[identity]
	state.consecutive++
	state.seen = now
	r.misses[identity] = state
	if state.consecutive >= consecutiveMissThreshold {
		return 2
	}
	return 1
}

// RecordSuccess clears the identity's consecutive miss state.
func (r *Registry) RecordSuccess(identity string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.misses, identity)
}

// Len reports the number of live limiter records for tests and diagnostics.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.buckets) + len(r.misses)
}

func (r *Registry) evict(now time.Time) {
	for key, state := range r.buckets {
		if now.Sub(state.seen) >= r.staleAfter {
			delete(r.buckets, key)
		}
	}
	for identity, state := range r.misses {
		if now.Sub(state.seen) >= r.staleAfter {
			delete(r.misses, identity)
		}
	}
	if len(r.buckets)+len(r.misses) < r.maxEntries {
		return
	}
	for key := range r.buckets {
		delete(r.buckets, key)
		if len(r.buckets)+len(r.misses) < r.maxEntries {
			return
		}
	}
	for identity := range r.misses {
		delete(r.misses, identity)
		if len(r.buckets)+len(r.misses) < r.maxEntries {
			return
		}
	}
}

func min(left, right float64) float64 {
	if left < right {
		return left
	}
	return right
}
