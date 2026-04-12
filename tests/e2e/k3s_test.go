//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	k3sImage     = "rancher/k3s:v1.31.4-k3s1"
	pollInterval = 500 * time.Millisecond
)

// sharedK3sCluster holds the single k3s instance shared across all E2E tests.
type sharedK3sCluster struct {
	container      *k3s.K3sContainer
	kubeConfigYAML []byte
}

// globalK3s is set by TestMain and shared across all E2E tests.
// It is nil if k3s failed to start, in which case tests are skipped.
var globalK3s *sharedK3sCluster

// startSharedK3s starts a single k3s cluster with the mcp-anything CRDs pre-loaded.
// It is called once from TestMain and its result stored in globalK3s.
func startSharedK3s(ctx context.Context) (*sharedK3sCluster, error) {
	total := time.Now()

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

	configStart := time.Now()
	slog.Info("k3s: fetching kubeconfig")
	kubeConfigYAML, err := k3sCtr.GetKubeConfig(ctx)
	if err != nil {
		_ = testcontainers.TerminateContainer(k3sCtr)
		return nil, fmt.Errorf("getting kubeconfig: %w", err)
	}
	slog.Info("k3s: kubeconfig obtained", "elapsed", time.Since(configStart).Round(time.Millisecond))

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

// waitForCRDs waits until the mcp-anything CRDs are established in the cluster.
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
			BindAddress: "0",
		},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
		Controller: ctrlconfig.Controller{
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

	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := mgr.Start(mgrCtx); err != nil && mgrCtx.Err() == nil {
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

