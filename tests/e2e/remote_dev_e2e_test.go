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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/gaarutyunov/mcp-anything/pkg/crd/v1alpha1"
)

const (
	remoteDevNamespace  = "remote-dev-e2e"
	remoteDevProxyName  = "remote-dev-proxy"
	remoteDevUpstream   = "remote-dev"
	remoteDevProxyPort  = 8080

	// mainGoWithLintIssue is the initial workspace main.go that contains a deliberate
	// errcheck lint violation: the result of os.Remove is not checked.
	mainGoWithLintIssue = `package main

import (
	"fmt"
	"os"
)

func main() {
	os.Remove("/tmp/mcp-test-cleanup") // errcheck: result of os.Remove should be checked
	fmt.Println("Hello, workspace!")
}
`

	// mainGoFixed replaces the unchecked os.Remove with a properly handled error.
	mainGoFixed = `package main

import (
	"fmt"
	"os"
)

func main() {
	if err := os.Remove("/tmp/mcp-test-cleanup"); err != nil && !os.IsNotExist(err) {
		fmt.Printf("cleanup error: %v\n", err)
	}
	fmt.Println("Hello, workspace!")
}
`

	// helloGoContent is a small valid Go file written during the test.
	helloGoContent = `package main

import "fmt"

func hello() string {
	return "hello from write_file test"
}

func init() {
	fmt.Println(hello())
}
`
)

// TestRemoteDevWorkspaceE2E deploys a remote development workspace proxy to the shared
// k3s cluster via the in-process operator, then exercises all six command tools:
// write_file, read_file, list_files, search_files, run_build, and run_lint.
//
// The test requires a WORKSPACE_IMAGE environment variable pointing to a container image
// that includes the proxy binary, Go toolchain, golangci-lint, the write-file helper,
// and a sample Go project pre-seeded at /workspace.
//
// The test:
//  1. Loads the workspace image into k3s.
//  2. Creates MCPUpstream CRD with type: command and all 6 tool definitions.
//  3. Creates MCPProxy CRD pointing to the workspace image.
//  4. Waits for the proxy pod to become Ready.
//  5. Port-forwards to the proxy pod and connects an MCP client.
//  6. Exercises each tool and validates responses.
//  7. Verifies path traversal is rejected by the write-file helper.
func TestRemoteDevWorkspaceE2E(t *testing.T) {
	workspaceImage := os.Getenv("WORKSPACE_IMAGE")
	if workspaceImage == "" {
		t.Fatal("WORKSPACE_IMAGE must point to the workspace image built for this test run")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	// ── 1. Load workspace image into k3s ─────────────────────────────────────

	loadImageIntoK3s(ctx, t, globalK3s, workspaceImage)

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

	t.Logf("creating namespace %s", remoteDevNamespace)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: remoteDevNamespace}}
	if err := k8sClient.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("creating namespace: %v", err)
	}
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		existing := &corev1.Namespace{}
		if err := k8sClient.Get(cleanCtx, types.NamespacedName{Name: remoteDevNamespace}, existing); err == nil {
			if err := k8sClient.Delete(cleanCtx, existing); err != nil && !apierrors.IsNotFound(err) {
				t.Logf("cleanup: delete namespace %s: %v", remoteDevNamespace, err)
			}
		}
	})

	// ── 4. Start operator in-process ─────────────────────────────────────────

	t.Log("starting operator in-process")
	stopOperator := startOperator(ctx, t, globalK3s.kubeConfigYAML, scheme)
	defer stopOperator()

	// ── 5. Create MCPUpstream (type: command) ─────────────────────────────────

	t.Logf("creating MCPUpstream %s/%s", remoteDevNamespace, remoteDevUpstream)
	upstream := buildRemoteDevUpstream()
	if err := k8sClient.Create(ctx, upstream); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("creating MCPUpstream: %v", err)
	}

	// ── 6. Create MCPProxy ────────────────────────────────────────────────────

	t.Logf("creating MCPProxy %s/%s", remoteDevNamespace, remoteDevProxyName)
	proxyResource := &v1alpha1.MCPProxy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      remoteDevProxyName,
			Namespace: remoteDevNamespace,
		},
		Spec: v1alpha1.MCPProxySpec{
			UpstreamSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"mcp-anything.ai/proxy": remoteDevProxyName,
				},
			},
			Image: workspaceImage,
			Server: v1alpha1.ProxyServerSpec{
				Port: remoteDevProxyPort,
			},
			Naming: v1alpha1.ProxyNamingSpec{
				Separator: "__",
			},
		},
	}
	if err := k8sClient.Create(ctx, proxyResource); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("creating MCPProxy: %v", err)
	}

	// ── 7. Wait for proxy pod to be ready ─────────────────────────────────────

	t.Log("waiting for proxy pod to become Ready (up to 5 minutes)")
	podName, err := waitForProxyPod(ctx, t, k8sClient, remoteDevNamespace, remoteDevProxyName)
	if err != nil {
		t.Fatalf("proxy pod not ready: %v", err)
	}
	t.Logf("proxy pod ready: %s", podName)

	// ── 8. Port-forward to proxy pod ─────────────────────────────────────────

	localPort, err := findFreeLocalPort()
	if err != nil {
		t.Fatalf("finding free local port: %v", err)
	}
	t.Logf("port-forwarding localhost:%d → %s:%d", localPort, podName, remoteDevProxyPort)

	stopForward, err := portForwardToPod(ctx, t, restCfg, remoteDevNamespace, podName, localPort, remoteDevProxyPort)
	if err != nil {
		t.Fatalf("starting port-forward: %v", err)
	}
	defer stopForward()

	proxyURL := fmt.Sprintf("http://localhost:%d", localPort)

	// ── 9. Wait for proxy to be healthy ──────────────────────────────────────

	t.Log("waiting for proxy /healthz endpoint to return 200")
	healthCtx, healthCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer healthCancel()
	if err := waitForHTTPOK(healthCtx, proxyURL+"/healthz"); err != nil {
		t.Fatalf("proxy healthz not OK: %v", err)
	}
	t.Log("proxy is healthy")

	// ── 10. Connect MCP client ────────────────────────────────────────────────

	mcpTransport := &sdkmcp.StreamableClientTransport{
		Endpoint: proxyURL + "/mcp",
	}
	mcpClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "remote-dev-test", Version: "v0.0.1"}, nil)

	callCtx, callCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer callCancel()

	session, err := mcpClient.Connect(callCtx, mcpTransport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	// ── 11. Verify tool listing ───────────────────────────────────────────────

	toolsResult, err := session.ListTools(callCtx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	t.Logf("exposed %d tools: %v", len(toolsResult.Tools), toolNames(toolsResult.Tools))

	expectedTools := []string{
		"dev__write_file",
		"dev__read_file",
		"dev__list_files",
		"dev__search_files",
		"dev__run_lint",
		"dev__run_build",
	}
	toolSet := make(map[string]bool, len(toolsResult.Tools))
	for _, tool := range toolsResult.Tools {
		toolSet[tool.Name] = true
	}
	for _, want := range expectedTools {
		if !toolSet[want] {
			t.Errorf("expected tool %q not found in tool listing", want)
		}
	}

	// ── 12. Write a file ─────────────────────────────────────────────────────

	t.Log("calling dev__write_file to create /workspace/hello.go")
	writeResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name: "dev__write_file",
		Arguments: map[string]any{
			"path":    "/workspace/hello.go",
			"content": helloGoContent,
		},
	})
	if err != nil {
		t.Fatalf("call dev__write_file: %v", err)
	}
	if writeResult.IsError {
		t.Fatalf("dev__write_file returned error: %s", contentText(writeResult.Content))
	}
	t.Log("write_file succeeded")

	// ── 13. Read the file back ────────────────────────────────────────────────

	t.Log("calling dev__read_file to verify /workspace/hello.go")
	readResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name:      "dev__read_file",
		Arguments: map[string]any{"path": "/workspace/hello.go"},
	})
	if err != nil {
		t.Fatalf("call dev__read_file: %v", err)
	}
	if readResult.IsError {
		t.Fatalf("dev__read_file returned error: %s", contentText(readResult.Content))
	}
	readText := contentText(readResult.Content)
	if !strings.Contains(readText, "hello from write_file test") {
		t.Errorf("read_file content does not contain expected string; got: %s", readText)
	}
	t.Logf("read_file content verified")

	// ── 14. List files ────────────────────────────────────────────────────────

	t.Log("calling dev__list_files to list *.go files in /workspace")
	listResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name: "dev__list_files",
		Arguments: map[string]any{
			"directory": "/workspace",
			"pattern":   "*.go",
		},
	})
	if err != nil {
		t.Fatalf("call dev__list_files: %v", err)
	}
	if listResult.IsError {
		t.Fatalf("dev__list_files returned error: %s", contentText(listResult.Content))
	}
	listText := contentText(listResult.Content)
	if !strings.Contains(listText, "hello.go") {
		t.Errorf("list_files output does not contain hello.go; got: %s", listText)
	}
	t.Logf("list_files output: %s", listText)

	// ── 15. Search files ──────────────────────────────────────────────────────

	t.Log("calling dev__search_files for 'hello from write_file test' in /workspace")
	searchResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name: "dev__search_files",
		Arguments: map[string]any{
			"pattern":   "hello from write_file test",
			"directory": "/workspace",
		},
	})
	if err != nil {
		t.Fatalf("call dev__search_files: %v", err)
	}
	if searchResult.IsError {
		t.Fatalf("dev__search_files returned error: %s", contentText(searchResult.Content))
	}
	searchText := contentText(searchResult.Content)
	if !strings.Contains(searchText, "hello.go") {
		t.Errorf("search_files did not find pattern in hello.go; got: %s", searchText)
	}
	t.Log("search_files found expected match")

	// ── 16. Run build (expect success) ────────────────────────────────────────

	t.Log("calling dev__run_build with ./...")
	buildResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name:      "dev__run_build",
		Arguments: map[string]any{"path": "./..."},
	})
	if err != nil {
		t.Fatalf("call dev__run_build: %v", err)
	}
	if buildResult.IsError {
		t.Fatalf("dev__run_build returned unexpected error: %s", contentText(buildResult.Content))
	}
	t.Log("run_build succeeded (no build errors)")

	// ── 17. Run lint (expect failure on pre-seeded issue) ────────────────────

	t.Log("calling dev__run_lint with ./... (expect lint issues from main.go)")
	lintResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name:      "dev__run_lint",
		Arguments: map[string]any{"path": "./..."},
	})
	if err != nil {
		t.Fatalf("call dev__run_lint: %v", err)
	}
	if !lintResult.IsError {
		t.Fatalf("dev__run_lint expected to return lint issues but IsError=false; output: %s",
			contentText(lintResult.Content))
	}
	lintText := contentText(lintResult.Content)
	t.Logf("lint issues (expected): %s", lintText)

	// ── 18. Fix lint issue via write_file ─────────────────────────────────────

	t.Log("calling dev__write_file to fix main.go lint issue")
	fixResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name: "dev__write_file",
		Arguments: map[string]any{
			"path":    "/workspace/main.go",
			"content": mainGoFixed,
		},
	})
	if err != nil {
		t.Fatalf("call dev__write_file (fix): %v", err)
	}
	if fixResult.IsError {
		t.Fatalf("dev__write_file (fix) returned error: %s", contentText(fixResult.Content))
	}

	t.Log("calling dev__run_lint after fix (expect success)")
	lintFixed, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name:      "dev__run_lint",
		Arguments: map[string]any{"path": "./..."},
	})
	if err != nil {
		t.Fatalf("call dev__run_lint (after fix): %v", err)
	}
	if lintFixed.IsError {
		t.Fatalf("dev__run_lint still reports issues after fix: %s", contentText(lintFixed.Content))
	}
	t.Log("lint passes after fix")

	// ── 19. Path traversal rejection ─────────────────────────────────────────

	t.Log("calling dev__write_file with path traversal (expect error)")
	traversalResult, err := session.CallTool(callCtx, &sdkmcp.CallToolParams{
		Name: "dev__write_file",
		Arguments: map[string]any{
			"path":    "../../../etc/passwd",
			"content": "malicious content",
		},
	})
	if err != nil {
		t.Fatalf("call dev__write_file (traversal): %v", err)
	}
	if !traversalResult.IsError {
		t.Fatalf("dev__write_file with path traversal should have returned error, but succeeded")
	}
	t.Logf("path traversal correctly rejected: %s", contentText(traversalResult.Content))
}

// buildRemoteDevUpstream constructs the MCPUpstream CRD for the remote-dev test.
func buildRemoteDevUpstream() *v1alpha1.MCPUpstream {
	return &v1alpha1.MCPUpstream{
		ObjectMeta: metav1.ObjectMeta{
			Name:      remoteDevUpstream,
			Namespace: remoteDevNamespace,
			Labels: map[string]string{
				"mcp-anything.ai/proxy": remoteDevProxyName,
			},
		},
		Spec: v1alpha1.MCPUpstreamSpec{
			Type:       "command",
			ToolPrefix: "dev",
			Commands: []v1alpha1.MCPUpstreamCommandSpec{
				{
					ToolName:    "write_file",
					Description: "Write content to a file in the workspace",
					Command:     "/usr/local/bin/write-file {{.path}} {{.content}}",
					Timeout:     "10s",
					InputSchema: &v1alpha1.MCPUpstreamCommandInputSchema{
						Type: "object",
						Properties: map[string]v1alpha1.MCPUpstreamCommandSchemaProperty{
							"path": {
								Type:        "string",
								Description: "File path relative to /workspace (or absolute path under /workspace)",
							},
							"content": {
								Type:        "string",
								Description: "File content to write",
							},
						},
						Required: []string{"path", "content"},
					},
				},
				{
					ToolName:    "read_file",
					Description: "Read the contents of a file",
					Command:     "cat {{.path}}",
					Timeout:     "10s",
					InputSchema: &v1alpha1.MCPUpstreamCommandInputSchema{
						Type: "object",
						Properties: map[string]v1alpha1.MCPUpstreamCommandSchemaProperty{
							"path": {
								Type:        "string",
								Description: "Absolute file path to read",
							},
						},
						Required: []string{"path"},
					},
				},
				{
					ToolName:    "list_files",
					Description: "List files matching a pattern in a directory",
					Command:     "find {{.directory}} -type f -name {{.pattern}}",
					Timeout:     "10s",
					InputSchema: &v1alpha1.MCPUpstreamCommandInputSchema{
						Type: "object",
						Properties: map[string]v1alpha1.MCPUpstreamCommandSchemaProperty{
							"directory": {
								Type:        "string",
								Description: "Directory to search in",
							},
							"pattern": {
								Type:        "string",
								Description: "File name glob pattern (e.g. *.go)",
							},
						},
						Required: []string{"directory", "pattern"},
					},
				},
				{
					ToolName:    "search_files",
					Description: "Search file contents for a pattern (grep)",
					Command:     "grep -rn {{.pattern}} {{.directory}}",
					Timeout:     "30s",
					InputSchema: &v1alpha1.MCPUpstreamCommandInputSchema{
						Type: "object",
						Properties: map[string]v1alpha1.MCPUpstreamCommandSchemaProperty{
							"pattern": {
								Type:        "string",
								Description: "Search pattern (fixed string or regex)",
							},
							"directory": {
								Type:        "string",
								Description: "Directory to search in",
							},
						},
						Required: []string{"pattern", "directory"},
					},
				},
				{
					ToolName:    "run_lint",
					Description: "Run Go linter and return diagnostics",
					Command:     "golangci-lint run {{.path}}",
					WorkingDir:  "/workspace",
					Timeout:     "60s",
					InputSchema: &v1alpha1.MCPUpstreamCommandInputSchema{
						Type: "object",
						Properties: map[string]v1alpha1.MCPUpstreamCommandSchemaProperty{
							"path": {
								Type:        "string",
								Description: "Package path to lint (e.g. ./...)",
							},
						},
						Required: []string{"path"},
					},
				},
				{
					ToolName:    "run_build",
					Description: "Run Go build and return errors",
					Command:     "go build {{.path}}",
					WorkingDir:  "/workspace",
					Timeout:     "60s",
					InputSchema: &v1alpha1.MCPUpstreamCommandInputSchema{
						Type: "object",
						Properties: map[string]v1alpha1.MCPUpstreamCommandSchemaProperty{
							"path": {
								Type:        "string",
								Description: "Package path to build (e.g. ./...)",
							},
						},
						Required: []string{"path"},
					},
				},
			},
		},
	}
}
