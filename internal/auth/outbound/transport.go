package outbound

import (
	"fmt"
	"net/http"
)

// AuthTransport is an http.RoundTripper that injects outbound auth on every request.
type AuthTransport struct {
	Base     http.RoundTripper
	Provider TokenProvider
}

// RoundTrip injects authentication headers before forwarding the request.
// RawHeaders take precedence; if none are returned, a Bearer token is used.
func (t *AuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	headers, err := t.Provider.RawHeaders(req.Context())
	if err != nil {
		return nil, fmt.Errorf("get raw headers: %w", err)
	}

	if len(headers) > 0 {
		req = req.Clone(req.Context())
		for k, v := range headers {
			req.Header.Set(k, v)
		}
	} else {
		token, err := t.Provider.Token(req.Context())
		if err != nil {
			return nil, fmt.Errorf("get outbound token: %w", err)
		}
		if token != "" {
			req = req.Clone(req.Context())
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}

	return t.Base.RoundTrip(req)
}
