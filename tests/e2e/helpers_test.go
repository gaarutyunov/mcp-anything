//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// registerStub registers a WireMock stub mapping via the admin API.
func registerStub(t *testing.T, base, body string) {
	t.Helper()
	resp, err := http.Post(base+"/__admin/mappings", "application/json", bytes.NewBufferString(body)) //nolint:noctx // test helper
	if err != nil {
		t.Fatalf("register wiremock stub: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("register wiremock stub: got %d: %s", resp.StatusCode, b)
	}
}

// toolNames extracts tool names for use in log messages.
func toolNames(tools []*sdkmcp.Tool) []string {
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	return names
}

// contentText extracts the text from the first TextContent block.
func contentText(content []sdkmcp.Content) string {
	for _, c := range content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// findFreeLocalPort returns a free TCP port on localhost.
func findFreeLocalPort() (int, error) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return 0, fmt.Errorf("finding free local port: %w", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// waitForHTTPOK polls the given URL until it returns HTTP 200, or the context expires.
func waitForHTTPOK(ctx context.Context, targetURL string) error {
	httpClient := &http.Client{Timeout: 5 * time.Second}
	return wait.PollUntilContextTimeout(ctx, 3*time.Second, 2*time.Minute, true, func(_ context.Context) (bool, error) {
		resp, err := httpClient.Get(targetURL) //nolint:noctx // polling helper
		if err != nil {
			return false, nil
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK, nil
	})
}

// createOrUpdateConfigMap creates a ConfigMap, or updates it if one already exists.
func createOrUpdateConfigMap(ctx context.Context, c client.Client, cm *corev1.ConfigMap) error {
	if err := c.Create(ctx, cm); err == nil {
		return nil
	} else if !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating ConfigMap %s/%s: %w", cm.Namespace, cm.Name, err)
	}
	existing := &corev1.ConfigMap{}
	if err := c.Get(ctx, types.NamespacedName{Name: cm.Name, Namespace: cm.Namespace}, existing); err != nil {
		return fmt.Errorf("fetching ConfigMap %s/%s: %w", cm.Namespace, cm.Name, err)
	}
	existing.Data = cm.Data
	if err := c.Update(ctx, existing); err != nil {
		return fmt.Errorf("updating ConfigMap %s/%s: %w", cm.Namespace, cm.Name, err)
	}
	return nil
}

// portForwardToPod sets up a port-forward from localPort on the test host to
// remotePort on the named pod via the Kubernetes API. Returns a stop function.
func portForwardToPod(ctx context.Context, t *testing.T, cfg *rest.Config, ns, podName string, localPort, remotePort int) (func(), error) {
	t.Helper()

	rt, upgrader, err := spdy.RoundTripperFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating SPDY round tripper: %w", err)
	}

	pfURL, err := url.Parse(cfg.Host)
	if err != nil {
		return nil, fmt.Errorf("parsing k8s host URL %q: %w", cfg.Host, err)
	}
	pfURL.Path = fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", ns, podName)

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: rt}, http.MethodPost, pfURL)

	stopCh := make(chan struct{})
	readyCh := make(chan struct{})

	fw, err := portforward.New(dialer,
		[]string{fmt.Sprintf("%d:%d", localPort, remotePort)},
		stopCh, readyCh,
		io.Discard, io.Discard,
	)
	if err != nil {
		return nil, fmt.Errorf("creating port forwarder: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- fw.ForwardPorts()
	}()

	select {
	case <-readyCh:
		t.Logf("port-forward ready: localhost:%d → pod %s:%d", localPort, podName, remotePort)
		return func() { close(stopCh) }, nil
	case fwErr := <-errCh:
		return nil, fmt.Errorf("port-forward failed before ready: %w", fwErr)
	case <-ctx.Done():
		close(stopCh)
		return nil, fmt.Errorf("context cancelled while waiting for port-forward: %w", ctx.Err())
	}
}

