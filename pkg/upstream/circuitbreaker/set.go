package circuitbreaker

import (
	"fmt"
	"sort"
	"strings"
)

// Set is an immutable collection of Breakers used for readiness checking.
// It implements the IsReady pattern: returns false with the list of open upstreams
// if any circuit breaker is in the open state.
type Set struct {
	breakers []*Breaker
}

// NewSet creates a Set from the given breakers.
func NewSet(breakers []*Breaker) *Set {
	return &Set{breakers: breakers}
}

// IsReady returns false if any circuit breaker is currently open.
// The reason string lists the affected upstream names.
func (s *Set) IsReady() (bool, string) {
	var open []string
	for _, b := range s.breakers {
		if b.IsOpen() {
			open = append(open, b.UpstreamName())
		}
	}
	if len(open) > 0 {
		sort.Strings(open)
		return false, fmt.Sprintf("circuit open for upstream(s): %s", strings.Join(open, ", "))
	}
	return true, ""
}
