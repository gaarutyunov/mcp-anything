//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"io"
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
	caddyBuilderNamespace = "caddy-builder-e2e"
	caddyBuilderPort      = 8080

	// caddyCaddyfile configures Caddy to act as both a normal reverse proxy and
	// an MCP endpoint. /products/* is a plain HTTP reverse proxy to WireMock;
	// /mcp* is served by the mcpanything Caddy middleware. This coexistence is
	// the primary use-case the builder enables: operators don't need a separate
	// MCP server — their existing Caddy binary gains MCP tooling via the builder.
	caddyCaddyfile = `{
    order mcpanything before respond
}
:8080 {
    handle /products/* {
        reverse_proxy http://wiremock:8080
    }
    handle /mcp* {
        mcpanything {
            config_path /etc/mcp-anything/config.yaml
        }
    }
}
`

	// caddyMCPConfig is the mcp-anything config embedded inside the Caddy pod.
	// The upstream points to WireMock, which provides the product catalog REST API.
	// Caddy handles the HTTP server; mcp-anything's server.port is overridden in-process.
	caddyMCPConfig = `server:
  port: 8080
naming:
  separator: "__"
upstreams:
  - name: products
    tool_prefix: products
    base_url: http://wiremock:8080
    type: http
    openapi:
      source: /etc/mcp-anything/products-spec.yaml
`

	// caddyProductsSpec is a minimal OpenAPI 3.0 spec for the product catalog API
	// served by WireMock. Caddy's mcpanything handler exposes these paths as MCP tools.
	caddyProductsSpec = `openapi: "3.0.0"
info:
  title: Product Catalog API
  version: "1.0.0"
paths:
  /products:
    get:
      operationId: listProducts
      summary: List all products in the catalog
      responses:
        "200":
          description: Product list
          content:
            application/json:
              schema:
                type: array
                items:
                  type: object
                  properties:
                    id:
                      type: integer
                    name:
                      type: string
                    price:
                      type: number
  /products/{id}:
    get:
      operationId: getProduct
      summary: Get a product by its ID
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: integer
      responses:
        "200":
          description: Product detail
          content:
            application/json:
              schema:
                type: object
                properties:
                  id:
                    type: integer
                  name:
                    type: string
                  price:
                    type: number
`
)

// TestCaddyBuilderE2E verifies a custom Caddy binary produced by
// mcp-anything-builder --target caddy.
//
// Scenario: An e-commerce team runs Caddy as their edge proxy. They use the
// mcp-anything builder to embed MCP tooling directly into their Caddy binary
// so their AI assistants can query the product catalog — without deploying a
// separate MCP server. The same Caddy binary handles ordinary HTTP traffic
// (reverse-proxied to a product catalog backend) and MCP requests side-by-side.
//
// Required environment variable:
//   - CADDY_BUILDER_IMAGE: Docker image built with `mcp-anything-builder --target caddy`
//
// The test:
//  1. Loads CADDY_BUILDER_IMAGE into k3s.
//  2. Deploys WireMock as a mock product catalog backend.
//  3. Deploys the Caddy pod with a Caddyfile that routes /products/* to WireMock
//     and /mcp* to the mcpanything Caddy handler.
//  4. Registers WireMock stubs for GET /products and GET /products/{id}.
//  5. Verifies direct HTTP calls to /products/* work via Caddy's reverse proxy.
//  6. Connects an MCP client and verifies the same catalog data is reachable as
//     MCP tools (products__list_products, products__get_product).
func TestCaddyBuilderE2E(t *testing.T) {
	caddyBuilderImage := os.Getenv("CADDY_BUILDER_IMAGE")
	if caddyBuilderImage == "" {
		t.Skip("CADDY_BUILDER_IMAGE not set; skipping builder E2E test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	loadImageIntoK3s(ctx, t, globalK3s, caddyBuilderImage)

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

	t.Logf("creating namespace %s", caddyBuilderNamespace)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: caddyBuilderNamespace}}
	if err := k8sClient.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("creating namespace: %v", err)
	}
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		existing := &corev1.Namespace{}
		if err := k8sClient.Get(cleanCtx, types.NamespacedName{Name: caddyBuilderNamespace}, existing); err == nil {
			if err := k8sClient.Delete(cleanCtx, existing); err != nil && !apierrors.IsNotFound(err) {
				t.Logf("cleanup: delete namespace %s: %v", caddyBuilderNamespace, err)
			}
		}
	})

	// ── 2. Deploy WireMock (product catalog backend) ──────────────────────────

	t.Log("deploying WireMock as product catalog backend")
	wiremockPort, err := deployCaddyWiremock(ctx, t, k8sClient)
	if err != nil {
		t.Fatalf("deploying WireMock: %v", err)
	}
	t.Logf("WireMock ready on local port %d", wiremockPort)
	wiremockBase := fmt.Sprintf("http://localhost:%d", wiremockPort)

	// ── 3. Register WireMock stubs ────────────────────────────────────────────

	t.Log("registering WireMock stubs for product catalog")
	registerStub(t, wiremockBase, `{
		"request":  {"method": "GET", "url": "/products"},
		"response": {"status": 200, "jsonBody": [
			{"id": 1, "name": "Widget A", "price": 9.99},
			{"id": 2, "name": "Widget B", "price": 19.99}
		]}
	}`)
	registerStub(t, wiremockBase, `{
		"request":  {"method": "GET", "urlPattern": "/products/1"},
		"response": {"status": 200, "jsonBody": {"id": 1, "name": "Widget A", "price": 9.99}}
	}`)

	// ── 4. Create ConfigMaps ───────────────────────────────────────────────────

	t.Log("creating Caddy, MCP, and spec ConfigMaps")
	for _, cm := range []*corev1.ConfigMap{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "caddy-caddyfile", Namespace: caddyBuilderNamespace},
			Data:       map[string]string{"Caddyfile": caddyCaddyfile},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "caddy-mcp-config", Namespace: caddyBuilderNamespace},
			Data: map[string]string{
				"config.yaml":       caddyMCPConfig,
				"products-spec.yaml": caddyProductsSpec,
			},
		},
	} {
		if err := createOrUpdateConfigMap(ctx, k8sClient, cm); err != nil {
			t.Fatalf("creating ConfigMap %s: %v", cm.Name, err)
		}
	}

	// ── 5. Deploy Caddy pod ───────────────────────────────────────────────────

	t.Log("deploying custom Caddy pod")
	if err := deployCaddyPod(ctx, t, k8sClient, caddyBuilderImage); err != nil {
		t.Fatalf("deploying Caddy pod: %v", err)
	}

	// ── 6. Wait for Caddy pod ─────────────────────────────────────────────────

	t.Log("waiting for Caddy pod to be ready")
	caddyPod, err := waitForDeploymentPod(ctx, t, k8sClient, caddyBuilderNamespace, "caddy", 5*time.Minute)
	if err != nil {
		collectNamespaceDiagnostics(t, kubeconfigFileFromYAML(t, globalK3s.kubeConfigYAML), caddyBuilderNamespace)
		t.Fatalf("Caddy pod not ready: %v", err)
	}
	t.Logf("Caddy pod ready: %s", caddyPod)

	// ── 7. Port-forward to Caddy ──────────────────────────────────────────────

	localPort, err := findFreeLocalPort()
	if err != nil {
		t.Fatalf("finding free port: %v", err)
	}
	stopForward, err := portForwardToPod(ctx, t, restCfg, caddyBuilderNamespace, caddyPod, localPort, caddyBuilderPort)
	if err != nil {
		t.Fatalf("port-forwarding: %v", err)
	}
	defer stopForward()

	caddyURL := fmt.Sprintf("http://localhost:%d", localPort)

	// ── 8. Wait for Caddy to be healthy ───────────────────────────────────────

	// Caddy doesn't expose /healthz by default; poll the reverse-proxy path instead.
	t.Log("waiting for Caddy to accept connections on /products")
	healthCtx, healthCancel := context.WithTimeout(ctx, 3*time.Minute)
	defer healthCancel()
	if err := waitForHTTPOK(healthCtx, caddyURL+"/products"); err != nil {
		collectNamespaceDiagnostics(t, kubeconfigFileFromYAML(t, globalK3s.kubeConfigYAML), caddyBuilderNamespace)
		t.Fatalf("Caddy /products not reachable: %v", err)
	}
	t.Log("Caddy is ready")

	// ── 9. Verify normal HTTP reverse-proxy still works ───────────────────────

	t.Log("verifying Caddy reverse-proxy: GET /products")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Get(caddyURL + "/products") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /products through Caddy: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /products: unexpected status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Widget A") {
		t.Errorf("GET /products: expected Widget A in response, got: %s", body)
	}
	t.Logf("Caddy reverse-proxy response: %s", body)

	t.Log("verifying Caddy reverse-proxy: GET /products/1")
	resp2, err := (&http.Client{Timeout: 10 * time.Second}).Get(caddyURL + "/products/1") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /products/1 through Caddy: %v", err)
	}
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)
	if !strings.Contains(string(body2), "Widget A") {
		t.Errorf("GET /products/1: expected Widget A in response, got: %s", body2)
	}
	t.Logf("Caddy single-product response: %s", body2)

	// ── 10. Connect MCP client ────────────────────────────────────────────────

	t.Log("connecting MCP client to Caddy /mcp")
	mcpTransport := &sdkmcp.StreamableClientTransport{Endpoint: caddyURL + "/mcp"}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "caddy-builder-test", Version: "v0.0.1"}, nil)

	callCtx, callCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer callCancel()

	session, err := mcpClient.Connect(callCtx, mcpTransport, nil)
	if err != nil {
		t.Fatalf("connecting MCP client: %v", err)
	}
	defer session.Close()

	// ── 11. Verify MCP tool listing ────────────────────────────────────────────

	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	t.Logf("MCP tools exposed by Caddy: %v", toolNames(toolsResult.Tools))

	wantTools := []string{"products__list_products", "products__get_product"}
	toolSet := make(map[string]bool, len(toolsResult.Tools))
	for _, tool := range toolsResult.Tools {
		toolSet[tool.Name] = true
	}
	for _, want := range wantTools {
		if !toolSet[want] {
			t.Errorf("expected tool %q not exposed by Caddy; available: %v", want, toolNames(toolsResult.Tools))
		}
	}

	// ── 12. Call products__list_products ──────────────────────────────────────

	t.Log("calling products__list_products via MCP")
	listResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name:      "products__list_products",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("call products__list_products: %v", err)
	}
	if listResult.IsError {
		t.Fatalf("products__list_products error: %s", contentText(listResult.Content))
	}
	listText := contentText(listResult.Content)
	t.Logf("list_products MCP response: %s", listText)
	if !strings.Contains(listText, "Widget A") || !strings.Contains(listText, "Widget B") {
		t.Errorf("list_products MCP response missing expected products: %s", listText)
	}

	// ── 13. Call products__get_product ────────────────────────────────────────

	t.Log("calling products__get_product with id=1 via MCP")
	getResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name:      "products__get_product",
		Arguments: map[string]any{"id": 1},
	})
	if err != nil {
		t.Fatalf("call products__get_product: %v", err)
	}
	if getResult.IsError {
		t.Fatalf("products__get_product error: %s", contentText(getResult.Content))
	}
	getText := contentText(getResult.Content)
	t.Logf("get_product MCP response: %s", getText)
	if !strings.Contains(getText, "Widget A") {
		t.Errorf("get_product MCP response missing Widget A: %s", getText)
	}
	t.Log("Caddy builder E2E: both normal HTTP and MCP tools work through the custom binary")
}

// deployCaddyWiremock deploys a WireMock instance in the caddy-builder namespace,
// waits for it to be ready, and port-forwards to it. Returns the local port.
func deployCaddyWiremock(ctx context.Context, t *testing.T, c client.Client) (int, error) {
	t.Helper()

	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "wiremock", Namespace: caddyBuilderNamespace},
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
		ObjectMeta: metav1.ObjectMeta{Name: "wiremock", Namespace: caddyBuilderNamespace},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "wiremock"},
			Ports:    []corev1.ServicePort{{Port: wiremockPort, TargetPort: intstr.FromInt(wiremockPort), Protocol: corev1.ProtocolTCP}},
		},
	}
	if err := c.Create(ctx, svc); err != nil && !apierrors.IsAlreadyExists(err) {
		return 0, fmt.Errorf("creating WireMock Service: %w", err)
	}

	podName, err := waitForDeploymentPod(ctx, t, c, caddyBuilderNamespace, "wiremock", 3*time.Minute)
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
	stopFwd, err := portForwardToPod(ctx, t, restCfg, caddyBuilderNamespace, podName, localPort, wiremockAdminPort)
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

// deployCaddyPod creates the Caddy Deployment in the caddy-builder namespace.
// It mounts Caddyfile, mcp-anything config, and the products OpenAPI spec from
// ConfigMaps created before this call.
func deployCaddyPod(ctx context.Context, t *testing.T, c client.Client, image string) error {
	t.Helper()

	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "caddy", Namespace: caddyBuilderNamespace},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "caddy"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "caddy"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "caddy",
						Image: image,
						Ports: []corev1.ContainerPort{{ContainerPort: caddyBuilderPort, Protocol: corev1.ProtocolTCP}},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/products",
									Port: intstr.FromInt(caddyBuilderPort),
								},
							},
							InitialDelaySeconds: 5,
							PeriodSeconds:       5,
							FailureThreshold:    24,
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "caddyfile",
								MountPath: "/etc/caddy/Caddyfile",
								SubPath:   "Caddyfile",
							},
							{
								Name:      "mcp-config",
								MountPath: "/etc/mcp-anything/config.yaml",
								SubPath:   "config.yaml",
							},
							{
								Name:      "mcp-config",
								MountPath: "/etc/mcp-anything/products-spec.yaml",
								SubPath:   "products-spec.yaml",
							},
						},
					}},
					Volumes: []corev1.Volume{
						{
							Name: "caddyfile",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: "caddy-caddyfile"},
								},
							},
						},
						{
							Name: "mcp-config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: "caddy-mcp-config"},
								},
							},
						},
					},
				},
			},
		},
	}
	if err := c.Create(ctx, dep); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating Caddy Deployment: %w", err)
	}
	return nil
}

// kubeconfigFileFromYAML writes kubeconfig YAML bytes to a temp file and returns
// the path. The caller must clean it up; the file is registered with t.Cleanup.
func kubeconfigFileFromYAML(t *testing.T, kubeConfigYAML []byte) string {
	t.Helper()
	f, err := os.CreateTemp("", "kubeconfig-*.yaml")
	if err != nil {
		t.Fatalf("creating temp kubeconfig: %v", err)
	}
	if _, err := f.Write(kubeConfigYAML); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		t.Fatalf("writing kubeconfig: %v", err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })
	return f.Name()
}
