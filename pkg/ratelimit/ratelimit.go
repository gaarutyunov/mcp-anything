// Package ratelimit provides per-tool rate limiting for mcp-anything.
//
// Rate limit stores are registered via the registry pattern so that unused stores
// are excluded from the binary by the Go linker. Import the sub-packages you need:
//
//	import _ "github.com/gaarutyunov/mcp-anything/pkg/ratelimit/all"   // all stores
//	import _ "github.com/gaarutyunov/mcp-anything/pkg/ratelimit/memory" // in-memory only
//	import _ "github.com/gaarutyunov/mcp-anything/pkg/ratelimit/redis"  // Redis only
package ratelimit

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ulule/limiter/v3"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
)

// StoreFactory creates a limiter.Store from the proxy config.
// Called from init() in store sub-packages.
type StoreFactory func(ctx context.Context, cfg *config.ProxyConfig) (limiter.Store, error)

var (
	storeMu        sync.RWMutex
	storeFactories = make(map[string]StoreFactory)
)

// Register adds a store factory for the given store type name.
// Typically called from init() in store sub-packages.
func Register(name string, f StoreFactory) {
	storeMu.Lock()
	defer storeMu.Unlock()
	storeFactories[name] = f
}

// clientIPKey is an unexported context key for storing the client IP.
type clientIPKey struct{}

// WithClientIP stores the client IP in ctx for use by source: ip rate limiters.
func WithClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, clientIPKey{}, ip)
}

// ClientIPFromContext returns the client IP stored in ctx, or empty string if absent.
func ClientIPFromContext(ctx context.Context) string {
	v, _ := ctx.Value(clientIPKey{}).(string)
	return v
}

// ClientIPMiddleware is an HTTP middleware that extracts the client IP and stores it
// in the request context so that rate limiters with source: ip can use it.
func ClientIPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := extractClientIP(r)
		ctx := WithClientIP(r.Context(), ip)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// extractClientIP returns the real client IP from the HTTP request.
// Checks X-Forwarded-For, then X-Real-IP, then falls back to RemoteAddr (port stripped).
func extractClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		return addr[:idx]
	}
	return addr
}

// Enforcer checks and records rate limit hits for named rate limit configurations.
// A nil Enforcer is valid and always allows requests (no rate limiting configured).
type Enforcer struct {
	limiters map[string]*limiter.Limiter       // limitName → limiter
	configs  map[string]config.RateLimitConfig // limitName → config (for source lookup)
}

// New creates an Enforcer from the ProxyConfig.
// Returns (nil, nil) when no rate limits are configured (len(cfg.RateLimits) == 0).
// Returns an error if the required store sub-package has not been imported.
func New(ctx context.Context, cfg *config.ProxyConfig) (*Enforcer, error) {
	if len(cfg.RateLimits) == 0 {
		return nil, nil
	}

	storeName := "memory"
	if cfg.RateLimitStore.Redis != nil {
		storeName = "redis"
	}

	storeMu.RLock()
	factory, ok := storeFactories[storeName]
	storeMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf(
			"unknown rate limit store %q — import the store package or pkg/ratelimit/all",
			storeName,
		)
	}

	store, err := factory(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("building rate limit store %q: %w", storeName, err)
	}

	e := &Enforcer{
		limiters: make(map[string]*limiter.Limiter, len(cfg.RateLimits)),
		configs:  make(map[string]config.RateLimitConfig, len(cfg.RateLimits)),
	}

	for name, rlCfg := range cfg.RateLimits {
		rate := limiter.Rate{
			Limit:  rlCfg.Average + rlCfg.Burst,
			Period: rlCfg.Period,
		}
		e.limiters[name] = limiter.New(store, rate)
		e.configs[name] = rlCfg
	}

	return e, nil
}

// Source returns the source criterion ("user", "ip", or "session") for the named limit.
// Returns empty string for unknown limit names.
func (e *Enforcer) Source(limitName string) string {
	if e == nil {
		return ""
	}
	cfg, ok := e.configs[limitName]
	if !ok {
		return ""
	}
	return cfg.Source
}

// Allow checks whether the request keyed by key is within the named rate limit,
// recording the hit. Returns (remaining, reset, reached, nil) on success.
// reached=true means the limit was exceeded and the request should be rejected.
// Returns (0, zero, false, nil) when the Enforcer is nil (no rate limiting configured).
func (e *Enforcer) Allow(ctx context.Context, limitName, key string) (int64, time.Time, bool, error) {
	if e == nil {
		return 0, time.Time{}, false, nil
	}
	lim, ok := e.limiters[limitName]
	if !ok {
		return 0, time.Time{}, false, fmt.Errorf("unknown rate limit %q", limitName)
	}
	lctx, err := lim.Get(ctx, key)
	if err != nil {
		return 0, time.Time{}, false, fmt.Errorf("rate limit check: %w", err)
	}
	reset := time.Unix(lctx.Reset, 0)
	return lctx.Remaining, reset, lctx.Reached, nil
}
