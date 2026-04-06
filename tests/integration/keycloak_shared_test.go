//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
	"unicode"

	dockernetwork "github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
)

// useSharedKeycloak connects the global shared Keycloak container to the test's
// Docker network (with alias "keycloak") and creates a unique realm for the test.
// It falls back to starting a fresh Keycloak if the global instance is unavailable.
//
// Use this instead of startKeycloak in any test that needs an OIDC/Keycloak server.
// networkID is net.ID and networkName is net.Name from testcontainers.DockerNetwork.
func useSharedKeycloak(ctx context.Context, t *testing.T, networkID, networkName string) *keycloakSetup {
	t.Helper()

	if globalKC == nil {
		// Graceful fallback: start a fresh Keycloak for this test.
		return startKeycloak(ctx, t, networkName)
	}

	containerID := globalKC.container.GetContainerID()
	if err := kcNetworkConnectWithRetry(ctx, t, networkID, networkName, containerID); err != nil {
		t.Logf("useSharedKeycloak: connect to network %q failed after retries (%v); falling back to fresh Keycloak", networkName, err)
		return startKeycloak(ctx, t, networkName)
	}
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := kcNetworkDisconnect(cleanCtx, networkID, containerID); err != nil {
			t.Logf("useSharedKeycloak: disconnect from network %q: %v", networkName, err)
		}
	})

	// Each test gets its own realm to avoid cross-test interference.
	realm := kcRealmName(t.Name())
	adminToken := kcAdminToken(t, globalKC.externalURL)
	kcCreateRealm(t, globalKC.externalURL, adminToken, realm)

	return &keycloakSetup{
		ExternalURL: globalKC.externalURL,
		InternalURL: "http://keycloak:8080",
		Realm:       realm,
	}
}

// kcNetworkConnectWithRetry attaches containerID to networkID, retrying up to 3
// times with 500 ms back-off to handle transient Docker API failures. It also
// treats "already exists" as a success so idempotent re-connects don't fall back
// to an expensive per-test Keycloak startup.
func kcNetworkConnectWithRetry(ctx context.Context, t *testing.T, networkID, networkName, containerID string) error {
	t.Helper()
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := kcNetworkConnect(ctx, networkID, containerID)
		if err == nil {
			return nil
		}
		// "already exists" means the container is already on this network — success.
		if strings.Contains(err.Error(), "already exists") {
			return nil
		}
		slog.Warn("kcNetworkConnect attempt failed",
			"attempt", attempt,
			"max_attempts", maxAttempts,
			"network", networkName,
			"error", err,
		)
		lastErr = err
		if attempt < maxAttempts {
			time.Sleep(500 * time.Millisecond)
		}
	}
	return lastErr
}

// kcNetworkConnect attaches containerID to networkID with the DNS alias "keycloak".
func kcNetworkConnect(ctx context.Context, networkID, containerID string) error {
	cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}
	defer cli.Close()
	return cli.NetworkConnect(ctx, networkID, containerID, &dockernetwork.EndpointSettings{
		Aliases: []string{"keycloak"},
	})
}

// kcNetworkDisconnect detaches containerID from networkID.
func kcNetworkDisconnect(ctx context.Context, networkID, containerID string) error {
	cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}
	defer cli.Close()
	return cli.NetworkDisconnect(ctx, networkID, containerID, false)
}

// kcRealmName converts a Go test name into a Keycloak-safe realm name.
// Keycloak realm names may contain letters, digits, underscores, and hyphens.
func kcRealmName(testName string) string {
	var b strings.Builder
	for _, r := range testName {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteByte('-')
		}
	}
	result := b.String()
	// Collapse consecutive hyphens and trim leading/trailing hyphens.
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}
	result = strings.Trim(result, "-")
	if len(result) > 64 {
		// Keep the suffix to preserve the most specific part of the name.
		result = strings.TrimLeft(result[len(result)-64:], "-")
	}
	return result
}
