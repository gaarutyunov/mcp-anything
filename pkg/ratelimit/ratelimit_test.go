package ratelimit_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
	"github.com/gaarutyunov/mcp-anything/pkg/ratelimit"
	_ "github.com/gaarutyunov/mcp-anything/pkg/ratelimit/memory"
)

func TestClientIPMiddleware(t *testing.T) {
	var capturedIP string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedIP = ratelimit.ClientIPFromContext(r.Context())
	})
	handler := ratelimit.ClientIPMiddleware(next)

	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		xri        string
		wantIP     string
	}{
		{
			name:       "remote addr only",
			remoteAddr: "1.2.3.4:5678",
			wantIP:     "1.2.3.4",
		},
		{
			name:       "X-Forwarded-For single",
			remoteAddr: "10.0.0.1:1234",
			xff:        "203.0.113.1",
			wantIP:     "203.0.113.1",
		},
		{
			name:       "X-Forwarded-For multiple",
			remoteAddr: "10.0.0.1:1234",
			xff:        "203.0.113.1, 10.0.0.2",
			wantIP:     "203.0.113.1",
		},
		{
			name:       "X-Real-IP",
			remoteAddr: "10.0.0.1:1234",
			xri:        "198.51.100.5",
			wantIP:     "198.51.100.5",
		},
		{
			name:       "X-Forwarded-For takes precedence over X-Real-IP",
			remoteAddr: "10.0.0.1:1234",
			xff:        "203.0.113.1",
			xri:        "198.51.100.5",
			wantIP:     "203.0.113.1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			if tc.xri != "" {
				req.Header.Set("X-Real-IP", tc.xri)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if capturedIP != tc.wantIP {
				t.Errorf("got IP %q, want %q", capturedIP, tc.wantIP)
			}
		})
	}
}

func TestEnforcer_NilWhenNoRateLimits(t *testing.T) {
	cfg := &config.ProxyConfig{}
	enf, err := ratelimit.New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if enf != nil {
		t.Fatal("expected nil Enforcer when no rate limits configured")
	}
}

func TestEnforcer_AllowUnderLimit(t *testing.T) {
	cfg := &config.ProxyConfig{
		RateLimits: map[string]config.RateLimitConfig{
			"test": {
				Average: 10,
				Period:  time.Minute,
				Burst:   5,
				Source:  "ip",
			},
		},
	}
	enf, err := ratelimit.New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if enf == nil {
		t.Fatal("expected non-nil Enforcer")
	}

	ctx := context.Background()
	_, _, reached, err := enf.Allow(ctx, "test", "192.168.1.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reached {
		t.Error("expected request to be allowed under limit")
	}
}

func TestEnforcer_RejectOverLimit(t *testing.T) {
	cfg := &config.ProxyConfig{
		RateLimits: map[string]config.RateLimitConfig{
			"strict": {
				Average: 2,
				Period:  time.Minute,
				Burst:   0,
				Source:  "ip",
			},
		},
	}
	enf, err := ratelimit.New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	key := "192.168.1.100"

	// First 2 requests should succeed (average=2, burst=0 → limit=2).
	for i := 0; i < 2; i++ {
		_, _, reached, err := enf.Allow(ctx, "strict", key)
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i+1, err)
		}
		if reached {
			t.Errorf("request %d: expected to be allowed, was rejected", i+1)
		}
	}

	// Third request should be rejected.
	_, reset, reached, err := enf.Allow(ctx, "strict", key)
	if err != nil {
		t.Fatalf("request 3: unexpected error: %v", err)
	}
	if !reached {
		t.Error("request 3: expected to be rejected, was allowed")
	}
	if reset.IsZero() {
		t.Error("reset time should not be zero on rejection")
	}
}

func TestEnforcer_Source(t *testing.T) {
	cfg := &config.ProxyConfig{
		RateLimits: map[string]config.RateLimitConfig{
			"by-user":    {Average: 10, Period: time.Minute, Source: "user"},
			"by-ip":      {Average: 10, Period: time.Minute, Source: "ip"},
			"by-session": {Average: 10, Period: time.Minute, Source: "session"},
		},
	}
	enf, err := ratelimit.New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := enf.Source("by-user"); got != "user" {
		t.Errorf("Source(by-user) = %q, want %q", got, "user")
	}
	if got := enf.Source("by-ip"); got != "ip" {
		t.Errorf("Source(by-ip) = %q, want %q", got, "ip")
	}
	if got := enf.Source("by-session"); got != "session" {
		t.Errorf("Source(by-session) = %q, want %q", got, "session")
	}
	if got := enf.Source("unknown"); got != "" {
		t.Errorf("Source(unknown) = %q, want empty", got)
	}
}

func TestEnforcer_UnknownLimitName(t *testing.T) {
	cfg := &config.ProxyConfig{
		RateLimits: map[string]config.RateLimitConfig{
			"known": {Average: 10, Period: time.Minute, Source: "ip"},
		},
	}
	enf, err := ratelimit.New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, _, _, err = enf.Allow(context.Background(), "unknown-limit", "key")
	if err == nil {
		t.Fatal("expected error for unknown limit name")
	}
}

func TestEnforcer_NilAllowAlwaysPasses(t *testing.T) {
	var enf *ratelimit.Enforcer
	_, _, reached, err := enf.Allow(context.Background(), "any", "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reached {
		t.Error("nil Enforcer should always allow")
	}
}
