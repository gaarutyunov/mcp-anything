//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
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
	kongJSNamespace = "kong-js-tools-e2e"
	kongProxyPort   = 8000
	kongAdminPort   = 8001

	// kongDeclarativeConfig configures Kong in db-less mode.
	// Two services:
	//   1. rates-api: normal HTTP routing to WireMock (demonstrates Kong API gateway)
	//   2. mcp-proxy: MCP endpoint routed via the mcp-anything plugin (JavaScript tools)
	kongDeclarativeConfig = `_format_version: "3.0"
services:
  - name: rates-api
    host: wiremock
    port: 8080
    protocol: http
    routes:
      - name: rates-route
        paths:
          - /rates
        strip_path: false
  - name: mcp-proxy
    host: 127.0.0.1
    port: 9090
    protocol: http
    routes:
      - name: mcp-route
        paths:
          - /mcp
        strip_path: false
    plugins:
      - name: mcp-anything
        config:
          config_path: /etc/mcp-anything/config.yaml
`

	// kongJSMCPConfig is the mcp-anything config for the embedded proxy started
	// by the Kong plugin. The server address is overridden by the plugin at startup;
	// the port field here is a placeholder.
	kongJSMCPConfig = `server:
  port: 9090
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

	// kongJSCalculateValueScript computes portfolio value via ctx.fetch + JS arithmetic.
	kongJSCalculateValueScript = `module.exports = function(args, ctx) {
    var coin = args.coin;
    var quantity = args.quantity;
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

	// kongJSComparePairsScript fetches multiple FX pairs and returns a comparison table.
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

// TestKongJSToolsE2E verifies that a custom Kong Go plugin binary produced by
// mcp-anything-builder --target kong delivers JavaScript MCP tools through Kong Gateway.
//
// Scenario: A fintech team uses Kong Gateway to manage their crypto/FX rates APIs.
// Kong's built-in MCP support handles simple HTTP→MCP bridging, but cannot execute
// arbitrary JavaScript. The team uses the mcp-anything Kong plugin (built with the
// builder) to add JavaScript tools: tools that fetch live rates, apply custom
// computation, and return structured results — capabilities Kong's native plugins lack.
//
// The same Kong deployment handles both:
//   - Normal HTTP traffic: GET /rates/* is reverse-proxied to WireMock (standard Kong)
//   - MCP JavaScript tools: GET/POST /mcp goes through the mcp-anything plugin (JS engine)
//
// Required environment variable:
//   - KONG_BUILDER_IMAGE: Kong Gateway image built with `mcp-anything-builder --target kong`
//     (see Dockerfile.kong-builder)
//
// The test:
//  1. Loads KONG_BUILDER_IMAGE into k3s.
//  2. Deploys WireMock simulating a crypto/FX rates API.
//  3. Deploys Kong Gateway (KONG_BUILDER_IMAGE) with declarative config.
//  4. Verifies normal HTTP: GET /rates/BTC through Kong routes to WireMock.
//  5. Connects an MCP client to Kong's /mcp route (via the mcp-anything plugin).
//  6. Calls portfolio__calculate_value: JS fetches price + computes total.
//  7. Calls portfolio__compare_pairs: JS loops, fetches, returns comparison table.
func TestKongJSToolsE2E(t *testing.T) {
	kongBuilderImage := os.Getenv("KONG_BUILDER_IMAGE")
	if kongBuilderImage == "" {
		t.Skip("KONG_BUILDER_IMAGE not set; skipping Kong JS builder E2E test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	loadImageIntoK3s(ctx, t, globalK3s, kongBuilderImage)

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

	t.Log("creating ConfigMaps for Kong, mcp-anything config, and JS scripts")
	for _, cm := range []*corev1.ConfigMap{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "kong-config", Namespace: kongJSNamespace},
			Data:       map[string]string{"kong.yaml": kongDeclarativeConfig},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "kong-mcp-config", Namespace: kongJSNamespace},
			Data:       map[string]string{"config.yaml": kongJSMCPConfig},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "kong-js-scripts", Namespace: kongJSNamespace},
			Data: map[string]string{
				"calculate_value.js": kongJSCalculateValueScript,
				"compare_pairs.js":   kongJSComparePairsScript,
			},
		},
	} {
		if err := createOrUpdateConfigMap(ctx, k8sClient, cm); err != nil {
			t.Fatalf("creating ConfigMap %s: %v", cm.Name, err)
		}
	}

	// ── 5. Deploy Kong Gateway with the mcp-anything plugin ───────────────────

	t.Log("deploying Kong Gateway with mcp-anything plugin")
	if err := deployKong(ctx, t, k8sClient, kongBuilderImage); err != nil {
		t.Fatalf("deploying Kong: %v", err)
	}

	// ── 6. Wait for Kong pod to be ready ─────────────────────────────────────

	t.Log("waiting for Kong pod to be ready")
	kongPod, err := waitForDeploymentPod(ctx, t, k8sClient, kongJSNamespace, "kong", 5*time.Minute)
	if err != nil {
		collectNamespaceDiagnostics(t, kubeconfigFileFromYAML(t, globalK3s.kubeConfigYAML), kongJSNamespace)
		t.Fatalf("Kong pod not ready: %v", err)
	}
	t.Logf("Kong pod ready: %s", kongPod)

	// ── 7. Port-forward to Kong proxy port ────────────────────────────────────

	localKongPort, err := findFreeLocalPort()
	if err != nil {
		t.Fatalf("finding free port: %v", err)
	}
	stopForward, err := portForwardToPod(ctx, t, restCfg, kongJSNamespace, kongPod, localKongPort, kongProxyPort)
	if err != nil {
		t.Fatalf("port-forwarding to Kong proxy: %v", err)
	}
	defer stopForward()

	kongURL := fmt.Sprintf("http://localhost:%d", localKongPort)

	// ── 8. Wait for Kong proxy to accept connections ───────────────────────────
	// Kong returns 404 for undefined routes; the rates route verifies it's up.

	t.Log("waiting for Kong proxy to be ready (GET /rates/BTC)")
	healthCtx, healthCancel := context.WithTimeout(ctx, 3*time.Minute)
	defer healthCancel()
	if err := waitForHTTPOK(healthCtx, kongURL+"/rates/BTC"); err != nil {
		collectNamespaceDiagnostics(t, kubeconfigFileFromYAML(t, globalK3s.kubeConfigYAML), kongJSNamespace)
		t.Fatalf("Kong /rates/BTC not reachable: %v", err)
	}
	t.Log("Kong is ready")

	// ── 9. Verify normal HTTP routing: GET /rates/BTC via Kong → WireMock ─────
	// This confirms Kong's standard API gateway capability still works alongside
	// the JavaScript MCP plugin.

	t.Log("verifying normal HTTP routing: GET /rates/BTC through Kong")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Get(kongURL + "/rates/BTC") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /rates/BTC through Kong: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /rates/BTC: unexpected status %d", resp.StatusCode)
	}
	t.Log("normal HTTP routing via Kong works")

	// ── 10. Warm up the MCP plugin (triggers startOnce.Do(startProxy)) ────────
	// Sending an HTTP request to /mcp before the MCP handshake causes the plugin
	// to start the embedded proxy in the background. This avoids the MCP client
	// timing out while waiting for startProxy to complete.

	t.Log("warming up Kong mcp-anything plugin")
	warmCtx, warmCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer warmCancel()
	warmupClient := &http.Client{Timeout: 35 * time.Second}
	if err := waitForHTTPNon5xx(warmCtx, warmupClient, kongURL+"/mcp"); err != nil {
		collectNamespaceDiagnostics(t, kubeconfigFileFromYAML(t, globalK3s.kubeConfigYAML), kongJSNamespace)
		t.Fatalf("Kong /mcp plugin did not warm up: %v", err)
	}
	t.Log("Kong mcp-anything plugin is warm")

	// ── 11. Connect MCP client ────────────────────────────────────────────────

	t.Log("connecting MCP client to Kong /mcp")
	mcpTransport := &sdkmcp.StreamableClientTransport{Endpoint: kongURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "kong-js-test", Version: "v0.0.1"}, nil)

	callCtx, callCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer callCancel()

	session, err := mcpClient.Connect(callCtx, mcpTransport, nil)
	if err != nil {
		t.Fatalf("connecting MCP client: %v", err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Logf("closing MCP session: %v", err)
		}
	}()

	// ── 12. Verify tool listing ────────────────────────────────────────────────

	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	t.Logf("JavaScript tools exposed via Kong: %v", toolNames(toolsResult.Tools))

	wantTools := []string{"portfolio__calculate_value", "portfolio__compare_pairs"}
	toolSet := make(map[string]bool, len(toolsResult.Tools))
	for _, tool := range toolsResult.Tools {
		toolSet[tool.Name] = true
	}
	for _, want := range wantTools {
		if !toolSet[want] {
			t.Errorf("expected JS tool %q not found via Kong; available: %v", want, toolNames(toolsResult.Tools))
		}
	}

	// ── 13. Call portfolio__calculate_value (BTC, 0.5 coins) ──────────────────
	// JS fetches /rates/BTC from WireMock (67000.50) and computes 0.5 * 67000.50 = 33500.25.

	t.Log("calling portfolio__calculate_value for 0.5 BTC via Kong")
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

	for _, want := range []string{"BTC", "67000", "33500"} {
		if !strings.Contains(calcText, want) {
			t.Errorf("calculate_value response missing %q; full response: %s", want, calcText)
		}
	}

	// ── 14. Call portfolio__calculate_value (ETH, 2.0 coins) ─────────────────
	// 2.0 * 3200.00 = 6400.00

	t.Log("calling portfolio__calculate_value for 2.0 ETH via Kong")
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

	// ── 15. Call portfolio__compare_pairs (USDEUR, USDJPY) ────────────────────
	// JS loops over pairs, calls ctx.fetch for each, returns comparison array.

	t.Log("calling portfolio__compare_pairs for [USDEUR, USDJPY] via Kong")
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
	t.Log("Kong JS tools E2E: JavaScript tools (ctx.fetch + computation) work end-to-end via Kong plugin")
}

// waitForHTTPNon5xx polls the given URL until it returns a non-5xx HTTP status,
// or the context expires. Used to warm up the Kong plugin (startOnce.Do) before
// connecting the MCP client.
func waitForHTTPNon5xx(ctx context.Context, c *http.Client, targetURL string) error {
	var lastStatus int
	for {
		resp, err := c.Get(targetURL) //nolint:noctx
		if err == nil {
			lastStatus = resp.StatusCode
			_ = resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("context expired waiting for non-5xx from %s (last status: %d): %w", targetURL, lastStatus, ctx.Err())
		case <-time.After(3 * time.Second):
		}
	}
}

// deployKongJSWiremock deploys WireMock in the kong-js-tools namespace and
// port-forwards to it. Returns the local port for stub registration.
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
	stopFwd, err := portForwardToPod(ctx, t, restCfg, kongJSNamespace, podName, localPort, wiremockPort)
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

// deployKong creates the Kong Gateway Deployment in the kong-js-tools namespace.
// Kong is configured in db-less mode via a declarative config ConfigMap.
// The mcp-anything Go plugin binary is pre-installed in the image (KONG_BUILDER_IMAGE).
func deployKong(ctx context.Context, t *testing.T, c client.Client, image string) error {
	t.Helper()

	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "kong", Namespace: kongJSNamespace},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "kong"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "kong"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "kong",
						Image: image,
						Ports: []corev1.ContainerPort{
							{ContainerPort: kongProxyPort, Protocol: corev1.ProtocolTCP},
							{ContainerPort: kongAdminPort, Protocol: corev1.ProtocolTCP},
						},
						Env: []corev1.EnvVar{
							{Name: "KONG_DATABASE", Value: "off"},
							{Name: "KONG_DECLARATIVE_CONFIG", Value: "/etc/kong/kong.yaml"},
							{Name: "KONG_PROXY_LISTEN", Value: "0.0.0.0:8000"},
							{Name: "KONG_ADMIN_LISTEN", Value: "0.0.0.0:8001"},
							// Point Kong to the directory where the plugin binary is installed.
							{Name: "KONG_GO_PLUGINS_DIR", Value: "/usr/local/share/lua/5.1/go-plugins"},
							// Register the mcp-anything plugin alongside bundled plugins.
							{Name: "KONG_PLUGINS", Value: "bundled,mcp-anything"},
							{Name: "KONG_LOG_LEVEL", Value: "info"},
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/status",
									Port: intstr.FromInt(kongAdminPort),
								},
							},
							InitialDelaySeconds: 10,
							PeriodSeconds:       5,
							FailureThreshold:    24,
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "kong-config",
								MountPath: "/etc/kong/kong.yaml",
								SubPath:   "kong.yaml",
							},
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
							Name: "kong-config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: "kong-config"},
								},
							},
						},
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
		return fmt.Errorf("creating Kong Deployment: %w", err)
	}
	return nil
}
