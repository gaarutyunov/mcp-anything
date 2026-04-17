// Package main is the entry point for the mcp-anything Kubernetes operator.
package main

import (
	"flag"
	"log/slog"
	"os"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/gaarutyunov/mcp-anything/pkg/crd/v1alpha1"
	"github.com/gaarutyunov/mcp-anything/pkg/operator/controller"
	operatorwebhook "github.com/gaarutyunov/mcp-anything/pkg/operator/webhook"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	utilruntime.Must(gatewayv1.Install(scheme))
}

func main() {
	var (
		metricsAddr          string
		healthProbeAddr      string
		enableLeaderElection bool
		namespace            string
		enableWebhooks       bool
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8443", "The address the metrics endpoint binds to.")
	flag.StringVar(&healthProbeAddr, "health-probe-bind-address", ":8081", "The address the health probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for the operator. Ensures only one active controller instance at a time.")
	flag.StringVar(&namespace, "namespace", "", "Restrict the operator to a single namespace. Empty means all namespaces.")
	flag.BoolVar(&enableWebhooks, "enable-webhooks", false, "Enable validating admission webhooks.")
	flag.Parse()

	mgrOpts := ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: healthProbeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "mcp-anything-operator.mcp-anything.ai",
	}
	if namespace != "" {
		mgrOpts.Cache.DefaultNamespaces = map[string]cache.Config{
			namespace: {},
		}
	}
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), mgrOpts)
	if err != nil {
		slog.Error("creating manager", "error", err)
		os.Exit(1)
	}

	if err := (&controller.MCPProxyReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		slog.Error("setting up MCPProxy controller", "error", err)
		os.Exit(1)
	}

	if err := (&controller.MCPUpstreamReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		slog.Error("setting up MCPUpstream controller", "error", err)
		os.Exit(1)
	}

	if enableWebhooks {
		hookServer := mgr.GetWebhookServer()

		hookServer.Register("/validate-mcp-anything-ai-v1alpha1-mcpproxy",
			admission.WithValidator[*v1alpha1.MCPProxy](mgr.GetScheme(), &operatorwebhook.MCPProxyValidator{}),
		)

		hookServer.Register("/validate-mcp-anything-ai-v1alpha1-mcpupstream",
			admission.WithValidator[*v1alpha1.MCPUpstream](mgr.GetScheme(), &operatorwebhook.MCPUpstreamValidator{}),
		)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		slog.Error("setting up healthz check", "error", err)
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		slog.Error("setting up readyz check", "error", err)
		os.Exit(1)
	}

	slog.Info("starting mcp-anything operator",
		"metrics_addr", metricsAddr,
		"health_probe_addr", healthProbeAddr,
		"leader_elect", enableLeaderElection,
		"namespace", namespace,
		"webhooks", enableWebhooks,
	)

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		slog.Error("running manager", "error", err)
		os.Exit(1)
	}
}
