// Package runtime provides bounded semaphore pools for script runtime instances.
// Bounding the number of concurrent runtimes prevents OOM conditions and
// denial-of-service attacks caused by excessive memory growth under load.
package runtime

import (
	"context"
	"fmt"
)

// Pool is a bounded semaphore that limits the number of concurrently active
// script runtimes. When all slots are in use, Acquire blocks until a slot
// becomes available or ctx expires.
//
// Acquire returns a release function; the caller must invoke it (e.g. via defer)
// to return the slot to the pool when the runtime is no longer needed.
type Pool struct {
	slots chan struct{}
}

// NewPool creates a Pool that permits at most max concurrent runtime instances.
// If max <= 0 it panics; callers must supply a positive value (validated at
// construction time by NewRegistry).
func NewPool(max int) *Pool {
	p := &Pool{slots: make(chan struct{}, max)}
	for i := 0; i < max; i++ {
		p.slots <- struct{}{}
	}
	return p
}

// Acquire waits for a slot and returns a release function.
// The release function must be called exactly once when the runtime is done.
// Returns an error if ctx expires before a slot becomes available.
func (p *Pool) Acquire(ctx context.Context) (func(), error) {
	select {
	case <-p.slots:
		return func() { p.slots <- struct{}{} }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("acquiring runtime slot: %w", ctx.Err())
	}
}

// Cap returns the pool's maximum concurrency (number of slots).
func (p *Pool) Cap() int {
	return cap(p.slots)
}
