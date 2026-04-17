// Package redis provides a Redis-backed distributed cache store for tool results.
// It registers itself as the "redis" provider via init().
// Import this package with a blank import to make the provider available:
//
//	import _ "github.com/gaarutyunov/mcp-anything/pkg/cache/redis"
package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	goredis "github.com/redis/go-redis/v9"

	pkgcache "github.com/gaarutyunov/mcp-anything/pkg/cache"
	"github.com/gaarutyunov/mcp-anything/pkg/config"
)

func init() {
	pkgcache.Register("redis", func(ctx context.Context, cfg *config.CacheStoreConfig) (pkgcache.Store, error) {
		return newStore(ctx, cfg)
	})
}

type store struct {
	client *goredis.Client
}

func newStore(ctx context.Context, cfg *config.CacheStoreConfig) (*store, error) {
	if cfg.Redis == nil {
		return nil, fmt.Errorf("cache_store.redis config is required for provider \"redis\"")
	}
	client := goredis.NewClient(&goredis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("connecting to Redis at %q: %w", cfg.Redis.Addr, err)
	}
	return &store{client: client}, nil
}

func (s *store) Get(ctx context.Context, key string) (*sdkmcp.CallToolResult, bool) {
	data, err := s.client.Get(ctx, key).Bytes()
	if err != nil {
		return nil, false
	}
	var result sdkmcp.CallToolResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, false
	}
	return &result, true
}

func (s *store) Set(ctx context.Context, key string, value *sdkmcp.CallToolResult, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshalling cache value: %w", err)
	}
	return s.client.Set(ctx, key, data, ttl).Err()
}
