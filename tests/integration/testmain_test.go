//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// globalKC holds the shared Keycloak container started once per test binary run.
// All tests that need Keycloak share this instance to avoid starting one per test,
// which is the primary cause of the integration test suite exceeding the CI timeout.
var globalKC *sharedKeycloakContainer

type sharedKeycloakContainer struct {
	container   testcontainers.Container
	externalURL string // http://host:PORT — accessible from the test machine
}

// TestMain starts shared containers before running all integration tests and
// terminates them once all tests have completed.
//   - Keycloak: shared by JWT/OAuth2 tests; each test connects it to its bridge network.
//   - k3s: shared by operator E2E tests; each test uses its own namespace.
func TestMain(m *testing.M) {
	ctx := context.Background()

	kc, err := startSharedKeycloak(ctx)
	if err != nil {
		slog.Warn("shared Keycloak unavailable; JWT/OAuth2 tests will start their own instances", "error", err)
	} else {
		globalKC = kc
	}

	k3sCluster, err := startSharedK3s(ctx)
	if err != nil {
		slog.Warn("shared k3s cluster unavailable; operator tests will be skipped", "error", err)
	} else {
		globalK3s = k3sCluster
	}

	code := m.Run()

	if globalKC != nil {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if termErr := globalKC.container.Terminate(termCtx); termErr != nil {
			slog.Warn("terminate shared Keycloak", "error", termErr)
		}
	}

	if globalK3s != nil {
		if termErr := testcontainers.TerminateContainer(globalK3s.container); termErr != nil {
			slog.Warn("terminate shared k3s", "error", termErr)
		}
	}

	os.Exit(code)
}

// startSharedKeycloak starts a single Keycloak instance that is shared across tests.
// KC_HOSTNAME is set to http://keycloak:8080 so tokens carry that as their iss claim,
// matching the per-test proxy config. KC_HOSTNAME_STRICT=false allows admin API access
// via the mapped host port.
func startSharedKeycloak(ctx context.Context) (*sharedKeycloakContainer, error) {
	kc, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "quay.io/keycloak/keycloak:25.0",
			Cmd:          []string{"start-dev"},
			ExposedPorts: []string{"8080/tcp"},
			Env: map[string]string{
				"KEYCLOAK_ADMIN":          "admin",
				"KEYCLOAK_ADMIN_PASSWORD": "admin",
				// Tokens carry iss=http://keycloak:8080/realms/<realm>, matching
				// the issuer the proxy is configured with in each test.
				"KC_HOSTNAME":        "http://keycloak:8080",
				"KC_HOSTNAME_STRICT": "false",
			},
			WaitingFor: wait.ForHTTP("/realms/master").
				WithPort("8080").
				WithStatusCodeMatcher(func(status int) bool { return status == http.StatusOK }).
				WithStartupTimeout(3 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		return nil, fmt.Errorf("start shared Keycloak: %w", err)
	}

	host, err := kc.Host(ctx)
	if err != nil {
		_ = kc.Terminate(context.Background())
		return nil, fmt.Errorf("get host: %w", err)
	}
	port, err := kc.MappedPort(ctx, "8080")
	if err != nil {
		_ = kc.Terminate(context.Background())
		return nil, fmt.Errorf("get mapped port: %w", err)
	}

	return &sharedKeycloakContainer{
		container:   kc,
		externalURL: fmt.Sprintf("http://%s:%s", host, port.Port()),
	}, nil
}
