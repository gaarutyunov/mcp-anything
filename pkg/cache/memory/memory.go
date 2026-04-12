// Package memory provides an in-process LRU cache store backed by Ristretto.
// It registers itself as the "memory" provider via init().
package memory

import (
	"context"
	"fmt"
	"time"

	"github.com/dgraph-io/ristretto"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	pkgcache "github.com/gaarutyunov/mcp-anything/pkg/cache"
	"github.com/gaarutyunov/mcp-anything/pkg/config"
)

func init() {
	pkgcache.Register("memory", func(ctx context.Context, cfg *config.CacheStoreConfig) (pkgcache.Store, error) {
		return newStore()
	})
}

type store struct {
	cache *ristretto.Cache
}

func newStore() (*store, error) {
	c, err := ristretto.NewCache(&ristretto.Config{
		// NumCounters tracks admission/eviction frequency for 10M keys.
		NumCounters: 1e7,
		// MaxCost is the maximum number of items (cost=1 per item).
		MaxCost:     1 << 20,
		BufferItems: 64,
	})
	if err != nil {
		return nil, fmt.Errorf("creating ristretto cache: %w", err)
	}
	return &store{cache: c}, nil
}

func (s *store) Get(_ context.Context, key string) (*sdkmcp.CallToolResult, bool) {
	val, ok := s.cache.Get(key)
	if !ok {
		return nil, false
	}
	result, ok := val.(*sdkmcp.CallToolResult)
	return result, ok
}

func (s *store) Set(_ context.Context, key string, value *sdkmcp.CallToolResult, ttl time.Duration) error {
	s.cache.SetWithTTL(key, value, 1, ttl)
	// Wait ensures the item is available for the next Get call.
	s.cache.Wait()
	return nil
}
