// Package registry provides a generic, thread-safe, string-keyed registry
// used throughout mcp-anything for pluggable components (middleware factories,
// store factories, upstream builders, etc.).
//
// The zero value of Registry is ready to use — no constructor is needed.
// Each pluggable sub-system declares a package-level variable of the appropriate
// concrete type:
//
//	var middlewareRegistry registry.Registry[middleware.Factory]
//	var cacheRegistry      registry.Registry[cache.StoreFactory]
//
// Sub-packages register themselves from their init() functions:
//
//	func init() { middlewareRegistry.Register("jwt", newJWT) }
package registry

import "sync"

// Registry is a thread-safe, string-keyed store for named values of type T.
// The zero value is ready to use.
type Registry[T any] struct {
	mu    sync.RWMutex
	items map[string]T
}

// Register adds or replaces the value for key.
// Safe for concurrent use.
func (r *Registry[T]) Register(key string, value T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.items == nil {
		r.items = make(map[string]T)
	}
	r.items[key] = value
}

// RegisterIfAbsent sets the value for key only if it does not already exist.
// Returns true if the key was newly registered, false if it was already present.
// Safe for concurrent use.
func (r *Registry[T]) RegisterIfAbsent(key string, value T) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.items == nil {
		r.items = make(map[string]T)
	}
	if _, exists := r.items[key]; exists {
		return false
	}
	r.items[key] = value
	return true
}

// Get returns the value for key and whether it was found.
// Safe for concurrent use.
func (r *Registry[T]) Get(key string) (T, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.items[key]
	return v, ok
}

// Snapshot returns a shallow copy of all entries, safe for iteration
// without holding the registry lock.
func (r *Registry[T]) Snapshot() map[string]T {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]T, len(r.items))
	for k, v := range r.items {
		out[k] = v
	}
	return out
}
