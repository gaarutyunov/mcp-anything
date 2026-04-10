package upstream

import "fmt"

// ReadinessChecker can report whether the proxy is ready to serve.
// If ready is false, reason should contain a human-readable explanation.
type ReadinessChecker interface {
	IsReady() (ready bool, reason string)
}

// HealthChecker reports the health of a single upstream.
// Implemented by Refresher in the internal/upstream package.
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
