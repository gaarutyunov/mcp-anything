package upstream

import "fmt"

// ReadinessChecker can report whether the proxy is ready to serve.
// If ready is false, reason should contain a human-readable explanation.
type ReadinessChecker interface {
	IsReady() (ready bool, reason string)
}

// ReadinessCheckerFunc adapts a plain function to the ReadinessChecker interface.
type ReadinessCheckerFunc func() (bool, string)

// IsReady implements ReadinessChecker.
func (f ReadinessCheckerFunc) IsReady() (bool, string) { return f() }

// HealthChecker reports the health of a single upstream.
// Implemented by Refresher in the pkg/upstream/http package.
type HealthChecker interface {
	IsHealthy() bool
	UpstreamName() string
}

// RefresherSet holds a collection of HealthCheckers and implements ReadinessChecker.
type RefresherSet struct {
	healthcheckers []HealthChecker
}

// NewRefresherSet creates a RefresherSet from a slice of HealthCheckers.
func NewRefresherSet(checkers []HealthChecker) *RefresherSet {
	return &RefresherSet{healthcheckers: checkers}
}

// IsReady returns false (with the upstream name) if any checker is unhealthy.
func (rs *RefresherSet) IsReady() (bool, string) {
	for _, hc := range rs.healthcheckers {
		if !hc.IsHealthy() {
			return false, fmt.Sprintf("upstream %q has exceeded max_refresh_failures", hc.UpstreamName())
		}
	}
	return true, ""
}

// CompositeReadiness combines multiple ReadinessCheckers with AND semantics.
// IsReady returns false as soon as any checker reports not ready.
type CompositeReadiness struct {
	checkers []ReadinessChecker
}

// NewCompositeReadiness creates a CompositeReadiness from the given checkers.
func NewCompositeReadiness(checkers ...ReadinessChecker) *CompositeReadiness {
	return &CompositeReadiness{checkers: checkers}
}

// IsReady returns false with the first failing reason if any checker is not ready.
func (c *CompositeReadiness) IsReady() (bool, string) {
	for _, ch := range c.checkers {
		if ready, reason := ch.IsReady(); !ready {
			return false, reason
		}
	}
	return true, ""
}
