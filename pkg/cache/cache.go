// Package cache provides the cache store registry and the Store interface for tool result caching.
// Cache store backends follow the registry pattern: sub-packages register themselves
// via init() when imported. Use the all sub-package to import all built-in backends.
package cache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
)

// Store is the interface for a cache backend.
type Store interface {
	// Get returns the cached result for key, or (nil, false) on a miss.
	Get(ctx context.Context, key string) (*sdkmcp.CallToolResult, bool)
	// Set stores value under key for the given ttl. Errors are non-fatal; a failed
	// Set simply means the next call will be a cache miss.
	Set(ctx context.Context, key string, value *sdkmcp.CallToolResult, ttl time.Duration) error
}

// StoreFactory creates a Store from config.
type StoreFactory func(ctx context.Context, cfg *config.CacheStoreConfig) (Store, error)

var (
	mu       sync.RWMutex
	registry = make(map[string]StoreFactory)
)

// Register adds a factory for the given provider name.
// Typically called from init() in store sub-packages.
func Register(provider string, factory StoreFactory) {
	mu.Lock()
	defer mu.Unlock()
	registry[provider] = factory
}

// New creates a Store from config.
// Defaults to "memory" provider if cfg.Provider is empty.
func New(ctx context.Context, cfg *config.CacheStoreConfig) (Store, error) {
	provider := cfg.Provider
	if provider == "" {
		provider = "memory"
	}
	mu.RLock()
	f, ok := registry[provider]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown cache store provider %q — did you forget to import _ %q?",
			provider,
			"github.com/gaarutyunov/mcp-anything/pkg/cache/"+provider)
	}
	return f(ctx, cfg)
}

// ComputeKey computes the SHA-256 cache key for a tool call.
// With perUser=true and a non-empty subject the subject is included in the key
// so that different users with identical arguments get separate entries.
func ComputeKey(toolName string, args map[string]any, perUser bool, subject string) (string, error) {
	var parts []string
	parts = append(parts, toolName)
	if perUser && subject != "" {
		parts = append(parts, subject)
	}
	canonical, err := canonicalJSONBytes(args)
	if err != nil {
		return "", fmt.Errorf("canonical JSON for cache key: %w", err)
	}
	parts = append(parts, string(canonical))
	h := sha256.Sum256([]byte(strings.Join(parts, ":")))
	return hex.EncodeToString(h[:]), nil
}

// canonicalJSONBytes marshals args to JSON with object keys sorted recursively.
// This ensures identical args produce the same cache key regardless of insertion order.
func canonicalJSONBytes(v any) ([]byte, error) {
	if v == nil {
		return []byte("null"), nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var parsed any
	if err := json.Unmarshal(b, &parsed); err != nil {
		return nil, err
	}
	return marshalSorted(parsed)
}

// marshalSorted recursively marshals v with map keys sorted alphabetically.
func marshalSorted(v any) ([]byte, error) {
	switch val := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var buf bytes.Buffer
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return nil, err
			}
			buf.Write(kb)
			buf.WriteByte(':')
			vb, err := marshalSorted(val[k])
			if err != nil {
				return nil, err
			}
			buf.Write(vb)
		}
		buf.WriteByte('}')
		return buf.Bytes(), nil
	case []any:
		var buf bytes.Buffer
		buf.WriteByte('[')
		for i, item := range val {
			if i > 0 {
				buf.WriteByte(',')
			}
			ib, err := marshalSorted(item)
			if err != nil {
				return nil, err
			}
			buf.Write(ib)
		}
		buf.WriteByte(']')
		return buf.Bytes(), nil
	default:
		return json.Marshal(v)
	}
}
