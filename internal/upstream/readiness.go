package upstream

import "fmt"

// RefresherSet holds a collection of Refreshers and implements server.ReadinessChecker.
type RefresherSet struct {
	refreshers []*Refresher
}

// NewRefresherSet creates a RefresherSet from a slice of Refreshers.
func NewRefresherSet(refreshers []*Refresher) *RefresherSet {
	return &RefresherSet{refreshers: refreshers}
}

// IsReady returns false (with the upstream name) if any Refresher is unhealthy.
func (rs *RefresherSet) IsReady() (bool, string) {
	for _, r := range rs.refreshers {
		if !r.IsHealthy() {
			return false, fmt.Sprintf("upstream %q has exceeded max_refresh_failures", r.cfg.Name)
		}
	}
	return true, ""
}
