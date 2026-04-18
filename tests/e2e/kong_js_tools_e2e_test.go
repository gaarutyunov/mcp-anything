//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	kongJSNamespace  = "kong-js-tools-e2e"
	kongJSProxyPort  = 8080

	// kongJSMCPConfig is the mcp-anything config for the Kong JS tools test.
	// It uses a script upstream with a JavaScript tool that fetches crypto prices
	// from WireMock and computes portfolio value. Kong's native MCP plugins cannot
	// execute arbitrary JavaScript; this demonstrates a key differentiating capability.
	kongJSMCPConfig = `server:
  port: 8080
naming:
  separator: "__"
js_script_pool:
  max_concurrent: 10
upstreams:
  - name: portfolio
    tool_prefix: portfolio
    type: script
    scripts:
      - tool_name: calculate_value
        description: |
          Calculate the current USD value of a crypto portfolio position.
          Fetches the live price from the rates service and applies JavaScript
          math to compute the total value. This JavaScript computation capability
          is not available in Kong's native MCP plugin support.
        script_path: /etc/mcp-anything/scripts/calculate_value.js
        timeout: 15s
        input_schema:
          type: object
          properties:
            coin:
              type: string
              description: Cryptocurrency symbol (e.g. BTC, ETH)
            quantity:
              type: number
              description: Quantity of the coin held
          required:
            - coin
            - quantity
      - tool_name: compare_pairs
        description: |
          Compare multiple currency pairs side-by-side.
          Uses JavaScript to fetch all pairs concurrently and build a comparison
          table. Demonstrates ctx.fetch and array manipulation in JS.
        script_path: /etc/mcp-anything/scripts/compare_pairs.js
        timeout: 20s
        input_schema:
          type: object
          properties:
            pairs:
              type: array
              description: List of currency pairs to compare (e.g. [USDEUR, USDJPY])
              items:
                type: string
          required:
            - pairs
`

	// kongJSCalculateValueScript is the JavaScript tool that computes portfolio value.
	// It uses ctx.fetch to call the WireMock rates API synchronously (Sobek does not
	// support async/await), then applies JavaScript arithmetic and string formatting.
	// This represents JavaScript tooling capability that Kong's native MCP lacks.
	kongJSCalculateValueScript = `module.exports = function(args, ctx) {
    var coin = args.coin;
    var quantity = args.amount || args.quantity;
    var data = ctx.fetch("http://wiremock:8080/rates/" + coin);
    var totalValue = data.usd_price * quantity;
    var rounded = Math.round(totalValue * 100) / 100;
    return {
        coin: data.coin,
        quantity: quantity,
        price_per_coin: data.usd_price,
        total_value_usd: rounded,
        summary: quantity + " " + coin + " = $" + rounded.toFixed(2) + " USD"
    };
};
`

	// kongJSComparePairsScript fetches multiple currency pairs and returns a comparison.
	// It demonstrates iteration, accumulation, and formatted output in JavaScript.
	kongJSComparePairsScript = `module.exports = function(args, ctx) {
    var pairs = args.pairs;
    var results = [];
    for (var i = 0; i < pairs.length; i++) {
        var pair = pairs[i];
        var data = ctx.fetch("http://wiremock:8080/rates/" + pair);
        results.push({
            pair: pair,
            rate: data.rate,
            label: "1 " + pair.slice(0, 3) + " = " + data.rate + " " + pair.slice(3)
        });
    }
    return {
        pairs: results,
        count: results.length
    };
};
`
)

// TestKongJSToolsE2E verifies that mcp-anything's JavaScript tool capability
// (script upstream) works end-to-end when deployed alongside Kong.
//
// Scenario: A team uses Kong Gateway for API management. Kong's built-in MCP
// support handles simple HTTP → MCP bridging, but cannot execute arbitrary
// JavaScript. The team deploys mcp-anything alongside Kong to add JavaScript
// tools: tools that fetch data, apply custom computation, and return structured
// results — capabilities Kong's native plugins lack.
//
// This test deploys the standard mcp-anything proxy image with a script upstream
// config. Two JavaScript tools are exercised:
//  1. portfolio__calculate_value — fetches a crypto price and computes total value
//  2. portfolio__compare_pairs   — fetches multiple FX pairs and builds a comparison
//
// Required environment variable:
//   - PROXY_IMAGE: the standard mcp-anything proxy image (includes script upstream)
//
// The test:
//  1. Deploys WireMock simulating a crypto/FX rates API.
//  2. Deploys the proxy with a script upstream config and two JS tool scripts.
//  3. Connects an MCP client and lists tools.
//  4. Calls portfolio__calculate_value and verifies the computed USD value.
//  5. Calls portfolio__compare_pairs and verifies the comparison result.
func TestKongJSToolsE2E(t *testing.T) {
	proxyImage := os.Getenv("PROXY_IMAGE")
	if proxyImage == "" {
		t.Fatal("PROXY_IMAGE must point to the image built for this test run")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	loadImageIntoK3s(ctx, t, globalK3s, proxyImage)

	scheme := buildOperatorScheme()
	restCfg, err := clientcmd.RESTConfigFromKubeConfig(globalK3s.kubeConfigYAML)
	if err != nil {
		t.Fatalf("building REST config: %v", err)
	}
	k8sClient, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("creating k8s client: %v", err)
	}

	// ── 1. Create namespace ───────────────────────────────────────────────────

	t.Logf("creating namespace %s", kongJSNamespace)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: kongJSNamespace}}
	if err := k8sClient.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("creating namespace: %v", err)
	}
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		existing := &corev1.Namespace{}
		if err := k8sClient.Get(cleanCtx, types.NamespacedName{Name: kongJSNamespace}, existing); err == nil {
			if err := k8sClient.Delete(cleanCtx, existing); err != nil && !apierrors.IsNotFound(err) {
				t.Logf("cleanup: delete namespace %s: %v", kongJSNamespace, err)
			}
		}
	})

	// ── 2. Deploy WireMock (crypto/FX rates API) ──────────────────────────────

	t.Log("deploying WireMock as crypto/FX rates API")
	ratesPort, err := deployKongJSWiremock(ctx, t, k8sClient)
	if err != nil {
		t.Fatalf("deploying WireMock: %v", err)
	}
	t.Logf("WireMock ready on local port %d", ratesPort)
	wiremockBase := fmt.Sprintf("http://localhost:%d", ratesPort)

	// ── 3. Register WireMock stubs ────────────────────────────────────────────

	t.Log("registering WireMock stubs for rates API")
	registerStub(t, wiremockBase, `{
		"request":  {"method": "GET", "urlPattern": "/rates/BTC"},
		"response": {"status": 200, "jsonBody": {"coin": "BTC", "usd_price": 67000.50}}
	}`)
	registerStub(t, wiremockBase, `{
		"request":  {"method": "GET", "urlPattern": "/rates/ETH"},
		"response": {"status": 200, "jsonBody": {"coin": "ETH", "usd_price": 3200.00}}
	}`)
	registerStub(t, wiremockBase, `{
		"request":  {"method": "GET", "urlPattern": "/rates/USDEUR"},
		"response": {"status": 200, "jsonBody": {"pair": "USDEUR", "rate": 0.92}}
	}`)
	registerStub(t, wiremockBase, `{
		"request":  {"method": "GET", "urlPattern": "/rates/USDJPY"},
		"response": {"status": 200, "jsonBody": {"pair": "USDJPY", "rate": 149.50}}
	}`)

	// ── 4. Create ConfigMaps ───────────────────────────────────────────────────

	t.Log("creating ConfigMaps for mcp-anything config and JS scripts")
	if err := createOrUpdateConfigMap(ctx, k8sClient, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "kong-mcp-config", Namespace: kongJSNamespace},
		Data:       map[string]string{"config.yaml": kongJSMCPConfig},
	}); err != nil {
		t.Fatalf("creating mcp config ConfigMap: %v", err)
	}
	if err := createOrUpdateConfigMap(ctx, k8sClient, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "kong-js-scripts", Namespace: kongJSNamespace},
		Data: map[string]string{
			"calculate_value.js": kongJSCalculateValueScript,
			"compare_pairs.js":   kongJSComparePairsScript,
		},
	}); err != nil {
		t.Fatalf("creating JS scripts ConfigMap: %v", err)
	}

	// ── 5. Deploy mcp-anything proxy with JS tools ────────────────────────────

	t.Log("deploying mcp-anything proxy with JavaScript script upstream")
	if err := deployKongJSProxy(ctx, t, k8sClient, proxyImage); err != nil {
		t.Fatalf("deploying proxy: %v", err)
	}

	// ── 6. Wait for proxy pod to be ready ─────────────────────────────────────

	t.Log("waiting for proxy pod to be ready")
	proxyPod, err := waitForDeploymentPod(ctx, t, k8sClient, kongJSNamespace, "portfolio-proxy", 5*time.Minute)
	if err != nil {
		collectNamespaceDiagnostics(t, kubeconfigFileFromYAML(t, globalK3s.kubeConfigYAML), kongJSNamespace)
		t.Fatalf("proxy pod not ready: %v", err)
	}
	t.Logf("proxy pod ready: %s", proxyPod)

	// ── 7. Port-forward to proxy ──────────────────────────────────────────────

	localPort, err := findFreeLocalPort()
	if err != nil {
		t.Fatalf("finding free port: %v", err)
	}
	stopForward, err := portForwardToPod(ctx, t, restCfg, kongJSNamespace, proxyPod, localPort, kongJSProxyPort)
	if err != nil {
		t.Fatalf("port-forwarding to proxy: %v", err)
	}
	defer stopForward()

	proxyURL := fmt.Sprintf("http://localhost:%d", localPort)

	// ── 8. Wait for proxy /healthz ────────────────────────────────────────────

	t.Log("waiting for proxy /healthz")
	healthCtx, healthCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer healthCancel()
	if err := waitForHTTPOK(healthCtx, proxyURL+"/healthz"); err != nil {
		t.Fatalf("proxy healthz not ready: %v", err)
	}
	t.Log("proxy is healthy")

	// ── 9. Connect MCP client ─────────────────────────────────────────────────

	mcpTransport := &sdkmcp.StreamableClientTransport{Endpoint: proxyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "kong-js-test", Version: "v0.0.1"}, nil)

	callCtx, callCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer callCancel()

	session, err := mcpClient.Connect(callCtx, mcpTransport, nil)
	if err != nil {
		t.Fatalf("connecting MCP client: %v", err)
	}
	defer session.Close()

	// ── 10. Verify tool listing ────────────────────────────────────────────────

	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	t.Logf("JavaScript tools exposed: %v", toolNames(toolsResult.Tools))

	wantTools := []string{"portfolio__calculate_value", "portfolio__compare_pairs"}
	toolSet := make(map[string]bool, len(toolsResult.Tools))
	for _, tool := range toolsResult.Tools {
		toolSet[tool.Name] = true
	}
	for _, want := range wantTools {
		if !toolSet[want] {
			t.Errorf("expected JS tool %q not found; available: %v", want, toolNames(toolsResult.Tools))
		}
	}

	// ── 11. Call portfolio__calculate_value (BTC, 0.5 coins) ──────────────────
	// The JS script fetches /rates/BTC from WireMock (67000.50) and computes:
	// 0.5 * 67000.50 = 33500.25. This demonstrates JS math + ctx.fetch.

	t.Log("calling portfolio__calculate_value for 0.5 BTC")
	calcResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name:      "portfolio__calculate_value",
		Arguments: map[string]any{"coin": "BTC", "quantity": 0.5},
	})
	if err != nil {
		t.Fatalf("call portfolio__calculate_value: %v", err)
	}
	if calcResult.IsError {
		t.Fatalf("portfolio__calculate_value returned error: %s", contentText(calcResult.Content))
	}
	calcText := contentText(calcResult.Content)
	t.Logf("calculate_value response: %s", calcText)

	// The response must reference BTC, the price, and the computed value.
	for _, want := range []string{"BTC", "67000", "33500"} {
		if !strings.Contains(calcText, want) {
			t.Errorf("calculate_value response missing %q; full response: %s", want, calcText)
		}
	}

	// ── 12. Call portfolio__calculate_value (ETH, 2.0 coins) ─────────────────
	// 2.0 * 3200.00 = 6400.00 — verifies script re-use with different inputs.

	t.Log("calling portfolio__calculate_value for 2.0 ETH")
	ethResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name:      "portfolio__calculate_value",
		Arguments: map[string]any{"coin": "ETH", "quantity": 2.0},
	})
	if err != nil {
		t.Fatalf("call portfolio__calculate_value (ETH): %v", err)
	}
	if ethResult.IsError {
		t.Fatalf("portfolio__calculate_value (ETH) returned error: %s", contentText(ethResult.Content))
	}
	ethText := contentText(ethResult.Content)
	t.Logf("calculate_value ETH response: %s", ethText)
	if !strings.Contains(ethText, "ETH") || !strings.Contains(ethText, "6400") {
		t.Errorf("calculate_value ETH response unexpected: %s", ethText)
	}

	// ── 13. Call portfolio__compare_pairs (USDEUR, USDJPY) ────────────────────
	// The JS script loops over pairs, calls ctx.fetch for each, and returns a
	// comparison array. Verifies multi-fetch iteration in JavaScript.

	t.Log("calling portfolio__compare_pairs for [USDEUR, USDJPY]")
	compareResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name:      "portfolio__compare_pairs",
		Arguments: map[string]any{"pairs": []any{"USDEUR", "USDJPY"}},
	})
	if err != nil {
		t.Fatalf("call portfolio__compare_pairs: %v", err)
	}
	if compareResult.IsError {
		t.Fatalf("portfolio__compare_pairs returned error: %s", contentText(compareResult.Content))
	}
	compareText := contentText(compareResult.Content)
	t.Logf("compare_pairs response: %s", compareText)

	for _, want := range []string{"USDEUR", "USDJPY", "0.92", "149"} {
		if !strings.Contains(compareText, want) {
			t.Errorf("compare_pairs response missing %q; full response: %s", want, compareText)
		}
	}
	t.Log("Kong JS tools E2E: JavaScript tools (ctx.fetch + computation) work end-to-end")
}

// deployKongJSWiremock deploys WireMock in the kong-js-tools namespace for use
// as the mock crypto/FX rates API, port-forwards to it, and returns the local port.
func deployKongJSWiremock(ctx context.Context, t *testing.T, c client.Client) (int, error) {
	t.Helper()

	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "wiremock", Namespace: kongJSNamespace},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "wiremock"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "wiremock"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "wiremock",
						Image: wiremockImage,
						Ports: []corev1.ContainerPort{{ContainerPort: wiremockPort, Protocol: corev1.ProtocolTCP}},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/__admin/health",
									Port: intstr.FromInt(wiremockPort),
								},
							},
							InitialDelaySeconds: 5,
							PeriodSeconds:       3,
							FailureThreshold:    20,
						},
					}},
				},
			},
		},
	}
	if err := c.Create(ctx, dep); err != nil && !apierrors.IsAlreadyExists(err) {
		return 0, fmt.Errorf("creating WireMock Deployment: %w", err)
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "wiremock", Namespace: kongJSNamespace},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "wiremock"},
			Ports:    []corev1.ServicePort{{Port: wiremockPort, TargetPort: intstr.FromInt(wiremockPort), Protocol: corev1.ProtocolTCP}},
		},
	}
	if err := c.Create(ctx, svc); err != nil && !apierrors.IsAlreadyExists(err) {
		return 0, fmt.Errorf("creating WireMock Service: %w", err)
	}

	podName, err := waitForDeploymentPod(ctx, t, c, kongJSNamespace, "wiremock", 3*time.Minute)
	if err != nil {
		return 0, fmt.Errorf("WireMock pod not ready: %w", err)
	}

	restCfg, err := clientcmd.RESTConfigFromKubeConfig(globalK3s.kubeConfigYAML)
	if err != nil {
		return 0, fmt.Errorf("building REST config: %w", err)
	}
	localPort, err := findFreeLocalPort()
	if err != nil {
		return 0, fmt.Errorf("finding free port: %w", err)
	}
	stopFwd, err := portForwardToPod(ctx, t, restCfg, kongJSNamespace, podName, localPort, wiremockAdminPort)
	if err != nil {
		return 0, fmt.Errorf("port-forwarding to WireMock: %w", err)
	}
	t.Cleanup(stopFwd)

	adminURL := fmt.Sprintf("http://localhost:%d", localPort)
	adminCtx, adminCancel := context.WithTimeout(ctx, 30*time.Second)
	defer adminCancel()
	if err := waitForWiremockAdmin(adminCtx, adminURL); err != nil {
		return 0, fmt.Errorf("WireMock admin not ready: %w", err)
	}
	return localPort, nil
}

// deployKongJSProxy creates the portfolio proxy Deployment. The proxy uses the
// standard PROXY_IMAGE but is configured with a script upstream: JS tools are
// mounted from the kong-js-scripts ConfigMap and the mcp-anything config from
// kong-mcp-config.
func deployKongJSProxy(ctx context.Context, t *testing.T, c client.Client, image string) error {
	t.Helper()

	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "portfolio-proxy", Namespace: kongJSNamespace},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "portfolio-proxy"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "portfolio-proxy"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "proxy",
						Image: image,
						Ports: []corev1.ContainerPort{{ContainerPort: kongJSProxyPort, Protocol: corev1.ProtocolTCP}},
						Env: []corev1.EnvVar{{
							Name:  "CONFIG_PATH",
							Value: "/etc/mcp-anything/config.yaml",
						}},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/healthz",
									Port: intstr.FromInt(kongJSProxyPort),
								},
							},
							InitialDelaySeconds: 5,
							PeriodSeconds:       5,
							FailureThreshold:    24,
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "mcp-config",
								MountPath: "/etc/mcp-anything/config.yaml",
								SubPath:   "config.yaml",
							},
							{
								Name:      "js-scripts",
								MountPath: "/etc/mcp-anything/scripts",
							},
						},
					}},
					Volumes: []corev1.Volume{
						{
							Name: "mcp-config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: "kong-mcp-config"},
								},
							},
						},
						{
							Name: "js-scripts",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: "kong-js-scripts"},
								},
							},
						},
					},
				},
			},
		},
	}
	if err := c.Create(ctx, dep); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating portfolio proxy Deployment: %w", err)
	}
	return nil
}
