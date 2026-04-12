//go:build e2e

package e2e_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/gaarutyunov/mcp-anything/pkg/crd/v1alpha1"
)

// ── constants ──────────────────────────────────────────────────────────────────

const (
	kagentTestNamespace = "kagent-e2e"
	kagentNamespace     = "kagent"
	kagentChartVersion  = "v0.9.0-beta3"
	kagentHelmRegistry  = "oci://ghcr.io/kagent-dev/kagent/helm/"

	kagentWiremockLLMName      = "wiremock-llm"
	kagentWiremockUpstreamName = "wiremock-upstream"
	kagentMCPProxyName         = "demo-proxy"
	kagentMCPUpstreamName      = "demo"
	kagentModelConfigName      = "test-model"
	kagentRemoteMCPServerName  = "mcp-anything"
	kagentAgentName            = "test-agent"
	kagentLLMSecretName        = "llm-api-key"
)

// helmChartGVR is the GVR for the k3s HelmChart CRD (built into all k3s clusters).
var helmChartGVR = schema.GroupVersionResource{
	Group:    "helm.cattle.io",
	Version:  "v1",
	Resource: "helmcharts",
}

// kagentInstallOnce ensures kagent CRDs and controller are only installed once
// across all sub-tests within a single test binary run.
var (
	kagentInstallOnce sync.Once
	kagentInstallErr  error
)

// ── test entry point ───────────────────────────────────────────────────────────

// TestKagentRemoteMCPServerE2E deploys the full stack inside the shared k3s
// cluster and verifies end-to-end that kagent can discover and invoke tools
// served by mcp-anything.
//
// Infrastructure:
//
//	wiremock-llm      – stubs the Anthropic Messages API with deterministic
//	                    tool-use and final-answer responses.
//	wiremock-upstream – stubs the upstream REST API and serves the OpenAPI spec.
//	mcp-anything      – deployed via the in-process operator; proxies the demo
//	                    upstream and exposes its tools over MCP.
//	kagent            – deployed via the k3s built-in Helm chart controller;
//	                    receives a user task, calls the LLM stub, invokes
//	                    demo__get_item, and returns the final answer.
func TestKagentRemoteMCPServerE2E(t *testing.T) {
	proxyImage := getProxyImage(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	// ── Build k8s clients ──────────────────────────────────────────────────────

	restCfg, err := clientcmd.RESTConfigFromKubeConfig(globalK3s.kubeConfigYAML)
	if err != nil {
		t.Fatalf("building REST config: %v", err)
	}

	scheme := buildOperatorScheme()
	k8sClient, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("creating k8s client: %v", err)
	}

	dynamicClient, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		t.Fatalf("creating dynamic client: %v", err)
	}

	// ── Load proxy image into k3s ──────────────────────────────────────────────

	t.Logf("loading proxy image %q into k3s", proxyImage)
	if err := globalK3s.container.LoadImages(ctx, proxyImage); err != nil {
		t.Fatalf("loading proxy image into k3s: %v", err)
	}

	// ── Install kagent (once) ──────────────────────────────────────────────────

	t.Log("ensuring kagent CRDs and controller are installed")
	kagentInstallOnce.Do(func() {
		kagentInstallErr = installKagentViaHelmChart(ctx, t, dynamicClient, k8sClient)
	})
	if kagentInstallErr != nil {
		t.Fatalf("installing kagent: %v", kagentInstallErr)
	}

	// ── Create test namespace ──────────────────────────────────────────────────

	t.Logf("creating test namespace %s", kagentTestNamespace)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: kagentTestNamespace}}
	if err := k8sClient.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("creating namespace: %v", err)
	}
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		existing := &corev1.Namespace{}
		if err := k8sClient.Get(cleanCtx, types.NamespacedName{Name: kagentTestNamespace}, existing); err == nil {
			_ = k8sClient.Delete(cleanCtx, existing)
		}
	})

	// ── Deploy WireMock instances in-cluster ───────────────────────────────────

	t.Log("deploying wiremock-llm and wiremock-upstream in cluster")
	deployWireMockInCluster(ctx, t, k8sClient, kagentTestNamespace, kagentWiremockLLMName)
	deployWireMockInCluster(ctx, t, k8sClient, kagentTestNamespace, kagentWiremockUpstreamName)

	t.Log("waiting for WireMock deployments to become ready")
	if err := waitForKagentDeployment(ctx, t, k8sClient, kagentTestNamespace, kagentWiremockLLMName); err != nil {
		t.Fatalf("wiremock-llm not ready: %v", err)
	}
	if err := waitForKagentDeployment(ctx, t, k8sClient, kagentTestNamespace, kagentWiremockUpstreamName); err != nil {
		t.Fatalf("wiremock-upstream not ready: %v", err)
	}

	// ── Find WireMock pods for port-forward ────────────────────────────────────

	llmPod, err := findPodForDeployment(ctx, k8sClient, kagentTestNamespace, kagentWiremockLLMName)
	if err != nil {
		t.Fatalf("finding wiremock-llm pod: %v", err)
	}
	upstreamPod, err := findPodForDeployment(ctx, k8sClient, kagentTestNamespace, kagentWiremockUpstreamName)
	if err != nil {
		t.Fatalf("finding wiremock-upstream pod: %v", err)
	}

	// ── Register WireMock stubs ────────────────────────────────────────────────

	t.Log("registering wiremock-upstream stubs (openapi.yaml + GET /items/42)")
	upstreamLocalPort, err := findFreeLocalPort()
	if err != nil {
		t.Fatalf("finding free local port for wiremock-upstream: %v", err)
	}
	stopUpstreamFwd, err := portForwardToPod(ctx, t, restCfg, kagentTestNamespace, upstreamPod, upstreamLocalPort, 8080)
	if err != nil {
		t.Fatalf("port-forward to wiremock-upstream: %v", err)
	}
	upstreamAdminURL := fmt.Sprintf("http://localhost:%d", upstreamLocalPort)

	// Wait for WireMock admin API to be up
	if err := waitForHTTPOK(ctx, upstreamAdminURL+"/__admin/mappings"); err != nil {
		t.Fatalf("wiremock-upstream admin not ready: %v", err)
	}

	// Serve the OpenAPI spec
	registerStub(t, upstreamAdminURL, kagentOpenAPIStub())
	// Serve the items endpoint
	registerStub(t, upstreamAdminURL, kagentItemsStub())

	stopUpstreamFwd()

	t.Log("registering wiremock-llm stubs (Anthropic Messages API turns 1 and 2)")
	llmLocalPort, err := findFreeLocalPort()
	if err != nil {
		t.Fatalf("finding free local port for wiremock-llm: %v", err)
	}
	stopLLMFwd, err := portForwardToPod(ctx, t, restCfg, kagentTestNamespace, llmPod, llmLocalPort, 8080)
	if err != nil {
		t.Fatalf("port-forward to wiremock-llm: %v", err)
	}
	llmAdminURL := fmt.Sprintf("http://localhost:%d", llmLocalPort)

	if err := waitForHTTPOK(ctx, llmAdminURL+"/__admin/mappings"); err != nil {
		t.Fatalf("wiremock-llm admin not ready: %v", err)
	}

	// Turn 1: LLM decides to call demo__get_item
	registerStub(t, llmAdminURL, kagentLLMTurn1Stub())
	// Turn 2: LLM returns final answer after receiving tool result
	registerStub(t, llmAdminURL, kagentLLMTurn2Stub())

	stopLLMFwd()

	// ── Deploy mcp-anything via operator ──────────────────────────────────────

	t.Log("starting mcp-anything operator in-process")
	stopOperator := startOperator(ctx, t, globalK3s.kubeConfigYAML, scheme)
	defer stopOperator()

	// MCPUpstream: uses wiremock-upstream for both spec and REST calls
	upstreamSvcURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080",
		kagentWiremockUpstreamName, kagentTestNamespace)

	t.Logf("creating MCPUpstream %s/%s", kagentTestNamespace, kagentMCPUpstreamName)
	upstream := &v1alpha1.MCPUpstream{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kagentMCPUpstreamName,
			Namespace: kagentTestNamespace,
			Labels:    map[string]string{"mcp-anything.ai/proxy": kagentMCPProxyName},
		},
		Spec: v1alpha1.MCPUpstreamSpec{
			ToolPrefix: "demo",
			BaseURL:    upstreamSvcURL,
			OpenAPI: v1alpha1.MCPUpstreamOpenAPISpec{
				URL: upstreamSvcURL + "/openapi.yaml",
			},
		},
	}
	if err := k8sClient.Create(ctx, upstream); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("creating MCPUpstream: %v", err)
	}

	t.Logf("creating MCPProxy %s/%s", kagentTestNamespace, kagentMCPProxyName)
	proxyResource := &v1alpha1.MCPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: kagentMCPProxyName, Namespace: kagentTestNamespace},
		Spec: v1alpha1.MCPProxySpec{
			UpstreamSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"mcp-anything.ai/proxy": kagentMCPProxyName},
			},
			Image: proxyImage,
			Server: v1alpha1.ProxyServerSpec{
				Port: 8080,
			},
			Naming: v1alpha1.ProxyNamingSpec{Separator: "__"},
		},
	}
	if err := k8sClient.Create(ctx, proxyResource); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("creating MCPProxy: %v", err)
	}

	t.Log("waiting for mcp-anything proxy pod to become Ready")
	mcpPodName, err := waitForProxyPod(ctx, t, k8sClient, kagentTestNamespace, kagentMCPProxyName)
	if err != nil {
		t.Fatalf("mcp-anything proxy pod not ready: %v", err)
	}
	t.Logf("mcp-anything proxy pod ready: %s", mcpPodName)

	// The Service created by the operator is named after the MCPProxy.
	mcpServiceURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080",
		kagentMCPProxyName, kagentTestNamespace)

	// ── Apply kagent CRs ───────────────────────────────────────────────────────

	t.Log("creating kagent Secret, ModelConfig, RemoteMCPServer, and Agent")

	llmSvcURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080",
		kagentWiremockLLMName, kagentTestNamespace)

	if err := applyKagentCRs(ctx, t, k8sClient, dynamicClient,
		kagentTestNamespace, mcpServiceURL, llmSvcURL); err != nil {
		t.Fatalf("applying kagent CRs: %v", err)
	}

	// ── Wait for RemoteMCPServer to discover tools ─────────────────────────────

	t.Log("waiting for kagent to discover tools from mcp-anything")
	if err := waitForKagentToolDiscovery(ctx, t, dynamicClient, kagentTestNamespace, kagentRemoteMCPServerName); err != nil {
		t.Fatalf("tool discovery failed: %v", err)
	}
	t.Log("tools discovered by kagent RemoteMCPServer")

	// ── Wait for Agent to be accepted ─────────────────────────────────────────

	t.Log("waiting for kagent Agent to be Accepted")
	if err := waitForKagentAgent(ctx, t, dynamicClient, kagentTestNamespace, kagentAgentName); err != nil {
		t.Fatalf("agent not accepted: %v", err)
	}

	// ── Port-forward to kagent controller ─────────────────────────────────────

	controllerPort, err := findFreeLocalPort()
	if err != nil {
		t.Fatalf("finding free port for kagent controller: %v", err)
	}

	controllerPod, err := findPodForDeployment(ctx, k8sClient, kagentNamespace, "kagent-controller")
	if err != nil {
		t.Fatalf("finding kagent controller pod: %v", err)
	}

	stopControllerFwd, err := portForwardToPod(ctx, t, restCfg, kagentNamespace, controllerPod, controllerPort, 8083)
	if err != nil {
		t.Fatalf("port-forward to kagent controller: %v", err)
	}
	defer stopControllerFwd()

	controllerURL := fmt.Sprintf("http://localhost:%d", controllerPort)

	// Wait for controller API to be ready
	if err := waitForHTTPOK(ctx, controllerURL+"/api/health"); err != nil {
		t.Fatalf("kagent controller API not ready: %v", err)
	}

	// ── Submit task via A2A protocol ───────────────────────────────────────────

	t.Log("submitting task to kagent agent: 'What is the name of item 42?'")
	finalAnswer, err := submitKagentTask(ctx, t, controllerURL,
		kagentTestNamespace, kagentAgentName, "What is the name of item 42?")
	if err != nil {
		t.Fatalf("submitting task to kagent: %v", err)
	}
	t.Logf("agent final answer: %s", finalAnswer)

	if !strings.Contains(finalAnswer, "Widget A") {
		t.Errorf("expected final answer to contain 'Widget A', got: %s", finalAnswer)
	}

	// ── Verify WireMock request journals ──────────────────────────────────────

	t.Log("verifying wiremock-upstream received GET /items/42")
	upstreamFwdPort, err := findFreeLocalPort()
	if err != nil {
		t.Fatalf("finding port for upstream verify: %v", err)
	}
	stopUpstreamVerify, err := portForwardToPod(ctx, t, restCfg, kagentTestNamespace, upstreamPod, upstreamFwdPort, 8080)
	if err != nil {
		t.Fatalf("port-forward for upstream verify: %v", err)
	}
	defer stopUpstreamVerify()
	verifyWireMockRequest(t, fmt.Sprintf("http://localhost:%d", upstreamFwdPort), "/items/42")

	t.Log("verifying wiremock-llm received both LLM API calls")
	llmFwdPort, err := findFreeLocalPort()
	if err != nil {
		t.Fatalf("finding port for llm verify: %v", err)
	}
	stopLLMVerify, err := portForwardToPod(ctx, t, restCfg, kagentTestNamespace, llmPod, llmFwdPort, 8080)
	if err != nil {
		t.Fatalf("port-forward for llm verify: %v", err)
	}
	defer stopLLMVerify()
	verifyWireMockRequestCount(t, fmt.Sprintf("http://localhost:%d", llmFwdPort), "/v1/messages", 2)
}

// ── WireMock deployment helpers ────────────────────────────────────────────────

// deployWireMockInCluster creates a Deployment and ClusterIP Service for a
// WireMock instance identified by name inside ns.
func deployWireMockInCluster(ctx context.Context, t *testing.T, c client.Client, ns, name string) {
	t.Helper()
	labels := map[string]string{"app": name}
	replicas := int32(1)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "wiremock",
						Image: "wiremock/wiremock:3.9.1",
						Ports: []corev1.ContainerPort{{ContainerPort: 8080, Protocol: corev1.ProtocolTCP}},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/__admin/mappings",
									Port: intstr.FromInt32(8080),
								},
							},
							InitialDelaySeconds: 5,
							PeriodSeconds:       5,
							TimeoutSeconds:      3,
						},
					}},
				},
			},
		},
	}
	if err := c.Create(ctx, dep); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("creating wiremock deployment %s: %v", name, err)
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Port:       8080,
				TargetPort: intstr.FromInt32(8080),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
	if err := c.Create(ctx, svc); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("creating wiremock service %s: %v", name, err)
	}
	t.Logf("created wiremock deployment+service %s/%s", ns, name)
}

// waitForKagentDeployment polls until a Deployment has at least one ready replica.
func waitForKagentDeployment(ctx context.Context, t *testing.T, c client.Client, ns, name string) error {
	t.Helper()
	return wait.PollUntilContextTimeout(ctx, 5*time.Second, 5*time.Minute, false,
		func(pollCtx context.Context) (bool, error) {
			dep := &appsv1.Deployment{}
			if err := c.Get(pollCtx, types.NamespacedName{Name: name, Namespace: ns}, dep); err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			ready := dep.Status.ReadyReplicas >= 1
			if !ready {
				t.Logf("deployment %s/%s not ready yet (ready=%d)", ns, name, dep.Status.ReadyReplicas)
			}
			return ready, nil
		},
	)
}

// findPodForDeployment returns the name of the first Running+Ready pod belonging
// to the Deployment identified by ns/deploymentName.
func findPodForDeployment(ctx context.Context, c client.Client, ns, deploymentName string) (string, error) {
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Name: deploymentName, Namespace: ns}, dep); err != nil {
		return "", fmt.Errorf("getting deployment %s/%s: %w", ns, deploymentName, err)
	}
	matchLabels := dep.Spec.Selector.MatchLabels

	podList := &corev1.PodList{}
	if err := c.List(ctx, podList,
		client.InNamespace(ns),
		client.MatchingLabels(matchLabels),
	); err != nil {
		return "", fmt.Errorf("listing pods for deployment %s/%s: %w", ns, deploymentName, err)
	}

	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				return pod.Name, nil
			}
		}
	}
	return "", fmt.Errorf("no ready pod found for deployment %s/%s", ns, deploymentName)
}

// ── kagent installation via k3s HelmChart CRD ─────────────────────────────────

// installKagentViaHelmChart uses the k3s built-in Helm chart controller to
// install kagent CRDs and then the kagent application.  k3s ships with
// HelmChart/HelmChartConfig CRDs and a controller that deploys charts by
// creating Jobs; this requires no external Helm binary or Go library.
func installKagentViaHelmChart(ctx context.Context, t *testing.T, dc dynamic.Interface, c client.Client) error {
	t.Helper()
	t.Log("installing kagent-crds via k3s HelmChart CRD")

	crdsValues := "" // no extra values for the CRDs chart

	if err := createHelmChart(ctx, dc, "kagent-crds",
		kagentHelmRegistry+"kagent-crds",
		kagentChartVersion,
		kagentNamespace,
		crdsValues,
	); err != nil {
		return fmt.Errorf("creating kagent-crds HelmChart: %w", err)
	}

	t.Log("waiting for kagent-crds HelmChart job to complete")
	if err := waitForHelmChartJob(ctx, t, dc, "kagent-crds"); err != nil {
		return fmt.Errorf("kagent-crds HelmChart failed: %w", err)
	}
	t.Log("kagent-crds installed")

	// Install the main kagent chart with minimal values to reduce startup time.
	mainValues := kagentTestHelmValues()
	t.Log("installing kagent via k3s HelmChart CRD")
	if err := createHelmChart(ctx, dc, "kagent",
		kagentHelmRegistry+"kagent",
		kagentChartVersion,
		kagentNamespace,
		mainValues,
	); err != nil {
		return fmt.Errorf("creating kagent HelmChart: %w", err)
	}

	t.Log("waiting for kagent HelmChart job to complete")
	if err := waitForHelmChartJob(ctx, t, dc, "kagent"); err != nil {
		return fmt.Errorf("kagent HelmChart failed: %w", err)
	}
	t.Log("kagent chart installed; waiting for controller deployment to be ready")

	// Wait for the kagent controller Deployment to have at least one ready replica.
	if err := waitForKagentDeployment(ctx, t, c, kagentNamespace, "kagent-controller"); err != nil {
		return fmt.Errorf("kagent controller not ready: %w", err)
	}
	t.Log("kagent controller is ready")
	return nil
}

// createHelmChart creates a HelmChart resource in kube-system, which is picked
// up by the k3s built-in Helm chart controller and results in a helm install Job.
func createHelmChart(ctx context.Context, dc dynamic.Interface, name, chart, version, targetNS, valuesContent string) error {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "helm.cattle.io/v1",
			"kind":       "HelmChart",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": "kube-system",
			},
			"spec": map[string]interface{}{
				"chart":           chart,
				"version":         version,
				"targetNamespace": targetNS,
				"createNamespace": true,
				"valuesContent":   valuesContent,
			},
		},
	}
	_, err := dc.Resource(helmChartGVR).Namespace("kube-system").Create(ctx, obj, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating HelmChart %s: %w", name, err)
	}
	return nil
}

// waitForHelmChartJob polls until the Job created by the k3s Helm chart
// controller completes successfully or the context expires.
func waitForHelmChartJob(ctx context.Context, t *testing.T, dc dynamic.Interface, chartName string) error {
	t.Helper()
	jobName := "helm-install-" + chartName
	return wait.PollUntilContextTimeout(ctx, 10*time.Second, 8*time.Minute, false,
		func(pollCtx context.Context) (bool, error) {
			// Fetch the HelmChart object and check jobName / status
			hc, err := dc.Resource(helmChartGVR).Namespace("kube-system").Get(pollCtx, chartName, metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			// The k3s HelmChart status field is "jobName" once a job is created.
			status, _, _ := unstructured.NestedString(hc.Object, "status", "jobName")
			if status != "" {
				t.Logf("HelmChart %s: job created: %s", chartName, status)
			}

			// Check the associated batch/v1 Job directly.
			scheme := buildOperatorScheme()
			restCfg, err := clientcmd.RESTConfigFromKubeConfig(globalK3s.kubeConfigYAML)
			if err != nil {
				return false, err
			}
			k8sClient, err := client.New(restCfg, client.Options{Scheme: scheme})
			if err != nil {
				return false, err
			}

			job := &batchv1.Job{}
			if err := k8sClient.Get(pollCtx, types.NamespacedName{Name: jobName, Namespace: "kube-system"}, job); err != nil {
				if apierrors.IsNotFound(err) {
					t.Logf("HelmChart %s: job %s not yet created", chartName, jobName)
					return false, nil
				}
				return false, err
			}

			for _, cond := range job.Status.Conditions {
				if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
					t.Logf("HelmChart %s: job %s completed successfully", chartName, jobName)
					return true, nil
				}
				if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
					return false, fmt.Errorf("HelmChart %s: job %s failed: %s", chartName, jobName, cond.Message)
				}
			}
			t.Logf("HelmChart %s: job %s still running", chartName, jobName)
			return false, nil
		},
	)
}

// ── kagent CR helpers ──────────────────────────────────────────────────────────

// applyKagentCRs creates the Secret, ModelConfig, RemoteMCPServer, and Agent
// resources in the test namespace.
func applyKagentCRs(
	ctx context.Context,
	t *testing.T,
	c client.Client,
	dc dynamic.Interface,
	ns, mcpServiceURL, llmBaseURL string,
) error {
	t.Helper()

	// Secret for the dummy LLM API key
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: kagentLLMSecretName, Namespace: ns},
		StringData: map[string]string{
			"ANTHROPIC_API_KEY": "dummy-test-key",
		},
	}
	if err := c.Create(ctx, secret); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating llm-api-key secret: %w", err)
	}

	modelConfigGVR := schema.GroupVersionResource{
		Group: "kagent.dev", Version: "v1alpha2", Resource: "modelconfigs",
	}
	remoteMCPServerGVR := schema.GroupVersionResource{
		Group: "kagent.dev", Version: "v1alpha2", Resource: "remotemcpservers",
	}
	agentGVR := schema.GroupVersionResource{
		Group: "kagent.dev", Version: "v1alpha2", Resource: "agents",
	}

	// ModelConfig pointing at wiremock-llm
	mc := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "kagent.dev/v1alpha2",
			"kind":       "ModelConfig",
			"metadata": map[string]interface{}{
				"name":      kagentModelConfigName,
				"namespace": ns,
			},
			"spec": map[string]interface{}{
				"model":            "claude-3-5-sonnet-20241022",
				"provider":         "Anthropic",
				"apiKeySecret":     kagentLLMSecretName,
				"apiKeySecretKey":  "ANTHROPIC_API_KEY",
				"anthropic": map[string]interface{}{
					"baseUrl":   llmBaseURL,
					"maxTokens": int64(4096),
				},
			},
		},
	}
	if _, err := dc.Resource(modelConfigGVR).Namespace(ns).Create(ctx, mc, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating ModelConfig: %w", err)
	}
	t.Logf("created ModelConfig %s/%s → LLM base URL: %s", ns, kagentModelConfigName, llmBaseURL)

	// RemoteMCPServer pointing at mcp-anything
	rmcp := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "kagent.dev/v1alpha2",
			"kind":       "RemoteMCPServer",
			"metadata": map[string]interface{}{
				"name":      kagentRemoteMCPServerName,
				"namespace": ns,
			},
			"spec": map[string]interface{}{
				"description": "mcp-anything proxy exposing the Demo Items API",
				"protocol":    "STREAMABLE_HTTP",
				"url":         mcpServiceURL + "/mcp",
			},
		},
	}
	if _, err := dc.Resource(remoteMCPServerGVR).Namespace(ns).Create(ctx, rmcp, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating RemoteMCPServer: %w", err)
	}
	t.Logf("created RemoteMCPServer %s/%s → MCP URL: %s/mcp", ns, kagentRemoteMCPServerName, mcpServiceURL)

	// Agent using ModelConfig + RemoteMCPServer
	agent := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "kagent.dev/v1alpha2",
			"kind":       "Agent",
			"metadata": map[string]interface{}{
				"name":      kagentAgentName,
				"namespace": ns,
			},
			"spec": map[string]interface{}{
				"type": "Declarative",
				"declarative": map[string]interface{}{
					"systemMessage": "You are a helpful assistant. Use the available tools to answer questions about items.",
					"modelConfig":   kagentModelConfigName,
					"tools": []interface{}{
						map[string]interface{}{
							"type": "McpServer",
							"mcpServer": map[string]interface{}{
								"kind":      "RemoteMCPServer",
								"apiGroup":  "kagent.dev",
								"name":      kagentRemoteMCPServerName,
								"namespace": ns,
							},
						},
					},
				},
			},
		},
	}
	if _, err := dc.Resource(agentGVR).Namespace(ns).Create(ctx, agent, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating Agent: %w", err)
	}
	t.Logf("created Agent %s/%s", ns, kagentAgentName)
	return nil
}

// waitForKagentToolDiscovery polls until the RemoteMCPServer status has at
// least one discovered tool.
func waitForKagentToolDiscovery(ctx context.Context, t *testing.T, dc dynamic.Interface, ns, name string) error {
	t.Helper()
	gvr := schema.GroupVersionResource{Group: "kagent.dev", Version: "v1alpha2", Resource: "remotemcpservers"}
	return wait.PollUntilContextTimeout(ctx, 10*time.Second, 5*time.Minute, false,
		func(pollCtx context.Context) (bool, error) {
			obj, err := dc.Resource(gvr).Namespace(ns).Get(pollCtx, name, metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			tools, _, _ := unstructured.NestedSlice(obj.Object, "status", "discoveredTools")
			if len(tools) > 0 {
				toolNames := make([]string, 0, len(tools))
				for _, tool := range tools {
					if m, ok := tool.(map[string]interface{}); ok {
						if n, ok := m["name"].(string); ok {
							toolNames = append(toolNames, n)
						}
					}
				}
				t.Logf("RemoteMCPServer %s/%s discovered %d tool(s): %v", ns, name, len(tools), toolNames)
				return true, nil
			}
			t.Logf("RemoteMCPServer %s/%s: no tools discovered yet", ns, name)
			return false, nil
		},
	)
}

// waitForKagentAgent polls until the Agent has an "Accepted" condition with
// status True.
func waitForKagentAgent(ctx context.Context, t *testing.T, dc dynamic.Interface, ns, name string) error {
	t.Helper()
	gvr := schema.GroupVersionResource{Group: "kagent.dev", Version: "v1alpha2", Resource: "agents"}
	return wait.PollUntilContextTimeout(ctx, 10*time.Second, 5*time.Minute, false,
		func(pollCtx context.Context) (bool, error) {
			obj, err := dc.Resource(gvr).Namespace(ns).Get(pollCtx, name, metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
			for _, cond := range conditions {
				m, ok := cond.(map[string]interface{})
				if !ok {
					continue
				}
				if m["type"] == "Accepted" && m["status"] == "True" {
					return true, nil
				}
			}
			t.Logf("Agent %s/%s not yet accepted", ns, name)
			return false, nil
		},
	)
}

// ── Task submission via A2A protocol ──────────────────────────────────────────

// submitKagentTask sends a user message to the agent via the kagent A2A
// JSON-RPC endpoint and streams the SSE response until a "completed" status
// is received.  Returns the text content of the final assistant message.
func submitKagentTask(
	ctx context.Context,
	t *testing.T,
	controllerURL, agentNS, agentName, task string,
) (string, error) {
	t.Helper()

	endpoint := fmt.Sprintf("%s/api/a2a/%s/%s", controllerURL, agentNS, agentName)
	t.Logf("submitting task to %s", endpoint)

	reqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "message/send",
		"params": map[string]interface{}{
			"message": map[string]interface{}{
				"kind": "message",
				"role": "user",
				"parts": []interface{}{
					map[string]interface{}{
						"kind": "text",
						"text": task,
					},
				},
				"messageId": "kagent-e2e-msg-1",
			},
		},
		"id": "kagent-e2e-req-1",
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshalling A2A request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("creating A2A request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-User-Id", "admin@kagent.dev")

	httpClient := &http.Client{Timeout: 5 * time.Minute}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("sending A2A request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("A2A request returned %d: %s", resp.StatusCode, string(b))
	}

	return parseA2ASSEResponse(ctx, t, resp.Body)
}

// parseA2ASSEResponse reads the SSE stream from the A2A endpoint and returns
// the text of the final assistant message once the task reaches "completed"
// state.
func parseA2ASSEResponse(ctx context.Context, t *testing.T, body io.Reader) (string, error) {
	scanner := bufio.NewScanner(body)
	var lastText string

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimSpace(data)
		if data == "" || data == "[DONE]" {
			continue
		}

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			t.Logf("A2A SSE: skipping non-JSON event: %s", data)
			continue
		}

		// Extract from JSON-RPC result
		result, _ := event["result"].(map[string]interface{})
		if result == nil {
			continue
		}

		// Collect any text content from assistant message parts
		if msg, ok := result["message"].(map[string]interface{}); ok {
			if parts, ok := msg["parts"].([]interface{}); ok {
				for _, part := range parts {
					if p, ok := part.(map[string]interface{}); ok {
						if p["kind"] == "text" {
							if txt, ok := p["text"].(string); ok && txt != "" {
								lastText = txt
							}
						}
					}
				}
			}
		}

		// Check for "status" variant (TaskStatusUpdateEvent)
		if status, ok := result["status"].(map[string]interface{}); ok {
			state, _ := status["state"].(string)
			t.Logf("A2A SSE: task state: %s", state)
			if state == "completed" {
				if msg, ok := status["message"].(map[string]interface{}); ok {
					if parts, ok := msg["parts"].([]interface{}); ok {
						for _, part := range parts {
							if p, ok := part.(map[string]interface{}); ok {
								if p["kind"] == "text" {
									if txt, ok := p["text"].(string); ok && txt != "" {
										return txt, nil
									}
								}
							}
						}
					}
				}
				if lastText != "" {
					return lastText, nil
				}
			}
			if state == "failed" {
				msg, _ := status["message"].(string)
				return "", fmt.Errorf("task failed: %s", msg)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("reading SSE stream: %w", err)
	}
	if lastText != "" {
		return lastText, nil
	}
	return "", fmt.Errorf("SSE stream ended without a completed event")
}

// ── WireMock journal verification ─────────────────────────────────────────────

// verifyWireMockRequest asserts that WireMock received at least one request
// matching the given URL path.
func verifyWireMockRequest(t *testing.T, adminURL, urlPath string) {
	t.Helper()
	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Get(adminURL + "/__admin/requests") //nolint:noctx
	if err != nil {
		t.Fatalf("get wiremock requests journal: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)

	var journal struct {
		Requests []struct {
			Request struct {
				URL string `json:"url"`
			} `json:"request"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(b, &journal); err != nil {
		t.Fatalf("parse wiremock journal: %v", err)
	}

	for _, req := range journal.Requests {
		if req.Request.URL == urlPath {
			return
		}
	}
	t.Errorf("wiremock did not receive a request to %s; journal: %s", urlPath, string(b))
}

// verifyWireMockRequestCount asserts WireMock received at least wantCount
// requests matching urlPath.
func verifyWireMockRequestCount(t *testing.T, adminURL, urlPath string, wantCount int) {
	t.Helper()
	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Get(adminURL + "/__admin/requests") //nolint:noctx
	if err != nil {
		t.Fatalf("get wiremock requests journal: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)

	var journal struct {
		Requests []struct {
			Request struct {
				URL string `json:"url"`
			} `json:"request"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(b, &journal); err != nil {
		t.Fatalf("parse wiremock journal: %v", err)
	}

	count := 0
	for _, req := range journal.Requests {
		if req.Request.URL == urlPath {
			count++
		}
	}
	if count < wantCount {
		t.Errorf("expected at least %d requests to %s, got %d; journal: %s",
			wantCount, urlPath, count, string(b))
	}
}

// ── Proxy image helper ─────────────────────────────────────────────────────────

// getProxyImage returns the proxy image from the PROXY_IMAGE env var, or
// fails the test if it is not set.
func getProxyImage(t *testing.T) string {
	t.Helper()
	img := os.Getenv("PROXY_IMAGE")
	if img == "" {
		t.Fatal("PROXY_IMAGE must be set for the kagent E2E test")
	}
	return img
}

// ── WireMock stub JSON bodies ──────────────────────────────────────────────────

// kagentOpenAPIStub returns a WireMock stub that serves the Demo Items OpenAPI
// spec at GET /openapi.yaml.
func kagentOpenAPIStub() string {
	spec := strings.ReplaceAll(kagentDemoOpenAPISpec, `"`, `\"`)
	spec = strings.ReplaceAll(spec, "\n", `\n`)
	return fmt.Sprintf(`{
		"request": {"method": "GET", "url": "/openapi.yaml"},
		"response": {
			"status": 200,
			"body": "%s",
			"headers": {"Content-Type": "application/yaml"}
		}
	}`, spec)
}

// kagentItemsStub returns a WireMock stub for GET /items/42.
func kagentItemsStub() string {
	return `{
		"request": {"method": "GET", "url": "/items/42"},
		"response": {
			"status": 200,
			"jsonBody": {"id": 42, "name": "Widget A", "price": 9.99},
			"headers": {"Content-Type": "application/json"}
		}
	}`
}

// kagentLLMTurn1Stub returns a WireMock scenario stub for the first LLM call.
// The LLM instructs kagent to call the demo__get_item tool.
func kagentLLMTurn1Stub() string {
	return `{
		"scenarioName": "llm-conversation",
		"requiredScenarioState": "Started",
		"newScenarioState": "ToolUsed",
		"request": {"method": "POST", "url": "/v1/messages"},
		"response": {
			"status": 200,
			"jsonBody": {
				"id": "msg_stub_1",
				"type": "message",
				"role": "assistant",
				"content": [
					{
						"type": "tool_use",
						"id": "tool_stub_1",
						"name": "demo__get_item",
						"input": {"id": "42"}
					}
				],
				"model": "claude-3-5-sonnet-20241022",
				"stop_reason": "tool_use",
				"stop_sequence": null,
				"usage": {"input_tokens": 100, "output_tokens": 50}
			},
			"headers": {"Content-Type": "application/json"}
		}
	}`
}

// kagentLLMTurn2Stub returns a WireMock scenario stub for the second LLM call.
// The LLM provides the final answer after receiving the tool result.
func kagentLLMTurn2Stub() string {
	return `{
		"scenarioName": "llm-conversation",
		"requiredScenarioState": "ToolUsed",
		"request": {"method": "POST", "url": "/v1/messages"},
		"response": {
			"status": 200,
			"jsonBody": {
				"id": "msg_stub_2",
				"type": "message",
				"role": "assistant",
				"content": [{"type": "text", "text": "The item name is Widget A."}],
				"model": "claude-3-5-sonnet-20241022",
				"stop_reason": "end_turn",
				"stop_sequence": null,
				"usage": {"input_tokens": 150, "output_tokens": 20}
			},
			"headers": {"Content-Type": "application/json"}
		}
	}`
}

// kagentTestHelmValues returns the Helm values YAML for a minimal kagent
// installation suitable for integration testing.
func kagentTestHelmValues() string {
	return `
kmcp:
  enabled: false
agents:
  k8s-agent:
    enabled: false
  kgateway-agent:
    enabled: false
  istio-agent:
    enabled: false
  promql-agent:
    enabled: false
  observability-agent:
    enabled: false
  argo-rollouts-agent:
    enabled: false
  helm-agent:
    enabled: false
  cilium-policy-agent:
    enabled: false
  cilium-manager-agent:
    enabled: false
  cilium-debug-agent:
    enabled: false
tools:
  grafana-mcp:
    enabled: false
  querydoc:
    enabled: false
ui:
  enabled: false
controller:
  auth:
    mode: unsecure
database:
  postgres:
    bundled:
      enabled: true
      storage: 100Mi
      resources:
        requests:
          cpu: 100m
          memory: 128Mi
        limits:
          cpu: 250m
          memory: 256Mi
providers:
  default: anthropic
  anthropic:
    model: claude-3-5-sonnet-20241022
    apiKey: "dummy-test-key"
`
}

// kagentDemoOpenAPISpec is the OpenAPI 3.0 spec for the Demo Items API that
// wiremock-upstream serves at GET /openapi.yaml.  The operationId "get_item"
// results in the MCP tool name "demo__get_item".
const kagentDemoOpenAPISpec = `openapi: "3.0.0"
info:
  title: Demo Items API
  version: "1.0"
paths:
  /items/{id}:
    get:
      operationId: get_item
      summary: Get item by ID
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: OK
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
