//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/yaml"

	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/k3s"

	v1alpha1 "github.com/gaarutyunov/mcp-anything/pkg/crd/v1alpha1"
	"github.com/gaarutyunov/mcp-anything/pkg/operator/controller"
)

const (
	k3sImage             = "rancher/k3s:v1.31.4-k3s1"
	operatorStartTimeout = 30 * time.Second
	reconcileTimeout     = 60 * time.Second
	pollInterval         = 500 * time.Millisecond
)

// sharedK3sCluster holds the single k3s instance shared across all operator tests.
type sharedK3sCluster struct {
	container      *k3s.K3sContainer
	kubeConfigYAML []byte
}

// globalK3s is set by TestMain and shared across all operator integration tests.
// It is nil if k3s failed to start, in which case operator tests are skipped.
var globalK3s *sharedK3sCluster

// startSharedK3s starts a single k3s cluster with the mcp-anything CRDs pre-loaded.
// It is called once from TestMain and its result stored in globalK3s.
func startSharedK3s(ctx context.Context) (*sharedK3sCluster, error) {
	crdsDir, err := os.MkdirTemp("", "mcp-crds-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(crdsDir)

	repoRoot := "../.."
	for _, pair := range []struct{ src, dst string }{
		{"deploy/helm/mcp-anything/crds/mcpproxy.yaml", "mcpproxy.yaml"},
		{"deploy/helm/mcp-anything/crds/mcpupstream.yaml", "mcpupstream.yaml"},
	} {
		data, err := os.ReadFile(filepath.Join(repoRoot, pair.src))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", pair.src, err)
		}
		if err := os.WriteFile(filepath.Join(crdsDir, pair.dst), data, 0o600); err != nil {
			return nil, fmt.Errorf("writing %s: %w", pair.dst, err)
		}
	}

	k3sCtr, err := k3s.Run(ctx, k3sImage,
		k3s.WithManifest(filepath.Join(crdsDir, "mcpproxy.yaml")),
		k3s.WithManifest(filepath.Join(crdsDir, "mcpupstream.yaml")),
	)
	if err != nil {
		return nil, fmt.Errorf("starting k3s container: %w", err)
	}

	kubeConfigYAML, err := k3sCtr.GetKubeConfig(ctx)
	if err != nil {
		_ = testcontainers.TerminateContainer(k3sCtr)
		return nil, fmt.Errorf("getting kubeconfig: %w", err)
	}

	// Build a client and wait for CRDs to be established before returning.
	scheme := buildOperatorScheme()
	restCfg, err := clientcmd.RESTConfigFromKubeConfig(kubeConfigYAML)
	if err != nil {
		_ = testcontainers.TerminateContainer(k3sCtr)
		return nil, fmt.Errorf("building REST config: %w", err)
	}
	c, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		_ = testcontainers.TerminateContainer(k3sCtr)
		return nil, fmt.Errorf("creating k8s client: %w", err)
	}

	crdCtx, crdCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer crdCancel()
	if err := waitForCRDs(crdCtx, c); err != nil {
		_ = testcontainers.TerminateContainer(k3sCtr)
		return nil, fmt.Errorf("waiting for CRDs: %w", err)
	}

	return &sharedK3sCluster{container: k3sCtr, kubeConfigYAML: kubeConfigYAML}, nil
}

// buildOperatorScheme returns a runtime.Scheme with all types the operator needs registered.
func buildOperatorScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	_ = apiextensionsv1.AddToScheme(s)
	_ = v1alpha1.AddToScheme(s)
	return s
}

// waitForCRDs waits until the mcp-anything CRDs are established in the cluster.
func waitForCRDs(ctx context.Context, c client.Client) error {
	crdNames := []string{
		"mcpproxies.mcp-anything.ai",
		"mcpupstreams.mcp-anything.ai",
	}
	for _, name := range crdNames {
		if err := wait.PollUntilContextTimeout(ctx, pollInterval, 60*time.Second, true, func(ctx context.Context) (bool, error) {
			crd := &apiextensionsv1.CustomResourceDefinition{}
			if err := c.Get(ctx, types.NamespacedName{Name: name}, crd); err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
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

// startOperator starts the MCPProxy and MCPUpstream controllers in-process and
// returns a cancel function that stops the manager.
func startOperator(ctx context.Context, t *testing.T, kubeConfigYAML []byte, scheme *runtime.Scheme) context.CancelFunc {
	t.Helper()

	restCfg, err := clientcmd.RESTConfigFromKubeConfig(kubeConfigYAML)
	if err != nil {
		t.Fatalf("building REST config: %v", err)
	}

	mgrCtx, mgrCancel := context.WithCancel(ctx)

	skipNameValidation := true
	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		Scheme: scheme,
		Metrics: server.Options{
			BindAddress: "0", // disable metrics server in tests
		},
		HealthProbeBindAddress: "0", // disable health probe in tests
		LeaderElection:         false,
		Controller: ctrlconfig.Controller{
			// Allow multiple managers per process (one per test) without name collision.
			SkipNameValidation: &skipNameValidation,
		},
	})
	if err != nil {
		mgrCancel()
		t.Fatalf("creating manager: %v", err)
	}

	if err := (&controller.MCPProxyReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		mgrCancel()
		t.Fatalf("setting up MCPProxy controller: %v", err)
	}

	if err := (&controller.MCPUpstreamReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		mgrCancel()
		t.Fatalf("setting up MCPUpstream controller: %v", err)
	}

	go func() {
		if err := mgr.Start(mgrCtx); err != nil {
			t.Logf("manager exited: %v", err)
		}
	}()

	// Wait until the manager's cache is synced.
	if !mgr.GetCache().WaitForCacheSync(mgrCtx) {
		mgrCancel()
		t.Fatal("cache did not sync")
	}

	return mgrCancel
}

// TestOperatorCreatesMCPProxyResources is an E2E test that:
//  1. Reuses the shared k3s cluster (started by TestMain).
//  2. Runs the operator controllers in-process.
//  3. Creates an MCPUpstream with an autoDiscover OpenAPI source and an
//     MCPProxy that selects it.
//  4. Asserts that the operator creates the expected ConfigMap, Deployment,
//     and Service.
func TestOperatorCreatesMCPProxyResources(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}
	if globalK3s == nil {
		t.Skip("shared k3s cluster unavailable")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	scheme := buildOperatorScheme()

	restCfg, err := clientcmd.RESTConfigFromKubeConfig(globalK3s.kubeConfigYAML)
	if err != nil {
		t.Fatalf("building REST config: %v", err)
	}
	k8sClient, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("creating k8s client: %v", err)
	}

	// Start operator controllers.
	stopOperator := startOperator(ctx, t, globalK3s.kubeConfigYAML, scheme)
	defer stopOperator()

	// Create the test namespace.
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "operator-e2e"}}
	if err := k8sClient.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("creating namespace: %v", err)
	}

	// Create an MCPUpstream with autoDiscover pointing to a hypothetical service.
	upstream := &v1alpha1.MCPUpstream{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-upstream",
			Namespace: "operator-e2e",
			Labels: map[string]string{
				"mcp-anything.ai/proxy": "test-proxy",
			},
		},
		Spec: v1alpha1.MCPUpstreamSpec{
			ToolPrefix: "test",
			BaseURL:    "http://test-api.operator-e2e.svc.cluster.local:8080",
			OpenAPI: v1alpha1.MCPUpstreamOpenAPISpec{
				AutoDiscover: &v1alpha1.AutoDiscoverSpec{
					Path: "/openapi.json",
				},
			},
		},
	}
	if err := k8sClient.Create(ctx, upstream); err != nil {
		t.Fatalf("creating MCPUpstream: %v", err)
	}

	// Create an MCPProxy that selects the upstream.
	proxy := &v1alpha1.MCPProxy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-proxy",
			Namespace: "operator-e2e",
		},
		Spec: v1alpha1.MCPProxySpec{
			UpstreamSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"mcp-anything.ai/proxy": "test-proxy",
				},
			},
			Server: v1alpha1.ProxyServerSpec{
				Port: 8080,
			},
			Naming: v1alpha1.ProxyNamingSpec{
				Separator: "__",
			},
		},
	}
	if err := k8sClient.Create(ctx, proxy); err != nil {
		t.Fatalf("creating MCPProxy: %v", err)
	}

	proxyKey := types.NamespacedName{Name: "test-proxy", Namespace: "operator-e2e"}

	// Assert: the operator must create a ConfigMap named test-proxy-config.
	t.Run("ConfigMapCreated", func(t *testing.T) {
		cm := &corev1.ConfigMap{}
		if err := pollUntil(ctx, reconcileTimeout, pollInterval, func() error {
			return k8sClient.Get(ctx, types.NamespacedName{
				Name:      "test-proxy-config",
				Namespace: "operator-e2e",
			}, cm)
		}); err != nil {
			t.Fatalf("config ConfigMap not created: %v", err)
		}
		data, ok := cm.Data["config.yaml"]
		if !ok {
			t.Fatal("config.yaml key missing from ConfigMap")
		}
		if len(data) == 0 {
			t.Fatal("config.yaml is empty")
		}
		// Verify the generated YAML contains the upstream base URL.
		if !containsString(data, "http://test-api.operator-e2e.svc.cluster.local:8080") {
			t.Errorf("generated config does not contain expected base URL; got:\n%s", data)
		}
	})

	// Assert: the operator must create a Deployment named test-proxy.
	t.Run("DeploymentCreated", func(t *testing.T) {
		dep := &appsv1.Deployment{}
		if err := pollUntil(ctx, reconcileTimeout, pollInterval, func() error {
			return k8sClient.Get(ctx, proxyKey, dep)
		}); err != nil {
			t.Fatalf("Deployment not created: %v", err)
		}
		if dep.Spec.Template.Spec.Containers[0].Image != "ghcr.io/gaarutyunov/mcp-anything:latest" {
			t.Errorf("unexpected container image: %s", dep.Spec.Template.Spec.Containers[0].Image)
		}
	})

	// Assert: the operator must create a Service named test-proxy.
	t.Run("ServiceCreated", func(t *testing.T) {
		svc := &corev1.Service{}
		if err := pollUntil(ctx, reconcileTimeout, pollInterval, func() error {
			return k8sClient.Get(ctx, proxyKey, svc)
		}); err != nil {
			t.Fatalf("Service not created: %v", err)
		}
		if len(svc.Spec.Ports) == 0 {
			t.Fatal("Service has no ports")
		}
		if svc.Spec.Ports[0].Port != 8080 {
			t.Errorf("unexpected service port: %d", svc.Spec.Ports[0].Port)
		}
	})

	// Assert: MCPProxy status is updated with upstreamCount.
	t.Run("MCPProxyStatusUpdated", func(t *testing.T) {
		if err := pollUntil(ctx, reconcileTimeout, pollInterval, func() error {
			p := &v1alpha1.MCPProxy{}
			if err := k8sClient.Get(ctx, proxyKey, p); err != nil {
				return err
			}
			if p.Status.UpstreamCount != 1 {
				return fmt.Errorf("expected upstreamCount=1, got %d", p.Status.UpstreamCount)
			}
			return nil
		}); err != nil {
			t.Fatalf("MCPProxy status not updated: %v", err)
		}
	})
}

// TestOperatorLabelSelectorFiltersUpstreams verifies that the label selector on
// MCPProxy correctly filters MCPUpstream resources.
func TestOperatorLabelSelectorFiltersUpstreams(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}
	if globalK3s == nil {
		t.Skip("shared k3s cluster unavailable")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	scheme := buildOperatorScheme()

	restCfg, err := clientcmd.RESTConfigFromKubeConfig(globalK3s.kubeConfigYAML)
	if err != nil {
		t.Fatalf("building REST config: %v", err)
	}
	k8sClient, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("creating k8s client: %v", err)
	}

	stopOperator := startOperator(ctx, t, globalK3s.kubeConfigYAML, scheme)
	defer stopOperator()

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "selector-e2e"}}
	if err := k8sClient.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("creating namespace: %v", err)
	}

	// upstream-a: matches proxy-a selector
	upstreamA := newTestUpstream("upstream-a", "selector-e2e", "proxy-a", "http://api-a:8080")
	// upstream-b: matches proxy-b selector (should NOT be selected by proxy-a)
	upstreamB := newTestUpstream("upstream-b", "selector-e2e", "proxy-b", "http://api-b:8080")

	for _, u := range []*v1alpha1.MCPUpstream{upstreamA, upstreamB} {
		if err := k8sClient.Create(ctx, u); err != nil {
			t.Fatalf("creating upstream %s: %v", u.Name, err)
		}
	}

	proxy := &v1alpha1.MCPProxy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "proxy-a",
			Namespace: "selector-e2e",
		},
		Spec: v1alpha1.MCPProxySpec{
			UpstreamSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"mcp-anything.ai/proxy": "proxy-a"},
			},
			Server: v1alpha1.ProxyServerSpec{Port: 8080},
			Naming: v1alpha1.ProxyNamingSpec{Separator: "__"},
		},
	}
	if err := k8sClient.Create(ctx, proxy); err != nil {
		t.Fatalf("creating MCPProxy: %v", err)
	}

	// Wait for the proxy config to be generated with exactly 1 upstream.
	if err := pollUntil(ctx, reconcileTimeout, pollInterval, func() error {
		p := &v1alpha1.MCPProxy{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: "proxy-a", Namespace: "selector-e2e"}, p); err != nil {
			return err
		}
		if p.Status.UpstreamCount != 1 {
			return fmt.Errorf("expected upstreamCount=1, got %d", p.Status.UpstreamCount)
		}
		return nil
	}); err != nil {
		t.Fatalf("upstream selector filtering failed: %v", err)
	}

	// Verify that config does NOT contain upstream-b's URL.
	cm := &corev1.ConfigMap{}
	if err := k8sClient.Get(ctx, types.NamespacedName{
		Name:      "proxy-a-config",
		Namespace: "selector-e2e",
	}, cm); err != nil {
		t.Fatalf("fetching config ConfigMap: %v", err)
	}

	cfgData := cm.Data["config.yaml"]
	if containsString(cfgData, "http://api-b:8080") {
		t.Error("config should not contain upstream-b URL but it does")
	}
	if !containsString(cfgData, "http://api-a:8080") {
		t.Error("config should contain upstream-a URL but it does not")
	}
}

// TestOperatorCRDValidation verifies that the CRD OpenAPI schema rejects invalid specs.
func TestOperatorCRDValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}
	if globalK3s == nil {
		t.Skip("shared k3s cluster unavailable")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	scheme := buildOperatorScheme()

	restCfg, err := clientcmd.RESTConfigFromKubeConfig(globalK3s.kubeConfigYAML)
	if err != nil {
		t.Fatalf("building REST config: %v", err)
	}
	k8sClient, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("creating k8s client: %v", err)
	}

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "validation-e2e"}}
	if err := k8sClient.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("creating namespace: %v", err)
	}

	// MCPProxy with replicas=0 (minimum is 1 in the CRD schema).
	zero := int32(0)
	invalidProxy := &v1alpha1.MCPProxy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "invalid-proxy",
			Namespace: "validation-e2e",
		},
		Spec: v1alpha1.MCPProxySpec{
			Replicas: &zero,
		},
	}
	err = k8sClient.Create(ctx, invalidProxy)
	if err == nil {
		t.Error("expected error for MCPProxy with replicas=0, got nil")
	}
}

// --- helpers ---

func newTestUpstream(name, namespace, proxyLabel, baseURL string) *v1alpha1.MCPUpstream {
	return &v1alpha1.MCPUpstream{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"mcp-anything.ai/proxy": proxyLabel},
		},
		Spec: v1alpha1.MCPUpstreamSpec{
			ToolPrefix: name,
			BaseURL:    baseURL,
			OpenAPI: v1alpha1.MCPUpstreamOpenAPISpec{
				AutoDiscover: &v1alpha1.AutoDiscoverSpec{Path: "/openapi.json"},
			},
		},
	}
}

// pollUntil retries fn until it returns nil or the deadline is exceeded.
func pollUntil(ctx context.Context, timeout, interval time.Duration, fn func() error) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		time.Sleep(interval)
	}
	return fmt.Errorf("timed out after %s: %w", timeout, lastErr)
}

func containsString(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (func() bool {
		for i := 0; i <= len(s)-len(substr); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})()
}

// Ensure yaml import is used (for kubeconfig parsing in helpers).
var _ = yaml.Marshal
