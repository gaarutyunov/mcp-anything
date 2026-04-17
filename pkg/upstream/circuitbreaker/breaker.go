// Package circuitbreaker implements per-upstream circuit breaking using gobreaker/v2.
// A Breaker wraps upstream HTTP dispatch calls and trips after the configured error
// ratio exceeds the threshold. While open, calls fail immediately without reaching
// the upstream. After fallback_duration the circuit moves to half-open and allows
// one test request through; success closes the circuit, failure reopens it.
package circuitbreaker

import (
	"errors"
	"sync/atomic"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sony/gobreaker/v2"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
)

// ErrUpstreamFailure is a sentinel returned by the wrapped function to signal
// that the upstream returned a 5xx response. gobreaker counts this as a failure
// (err != nil) while the caller recovers the result from Execute's return value
// to forward the actual error body to the LLM.
var ErrUpstreamFailure = errors.New("upstream 5xx failure")

// Breaker implements upstream.ToolCallBreaker using gobreaker/v2.
type Breaker struct {
	cb               *gobreaker.CircuitBreaker[*sdkmcp.CallToolResult]
	upstreamName     string
	fallbackDuration time.Duration
	openedAt         atomic.Value // stores time.Time; set on transition to open
}

// New creates a Breaker for the named upstream using the given configuration.
func New(upstreamName string, cfg config.CircuitBreakerConfig) *Breaker {
	b := &Breaker{
		upstreamName:     upstreamName,
		fallbackDuration: cfg.FallbackDuration,
	}

	settings := gobreaker.Settings{
		Name:        upstreamName,
		Timeout:     cfg.FallbackDuration,
		MaxRequests: 1, // one test request allowed in half-open state
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			if counts.Requests < cfg.MinRequests {
				return false
			}
			ratio := float64(counts.TotalFailures) / float64(counts.Requests)
			return ratio >= cfg.Threshold
		},
		OnStateChange: func(_ string, _ gobreaker.State, to gobreaker.State) {
			if to == gobreaker.StateOpen {
				b.openedAt.Store(time.Now())
			}
		},
	}

	b.cb = gobreaker.NewCircuitBreaker[*sdkmcp.CallToolResult](settings)
	return b
}

// Execute wraps req with circuit breaker logic.
// When the circuit is open, gobreaker returns (nil, gobreaker.ErrOpenState)
// before calling req. When in half-open and MaxRequests exceeded, it returns
// (nil, gobreaker.ErrTooManyRequests).
func (b *Breaker) Execute(req func() (*sdkmcp.CallToolResult, error)) (*sdkmcp.CallToolResult, error) {
	return b.cb.Execute(req)
}

// IsOpen reports whether the circuit is currently in the open (fail-fast) state.
func (b *Breaker) IsOpen() bool {
	return b.cb.State() == gobreaker.StateOpen
}

// UpstreamName returns the name of the upstream this breaker protects.
func (b *Breaker) UpstreamName() string {
	return b.upstreamName
}

// EstimatedRecovery returns the estimated time when the circuit may transition
// from open to half-open. Returns zero time if the circuit has not yet opened
// or the state change time is unavailable.
func (b *Breaker) EstimatedRecovery() time.Time {
	t, ok := b.openedAt.Load().(time.Time)
	if !ok || t.IsZero() {
		return time.Time{}
	}
	return t.Add(b.fallbackDuration)
}
