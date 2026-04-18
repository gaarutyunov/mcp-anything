// Package redis registers the Redis-backed rate limit store via init().
// Import this package with a blank identifier to enable the Redis store:
//
//	import _ "github.com/gaarutyunov/mcp-anything/pkg/ratelimit/redis"
//
// Requires rate_limit_store.redis to be configured in ProxyConfig.
package redis

import (
	"context"
	"fmt"

	redisotel "github.com/redis/go-redis/extra/redisotel/v9"
	goredis "github.com/redis/go-redis/v9"
	"github.com/ulule/limiter/v3"
	redisstore "github.com/ulule/limiter/v3/drivers/store/redis"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
	pkgratelimit "github.com/gaarutyunov/mcp-anything/pkg/ratelimit"
)

func init() {
	pkgratelimit.Register("redis", func(ctx context.Context, cfg *config.ProxyConfig) (limiter.Store, error) {
		if cfg.RateLimitStore.Redis == nil {
			return nil, fmt.Errorf("rate_limit_store.redis is required for the redis store")
		}

		opts := &goredis.Options{
			Addr: cfg.RateLimitStore.Redis.Addr,
		}
		if cfg.RateLimitStore.Redis.Password != "" {
			opts.Password = cfg.RateLimitStore.Redis.Password
		}

		client := goredis.NewClient(opts)

		// Verify connectivity before returning.
		if err := client.Ping(ctx).Err(); err != nil {
			return nil, fmt.Errorf("connecting to Redis at %q: %w", cfg.RateLimitStore.Redis.Addr, err)
		}

		if err := redisotel.InstrumentTracing(client); err != nil {
			return nil, fmt.Errorf("instrumenting redis rate limit store tracing: %w", err)
		}

		store, err := redisstore.NewStore(client)
		if err != nil {
			return nil, fmt.Errorf("creating Redis rate limit store: %w", err)
		}

		return store, nil
	})
}
