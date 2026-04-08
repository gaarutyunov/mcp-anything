//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
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

	"github.com/go-logr/logr"
	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/k3s"

	v1alpha1 "github.com/gaarutyunov/mcp-anything/pkg/crd/v1alpha1"
	"github.com/gaarutyunov/mcp-anything/pkg/operator/controller"
)

const (
	k3sImage            = "rancher/k3s:v1.31.4-k3s1"
	reconcileTimeout    = 60 * time.Second
	pollInterval        = 500 * time.Millisecond
	progressLogInterval = 5 * time.Second
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
	total := time.Now()

	// --- Arrange: prepare CRD manifests ---
	slog.Info("k3s: preparing CRD manifests")
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

	// --- Act: start k3s container ---
	containerStart := time.Now()
	slog.Info("k3s: starting container", "image", k3sImage)
	k3sCtr, err := k3s.Run(ctx, k3sImage,
		k3s.WithManifest(filepath.Join(crdsDir, "mcpproxy.yaml")),
		k3s.WithManifest(filepath.Join(crdsDir, "mcpupstream.yaml")),
	)
	if err != nil {
		return nil, fmt.Errorf("starting k3s container: %w", err)
	}
	slog.Info("k3s: container started", "elapsed", time.Since(containerStart).Round(time.Millisecond))

	// --- Act: obtain kubeconfig ---
	configStart := time.Now()
	slog.Info("k3s: fetching kubeconfig")
	kubeConfigYAML, err := k3sCtr.GetKubeConfig(ctx)
	if err != nil {
		_ = testcontainers.TerminateContainer(k3sCtr)
		return nil, fmt.Errorf("getting kubeconfig: %w", err)
	}
	slog.Info("k3s: kubeconfig obtained", "elapsed", time.Since(configStart).Round(time.Millisecond))

	// --- Act: build client and wait for CRDs ---
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

	crdStart := time.Now()
	slog.Info("k3s: waiting for CRDs to be established")
	crdCtx, crdCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer crdCancel()
	if err := waitForCRDs(crdCtx, c); err != nil {
		_ = testcontainers.TerminateContainer(k3sCtr)
		return nil, fmt.Errorf("waiting for CRDs: %w", err)
	}
	slog.Info("k3s: CRDs established",
		"crd_wait", time.Since(crdStart).Round(time.Millisecond),
		"total", time.Since(total).Round(time.Millisecond),
	)

	return &sharedK3sCluster{container: k3sCtr, kubeConfigYAML: kubeConfigYAML}, nil
}

// buildOperatorScheme returns a runtime.Scheme with all types the operator needs registered.
// Panics if any registration fails — this is a programming error, not a runtime error.
func buildOperatorScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	for _, addFunc := range []func(*runtime.Scheme) error{
		clientgoscheme.AddToScheme,
		corev1.AddToScheme,
		appsv1.AddToScheme,
		apiextensionsv1.AddToScheme,
		v1alpha1.AddToScheme,
	} {
		if err := addFunc(s); err != nil {
			panic(fmt.Sprintf("failed to register scheme: %v", err))
		}
	}
	return s
}

// waitForCRDs waits until the mcp-anything CRDs are established in the cluster,
// logging progress for each CRD.
func waitForCRDs(ctx context.Context, c client.Client) error {
	crdNames := []string{
		"mcpproxies.mcp-anything.ai",
		"mcpupstreams.mcp-anything.ai",
	}
	for _, name := range crdNames {
		start := time.Now()
		slog.Info("k3s: waiting for CRD", "crd", name)
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
			return fmt.Errorf("waiting for CRD %s (elapsed=%s): %w", name, time.Since(start).Round(time.Millisecond), err)
		}
		slog.Info("k3s: CRD established", "crd", name, "elapsed", time.Since(start).Round(time.Millisecond))
	}
	return nil
}

// startOperator starts the MCPProxy and MCPUpstream controllers in-process and
// returns a cancel function that stops the manager and waits for the goroutine to exit.
func startOperator(ctx context.Context, t *testing.T, kubeConfigYAML []byte, scheme *runtime.Scheme) context.CancelFunc {
	t.Helper()

	// Suppress controller-runtime's "log.SetLogger was never called" warning.
	ctrl.SetLogger(logr.Discard())

	start := time.Now()
	t.Log("operator: building REST config")
	restCfg, err := clientcmd.RESTConfigFromKubeConfig(kubeConfigYAML)
	if err != nil {
		t.Fatalf("building REST config: %v", err)
	}

	mgrCtx, mgrCancel := context.WithCancel(ctx)

	skipNameValidation := true
	t.Log("operator: creating manager")
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
	t.Logf("operator: manager created [%.2fs]", time.Since(start).Seconds())

	t.Log("operator: registering MCPProxy controller")
	if err := (&controller.MCPProxyReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		mgrCancel()
		t.Fatalf("setting up MCPProxy controller: %v", err)
	}

	t.Log("operator: registering MCPUpstream controller")
	if err := (&controller.MCPUpstreamReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		mgrCancel()
		t.Fatalf("setting up MCPUpstream controller: %v", err)
	}

	// done is closed when the manager goroutine exits; used by the cleanup func
	// to ensure the goroutine does not outlive the test and call t.Logf after it ends.
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := mgr.Start(mgrCtx); err != nil && mgrCtx.Err() == nil {
			// Log only if the exit was not caused by our cancellation.
			slog.Error("operator manager exited unexpectedly", "error", err)
		}
	}()

	t.Log("operator: waiting for cache sync")
	cacheStart := time.Now()
	if !mgr.GetCache().WaitForCacheSync(mgrCtx) {
		mgrCancel()
		<-done
		t.Fatal("operator: cache did not sync")
	}
	t.Logf("operator: ready — setup=%.2fs cache_sync=%.2fs total=%.2fs",
		cacheStart.Sub(start).Seconds(),
		time.Since(cacheStart).Seconds(),
		time.Since(start).Seconds(),
	)

	return func() {
		mgrCancel()
		<-done
	}
}

// TestOperatorCreatesMCPProxyResources is an E2E test that verifies the operator
// creates the expected Kubernetes resources (ConfigMap, Deployment, Service) and
// updates MCPProxy status when an MCPUpstream is associated with an MCPProxy.
func TestOperatorCreatesMCPProxyResources(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}
	if globalK3s == nil {
		t.Skip("shared k3s cluster unavailable")
	}

	// ── Arrange ──────────────────────────────────────────────────────────────

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	scheme := buildOperatorScheme()

	t.Log("arrange: building k8s client")
	restCfg, err := clientcmd.RESTConfigFromKubeConfig(globalK3s.kubeConfigYAML)
	if err != nil {
		t.Fatalf("building REST config: %v", err)
	}
	k8sClient, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("creating k8s client: %v", err)
	}

	t.Log("arrange: starting operator")
	stopOperator := startOperator(ctx, t, globalK3s.kubeConfigYAML, scheme)
	defer stopOperator()

	t.Log("arrange: creating namespace operator-e2e")
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "operator-e2e"}}
	if err := k8sClient.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("creating namespace: %v", err)
	}

	// ── Act ──────────────────────────────────────────────────────────────────

	t.Log("act: creating MCPUpstream test-upstream")
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

	t.Log("act: creating MCPProxy test-proxy")
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

	// ── Assert ────────────────────────────────────────────────────────────────

	proxyKey := types.NamespacedName{Name: "test-proxy", Namespace: "operator-e2e"}

	t.Run("ConfigMapCreated", func(t *testing.T) {
		cm := &corev1.ConfigMap{}
		if err := pollWithProgress(ctx, t, "ConfigMap test-proxy-config", reconcileTimeout, pollInterval, func() error {
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
		if !strings.Contains(data, "http://test-api.operator-e2e.svc.cluster.local:8080") {
			t.Errorf("generated config does not contain expected base URL; got:\n%s", data)
		}
	})

	t.Run("DeploymentCreated", func(t *testing.T) {
		dep := &appsv1.Deployment{}
		if err := pollWithProgress(ctx, t, "Deployment test-proxy", reconcileTimeout, pollInterval, func() error {
			return k8sClient.Get(ctx, proxyKey, dep)
		}); err != nil {
			t.Fatalf("Deployment not created: %v", err)
		}
		if dep.Spec.Template.Spec.Containers[0].Image != "ghcr.io/gaarutyunov/mcp-anything:latest" {
			t.Errorf("unexpected container image: %s", dep.Spec.Template.Spec.Containers[0].Image)
		}
	})

	t.Run("ServiceCreated", func(t *testing.T) {
		svc := &corev1.Service{}
		if err := pollWithProgress(ctx, t, "Service test-proxy", reconcileTimeout, pollInterval, func() error {
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

	t.Run("MCPProxyStatusUpdated", func(t *testing.T) {
		if err := pollWithProgress(ctx, t, "MCPProxy status upstreamCount=1", reconcileTimeout, pollInterval, func() error {
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
// MCPProxy correctly includes matching upstreams and excludes non-matching ones.
func TestOperatorLabelSelectorFiltersUpstreams(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}
	if globalK3s == nil {
		t.Skip("shared k3s cluster unavailable")
	}

	// ── Arrange ──────────────────────────────────────────────────────────────

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	scheme := buildOperatorScheme()

	t.Log("arrange: building k8s client")
	restCfg, err := clientcmd.RESTConfigFromKubeConfig(globalK3s.kubeConfigYAML)
	if err != nil {
		t.Fatalf("building REST config: %v", err)
	}
	k8sClient, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("creating k8s client: %v", err)
	}

	t.Log("arrange: starting operator")
	stopOperator := startOperator(ctx, t, globalK3s.kubeConfigYAML, scheme)
	defer stopOperator()

	t.Log("arrange: creating namespace selector-e2e")
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "selector-e2e"}}
	if err := k8sClient.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("creating namespace: %v", err)
	}

	// upstream-a matches proxy-a; upstream-b matches proxy-b (not selected by proxy-a).
	t.Log("arrange: creating MCPUpstream upstream-a (label=proxy-a)")
	upstreamA := newTestUpstream("upstream-a", "selector-e2e", "proxy-a", "http://api-a:8080")
	t.Log("arrange: creating MCPUpstream upstream-b (label=proxy-b)")
	upstreamB := newTestUpstream("upstream-b", "selector-e2e", "proxy-b", "http://api-b:8080")

	for _, u := range []*v1alpha1.MCPUpstream{upstreamA, upstreamB} {
		if err := k8sClient.Create(ctx, u); err != nil {
			t.Fatalf("creating upstream %s: %v", u.Name, err)
		}
	}

	// ── Act ──────────────────────────────────────────────────────────────────

	t.Log("act: creating MCPProxy proxy-a with selector matching proxy-a label")
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

	// ── Assert ────────────────────────────────────────────────────────────────

	t.Log("assert: waiting for MCPProxy to report upstreamCount=1")
	if err := pollWithProgress(ctx, t, "MCPProxy proxy-a upstreamCount=1", reconcileTimeout, pollInterval, func() error {
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

	t.Log("assert: verifying config contains upstream-a URL and excludes upstream-b URL")
	cm := &corev1.ConfigMap{}
	if err := k8sClient.Get(ctx, types.NamespacedName{
		Name:      "proxy-a-config",
		Namespace: "selector-e2e",
	}, cm); err != nil {
		t.Fatalf("fetching config ConfigMap: %v", err)
	}

	cfgData := cm.Data["config.yaml"]
	if strings.Contains(cfgData, "http://api-b:8080") {
		t.Error("config must not contain upstream-b URL (selector should exclude it)")
	}
	if !strings.Contains(cfgData, "http://api-a:8080") {
		t.Errorf("config must contain upstream-a URL; got:\n%s", cfgData)
	}
}

// TestOperatorCRDValidation verifies that the CRD OpenAPI schema rejects invalid specs
// at the Kubernetes API layer, before any controller logic runs.
func TestOperatorCRDValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}
	if globalK3s == nil {
		t.Skip("shared k3s cluster unavailable")
	}

	// ── Arrange ──────────────────────────────────────────────────────────────

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	scheme := buildOperatorScheme()

	t.Log("arrange: building k8s client")
	restCfg, err := clientcmd.RESTConfigFromKubeConfig(globalK3s.kubeConfigYAML)
	if err != nil {
		t.Fatalf("building REST config: %v", err)
	}
	k8sClient, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("creating k8s client: %v", err)
	}

	t.Log("arrange: creating namespace validation-e2e")
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "validation-e2e"}}
	if err := k8sClient.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("creating namespace: %v", err)
	}

	// ── Act ──────────────────────────────────────────────────────────────────

	t.Log("act: attempting to create MCPProxy with replicas=0 (invalid per CRD schema)")
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

	// ── Assert ────────────────────────────────────────────────────────────────

	createErr := k8sClient.Create(ctx, invalidProxy)
	if createErr == nil {
		t.Error("expected validation error for MCPProxy with replicas=0, got nil")
	} else {
		t.Logf("assert: correctly rejected — %v", createErr)
	}
}

// --- helpers ---

// TestAnnotationBasedServiceDiscovery verifies that the operator discovers Services annotated
// with mcp-anything.ai/enabled=true and merges them with CRD-defined MCPUpstream resources
// into a single proxy config.
func TestAnnotationBasedServiceDiscovery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}
	if globalK3s == nil {
		t.Skip("shared k3s cluster unavailable")
	}

	// ── Arrange ──────────────────────────────────────────────────────────────

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	scheme := buildOperatorScheme()

	t.Log("arrange: building k8s client")
	restCfg, err := clientcmd.RESTConfigFromKubeConfig(globalK3s.kubeConfigYAML)
	if err != nil {
		t.Fatalf("building REST config: %v", err)
	}
	k8sClient, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("creating k8s client: %v", err)
	}

	t.Log("arrange: starting operator")
	stopOperator := startOperator(ctx, t, globalK3s.kubeConfigYAML, scheme)
	defer stopOperator()

	t.Log("arrange: creating namespace annotation-e2e")
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "annotation-e2e"}}
	if err := k8sClient.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("creating namespace: %v", err)
	}

	// ── Act ──────────────────────────────────────────────────────────────────

	t.Log("act: creating MCPProxy with serviceDiscovery.enabled=true")
	proxy := &v1alpha1.MCPProxy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "annot-proxy",
			Namespace: "annotation-e2e",
		},
		Spec: v1alpha1.MCPProxySpec{
			ServiceDiscovery: &v1alpha1.ServiceDiscoverySpec{
				Enabled: true,
				NamespaceSelector: &v1alpha1.ServiceDiscoveryNamespaceSelector{
					MatchNames: []string{"annotation-e2e"},
				},
			},
			Server: v1alpha1.ProxyServerSpec{Port: 8080},
			Naming: v1alpha1.ProxyNamingSpec{Separator: "__"},
		},
	}
	if err := k8sClient.Create(ctx, proxy); err != nil {
		t.Fatalf("creating MCPProxy: %v", err)
	}

	t.Log("act: creating annotated Service petstore-api")
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "petstore-api",
			Namespace: "annotation-e2e",
			Annotations: map[string]string{
				"mcp-anything.ai/enabled":      "true",
				"mcp-anything.ai/tool-prefix":  "pets",
				"mcp-anything.ai/openapi-url":  "http://petstore.example.com/openapi.json",
				"mcp-anything.ai/proxy":        "annot-proxy",
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Port: 8080, Protocol: corev1.ProtocolTCP},
			},
			Selector: map[string]string{"app": "petstore-api"},
		},
	}
	if err := k8sClient.Create(ctx, svc); err != nil {
		t.Fatalf("creating Service: %v", err)
	}

	// ── Assert ────────────────────────────────────────────────────────────────

	proxyKey := types.NamespacedName{Name: "annot-proxy", Namespace: "annotation-e2e"}

	t.Run("ConfigMapContainsServiceURL", func(t *testing.T) {
		cm := &corev1.ConfigMap{}
		if err := pollWithProgress(ctx, t, "ConfigMap annot-proxy-config", reconcileTimeout, pollInterval, func() error {
			return k8sClient.Get(ctx, types.NamespacedName{
				Name:      "annot-proxy-config",
				Namespace: "annotation-e2e",
			}, cm)
		}); err != nil {
			t.Fatalf("config ConfigMap not created: %v", err)
		}

		cfgData := cm.Data["config.yaml"]
		if !strings.Contains(cfgData, "petstore-api.annotation-e2e.svc.cluster.local:8080") {
			t.Errorf("config must contain the service base URL; got:\n%s", cfgData)
		}
		if !strings.Contains(cfgData, "http://petstore.example.com/openapi.json") {
			t.Errorf("config must contain the openapi-url annotation value; got:\n%s", cfgData)
		}
		if !strings.Contains(cfgData, "pets") {
			t.Errorf("config must contain the tool prefix 'pets'; got:\n%s", cfgData)
		}
	})

	t.Run("StatusReportsAnnotatedServiceCount", func(t *testing.T) {
		if err := pollWithProgress(ctx, t, "MCPProxy status annotatedServiceCount=1", reconcileTimeout, pollInterval, func() error {
			p := &v1alpha1.MCPProxy{}
			if err := k8sClient.Get(ctx, proxyKey, p); err != nil {
				return err
			}
			if p.Status.AnnotatedServiceCount != 1 {
				return fmt.Errorf("expected annotatedServiceCount=1, got %d", p.Status.AnnotatedServiceCount)
			}
			return nil
		}); err != nil {
			t.Fatalf("MCPProxy status not updated: %v", err)
		}
	})

	t.Run("PrefixConflictDetected", func(t *testing.T) {
		// Create a CRD MCPUpstream with the same tool prefix as the annotated Service.
		conflictUpstream := &v1alpha1.MCPUpstream{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "conflict-upstream",
				Namespace: "annotation-e2e",
				Labels:    map[string]string{"mcp-anything.ai/proxy": "annot-proxy"},
			},
			Spec: v1alpha1.MCPUpstreamSpec{
				ToolPrefix: "pets", // same as the annotated Service
				BaseURL:    "http://conflict-api.annotation-e2e.svc.cluster.local:9090",
				OpenAPI: v1alpha1.MCPUpstreamOpenAPISpec{
					URL: "http://conflict-api.example.com/openapi.json",
				},
			},
		}

		// MCPProxy needs upstreamSelector to pick up this CRD upstream.
		p := &v1alpha1.MCPProxy{}
		if err := k8sClient.Get(ctx, proxyKey, p); err != nil {
			t.Fatalf("fetching proxy: %v", err)
		}
		p.Spec.UpstreamSelector = metav1.LabelSelector{
			MatchLabels: map[string]string{"mcp-anything.ai/proxy": "annot-proxy"},
		}
		if err := k8sClient.Update(ctx, p); err != nil {
			t.Fatalf("updating proxy upstream selector: %v", err)
		}

		if err := k8sClient.Create(ctx, conflictUpstream); err != nil {
			t.Fatalf("creating conflicting MCPUpstream: %v", err)
		}

		if err := pollWithProgress(ctx, t, "PrefixConflict condition", reconcileTimeout, pollInterval, func() error {
			p := &v1alpha1.MCPProxy{}
			if err := k8sClient.Get(ctx, proxyKey, p); err != nil {
				return err
			}
			cond := apimeta.FindStatusCondition(p.Status.Conditions, "PrefixConflict")
			if cond == nil {
				return fmt.Errorf("PrefixConflict condition not set")
			}
			if cond.Status != metav1.ConditionTrue {
				return fmt.Errorf("expected PrefixConflict condition=True, got %s (msg: %s)", cond.Status, cond.Message)
			}
			return nil
		}); err != nil {
			t.Fatalf("prefix conflict not detected: %v", err)
		}
	})
}

// TestAnnotationBasedCrossNamespaceDiscovery verifies that the operator can discover annotated
// Services in a different namespace than the MCPProxy.
func TestAnnotationBasedCrossNamespaceDiscovery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}
	if globalK3s == nil {
		t.Skip("shared k3s cluster unavailable")
	}

	// ── Arrange ──────────────────────────────────────────────────────────────

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	scheme := buildOperatorScheme()

	t.Log("arrange: building k8s client")
	restCfg, err := clientcmd.RESTConfigFromKubeConfig(globalK3s.kubeConfigYAML)
	if err != nil {
		t.Fatalf("building REST config: %v", err)
	}
	k8sClient, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("creating k8s client: %v", err)
	}

	t.Log("arrange: starting operator")
	stopOperator := startOperator(ctx, t, globalK3s.kubeConfigYAML, scheme)
	defer stopOperator()

	for _, nsName := range []string{"cross-proxy-ns", "cross-svc-ns"} {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nsName}}
		if err := k8sClient.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatalf("creating namespace %s: %v", nsName, err)
		}
	}

	// ── Act ──────────────────────────────────────────────────────────────────

	t.Log("act: creating MCPProxy in cross-proxy-ns watching cross-svc-ns")
	proxy := &v1alpha1.MCPProxy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cross-proxy",
			Namespace: "cross-proxy-ns",
		},
		Spec: v1alpha1.MCPProxySpec{
			ServiceDiscovery: &v1alpha1.ServiceDiscoverySpec{
				Enabled: true,
				NamespaceSelector: &v1alpha1.ServiceDiscoveryNamespaceSelector{
					MatchNames: []string{"cross-svc-ns"},
				},
			},
			Server: v1alpha1.ProxyServerSpec{Port: 8080},
			Naming: v1alpha1.ProxyNamingSpec{Separator: "__"},
		},
	}
	if err := k8sClient.Create(ctx, proxy); err != nil {
		t.Fatalf("creating MCPProxy: %v", err)
	}

	t.Log("act: creating annotated Service in cross-svc-ns")
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "remote-api",
			Namespace: "cross-svc-ns",
			Annotations: map[string]string{
				"mcp-anything.ai/enabled":     "true",
				"mcp-anything.ai/tool-prefix": "remote",
				"mcp-anything.ai/openapi-url": "http://remote-api.example.com/openapi.json",
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Port: 9090, Protocol: corev1.ProtocolTCP},
			},
			Selector: map[string]string{"app": "remote-api"},
		},
	}
	if err := k8sClient.Create(ctx, svc); err != nil {
		t.Fatalf("creating Service: %v", err)
	}

	// ── Assert ────────────────────────────────────────────────────────────────

	t.Log("assert: MCPProxy discovers service in cross-svc-ns")
	if err := pollWithProgress(ctx, t, "MCPProxy status annotatedServiceCount=1", reconcileTimeout, pollInterval, func() error {
		p := &v1alpha1.MCPProxy{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: "cross-proxy", Namespace: "cross-proxy-ns"}, p); err != nil {
			return err
		}
		if p.Status.AnnotatedServiceCount != 1 {
			return fmt.Errorf("expected annotatedServiceCount=1, got %d", p.Status.AnnotatedServiceCount)
		}
		return nil
	}); err != nil {
		t.Fatalf("cross-namespace discovery failed: %v", err)
	}

	t.Log("assert: config contains remote service URL")
	cm := &corev1.ConfigMap{}
	if err := k8sClient.Get(ctx, types.NamespacedName{
		Name:      "cross-proxy-config",
		Namespace: "cross-proxy-ns",
	}, cm); err != nil {
		t.Fatalf("fetching config ConfigMap: %v", err)
	}
	if !strings.Contains(cm.Data["config.yaml"], "remote-api.cross-svc-ns.svc.cluster.local:9090") {
		t.Errorf("config must contain cross-namespace service URL; got:\n%s", cm.Data["config.yaml"])
	}
}

// newTestUpstream creates an MCPUpstream fixture with an autoDiscover OpenAPI source.
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

// pollWithProgress retries fn until it returns nil, the timeout expires, or ctx is cancelled.
// It emits a progress log every progressLogInterval so slow operations are visible and
// distinguishable from hangs in test output.
func pollWithProgress(ctx context.Context, t *testing.T, label string, timeout, interval time.Duration, fn func() error) error {
	t.Helper()
	start := time.Now()
	deadline := time.Now().Add(timeout)
	nextLog := time.Now().Add(progressLogInterval)
	var lastErr error

	t.Logf("[0.00s] %s: polling (timeout=%s)", label, timeout)

	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%s: context cancelled after %.2fs: %w", label, time.Since(start).Seconds(), err)
		}
		lastErr = fn()
		if lastErr == nil {
			t.Logf("[%.2fs] %s: done", time.Since(start).Seconds(), label)
			return nil
		}
		if time.Now().After(nextLog) {
			t.Logf("[%.2fs] %s: still waiting — %v", time.Since(start).Seconds(), label, lastErr)
			nextLog = time.Now().Add(progressLogInterval)
		}
		time.Sleep(interval)
	}
	return fmt.Errorf("%s: timed out after %.2fs: %w", label, time.Since(start).Seconds(), lastErr)
}
