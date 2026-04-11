//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/gaarutyunov/mcp-anything/pkg/crd/v1alpha1"
)

const (
	binanceTestNamespace = "binance-e2e"
	binanceProxyName     = "binance-proxy"
	binanceUpstreamName  = "market-data"

	// Paths to example files relative to the tests/integration directory.
	binanceSpecRelPath    = "../../deploy/examples/binance/spec.yaml"
	binanceOverlayRelPath = "../../deploy/examples/binance/overlay.yaml"
)

// TestBinanceMarketDataE2E deploys the proxy to the shared k3s cluster via the
// in-process operator, then makes real requests to the Binance public market data
// API through the MCP protocol.
//
// The test:
//  1. Loads the proxy image into k3s.
//  2. Applies the Binance spec and overlay as ConfigMaps.
//  3. Creates MCPUpstream and MCPProxy CRDs — the operator reconciles them
//     and creates the proxy Deployment, Service, and ConfigMaps.
//  4. Waits for the proxy pod to become Ready.
//  5. Port-forwards to the proxy pod and connects an MCP client.
//  6. Calls several Binance market data tools and verifies the responses.
//
func TestBinanceMarketDataE2E(t *testing.T) {
	if globalK3s == nil {
		t.Fatal("shared k3s cluster unavailable")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// ── 1. Determine and load proxy image ────────────────────────────────────

	proxyImage := os.Getenv("PROXY_IMAGE")
	if proxyImage == "" {
		proxyImage = "ghcr.io/gaarutyunov/mcp-anything:latest"
	}

	t.Logf("loading proxy image %q into k3s", proxyImage)
	if err := globalK3s.container.LoadImages(ctx, proxyImage); err != nil {
		t.Fatalf("cannot load proxy image %q into k3s: %v", proxyImage, err)
	}
	t.Log("proxy image loaded into k3s")

	// ── 2. Build k8s client ───────────────────────────────────────────────────

	scheme := buildOperatorScheme()
	restCfg, err := clientcmd.RESTConfigFromKubeConfig(globalK3s.kubeConfigYAML)
	if err != nil {
		t.Fatalf("building REST config: %v", err)
	}
	k8sClient, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("creating k8s client: %v", err)
	}

	// ── 3. Create namespace ───────────────────────────────────────────────────

	t.Logf("creating namespace %s", binanceTestNamespace)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: binanceTestNamespace}}
	if err := k8sClient.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("creating namespace: %v", err)
	}
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		existing := &corev1.Namespace{}
		if err := k8sClient.Get(cleanCtx, types.NamespacedName{Name: binanceTestNamespace}, existing); err == nil {
			if err := k8sClient.Delete(cleanCtx, existing); err != nil && !apierrors.IsNotFound(err) {
				t.Logf("cleanup: delete namespace %s: %v", binanceTestNamespace, err)
			}
		}
	})

	// ── 4. Load spec and overlay from example files ───────────────────────────

	specData, err := os.ReadFile(filepath.Join(".", binanceSpecRelPath))
	if err != nil {
		t.Fatalf("reading binance spec from %s: %v", binanceSpecRelPath, err)
	}
	overlayData, err := os.ReadFile(filepath.Join(".", binanceOverlayRelPath))
	if err != nil {
		t.Fatalf("reading binance overlay from %s: %v", binanceOverlayRelPath, err)
	}

	// ── 5. Create ConfigMaps for spec and overlay ─────────────────────────────

	t.Log("creating ConfigMaps for spec and overlay")
	if err := createOrUpdateConfigMap(ctx, k8sClient, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "binance-spec", Namespace: binanceTestNamespace},
		Data:       map[string]string{"spec.yaml": string(specData)},
	}); err != nil {
		t.Fatalf("creating binance-spec ConfigMap: %v", err)
	}
	if err := createOrUpdateConfigMap(ctx, k8sClient, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "binance-overlay", Namespace: binanceTestNamespace},
		Data:       map[string]string{"overlay.yaml": string(overlayData)},
	}); err != nil {
		t.Fatalf("creating binance-overlay ConfigMap: %v", err)
	}

	// ── 6. Start operator in-process ─────────────────────────────────────────

	t.Log("starting operator in-process")
	stopOperator := startOperator(ctx, t, globalK3s.kubeConfigYAML, scheme)
	defer stopOperator()

	// ── 7. Create MCPUpstream ─────────────────────────────────────────────────

	t.Logf("creating MCPUpstream %s/%s", binanceTestNamespace, binanceUpstreamName)
	upstream := &v1alpha1.MCPUpstream{
		ObjectMeta: metav1.ObjectMeta{
			Name:      binanceUpstreamName,
			Namespace: binanceTestNamespace,
			Labels: map[string]string{
				"mcp-anything.ai/proxy": binanceProxyName,
			},
		},
		Spec: v1alpha1.MCPUpstreamSpec{
			ToolPrefix: "binance",
			BaseURL:    "https://api.binance.com",
			OpenAPI: v1alpha1.MCPUpstreamOpenAPISpec{
				ConfigMapRef: &v1alpha1.ConfigMapKeyRef{
					Name: "binance-spec",
					Key:  "spec.yaml",
				},
			},
			Overlay: &v1alpha1.MCPUpstreamOverlaySpec{
				ConfigMapRef: &v1alpha1.ConfigMapKeyRef{
					Name: "binance-overlay",
					Key:  "overlay.yaml",
				},
			},
		},
	}
	if err := k8sClient.Create(ctx, upstream); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("creating MCPUpstream: %v", err)
	}

	// ── 8. Create MCPProxy ────────────────────────────────────────────────────

	t.Logf("creating MCPProxy %s/%s", binanceTestNamespace, binanceProxyName)
	proxyResource := &v1alpha1.MCPProxy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      binanceProxyName,
			Namespace: binanceTestNamespace,
		},
		Spec: v1alpha1.MCPProxySpec{
			UpstreamSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"mcp-anything.ai/proxy": binanceProxyName,
				},
			},
			Image: proxyImage,
			Server: v1alpha1.ProxyServerSpec{
				Port: 8080,
			},
			Naming: v1alpha1.ProxyNamingSpec{
				Separator: "__",
			},
		},
	}
	if err := k8sClient.Create(ctx, proxyResource); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("creating MCPProxy: %v", err)
	}

	// ── 9. Wait for proxy pod to be ready ─────────────────────────────────────

	t.Log("waiting for proxy pod to become Ready (up to 5 minutes)")
	podName, err := waitForBinanceProxyPod(ctx, t, k8sClient, binanceTestNamespace, binanceProxyName)
	if err != nil {
		t.Fatalf("proxy pod not ready: %v", err)
	}
	t.Logf("proxy pod ready: %s", podName)

	// ── 10. Port-forward to proxy pod ─────────────────────────────────────────

	localPort, err := findFreeLocalPort()
	if err != nil {
		t.Fatalf("finding free local port: %v", err)
	}
	t.Logf("port-forwarding localhost:%d → %s:8080", localPort, podName)

	stopForward, err := portForwardToPod(ctx, t, restCfg, binanceTestNamespace, podName, localPort, 8080)
	if err != nil {
		t.Fatalf("starting port-forward: %v", err)
	}
	defer stopForward()

	proxyURL := fmt.Sprintf("http://localhost:%d", localPort)

	// ── 11. Wait for proxy to be healthy ──────────────────────────────────────

	t.Log("waiting for proxy /healthz endpoint to return 200")
	healthCtx, healthCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer healthCancel()
	if err := waitForHTTPOK(healthCtx, proxyURL+"/healthz"); err != nil {
		t.Fatalf("proxy healthz not OK: %v", err)
	}
	t.Log("proxy is healthy")

	// ── 12. Connect MCP client ────────────────────────────────────────────────

	transport := &sdkmcp.StreamableClientTransport{
		Endpoint: proxyURL + "/mcp",
	}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "binance-test-client", Version: "v0.0.1"}, nil)

	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()

	session, err := mcpClient.Connect(callCtx, transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	// ── 13. List and verify available tools ───────────────────────────────────

	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	t.Logf("exposed %d Binance tools: %v", len(toolsResult.Tools), toolNames(toolsResult.Tools))

	if len(toolsResult.Tools) == 0 {
		t.Fatal("expected at least one Binance market data tool, got none")
	}

	// ── 14. Call get_price for BTCUSDT ────────────────────────────────────────

	t.Log("calling binance__get_price for BTCUSDT")
	priceResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name:      "binance__get_price",
		Arguments: map[string]any{"symbol": "BTCUSDT"},
	})
	if err != nil {
		t.Fatalf("call binance__get_price: %v", err)
	}
	if priceResult.IsError {
		errText := contentText(priceResult.Content)
		if strings.Contains(errText, "451") {
			t.Skipf("binance__get_price: Binance geo-restricted (HTTP 451) from this region — skipping API call assertions")
		}
		t.Fatalf("binance__get_price returned error: %s", errText)
	}
	priceText := contentText(priceResult.Content)
	t.Logf("BTCUSDT price response: %s", priceText)
	if !strings.Contains(priceText, "BTCUSDT") {
		t.Errorf("price response does not contain BTCUSDT symbol: %s", priceText)
	}
	if !strings.Contains(priceText, "price") {
		t.Errorf("price response does not contain price field: %s", priceText)
	}

	// ── 15. Call get_24hr_stats for ETHUSDT ───────────────────────────────────

	t.Log("calling binance__get_24hr_stats for ETHUSDT")
	statsResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name:      "binance__get_24hr_stats",
		Arguments: map[string]any{"symbol": "ETHUSDT"},
	})
	if err != nil {
		t.Fatalf("call binance__get_24hr_stats: %v", err)
	}
	if statsResult.IsError {
		errText := contentText(statsResult.Content)
		if strings.Contains(errText, "451") {
			t.Skipf("binance__get_24hr_stats: Binance geo-restricted (HTTP 451) from this region — skipping API call assertions")
		}
		t.Fatalf("binance__get_24hr_stats returned error: %s", errText)
	}
	statsText := contentText(statsResult.Content)
	t.Logf("ETHUSDT 24hr stats response: %s", statsText)
	if !strings.Contains(statsText, "ETHUSDT") {
		t.Errorf("24hr stats response does not contain ETHUSDT symbol: %s", statsText)
	}
	if !strings.Contains(statsText, "lastPrice") {
		t.Errorf("24hr stats response does not contain lastPrice field: %s", statsText)
	}

	// ── 16. Call get_avg_price for BNBUSDT ────────────────────────────────────

	t.Log("calling binance__get_avg_price for BNBUSDT")
	avgResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name:      "binance__get_avg_price",
		Arguments: map[string]any{"symbol": "BNBUSDT"},
	})
	if err != nil {
		t.Fatalf("call binance__get_avg_price: %v", err)
	}
	if avgResult.IsError {
		errText := contentText(avgResult.Content)
		if strings.Contains(errText, "451") {
			t.Skipf("binance__get_avg_price: Binance geo-restricted (HTTP 451) from this region — skipping API call assertions")
		}
		t.Fatalf("binance__get_avg_price returned error: %s", errText)
	}
	avgText := contentText(avgResult.Content)
	t.Logf("BNBUSDT avg price response: %s", avgText)
	if !strings.Contains(avgText, "price") {
		t.Errorf("avg price response does not contain price field: %s", avgText)
	}
}

// waitForBinanceProxyPod polls until a pod with the proxy labels is Running and Ready,
// or until the context is cancelled. Returns the pod name or an error.
// If an image pull failure is detected it returns immediately with an error.
func waitForBinanceProxyPod(ctx context.Context, t *testing.T, c client.Client, ns, proxyName string) (string, error) {
	t.Helper()

	podLabels := map[string]string{
		"app.kubernetes.io/name":      "mcp-anything",
		"app.kubernetes.io/instance":  proxyName,
		"app.kubernetes.io/component": "proxy",
	}

	var podName string
	err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 5*time.Minute, false, func(pollCtx context.Context) (bool, error) {
		podList := &corev1.PodList{}
		if listErr := c.List(pollCtx, podList,
			client.InNamespace(ns),
			client.MatchingLabels(podLabels),
		); listErr != nil {
			t.Logf("list pods: %v (retrying)", listErr)
			return false, nil
		}

		for i := range podList.Items {
			pod := &podList.Items[i]
			// Detect image pull failures early so we don't wait the full timeout.
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.State.Waiting != nil {
					reason := cs.State.Waiting.Reason
					if reason == "ImagePullBackOff" || reason == "ErrImagePull" || reason == "InvalidImageName" {
						return false, fmt.Errorf("pod %s image pull failed: %s — %s",
							pod.Name, reason, cs.State.Waiting.Message)
					}
				}
			}
			// Check for Ready condition.
			if pod.Status.Phase == corev1.PodRunning {
				for _, cond := range pod.Status.Conditions {
					if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
						podName = pod.Name
						return true, nil
					}
				}
			}
		}
		t.Logf("proxy pod not yet ready in namespace %s (proxy=%s); waiting…", ns, proxyName)
		return false, nil
	})
	if err != nil {
		return "", err
	}
	return podName, nil
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
