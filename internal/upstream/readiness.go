package upstream

import "fmt"

// RefreshableUpstream is implemented by upstream refreshers that support health checking.
type RefreshableUpstream interface {
	IsHealthy() bool
	UpstreamName() string
}

// RefresherSet holds a collection of RefreshableUpstreams and implements server.ReadinessChecker.
type RefresherSet struct {
	refreshers []RefreshableUpstream
}

// NewRefresherSet creates a RefresherSet from a slice of RefreshableUpstreams.
func NewRefresherSet(refreshers []RefreshableUpstream) *RefresherSet {
	return &RefresherSet{refreshers: refreshers}
}

// IsReady returns false (with the upstream name) if any Refresher is unhealthy.
func (rs *RefresherSet) IsReady() (bool, string) {
	for _, r := range rs.refreshers {
		if !r.IsHealthy() {
			return false, fmt.Sprintf("upstream %q has exceeded max_refresh_failures", r.UpstreamName())
		}
	}
	return true, ""
}
