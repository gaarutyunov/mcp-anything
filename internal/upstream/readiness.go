package upstream

import pkgupstream "github.com/gaarutyunov/mcp-anything/pkg/upstream"

// RefresherSet holds a collection of Refreshers and implements server.ReadinessChecker.
// See pkg/upstream.RefresherSet.
type RefresherSet = pkgupstream.RefresherSet

// HealthChecker reports health of a single upstream.
// See pkg/upstream.HealthChecker.
type HealthChecker = pkgupstream.HealthChecker

// NewRefresherSet creates a RefresherSet from a slice of Refreshers.
// This wrapper converts []*Refresher to the []HealthChecker interface
// expected by pkg/upstream.NewRefresherSet.
func NewRefresherSet(refreshers []*Refresher) *RefresherSet {
	hcs := make([]pkgupstream.HealthChecker, len(refreshers))
	for i, r := range refreshers {
		hcs[i] = r
	}
	return pkgupstream.NewRefresherSet(hcs)
}
