// Package runtime provides the Runtime interface and bounded semaphore pools for
// script runtime instances. Bounding the number of concurrent runtimes prevents
// OOM conditions and denial-of-service attacks caused by excessive memory growth
// under load.
//
// New runtime implementations (JS, Lua, Wasm, …) register themselves via init()
// by calling Register with a unique name and a Factory function. The proxy binary
// imports pkg/runtime/all (or individual sub-packages) to activate the desired runtimes.
package runtime

import "context"

// Runtime is the interface that all script execution runtimes must implement.
// It manages a bounded pool of concurrent script executions to prevent OOM
// conditions under high load.
type Runtime interface {
	// Acquire waits for a slot and returns a release function.
	// The caller must invoke release (e.g. via defer) to return the slot when done.
	// Returns an error if ctx expires before a slot becomes available.
	Acquire(ctx context.Context) (release func(), err error)
	// Cap returns the maximum number of concurrent slots in the pool.
	Cap() int64
}
