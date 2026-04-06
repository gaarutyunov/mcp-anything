package controller

import (
	"context"
	"fmt"
	"log/slog"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/gaarutyunov/mcp-anything/pkg/crd/v1alpha1"
)

// MCPUpstreamReconciler reconciles MCPUpstream resources and enqueues their owning MCPProxy instances.
type MCPUpstreamReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Reconcile implements reconcile.Reconciler for MCPUpstream.
func (r *MCPUpstreamReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := slog.Default().With("mcpupstream", req.NamespacedName)
	log.Info("reconciling MCPUpstream")

	upstream := &v1alpha1.MCPUpstream{}
	if err := r.Get(ctx, req.NamespacedName, upstream); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("fetching MCPUpstream: %w", err)
	}

	// Find all MCPProxy instances whose selector matches this upstream.
	assignedProxy, err := r.findAssignedProxy(ctx, upstream)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("finding assigned proxy: %w", err)
	}

	// Update status with the assigned proxy name.
	upstream.Status.AssignedProxy = assignedProxy
	apimeta.SetStatusCondition(&upstream.Status.Conditions, metav1.Condition{
		Type:               conditionTypeReconciled,
		Status:             metav1.ConditionTrue,
		Reason:             "Reconciled",
		Message:            fmt.Sprintf("upstream assigned to proxy %q", assignedProxy),
		ObservedGeneration: upstream.Generation,
	})

	if err := r.Status().Update(ctx, upstream); err != nil {
		return reconcile.Result{}, fmt.Errorf("updating MCPUpstream status: %w", err)
	}

	log.Info("MCPUpstream reconciled", "assigned_proxy", assignedProxy)
	return reconcile.Result{RequeueAfter: periodicSyncInterval}, nil
}

// findAssignedProxy returns the name of the first MCPProxy whose selector matches this upstream.
func (r *MCPUpstreamReconciler) findAssignedProxy(ctx context.Context, upstream *v1alpha1.MCPUpstream) (string, error) {
	proxyList := &v1alpha1.MCPProxyList{}
	if err := r.List(ctx, proxyList); err != nil {
		return "", fmt.Errorf("listing MCPProxy resources: %w", err)
	}

	for i := range proxyList.Items {
		proxy := &proxyList.Items[i]
		selector, err := metav1.LabelSelectorAsSelector(&proxy.Spec.UpstreamSelector)
		if err != nil {
			continue
		}

		namespaces := proxy.Spec.NamespaceSelector.MatchNames
		if len(namespaces) == 0 {
			namespaces = []string{proxy.Namespace}
		}

		for _, ns := range namespaces {
			if ns != upstream.Namespace {
				continue
			}
			if selector.Matches(labels.Set(upstream.Labels)) {
				return types.NamespacedName{
					Namespace: proxy.Namespace,
					Name:      proxy.Name,
				}.String(), nil
			}
		}
	}
	return "", nil
}

// SetupWithManager registers the MCPUpstream reconciler with a controller-runtime Manager.
func (r *MCPUpstreamReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.MCPUpstream{}).
		Complete(r)
}
