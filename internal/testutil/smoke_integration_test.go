//go:build integration

package testutil_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestSmokeTestcontainersWorks verifies that Testcontainers can start a container
// and that the CI environment (including TC_CLOUD_TOKEN if set) is working.
func TestSmokeTestcontainersWorks(t *testing.T) {
	ctx := context.Background()

	// Start a minimal HTTP container (nginx) to verify Docker/TC Cloud connectivity
	req := testcontainers.ContainerRequest{
		Image:        "nginx:alpine",
		ExposedPorts: []string{"80/tcp"},
		WaitingFor:   wait.ForHTTP("/").WithStartupTimeout(30 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start nginx container: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("get container host: %v", err)
	}
	port, err := c.MappedPort(ctx, "80")
	if err != nil {
		t.Fatalf("get mapped port: %v", err)
	}

	url := "http://" + host + ":" + port.Port() + "/"
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	t.Logf("testcontainers smoke test passed: nginx returned %d at %s", resp.StatusCode, url)
}
