//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	tcwait "github.com/testcontainers/testcontainers-go/wait"
)

const (
	helmInstallTimeout = 5 * time.Minute
	helmReleaseNS      = "mcp-anything-system"
	helmReleaseName    = "mcp-anything"
)

// TestHelmChartInstall verifies that the mcp-anything Helm chart can be installed
// from an OCI registry into a k3s cluster.
//
// If HELM_CHART_IMAGE is set, the chart is pulled from that OCI reference directly.
// This is used in CI after a sha-based chart has been pushed to GHCR:
//
//	HELM_CHART_IMAGE=oci://ghcr.io/gaarutyunov/mcp-anything
//	HELM_CHART_VERSION=sha-<sha>
//
// Otherwise, the chart is packaged from charts/mcp-anything and pushed to a
// temporary local registry started with testcontainers, then installed from there.
//
// In both cases, --wait=false is used so the test does not require the operator
// image to be pre-loaded into k3s. The test verifies that the CRDs and the
// operator Deployment resource are created by the chart.
func TestHelmChartInstall(t *testing.T) {
	helmPath, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm binary not found; install helm to run this test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), helmInstallTimeout)
	defer cancel()

	chartRef, chartVersion, registryCtr := resolveChartRef(ctx, t, helmPath)
	if registryCtr != nil {
		t.Cleanup(func() {
			if err := testcontainers.TerminateContainer(registryCtr); err != nil {
				t.Logf("terminate local registry: %v", err)
			}
		})
	}

	// Start a fresh k3s cluster for this test (not the shared globalK3s — helm
	// installs CRDs via the chart; the shared cluster already has CRDs loaded
	// from manifests and mixing both would complicate cleanup).
	slog.Info("helm: starting k3s cluster")
	start := time.Now()
	k3sCtr, err := k3s.Run(ctx, k3sImage)
	if err != nil {
		t.Fatalf("starting k3s: %v", err)
	}
	slog.Info("helm: k3s ready", "elapsed", time.Since(start).Round(time.Millisecond))
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(k3sCtr); err != nil {
			t.Logf("terminate k3s: %v", err)
		}
	})

	// Write kubeconfig to a temp file so helm can use it.
	kubeConfigYAML, err := k3sCtr.GetKubeConfig(ctx)
	if err != nil {
		t.Fatalf("getting kubeconfig: %v", err)
	}
	kubeconfigFile := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(kubeconfigFile, kubeConfigYAML, 0o600); err != nil {
		t.Fatalf("writing kubeconfig: %v", err)
	}

	// Install the chart via helm.
	helmInstall(ctx, t, helmPath, kubeconfigFile, chartRef, chartVersion, registryCtr != nil)

	// Verify the chart installation using the k8s client.
	verifyHelmRelease(ctx, t, kubeConfigYAML)
}

// resolveChartRef returns the OCI chart reference to install, the chart version,
// and an optional local registry container. If HELM_CHART_IMAGE is set it is used
// directly. Otherwise a local registry is started and the chart is packaged and pushed.
func resolveChartRef(ctx context.Context, t *testing.T, helmPath string) (ref, version string, registryCtr testcontainers.Container) {
	t.Helper()

	if img := os.Getenv("HELM_CHART_IMAGE"); img != "" {
		ver := os.Getenv("HELM_CHART_VERSION")
		slog.Info("helm: using pre-built chart", "ref", img, "version", ver)
		return img, ver, nil
	}

	slog.Info("helm: HELM_CHART_IMAGE not set; starting local OCI registry")
	reg, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "registry:2",
			ExposedPorts: []string{"5000/tcp"},
			WaitingFor: tcwait.ForHTTP("/v2/").
				WithPort("5000").
				WithStatusCodeMatcher(func(status int) bool { return status == http.StatusOK }).
				WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("starting local registry: %v", err)
	}

	host, err := reg.Host(ctx)
	if err != nil {
		_ = testcontainers.TerminateContainer(reg)
		t.Fatalf("getting registry host: %v", err)
	}
	port, err := reg.MappedPort(ctx, "5000")
	if err != nil {
		_ = testcontainers.TerminateContainer(reg)
		t.Fatalf("getting registry port: %v", err)
	}
	registryAddr := fmt.Sprintf("%s:%s", host, port.Port())
	slog.Info("helm: local registry ready", "addr", registryAddr)

	// Package the chart from the repo root.
	repoRoot := filepath.Join("..", "..")
	chartDir := filepath.Join(repoRoot, "charts", "mcp-anything")
	tmpDir := t.TempDir()

	slog.Info("helm: packaging chart", "dir", chartDir)
	out, err := exec.CommandContext(ctx, helmPath, "package", chartDir, "--destination", tmpDir).CombinedOutput()
	if err != nil {
		_ = testcontainers.TerminateContainer(reg)
		t.Fatalf("helm package: %v\n%s", err, out)
	}

	// Find the packaged .tgz and extract the chart version from its filename.
	matches, err := filepath.Glob(filepath.Join(tmpDir, "*.tgz"))
	if err != nil || len(matches) == 0 {
		_ = testcontainers.TerminateContainer(reg)
		t.Fatalf("no chart tgz found after helm package in %s", tmpDir)
	}
	chartTgz := matches[0]
	chartVer := parseChartVersion(filepath.Base(chartTgz))
	slog.Info("helm: chart packaged", "file", filepath.Base(chartTgz), "version", chartVer)

	// Push to the local registry using plain HTTP.
	slog.Info("helm: pushing chart to local registry", "addr", registryAddr)
	out, err = exec.CommandContext(ctx, helmPath,
		"push", chartTgz,
		fmt.Sprintf("oci://%s", registryAddr),
		"--plain-http",
	).CombinedOutput()
	if err != nil {
		_ = testcontainers.TerminateContainer(reg)
		t.Fatalf("helm push: %v\n%s", err, out)
	}
	slog.Info("helm: chart pushed")

	return fmt.Sprintf("oci://%s/mcp-anything", registryAddr), chartVer, reg
}

// parseChartVersion extracts the version from a packaged chart filename.
// For example "mcp-anything-0.1.0.tgz" → "0.1.0".
func parseChartVersion(filename string) string {
	name := strings.TrimSuffix(filename, ".tgz")
	idx := strings.LastIndex(name, "-")
	if idx < 0 {
		return ""
	}
	return name[idx+1:]
}

// helmInstall runs "helm install" to deploy the mcp-anything chart from the
// given OCI reference into the helmReleaseNS namespace of the k3s cluster.
func helmInstall(ctx context.Context, t *testing.T, helmPath, kubeconfigFile, chartRef, chartVersion string, plainHTTP bool) {
	t.Helper()

	args := []string{
		"install", helmReleaseName, chartRef,
		"--kubeconfig", kubeconfigFile,
		"--namespace", helmReleaseNS,
		"--create-namespace",
		// Do not wait for pods — the operator image is not loaded into k3s.
		"--wait=false",
		// Disable leader election to avoid needing extra RBAC for the lease.
		"--set", "leaderElect=false",
	}
	if chartVersion != "" {
		args = append(args, "--version", chartVersion)
	}
	if plainHTTP {
		args = append(args, "--plain-http")
	}

	slog.Info("helm: installing chart", "ref", chartRef, "namespace", helmReleaseNS)
	out, err := exec.CommandContext(ctx, helmPath, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm install: %v\n%s", err, out)
	}
	slog.Info("helm: install complete", "output", strings.TrimSpace(string(out)))
}

// verifyHelmRelease uses the k8s client to assert that the Helm chart created the
// expected resources: CRDs (via waitForCRDs) and the operator Deployment.
func verifyHelmRelease(ctx context.Context, t *testing.T, kubeConfigYAML []byte) {
	t.Helper()

	scheme := buildOperatorScheme()
	restCfg, err := clientcmd.RESTConfigFromKubeConfig(kubeConfigYAML)
	if err != nil {
		t.Fatalf("building REST config: %v", err)
	}
	c, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("creating k8s client: %v", err)
	}

	// CRDs are installed from the chart's crds/ directory.
	crdCtx, crdCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer crdCancel()
	if err := waitForCRDs(crdCtx, c); err != nil {
		t.Fatalf("CRDs not established after helm install: %v", err)
	}
	t.Log("helm: CRDs established")

	// The operator Deployment should exist (pod may not be running since the
	// operator image is not loaded into k3s — that is expected).
	var deployment appsv1.Deployment
	deployCtx, deployCancel := context.WithTimeout(ctx, 30*time.Second)
	defer deployCancel()
	if err := wait.PollUntilContextTimeout(deployCtx, pollInterval, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		err := c.Get(ctx, types.NamespacedName{
			Name:      helmReleaseName,
			Namespace: helmReleaseNS,
		}, &deployment)
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return err == nil, err
	}); err != nil {
		t.Fatalf("Deployment %s/%s not found after helm install: %v", helmReleaseNS, helmReleaseName, err)
	}
	t.Logf("helm: Deployment %s/%s created (replicas=%d)", deployment.Namespace, deployment.Name, *deployment.Spec.Replicas)
}
