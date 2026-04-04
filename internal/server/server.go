// Package server implements the HTTP server for mcp-anything.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/gaarutyunov/mcp-anything/internal/config"
)

// ReadinessChecker can report whether the proxy is ready to serve.
// If ready is false, reason should contain a human-readable explanation.
type ReadinessChecker interface {
	IsReady() (ready bool, reason string)
}

// Server wraps the net/http server and manages its lifecycle.
type Server struct {
	cfg        *config.ProxyConfig
	httpServer *http.Server
}

// New creates a new Server. mcpHandlers maps mount paths to their HTTP handlers.
// wellKnown is an optional handler for the OAuth 2.0 Protected Resource Metadata endpoint
// (GET /.well-known/oauth-protected-resource); pass nil to skip mounting it.
// reloadMetrics is an optional handler for the GET /metrics/reload endpoint; pass nil to skip.
// readiness is an optional checker for /readyz; pass nil to always return 200 OK.
func New(cfg *config.ProxyConfig, mcpHandlers map[string]http.Handler, wellKnown http.HandlerFunc, reloadMetrics http.HandlerFunc, readiness ReadinessChecker) *Server {
	r := chi.NewRouter()

	// Health endpoints.
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if readiness != nil {
			if ready, reason := readiness.IsReady(); !ready {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = fmt.Fprintf(w, "%s\n", reason)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	// Reload metrics endpoint.
	if reloadMetrics != nil {
		r.Get("/metrics/reload", reloadMetrics)
	}

	// Well-known OAuth metadata endpoint (always public, mounted before auth middleware).
	if wellKnown != nil {
		r.Get("/.well-known/oauth-protected-resource", wellKnown)
	}

	// Mount MCP handlers.
	for path, handler := range mcpHandlers {
		r.Mount(path, handler)
	}

	httpSrv := &http.Server{
		Addr:         net.JoinHostPort("", strconv.Itoa(cfg.Server.Port)),
		Handler:      r,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	return &Server{
		cfg:        cfg,
		httpServer: httpSrv,
	}
}

// Start begins serving HTTP requests and blocks until ctx is cancelled.
// It performs a graceful shutdown after the context is done.
func (s *Server) Start(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		slog.Info("server listening", "addr", s.httpServer.Addr)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		} else {
			errCh <- nil
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.Server.ShutdownTimeout)
		defer cancel()
		//nolint:contextcheck // parent ctx is cancelled; shutdown needs a fresh context with its own timeout
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
