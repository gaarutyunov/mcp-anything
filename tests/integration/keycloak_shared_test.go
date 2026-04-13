//go:build integration

package integration_test

import (
	"context"
	"strings"
	"testing"
	"unicode"
)

// useSharedKeycloak returns a keycloakSetup backed by the global shared Keycloak
// container. Each test gets its own realm to avoid cross-test interference.
// The proxy container must join kc.NetworkName to reach Keycloak by the alias "keycloak".
//
// Falls back to starting a fresh per-test Keycloak if the global instance is unavailable.
func useSharedKeycloak(ctx context.Context, t *testing.T, testNetworkName string) *keycloakSetup {
	t.Helper()

	if globalKC == nil {
		// Graceful fallback: start a fresh Keycloak on the test's own network.
		return startKeycloak(ctx, t, testNetworkName)
	}

	// Each test gets its own realm to avoid cross-test interference.
	realm := kcRealmName(t.Name())
	adminToken := kcAdminToken(t, globalKC.externalURL)
	kcCreateRealm(t, globalKC.externalURL, adminToken, realm)

	return &keycloakSetup{
		ExternalURL: globalKC.externalURL,
		InternalURL: "http://keycloak:8080",
		Realm:       realm,
		NetworkName: globalKC.networkName,
	}
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
