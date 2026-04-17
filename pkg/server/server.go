// Package server implements the HTTP server for mcp-anything.
package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
	pkgtelemetry "github.com/gaarutyunov/mcp-anything/pkg/telemetry"
	pkgtransport "github.com/gaarutyunov/mcp-anything/pkg/transport"
)

// ReadinessChecker can report whether the proxy is ready to serve.
// If ready is false, reason should contain a human-readable explanation.
type ReadinessChecker interface {
	IsReady() (ready bool, reason string)
}

// OAuthCallbackHandler handles OAuth2 authorization callback requests.
// Implemented by pkg/oauth/callbackmux.Mux.
type OAuthCallbackHandler interface {
	HandleCallback(w http.ResponseWriter, r *http.Request, upstreamName string)
}

// Server wraps the net/http server and manages its lifecycle.
type Server struct {
	cfg        *config.ProxySpec
	httpServer *http.Server
}

// New creates a new Server. mcpHandlers maps mount paths to their HTTP handlers.
// wellKnown is an optional handler for the OAuth 2.0 Protected Resource Metadata endpoint
// (GET /.well-known/oauth-protected-resource); pass nil to skip mounting it.
// reloadMetrics is an optional handler for the GET /metrics/reload endpoint; pass nil to skip.
// prometheusMetrics is an optional handler for the GET /metrics endpoint (Prometheus scrape); pass nil to skip.
// readiness is an optional checker for /readyz; pass nil to always return 200 OK.
// oauthCallback is an optional handler for GET /oauth/callback/{upstreamName}; pass nil to skip.
func New(cfg *config.ProxySpec, mcpHandlers map[string]http.Handler, wellKnown http.HandlerFunc, reloadMetrics http.HandlerFunc, prometheusMetrics http.Handler, readiness ReadinessChecker, oauthCallback OAuthCallbackHandler) *Server {
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

	// Reload metrics endpoint (plain text, backward compatible).
	if reloadMetrics != nil {
		r.Get("/metrics/reload", reloadMetrics)
	}

	// Prometheus scrape endpoint.
	if prometheusMetrics != nil {
		r.Get("/metrics", prometheusMetrics.ServeHTTP)
	}

	// Well-known OAuth metadata endpoint (always public, mounted before auth middleware).
	if wellKnown != nil {
		r.Get("/.well-known/oauth-protected-resource", wellKnown)
	}

	// OAuth2 callback endpoint (always public, mounted before auth middleware).
	if oauthCallback != nil {
		r.Get("/oauth/callback/{upstreamName}", func(w http.ResponseWriter, r *http.Request) {
			oauthCallback.HandleCallback(w, r, chi.URLParam(r, "upstreamName"))
		})
	}

	// Mount MCP handlers wrapped with OTel server instrumentation.
	for path, handler := range mcpHandlers {
		r.Mount(path, pkgtelemetry.ServerMiddleware(handler, path))
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
// If server.tls.cert_path is configured, it listens with TLS.
func (s *Server) Start(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		slog.Info("server listening", "addr", s.httpServer.Addr)
		var err error
		if s.cfg.Server.TLS.CertPath != "" {
			tlsCfg, buildErr := pkgtransport.BuildServerTLSConfig(s.cfg.Server.TLS)
			if buildErr != nil {
				errCh <- fmt.Errorf("server TLS config: %w", buildErr)
				return
			}
			ln, listenErr := tls.Listen("tcp", s.httpServer.Addr, tlsCfg)
			if listenErr != nil {
				errCh <- fmt.Errorf("TLS listen: %w", listenErr)
				return
			}
			err = s.httpServer.Serve(ln)
		} else {
			err = s.httpServer.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
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
