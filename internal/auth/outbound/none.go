package outbound

import "context"

// NoneProvider is a no-op provider that adds no authentication headers.
type NoneProvider struct{}

// Token returns an empty token; no authentication is injected.
func (p *NoneProvider) Token(_ context.Context) (string, error) { return "", nil }

// RawHeaders returns nil; no authentication headers are injected.
func (p *NoneProvider) RawHeaders(_ context.Context) (map[string]string, error) { return nil, nil }
