// Package memory registers the "memory" session store provider.
// The memory store is not durable — tokens are lost on proxy restart.
// Use it only for development and testing.
// Import this package with a blank import to make the provider available:
//
//	import _ "github.com/gaarutyunov/mcp-anything/pkg/session/memory"
package memory

import (
	"context"
	"sync"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
	"github.com/gaarutyunov/mcp-anything/pkg/session"
)

func init() {
	session.Register("memory", func(_ context.Context, _ *config.SessionStoreSpec) (session.Store, error) {
		return &Store{tokens: make(map[string]*config.OAuthToken)}, nil
	})
}

// Store is an in-memory session store. Tokens are not persisted across restarts.
type Store struct {
	mu     sync.RWMutex
	tokens map[string]*config.OAuthToken
}

func storeKey(userSubject, upstreamName string) string {
	return userSubject + "\x00" + upstreamName
}

// Save stores a token for the given user and upstream.
func (s *Store) Save(_ context.Context, userSubject, upstreamName string, token *config.OAuthToken) error {
	cp := *token
	s.mu.Lock()
	s.tokens[storeKey(userSubject, upstreamName)] = &cp
	s.mu.Unlock()
	return nil
}

// Load retrieves the token for the given user and upstream. Returns nil, nil if not found.
func (s *Store) Load(_ context.Context, userSubject, upstreamName string) (*config.OAuthToken, error) {
	s.mu.RLock()
	t, ok := s.tokens[storeKey(userSubject, upstreamName)]
	s.mu.RUnlock()
	if !ok {
		return nil, nil
	}
	cp := *t
	return &cp, nil
}

// Delete removes the token for the given user and upstream.
func (s *Store) Delete(_ context.Context, userSubject, upstreamName string) error {
	s.mu.Lock()
	delete(s.tokens, storeKey(userSubject, upstreamName))
	s.mu.Unlock()
	return nil
}
