//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// globalKC holds the shared Keycloak container started once per test binary run.
// All tests that need Keycloak share this instance to avoid starting one per test,
// which is the primary cause of the integration test suite exceeding the CI timeout.
var globalKC *sharedKeycloakContainer

type sharedKeycloakContainer struct {
	container   testcontainers.Container
	externalURL string // http://host:PORT — accessible from the test machine
	networkName string // dedicated Keycloak network — proxy containers join this
}

// TestMain starts shared containers before running all integration tests and
// terminates them once all tests have completed.
//   - Keycloak: shared by JWT/OAuth2 tests; each test connects it to its bridge network.
//   - k3s: shared by operator E2E tests; each test uses its own namespace.
//
// Both containers are started concurrently to minimise total startup time within
// the CI timeout budget. Timing is emitted for every phase so slow or hanging
// operations are visible in CI logs.
func TestMain(m *testing.M) {
	ctx := context.Background()
	total := time.Now()

	slog.Info("TestMain: starting shared containers concurrently")

	var (
		wg        sync.WaitGroup
		kcErr     error
		k3sErr    error
		kcResult  *sharedKeycloakContainer
		k3sResult *sharedK3sCluster
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		start := time.Now()
		slog.Info("Keycloak: startup begin")
		kcResult, kcErr = startSharedKeycloak(ctx)
		if kcErr != nil {
			slog.Error("Keycloak: startup failed", "elapsed", time.Since(start).Round(time.Millisecond), "error", kcErr)
		} else {
			slog.Info("Keycloak: startup done", "elapsed", time.Since(start).Round(time.Millisecond))
		}
	}()
	go func() {
		defer wg.Done()
		start := time.Now()
		slog.Info("k3s: startup begin")
		k3sResult, k3sErr = startSharedK3s(ctx)
		if k3sErr != nil {
			slog.Error("k3s: startup failed", "elapsed", time.Since(start).Round(time.Millisecond), "error", k3sErr)
		} else {
			slog.Info("k3s: startup done", "elapsed", time.Since(start).Round(time.Millisecond))
		}
	}()
	wg.Wait()

	slog.Info("TestMain: all containers started", "total_startup", time.Since(total).Round(time.Millisecond))

	if kcErr != nil {
		slog.Warn("shared Keycloak unavailable; JWT/OAuth2 tests will start their own instances", "error", kcErr)
	} else {
		globalKC = kcResult
	}

	if k3sErr != nil {
		slog.Warn("shared k3s cluster unavailable; operator tests will be skipped", "error", k3sErr)
	} else {
		globalK3s = k3sResult
	}

	slog.Info("TestMain: running tests")
	runStart := time.Now()
	code := m.Run()
	slog.Info("TestMain: tests finished", "elapsed", time.Since(runStart).Round(time.Millisecond), "exit_code", code)

	slog.Info("TestMain: terminating shared containers")
	if globalKC != nil {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		start := time.Now()
		if termErr := globalKC.container.Terminate(termCtx); termErr != nil {
			slog.Warn("terminate shared Keycloak", "error", termErr, "elapsed", time.Since(start).Round(time.Millisecond))
		} else {
			slog.Info("Keycloak: terminated", "elapsed", time.Since(start).Round(time.Millisecond))
		}
	}

	if globalK3s != nil {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		start := time.Now()
		if termErr := globalK3s.container.Terminate(termCtx); termErr != nil {
			slog.Warn("terminate shared k3s", "error", termErr, "elapsed", time.Since(start).Round(time.Millisecond))
		} else {
			slog.Info("k3s: terminated", "elapsed", time.Since(start).Round(time.Millisecond))
		}
	}

	os.Exit(code)
}

// startSharedKeycloak starts a single Keycloak instance on its own dedicated
// Docker network. Each test's proxy container joins this network to reach
// Keycloak by the alias "keycloak". This avoids runtime network connect/disconnect
// operations that can disrupt Keycloak's host port mapping (especially on
// Docker Desktop / OrbStack / GitHub Actions).
//
// KC_HOSTNAME is set to http://keycloak:8080 so tokens carry that as their iss
// claim, matching the per-test proxy config. KC_HOSTNAME_STRICT=false allows
// admin API access via the mapped host port.
func startSharedKeycloak(ctx context.Context) (*sharedKeycloakContainer, error) {
	start := time.Now()

	// Create a dedicated network for the shared Keycloak container.
	kcNet, err := network.New(ctx, network.WithDriver("bridge"))
	if err != nil {
		return nil, fmt.Errorf("create keycloak network: %w", err)
	}
	slog.Info("Keycloak: created dedicated network", "network", kcNet.Name)

	slog.Info("Keycloak: launching container")
	kc, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "quay.io/keycloak/keycloak:25.0",
			Cmd:          []string{"start-dev"},
			ExposedPorts: []string{"8080/tcp"},
			Networks:     []string{kcNet.Name},
			NetworkAliases: map[string][]string{
				kcNet.Name: {"keycloak"},
			},
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
		_ = kcNet.Remove(ctx)
		return nil, fmt.Errorf("start shared Keycloak: %w", err)
	}
	slog.Info("Keycloak: container ready", "container_startup", time.Since(start).Round(time.Millisecond))

	host, err := kc.Host(ctx)
	if err != nil {
		_ = kc.Terminate(context.Background())
		_ = kcNet.Remove(ctx)
		return nil, fmt.Errorf("get host: %w", err)
	}
	port, err := kc.MappedPort(ctx, "8080")
	if err != nil {
		_ = kc.Terminate(context.Background())
		_ = kcNet.Remove(ctx)
		return nil, fmt.Errorf("get mapped port: %w", err)
	}

	return &sharedKeycloakContainer{
		container:   kc,
		externalURL: fmt.Sprintf("http://%s:%s", host, port.Port()),
		networkName: kcNet.Name,
	}, nil
}
