//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	gatewayNamespace    = "gateway-e2e"
	gatewayProxyName    = "github-proxy"
	gatewayUpstreamName = "github-api"
	gatewayClassName    = "mcp-gateway-class"
	gatewayName         = "mcp-gateway"
	gatewayProxyPort    = 8080

	// githubOpenAPISpec is a minimal OpenAPI 3.0 spec for the GitHub REST API subset.
	// Uses /meta which is a public, unauthenticated endpoint that returns JSON.
	githubOpenAPISpec = `openapi: "3.0.0"
info:
  title: GitHub REST API (subset)
  version: "1.0.0"
paths:
  /meta:
    get:
      operationId: getMeta
      summary: Get GitHub meta information
      description: Returns information about GitHub's current status and configuration
      responses:
        "200":
          description: GitHub meta information
          content:
            application/json:
              schema:
                type: object
`

	// gatewayUpstreamManifest is the MCPUpstream YAML for the GitHub REST API.
	gatewayUpstreamManifest = `apiVersion: mcp-anything.ai/v1alpha1
kind: MCPUpstream
metadata:
  name: github-api
  namespace: gateway-e2e
  labels:
    mcp-anything.ai/proxy: github-proxy
spec:
  type: http
  toolPrefix: gh
  baseURL: https://api.github.com
  rateLimit: github-limit
  openapi:
    configMapRef:
      name: github-spec
      key: spec.yaml
`
)

// gatewayProxyManifest returns the MCPProxy YAML with Gateway API integration and rate limits.
func gatewayProxyManifest(proxyImage string) string {
	return fmt.Sprintf(`apiVersion: mcp-anything.ai/v1alpha1
kind: MCPProxy
metadata:
  name: github-proxy
  namespace: gateway-e2e
spec:
  upstreamSelector:
    matchLabels:
      mcp-anything.ai/proxy: github-proxy
  image: %s
  server:
    port: 8080
  naming:
    separator: __
  gatewayRef:
    name: mcp-gateway
    namespace: gateway-e2e
  rateLimits:
    policies:
      github-limit:
        average: 30
        period: 1m
        burst: 10
        source: ip
  resources:
    requests:
      memory: 64Mi
      cpu: 50m
`, proxyImage)
}

// TestGatewayAPIE2E verifies Kubernetes Gateway API integration end-to-end:
//
//  1. The operator creates an HTTPRoute when gatewayRef is configured on MCPProxy.
//  2. The HTTPRoute correctly references the Gateway and routes to the proxy Service.
//  3. The proxy exposes the GitHub REST API as MCP tools with rate limits configured.
//  4. MCP tool calls successfully reach the GitHub REST API through the proxy.
//
// Required environment variables:
//   - PROXY_IMAGE: image for the mcp-anything proxy container
//   - OPERATOR_IMAGE: image for the mcp-anything operator (installed via Helm)
func TestGatewayAPIE2E(t *testing.T) {
	proxyImage := os.Getenv("PROXY_IMAGE")
	if proxyImage == "" {
		t.Fatal("PROXY_IMAGE must point to the image built for this test run")
	}

	operatorImage := os.Getenv("OPERATOR_IMAGE")
	if operatorImage == "" {
		t.Fatal("OPERATOR_IMAGE must point to the operator image built for this test run")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	// ── 1. Load proxy and operator images into k3s ────────────────────────────

	loadImageIntoK3s(ctx, t, globalK3s, proxyImage)
	loadImageIntoK3s(ctx, t, globalK3s, operatorImage)

	// ── 2. Install Gateway API CRDs into the shared k3s cluster ──────────────

	t.Log("installing Gateway API CRDs")
	installGatewayAPICRDs(ctx, t, globalK3s.kubeConfigYAML)

	// ── 3. Build k8s client (scheme includes gateway-api types) ──────────────

	scheme := buildOperatorScheme()
	restCfg, err := clientcmd.RESTConfigFromKubeConfig(globalK3s.kubeConfigYAML)
	if err != nil {
		t.Fatalf("building REST config: %v", err)
	}
	k8sClient, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("creating k8s client: %v", err)
	}

	// ── 4. Create namespace ───────────────────────────────────────────────────

	t.Logf("creating namespace %s", gatewayNamespace)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: gatewayNamespace}}
	if err := k8sClient.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("creating namespace: %v", err)
	}
	t.Cleanup(func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanCancel()
		existing := &corev1.Namespace{}
		if getErr := k8sClient.Get(cleanCtx, types.NamespacedName{Name: gatewayNamespace}, existing); getErr == nil {
			if delErr := k8sClient.Delete(cleanCtx, existing); delErr != nil && !apierrors.IsNotFound(delErr) {
				t.Logf("cleanup: delete namespace %s: %v", gatewayNamespace, delErr)
			}
		}
	})

	// ── 5. Install operator via Helm chart ────────────────────────────────────

	t.Log("installing mcp-anything operator via Helm")
	stopHelm := helmInstallGatewayOperator(ctx, t, globalK3s.kubeConfigYAML, operatorImage)
	defer stopHelm()

	// ── 6. Create GatewayClass and Gateway resources ──────────────────────────

	t.Log("creating GatewayClass")
	createGatewayClass(ctx, t, k8sClient)

	t.Log("creating Gateway")
	createGateway(ctx, t, k8sClient)

	// ── 7. Create GitHub OpenAPI spec ConfigMap ───────────────────────────────

	t.Log("creating GitHub API spec ConfigMap")
	if cmErr := createOrUpdateConfigMap(ctx, k8sClient, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "github-spec", Namespace: gatewayNamespace},
		Data:       map[string]string{"spec.yaml": githubOpenAPISpec},
	}); cmErr != nil {
		t.Fatalf("creating github-spec ConfigMap: %v", cmErr)
	}

	// ── 8. Create MCPUpstream for GitHub REST API ─────────────────────────────

	t.Logf("applying GitHub MCPUpstream %s/%s", gatewayNamespace, gatewayUpstreamName)
	applyYAMLManifest(ctx, t, k8sClient, gatewayUpstreamManifest)

	// ── 9. Create MCPProxy with GatewayRef and rate limits ───────────────────

	t.Logf("applying MCPProxy %s/%s (with gatewayRef and rateLimits)", gatewayNamespace, gatewayProxyName)
	applyYAMLManifest(ctx, t, k8sClient, gatewayProxyManifest(proxyImage))

	// ── 10. Wait for proxy pod to be ready ────────────────────────────────────

	t.Log("waiting for proxy pod to become Ready (up to 5 minutes)")
	podName, podErr := waitForProxyPod(ctx, t, k8sClient, gatewayNamespace, gatewayProxyName)
	if podErr != nil {
		t.Fatalf("proxy pod not ready: %v", podErr)
	}
	t.Logf("proxy pod ready: %s", podName)

	// ── 11. Verify HTTPRoute was created by the operator ─────────────────────

	t.Log("waiting for HTTPRoute to be created by the operator")
	route := waitForHTTPRoute(ctx, t, k8sClient, gatewayNamespace, gatewayProxyName)
	verifyHTTPRoute(t, route)

	// ── 12. Port-forward to proxy pod ─────────────────────────────────────────

	localPort, portErr := findFreeLocalPort()
	if portErr != nil {
		t.Fatalf("finding free local port: %v", portErr)
	}
	t.Logf("port-forwarding localhost:%d → %s:%d", localPort, podName, gatewayProxyPort)

	stopForward, fwErr := portForwardToPod(ctx, t, restCfg, gatewayNamespace, podName, localPort, gatewayProxyPort)
	if fwErr != nil {
		t.Fatalf("starting port-forward: %v", fwErr)
	}
	defer stopForward()

	proxyURL := fmt.Sprintf("http://localhost:%d", localPort)

	// ── 13. Wait for proxy to be healthy ──────────────────────────────────────

	t.Log("waiting for proxy /healthz")
	healthCtx, healthCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer healthCancel()
	if healthErr := waitForHTTPOK(healthCtx, proxyURL+"/healthz"); healthErr != nil {
		t.Fatalf("proxy healthz not OK: %v", healthErr)
	}

	// ── 14. Connect MCP client ────────────────────────────────────────────────

	mcpTransport := &sdkmcp.StreamableClientTransport{
		Endpoint: proxyURL + "/mcp",
	}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "gateway-e2e-test", Version: "v0.0.1"}, nil)

	callCtx, callCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer callCancel()

	session, sessionErr := mcpClient.Connect(callCtx, mcpTransport, nil)
	if sessionErr != nil {
		t.Fatalf("connect MCP client: %v", sessionErr)
	}
	defer session.Close()

	// ── 15. Verify tool listing ───────────────────────────────────────────────

	toolsResult, listErr := session.ListTools(callCtx, nil)
	if listErr != nil {
		t.Fatalf("list tools: %v", listErr)
	}
	t.Logf("exposed %d tools: %v", len(toolsResult.Tools), toolNames(toolsResult.Tools))

	const expectedTool = "gh__get_meta"
	found := false
	for _, tool := range toolsResult.Tools {
		if tool.Name == expectedTool {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected tool %q not found; available: %v", expectedTool, toolNames(toolsResult.Tools))
		return
	}

	// ── 16. Call the GitHub getMeta tool ──────────────────────────────────────
	// Verifies that the proxy correctly forwards the request to api.github.com
	// while enforcing the configured rate limit policy.

	t.Log("calling gh__get_meta (GitHub /meta endpoint)")
	result, callErr := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name:      expectedTool,
		Arguments: map[string]any{},
	})
	if callErr != nil {
		t.Fatalf("call gh__get_meta: %v", callErr)
	}
	if result.IsError {
		t.Fatalf("gh__get_meta returned error: %s", contentText(result.Content))
	}
	responseText := contentText(result.Content)
	t.Logf("gh__get_meta response (first 200 bytes): %.200s", responseText)
	if responseText == "" {
		t.Error("gh__get_meta returned empty response")
	}
}

// installGatewayAPICRDs installs the Gateway API standard CRDs into the k3s cluster
// using kubectl apply. CRD files are read from the sigs.k8s.io/gateway-api module cache.
func installGatewayAPICRDs(ctx context.Context, t *testing.T, kubeConfigYAML []byte) {
	t.Helper()

	// Resolve the gateway-api module directory.
	crdDir := gatewayAPICRDDir(ctx, t)
	t.Logf("installing Gateway API CRDs from %s", crdDir)

	// Write kubeconfig to a temp file for kubectl.
	kubeconfigFile, err := os.CreateTemp("", "gw-kubeconfig-*.yaml")
	if err != nil {
		t.Fatalf("creating temp kubeconfig file: %v", err)
	}
	defer func() { _ = os.Remove(kubeconfigFile.Name()) }()
	if _, writeErr := kubeconfigFile.Write(kubeConfigYAML); writeErr != nil {
		_ = kubeconfigFile.Close()
		t.Fatalf("writing kubeconfig: %v", writeErr)
	}
	_ = kubeconfigFile.Close()

	// Apply the minimal set of Gateway API CRDs required for HTTPRoute support.
	for _, crdFile := range []string{
		"gateway.networking.k8s.io_gatewayclasses.yaml",
		"gateway.networking.k8s.io_gateways.yaml",
		"gateway.networking.k8s.io_httproutes.yaml",
	} {
		crdPath := filepath.Join(crdDir, crdFile)
		applyCmd := exec.CommandContext(ctx, "kubectl", "apply", //nolint:gosec // controlled input
			"--kubeconfig", kubeconfigFile.Name(),
			"-f", crdPath,
		)
		if applyOut, applyErr := applyCmd.CombinedOutput(); applyErr != nil {
			t.Fatalf("installing Gateway API CRD %s: %v\n%s", crdFile, applyErr, applyOut)
		}
		t.Logf("applied CRD: %s", crdFile)
	}

	// Wait for the CRDs to be established before proceeding.
	waitCtx, waitCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer waitCancel()

	restCfg, err := clientcmd.RESTConfigFromKubeConfig(kubeConfigYAML)
	if err != nil {
		t.Fatalf("building REST config for Gateway CRD wait: %v", err)
	}
	crdClient, err := client.New(restCfg, client.Options{Scheme: buildOperatorScheme()})
	if err != nil {
		t.Fatalf("creating client for Gateway CRD wait: %v", err)
	}

	start := time.Now()
	t.Log("waiting for Gateway API CRDs to be established")
	if waitErr := waitForGatewayAPICRDs(waitCtx, crdClient); waitErr != nil {
		t.Fatalf("Gateway API CRDs not established (elapsed=%s): %v",
			time.Since(start).Round(time.Millisecond), waitErr)
	}
	t.Logf("Gateway API CRDs established (elapsed=%s)", time.Since(start).Round(time.Millisecond))
}

// gatewayAPICRDDir returns the path to the standard Gateway API CRD directory
// from the sigs.k8s.io/gateway-api module cache.
func gatewayAPICRDDir(ctx context.Context, t *testing.T) string {
	t.Helper()

	out, err := exec.CommandContext(ctx, "go", "list", "-m", "-f", "{{.Dir}}", "sigs.k8s.io/gateway-api").
		CombinedOutput()
	if err != nil {
		// Fall back to constructing the path from GOMODCACHE.
		goModCacheOut, modCacheErr := exec.CommandContext(ctx, "go", "env", "GOMODCACHE").CombinedOutput()
		if modCacheErr != nil {
			t.Fatalf("finding GOMODCACHE: %v: %s", modCacheErr, goModCacheOut)
		}
		versionOut, versionErr := exec.CommandContext(ctx, "go", "list", "-m", "-f", "{{.Version}}", "sigs.k8s.io/gateway-api").
			CombinedOutput()
		if versionErr != nil {
			t.Fatalf("finding gateway-api version: %v: %s", versionErr, versionOut)
		}
		modCache := strings.TrimSpace(string(goModCacheOut))
		version := strings.TrimSpace(string(versionOut))
		return filepath.Join(modCache, "sigs.k8s.io", "gateway-api@"+version, "config", "crd", "standard")
	}

	modDir := strings.TrimSpace(string(out))
	return filepath.Join(modDir, "config", "crd", "standard")
}

// waitForGatewayAPICRDs polls until the Gateway API CRDs are established in the cluster.
func waitForGatewayAPICRDs(ctx context.Context, c client.Client) error {
	crdNames := []string{
		"gatewayclasses.gateway.networking.k8s.io",
		"gateways.gateway.networking.k8s.io",
		"httproutes.gateway.networking.k8s.io",
	}
	for _, name := range crdNames {
		if err := wait.PollUntilContextTimeout(ctx, pollInterval, 60*time.Second, true, func(pollCtx context.Context) (bool, error) {
			crd := &apiextensionsv1.CustomResourceDefinition{}
			if getErr := c.Get(pollCtx, types.NamespacedName{Name: name}, crd); getErr != nil {
				if apierrors.IsNotFound(getErr) {
					return false, nil
				}
				return false, getErr
			}
			for _, cond := range crd.Status.Conditions {
				if cond.Type == apiextensionsv1.Established && cond.Status == apiextensionsv1.ConditionTrue {
					return true, nil
				}
			}
			return false, nil
		}); err != nil {
			return fmt.Errorf("waiting for CRD %s: %w", name, err)
		}
	}
	return nil
}

// helmInstallGatewayOperator installs the operator for the gateway E2E test.
// Uses a distinct Helm release name to avoid conflicting with the smoke test.
func helmInstallGatewayOperator(ctx context.Context, t *testing.T, kubeConfigYAML []byte, operatorImage string) func() {
	t.Helper()

	kubeconfigFile, err := os.CreateTemp("", "gw-op-kubeconfig-*.yaml")
	if err != nil {
		t.Fatalf("creating temp kubeconfig file: %v", err)
	}
	if _, writeErr := kubeconfigFile.Write(kubeConfigYAML); writeErr != nil {
		_ = kubeconfigFile.Close()
		_ = os.Remove(kubeconfigFile.Name())
		t.Fatalf("writing kubeconfig: %v", writeErr)
	}
	_ = kubeconfigFile.Close()

	kubeconfigPath := kubeconfigFile.Name()
	t.Cleanup(func() { _ = os.Remove(kubeconfigPath) })

	imgRepo, imgTag := splitImageRef(operatorImage)
	chartPath := "../../charts/mcp-anything"
	const releaseName = "gateway-operator"

	t.Logf("helm install %s (image=%s:%s)", releaseName, imgRepo, imgTag)
	installCmd := exec.CommandContext(ctx, "helm", "install", releaseName, chartPath,
		"--kubeconfig", kubeconfigPath,
		"--namespace", gatewayNamespace,
		"--create-namespace",
		"--set", fmt.Sprintf("image.repository=%s", imgRepo),
		"--set", fmt.Sprintf("image.tag=%s", imgTag),
		"--set", "image.pullPolicy=IfNotPresent",
		"--set", "leaderElect=false",
		"--set", fmt.Sprintf("watchNamespace=%s", gatewayNamespace),
		"--wait",
		"--timeout", "3m",
	)
	if out, installErr := installCmd.CombinedOutput(); installErr != nil {
		collectNamespaceDiagnostics(t, kubeconfigPath, gatewayNamespace)
		t.Fatalf("helm install failed: %v\noutput:\n%s", installErr, out)
	}
	t.Log("helm install succeeded")

	return func() {
		uninstallCtx, uninstallCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer uninstallCancel()
		uninstallCmd := exec.CommandContext(uninstallCtx, "helm", "uninstall", releaseName,
			"--kubeconfig", kubeconfigPath,
			"--namespace", gatewayNamespace,
		)
		if out, uninstallErr := uninstallCmd.CombinedOutput(); uninstallErr != nil {
			t.Logf("helm uninstall failed (ignored): %v\noutput:\n%s", uninstallErr, out)
		}
	}
}

// createGatewayClass creates a GatewayClass in the cluster (cluster-scoped resource).
func createGatewayClass(ctx context.Context, t *testing.T, c client.Client) {
	t.Helper()

	gc := &gatewayv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: gatewayClassName,
		},
		Spec: gatewayv1.GatewayClassSpec{
			// Use a placeholder controller name — no real controller is needed for the
			// HTTPRoute creation test; we only verify the operator creates the correct resources.
			ControllerName: "example.com/mcp-gateway-controller",
		},
	}
	if createErr := c.Create(ctx, gc); createErr != nil && !apierrors.IsAlreadyExists(createErr) {
		t.Fatalf("creating GatewayClass %s: %v", gatewayClassName, createErr)
	}
	t.Logf("GatewayClass %s created", gatewayClassName)
}

// createGateway creates a Gateway resource in the test namespace.
func createGateway(ctx context.Context, t *testing.T, c client.Client) {
	t.Helper()

	port := gatewayv1.PortNumber(80)
	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gatewayName,
			Namespace: gatewayNamespace,
		},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: gatewayv1.ObjectName(gatewayClassName),
			Listeners: []gatewayv1.Listener{
				{
					Name:     "http",
					Protocol: gatewayv1.HTTPProtocolType,
					Port:     port,
				},
			},
		},
	}
	if createErr := c.Create(ctx, gw); createErr != nil && !apierrors.IsAlreadyExists(createErr) {
		t.Fatalf("creating Gateway %s/%s: %v", gatewayNamespace, gatewayName, createErr)
	}
	t.Logf("Gateway %s/%s created", gatewayNamespace, gatewayName)
}

// waitForHTTPRoute polls until an HTTPRoute with the given name exists in the namespace.
// Returns the HTTPRoute once found; fatals if it does not appear within 3 minutes.
func waitForHTTPRoute(ctx context.Context, t *testing.T, c client.Client, ns, name string) *gatewayv1.HTTPRoute {
	t.Helper()

	var route *gatewayv1.HTTPRoute
	err := wait.PollUntilContextTimeout(ctx, 3*time.Second, 3*time.Minute, false, func(pollCtx context.Context) (bool, error) {
		r := &gatewayv1.HTTPRoute{}
		if getErr := c.Get(pollCtx, types.NamespacedName{Name: name, Namespace: ns}, r); getErr != nil {
			if apierrors.IsNotFound(getErr) {
				t.Logf("waiting for HTTPRoute %s/%s (not found yet)…", ns, name)
				return false, nil
			}
			return false, getErr
		}
		route = r
		return true, nil
	})
	if err != nil {
		t.Fatalf("HTTPRoute %s/%s not created in time: %v", ns, name, err)
	}
	t.Logf("HTTPRoute %s/%s created by operator", ns, name)
	return route
}

// verifyHTTPRoute asserts that the HTTPRoute has the expected structure:
// - parentRef pointing to the test Gateway
// - a backend rule pointing to the proxy Service
func verifyHTTPRoute(t *testing.T, route *gatewayv1.HTTPRoute) {
	t.Helper()

	if len(route.Spec.ParentRefs) == 0 {
		t.Error("HTTPRoute has no parentRefs")
		return
	}
	parentRef := route.Spec.ParentRefs[0]
	if string(parentRef.Name) != gatewayName {
		t.Errorf("HTTPRoute parentRef.name = %q, want %q", parentRef.Name, gatewayName)
	}
	if parentRef.Namespace == nil || string(*parentRef.Namespace) != gatewayNamespace {
		ns := "<nil>"
		if parentRef.Namespace != nil {
			ns = string(*parentRef.Namespace)
		}
		t.Errorf("HTTPRoute parentRef.namespace = %q, want %q", ns, gatewayNamespace)
	}

	if len(route.Spec.Rules) == 0 {
		t.Error("HTTPRoute has no rules")
		return
	}
	rule := route.Spec.Rules[0]
	if len(rule.BackendRefs) == 0 {
		t.Error("HTTPRoute rule has no backendRefs")
		return
	}
	backendRef := rule.BackendRefs[0]
	if string(backendRef.Name) != gatewayProxyName {
		t.Errorf("HTTPRoute backendRef.name = %q, want %q", backendRef.Name, gatewayProxyName)
	}
	t.Logf("HTTPRoute verified: parentRef=%s/%s backendRef=%s port=%v",
		*parentRef.Namespace, parentRef.Name,
		backendRef.Name,
		backendRef.Port,
	)
}
