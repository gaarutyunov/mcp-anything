// Package ext_authz registers the "ext_authz" inbound auth strategy.
// Import this package (blank import) to make the strategy available via inbound.New().
//
// The strategy delegates authorization decisions to an external gRPC service that
// implements the Envoy envoy.service.auth.v3.Authorization/Check RPC. This makes
// mcp-anything compatible with OPA (with its Envoy plugin), custom auth sidecars,
// and any other Envoy ext_authz-compatible authorization server.
package ext_authz

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/gaarutyunov/mcp-anything/pkg/auth/inbound"
	"github.com/gaarutyunov/mcp-anything/pkg/config"
)

const defaultTimeout = 5 * time.Second

func init() {
	inbound.Register("ext_authz", func(ctx context.Context, cfg *config.InboundAuthConfig) (inbound.TokenValidator, string, error) {
		v, err := NewValidator(ctx, cfg.ExtAuthz)
		return v, "", err
	})
}

// Validator authorizes inbound requests via an Envoy-compatible ext_authz gRPC service.
// It implements inbound.RequestValidator (preferred by the middleware) as well as
// inbound.TokenValidator (required by the ValidatorFactory signature).
type Validator struct {
	client   authv3.AuthorizationClient
	timeout  time.Duration
	metadata map[string]string
}

// NewValidator creates a Validator from the given config.
// Returns an error if grpc_address is empty or the gRPC connection cannot be established.
func NewValidator(_ context.Context, cfg config.ExtAuthzConfig) (*Validator, error) {
	if cfg.GRPCAddress == "" {
		return nil, fmt.Errorf("ext_authz: grpc_address is required")
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	var creds credentials.TransportCredentials
	if cfg.TLS {
		creds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	} else {
		creds = insecure.NewCredentials()
	}

	conn, err := grpc.NewClient(cfg.GRPCAddress, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("ext_authz: connecting to %q: %w", cfg.GRPCAddress, err)
	}

	return &Validator{
		client:   authv3.NewAuthorizationClient(conn),
		timeout:  timeout,
		metadata: cfg.Metadata,
	}, nil
}

// ValidateRequest implements inbound.RequestValidator.
// It builds a CheckRequest from the inbound HTTP request, calls the ext_authz service,
// and returns:
//   - On OkResponse: TokenInfo (subject from gRPC peer, if available) and any headers_to_add.
//   - On DeniedResponse: a *inbound.DeniedError with the status code and raw body.
//   - On gRPC error or timeout: a *inbound.DeniedError with HTTP 503.
func (v *Validator) ValidateRequest(ctx context.Context, r *http.Request) (*inbound.TokenInfo, map[string]string, error) {
	checkCtx, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()

	req := buildCheckRequest(r, v.metadata)

	resp, err := v.client.Check(checkCtx, req)
	if err != nil {
		slog.Default().WarnContext(ctx, "ext_authz check failed", "error", err, "address", "grpc")
		return nil, nil, &inbound.DeniedError{
			Status:  http.StatusServiceUnavailable,
			Message: "ext_authz_unavailable",
		}
	}

	switch hr := resp.GetHttpResponse().(type) {
	case *authv3.CheckResponse_OkResponse:
		ok := hr.OkResponse
		injected := make(map[string]string, len(ok.GetHeaders()))
		for _, hvo := range ok.GetHeaders() {
			if hvo.GetHeader() != nil {
				injected[hvo.GetHeader().GetKey()] = hvo.GetHeader().GetValue()
			}
		}
		if len(injected) == 0 {
			injected = nil
		}
		return &inbound.TokenInfo{
			Subject: subjectFromContext(r),
		}, injected, nil

	case *authv3.CheckResponse_DeniedResponse:
		denied := hr.DeniedResponse
		statusCode := http.StatusForbidden
		if s := denied.GetStatus(); s != nil {
			statusCode = int(s.GetCode())
		}
		body := denied.GetBody()
		return nil, nil, &inbound.DeniedError{
			Status:      statusCode,
			Message:     "ext_authz_denied",
			RawBody:     []byte(body),
			RawBodyType: "text/plain",
		}

	default:
		// Treat unknown or error responses as 503.
		slog.Default().WarnContext(ctx, "ext_authz returned unexpected response type")
		return nil, nil, &inbound.DeniedError{
			Status:  http.StatusServiceUnavailable,
			Message: "ext_authz_error",
		}
	}
}

// ValidateToken satisfies the inbound.TokenValidator interface required by ValidatorFactory.
// The middleware prefers ValidateRequest when available; this method should never be called
// in normal operation.
func (v *Validator) ValidateToken(_ context.Context, _ string) (*inbound.TokenInfo, error) {
	return nil, fmt.Errorf("ext_authz: ValidateToken called directly; the middleware should use ValidateRequest")
}

// buildCheckRequest constructs an Envoy CheckRequest from an inbound HTTP request.
func buildCheckRequest(r *http.Request, metadata map[string]string) *authv3.CheckRequest {
	headers := make(map[string]string, len(r.Header))
	for k, vs := range r.Header {
		if len(vs) > 0 {
			headers[k] = vs[0]
		}
	}

	path := r.URL.RequestURI()

	return &authv3.CheckRequest{
		Attributes: &authv3.AttributeContext{
			Request: &authv3.AttributeContext_Request{
				Http: &authv3.AttributeContext_HttpRequest{
					Method:   r.Method,
					Path:     path,
					Host:     r.Host,
					Headers:  headers,
					Protocol: r.Proto,
				},
			},
			ContextExtensions: metadata,
		},
	}
}

// subjectFromContext extracts a best-effort subject identifier from the request.
// For ext_authz, the subject is not available from the authorization response itself,
// so we use the remote address as a fallback identifier.
func subjectFromContext(r *http.Request) string {
	return r.RemoteAddr
}
