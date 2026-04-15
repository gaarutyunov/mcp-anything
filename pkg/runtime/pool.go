package runtime

import (
	"context"
	"fmt"

	"golang.org/x/sync/semaphore"
)

// Pool is a bounded semaphore that limits the number of concurrently active
// script runtimes. When all slots are in use, Acquire blocks until a slot
// becomes available or ctx expires.
//
// Acquire returns a release function; the caller must invoke it (e.g. via defer)
// to return the slot to the pool when the runtime is no longer needed.
//
// Pool implements Runtime.
type Pool struct {
	sem *semaphore.Weighted
	cap int64
}

// NewPool creates a Pool that permits at most max concurrent runtime instances.
// If max <= 0 it panics; callers must supply a positive value (validated at
// construction time by NewRegistry).
func NewPool(max int64) *Pool {
	if max <= 0 {
		panic("runtime.NewPool: max must be > 0")
	}
	return &Pool{
		sem: semaphore.NewWeighted(max),
		cap: max,
	}
}

// Acquire waits for a slot and returns a release function.
// The release function must be called exactly once when the runtime is done.
// Returns an error if ctx expires before a slot becomes available.
func (p *Pool) Acquire(ctx context.Context) (func(), error) {
	if err := p.sem.Acquire(ctx, 1); err != nil {
		return nil, fmt.Errorf("acquiring runtime slot: %w", err)
	}
	return func() { p.sem.Release(1) }, nil
}

// Cap returns the pool's maximum concurrency (number of slots).
func (p *Pool) Cap() int64 {
	return p.cap
}
