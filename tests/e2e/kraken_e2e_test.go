//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
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
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/gaarutyunov/mcp-anything/pkg/crd/v1alpha1"
)

const (
	krakenTestNamespace = "kraken-e2e"
	krakenProxyName     = "kraken-proxy"
	krakenUpstreamName  = "market-data"

	// Paths to example files relative to the tests/e2e directory.
	krakenSpecRelPath    = "../../examples/kraken/spec.yaml"
	krakenOverlayRelPath = "../../examples/kraken/overlay.yaml"
)

// TestKrakenMarketDataE2E deploys the proxy to the shared k3s cluster via the
// in-process operator, then makes real requests to the Kraken public market data
// API through the MCP protocol.
//
// The test:
//  1. Loads the proxy image into k3s.
//  2. Applies the Kraken spec and overlay as ConfigMaps.
//  3. Creates MCPUpstream and MCPProxy CRDs — the operator reconciles them
//     and creates the proxy Deployment, Service, and ConfigMaps.
//  4. Waits for the proxy pod to become Ready.
//  5. Port-forwards to the proxy pod and connects an MCP client.
//  6. Calls several Kraken market data tools and verifies the responses.
func TestKrakenMarketDataE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// ── 1. Determine and load proxy image ────────────────────────────────────

	proxyImage := os.Getenv("PROXY_IMAGE")
	if proxyImage == "" {
		t.Fatal("PROXY_IMAGE must point to the image built for this test run")
	}

	loadImageIntoK3s(ctx, t, globalK3s, proxyImage)

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

	t.Logf("creating namespace %s", krakenTestNamespace)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: krakenTestNamespace}}
	if err := k8sClient.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("creating namespace: %v", err)
	}
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		existing := &corev1.Namespace{}
		if err := k8sClient.Get(cleanCtx, types.NamespacedName{Name: krakenTestNamespace}, existing); err == nil {
			if err := k8sClient.Delete(cleanCtx, existing); err != nil && !apierrors.IsNotFound(err) {
				t.Logf("cleanup: delete namespace %s: %v", krakenTestNamespace, err)
			}
		}
	})

	// ── 4. Load spec and overlay from example files ───────────────────────────

	specData, err := os.ReadFile(filepath.Join(".", krakenSpecRelPath))
	if err != nil {
		t.Fatalf("reading kraken spec from %s: %v", krakenSpecRelPath, err)
	}
	overlayData, err := os.ReadFile(filepath.Join(".", krakenOverlayRelPath))
	if err != nil {
		t.Fatalf("reading kraken overlay from %s: %v", krakenOverlayRelPath, err)
	}

	// ── 5. Create ConfigMaps for spec and overlay ─────────────────────────────

	t.Log("creating ConfigMaps for spec and overlay")
	if err := createOrUpdateConfigMap(ctx, k8sClient, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "kraken-spec", Namespace: krakenTestNamespace},
		Data:       map[string]string{"spec.yaml": string(specData)},
	}); err != nil {
		t.Fatalf("creating kraken-spec ConfigMap: %v", err)
	}
	if err := createOrUpdateConfigMap(ctx, k8sClient, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "kraken-overlay", Namespace: krakenTestNamespace},
		Data:       map[string]string{"overlay.yaml": string(overlayData)},
	}); err != nil {
		t.Fatalf("creating kraken-overlay ConfigMap: %v", err)
	}

	// ── 6. Start operator in-process ─────────────────────────────────────────

	t.Log("starting operator in-process")
	stopOperator := startOperator(ctx, t, globalK3s.kubeConfigYAML, scheme)
	defer stopOperator()

	// ── 7. Create MCPUpstream ─────────────────────────────────────────────────

	t.Logf("creating MCPUpstream %s/%s", krakenTestNamespace, krakenUpstreamName)
	upstream := &v1alpha1.MCPUpstream{
		ObjectMeta: metav1.ObjectMeta{
			Name:      krakenUpstreamName,
			Namespace: krakenTestNamespace,
			Labels: map[string]string{
				"mcp-anything.ai/proxy": krakenProxyName,
			},
		},
		Spec: v1alpha1.MCPUpstreamSpec{
			ToolPrefix: "kraken",
			BaseURL:    "https://api.kraken.com",
			OpenAPI: v1alpha1.MCPUpstreamOpenAPISpec{
				ConfigMapRef: &v1alpha1.ConfigMapKeyRef{
					Name: "kraken-spec",
					Key:  "spec.yaml",
				},
			},
			Overlay: &v1alpha1.MCPUpstreamOverlaySpec{
				ConfigMapRef: &v1alpha1.ConfigMapKeyRef{
					Name: "kraken-overlay",
					Key:  "overlay.yaml",
				},
			},
		},
	}
	if err := k8sClient.Create(ctx, upstream); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("creating MCPUpstream: %v", err)
	}

	// ── 8. Create MCPProxy ────────────────────────────────────────────────────

	t.Logf("creating MCPProxy %s/%s", krakenTestNamespace, krakenProxyName)
	proxyResource := &v1alpha1.MCPProxy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      krakenProxyName,
			Namespace: krakenTestNamespace,
		},
		Spec: v1alpha1.MCPProxySpec{
			UpstreamSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"mcp-anything.ai/proxy": krakenProxyName,
				},
			},
			Image: proxyImage,
			Server: v1alpha1.ProxyServerSpec{
				Port: 8080,
			},
			Naming: v1alpha1.NamingSpec{
				Separator: "__",
			},
		},
	}
	if err := k8sClient.Create(ctx, proxyResource); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("creating MCPProxy: %v", err)
	}

	// ── 9. Wait for proxy pod to be ready ─────────────────────────────────────

	t.Log("waiting for proxy pod to become Ready (up to 5 minutes)")
	podName, err := waitForProxyPod(ctx, t, k8sClient, krakenTestNamespace, krakenProxyName)
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

	stopForward, err := portForwardToPod(ctx, t, restCfg, krakenTestNamespace, podName, localPort, 8080)
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

	mcpTransport := &sdkmcp.StreamableClientTransport{
		Endpoint: proxyURL + "/mcp",
	}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "kraken-test-client", Version: "v0.0.1"}, nil)

	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()

	session, err := mcpClient.Connect(callCtx, mcpTransport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	// ── 13. List and verify available tools ───────────────────────────────────

	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	t.Logf("exposed %d Kraken tools: %v", len(toolsResult.Tools), toolNames(toolsResult.Tools))

	if len(toolsResult.Tools) == 0 {
		t.Fatal("expected at least one Kraken market data tool, got none")
	}

	// ── 14. Call get_system_status ────────────────────────────────────────────

	t.Log("calling kraken__get_system_status")
	statusResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name:      "kraken__get_system_status",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("call kraken__get_system_status: %v", err)
	}
	if statusResult.IsError {
		t.Fatalf("kraken__get_system_status returned error: %s", contentText(statusResult.Content))
	}
	statusText := contentText(statusResult.Content)
	t.Logf("Kraken system status: %s", statusText)
	if !strings.Contains(statusText, "status") {
		t.Errorf("system status response does not contain status field: %s", statusText)
	}

	// ── 15. Call get_ticker for XBTUSD ────────────────────────────────────────

	t.Log("calling kraken__get_ticker for XBTUSD")
	tickerResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name:      "kraken__get_ticker",
		Arguments: map[string]any{"pair": "XBTUSD"},
	})
	if err != nil {
		t.Fatalf("call kraken__get_ticker: %v", err)
	}
	if tickerResult.IsError {
		t.Fatalf("kraken__get_ticker returned error: %s", contentText(tickerResult.Content))
	}
	tickerText := contentText(tickerResult.Content)
	t.Logf("XBTUSD ticker response: %s", tickerText)
	if !strings.Contains(tickerText, "lastPrice") {
		t.Errorf("ticker response does not contain lastPrice field: %s", tickerText)
	}

	// ── 16. Call get_ohlc for XBTUSD with hourly interval ────────────────────

	t.Log("calling kraken__get_ohlc for XBTUSD at 60-minute interval")
	ohlcResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name:      "kraken__get_ohlc",
		Arguments: map[string]any{"pair": "XBTUSD", "interval": 60},
	})
	if err != nil {
		t.Fatalf("call kraken__get_ohlc: %v", err)
	}
	if ohlcResult.IsError {
		t.Fatalf("kraken__get_ohlc returned error: %s", contentText(ohlcResult.Content))
	}
	ohlcText := contentText(ohlcResult.Content)
	t.Logf("XBTUSD OHLC response (truncated): %.200s…", ohlcText)
	if !strings.Contains(ohlcText, "candles") {
		t.Errorf("OHLC response does not contain candles field: %s", ohlcText)
	}
}

// waitForProxyPod polls until a pod with the proxy labels is Running and Ready,
// or until the context is cancelled. Returns the pod name or an error.
// If an image pull failure is detected it returns immediately with an error.
func waitForProxyPod(ctx context.Context, t *testing.T, c client.Client, ns, proxyName string) (string, error) {
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
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.State.Waiting != nil {
					reason := cs.State.Waiting.Reason
					if reason == "ImagePullBackOff" || reason == "ErrImagePull" || reason == "InvalidImageName" {
						return false, fmt.Errorf("pod %s image pull failed: %s — %s",
							pod.Name, reason, cs.State.Waiting.Message)
					}
				}
			}
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
