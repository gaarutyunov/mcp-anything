// Package testutil provides shared test infrastructure for mcp-anything integration tests.
// All helpers in this package are intended for use in *_integration_test.go files only.
package testutil

import (
	"context"
	"testing"

	"github.com/testcontainers/testcontainers-go"
)

// MustStartContainer starts a container and registers its cleanup with t.Cleanup.
// It calls t.Fatal if the container fails to start.
func MustStartContainer(ctx context.Context, t *testing.T, req testcontainers.ContainerRequest) testcontainers.Container {
	t.Helper()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start container %q: %v", req.Image, err)
	}
	t.Cleanup(func() {
		if err := c.Terminate(context.Background()); err != nil {
			t.Logf("terminate container %q: %v", req.Image, err)
		}
	})
	return c
}
