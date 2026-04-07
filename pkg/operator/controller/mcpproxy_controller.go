// Package controller contains controller-runtime reconcilers for mcp-anything CRDs.
package controller

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/gaarutyunov/mcp-anything/pkg/crd/v1alpha1"
	"github.com/gaarutyunov/mcp-anything/pkg/operator/configgen"
)

const (
	defaultProxyImage           = "ghcr.io/gaarutyunov/mcp-anything:latest"
	defaultProxyPort            = int32(8080)
	configMountPath             = "/etc/mcp-anything/config.yaml"
	specsMountPath              = "/etc/mcp-anything/specs"
	overlaysMountPath           = "/etc/mcp-anything/overlays"
	configVolumeName            = "proxy-config"
	specsVolumeName             = "upstream-specs"
	overlaysVolumeName          = "upstream-overlays"
	conditionTypeReady          = "Ready"
	conditionTypeReconciled     = "Reconciled"
	conditionTypePrefixConflict = "PrefixConflict"
	periodicSyncInterval        = 5 * time.Minute
)

// MCPProxyReconciler reconciles MCPProxy resources.
type MCPProxyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Reconcile implements reconcile.Reconciler for MCPProxy.
func (r *MCPProxyReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := slog.Default().With("mcpproxy", req.NamespacedName)
	log.Info("reconciling MCPProxy")

	proxy := &v1alpha1.MCPProxy{}
	if err := r.Get(ctx, req.NamespacedName, proxy); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("fetching MCPProxy: %w", err)
	}

	// Collect MCPUpstream CRD resources from configured namespaces.
	crdUpstreams, err := r.listUpstreams(ctx, proxy)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("listing upstreams: %w", err)
	}

	// Collect synthetic upstreams from annotated Services.
	svcUpstreams, err := r.listAnnotatedServices(ctx, proxy)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("listing annotated services: %w", err)
	}

	// Detect prefix conflicts — reported in status but do not halt reconciliation.
	conflicts := detectPrefixConflicts(crdUpstreams, svcUpstreams)
	if len(conflicts) > 0 {
		log.Warn("prefix conflicts detected", "conflicts", conflicts)
	}

	// Merge CRD and annotation-based upstreams.
	allUpstreams := append(crdUpstreams, svcUpstreams...)

	// Generate config YAML.
	configData, err := configgen.Generate(ctx, proxy, allUpstreams)
	if err != nil {
		if setErr := r.setCondition(ctx, proxy, conditionTypeReconciled, metav1.ConditionFalse, "ConfigGenFailed", err.Error()); setErr != nil {
			log.Error("setting condition", "error", setErr)
		}
		return reconcile.Result{}, fmt.Errorf("generating config: %w", err)
	}

	// Reconcile main config ConfigMap.
	if err := r.reconcileConfigMap(ctx, proxy, configData); err != nil {
		return reconcile.Result{}, fmt.Errorf("reconciling config configmap: %w", err)
	}

	// Reconcile per-upstream spec/overlay ConfigMaps.
	if err := r.reconcileUpstreamConfigMaps(ctx, proxy, allUpstreams); err != nil {
		return reconcile.Result{}, fmt.Errorf("reconciling upstream configmaps: %w", err)
	}

	// Reconcile Deployment.
	if err := r.reconcileDeployment(ctx, proxy, allUpstreams); err != nil {
		return reconcile.Result{}, fmt.Errorf("reconciling deployment: %w", err)
	}

	// Reconcile Service.
	if err := r.reconcileService(ctx, proxy); err != nil {
		return reconcile.Result{}, fmt.Errorf("reconciling service: %w", err)
	}

	// Update status (including prefix conflict condition) in a single API call.
	if err := r.updateStatus(ctx, proxy, crdUpstreams, svcUpstreams, conflicts); err != nil {
		return reconcile.Result{}, fmt.Errorf("updating status: %w", err)
	}

	log.Info("MCPProxy reconciled",
		"crd_upstream_count", len(crdUpstreams),
		"annotated_service_count", len(svcUpstreams),
	)
	return reconcile.Result{RequeueAfter: periodicSyncInterval}, nil
}

// listUpstreams returns all MCPUpstream resources that match the proxy's selector.
func (r *MCPProxyReconciler) listUpstreams(ctx context.Context, proxy *v1alpha1.MCPProxy) ([]v1alpha1.MCPUpstream, error) {
	selector, err := metav1.LabelSelectorAsSelector(&proxy.Spec.UpstreamSelector)
	if err != nil {
		return nil, fmt.Errorf("parsing upstream selector: %w", err)
	}

	namespaces := proxy.Spec.NamespaceSelector.MatchNames
	if len(namespaces) == 0 {
		namespaces = []string{proxy.Namespace}
	}

	var all []v1alpha1.MCPUpstream
	for _, ns := range namespaces {
		list := &v1alpha1.MCPUpstreamList{}
		if err := r.List(ctx, list,
			client.InNamespace(ns),
			client.MatchingLabelsSelector{Selector: selector},
		); err != nil {
			return nil, fmt.Errorf("listing upstreams in namespace %s: %w", ns, err)
		}
		all = append(all, list.Items...)
	}
	return all, nil
}

// reconcileConfigMap creates or updates the main proxy config ConfigMap.
func (r *MCPProxyReconciler) reconcileConfigMap(ctx context.Context, proxy *v1alpha1.MCPProxy, configData []byte) error {
	name := proxy.Name + "-config"
	cm := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: proxy.Namespace}, cm)
	if apierrors.IsNotFound(err) {
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: proxy.Namespace,
			},
			Data: map[string]string{
				"config.yaml": string(configData),
			},
		}
		if setErr := ctrl.SetControllerReference(proxy, cm, r.Scheme); setErr != nil {
			return fmt.Errorf("setting owner reference: %w", setErr)
		}
		if createErr := r.Create(ctx, cm); createErr != nil {
			return fmt.Errorf("creating config configmap: %w", createErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("fetching config configmap: %w", err)
	}

	newData := map[string]string{"config.yaml": string(configData)}
	if reflect.DeepEqual(cm.Data, newData) {
		slog.Debug("config ConfigMap unchanged, skipping update", "name", name)
		return nil
	}
	cm.Data = newData
	if updateErr := r.Update(ctx, cm); updateErr != nil {
		return fmt.Errorf("updating config configmap: %w", updateErr)
	}
	return nil
}

// reconcileUpstreamConfigMaps copies OpenAPI spec and overlay data from source ConfigMaps
// (potentially in other namespaces) into local ConfigMaps mounted by the proxy pod.
func (r *MCPProxyReconciler) reconcileUpstreamConfigMaps(ctx context.Context, proxy *v1alpha1.MCPProxy, upstreams []v1alpha1.MCPUpstream) error {
	for i := range upstreams {
		up := &upstreams[i]
		if err := r.reconcileUpstreamSpecCM(ctx, proxy, up); err != nil {
			return err
		}
		if err := r.reconcileUpstreamOverlayCM(ctx, proxy, up); err != nil {
			return err
		}
	}
	return nil
}

func (r *MCPProxyReconciler) reconcileUpstreamSpecCM(ctx context.Context, proxy *v1alpha1.MCPProxy, up *v1alpha1.MCPUpstream) error {
	ref := up.Spec.OpenAPI.ConfigMapRef
	if ref == nil {
		return nil
	}

	src := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: up.Namespace}, src); err != nil {
		return fmt.Errorf("fetching source openapi configmap %s/%s: %w", up.Namespace, ref.Name, err)
	}

	value, ok := src.Data[ref.Key]
	if !ok {
		return fmt.Errorf("key %q not found in configmap %s/%s", ref.Key, up.Namespace, ref.Name)
	}

	destName := proxy.Name + "-specs"
	fileKey := fmt.Sprintf("%s_%s.yaml", up.Namespace, up.Name)
	return r.upsertConfigMap(ctx, proxy, destName, proxy.Namespace, fileKey, value)
}

func (r *MCPProxyReconciler) reconcileUpstreamOverlayCM(ctx context.Context, proxy *v1alpha1.MCPProxy, up *v1alpha1.MCPUpstream) error {
	if up.Spec.Overlay == nil || up.Spec.Overlay.ConfigMapRef == nil {
		return nil
	}
	ref := up.Spec.Overlay.ConfigMapRef

	src := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: up.Namespace}, src); err != nil {
		return fmt.Errorf("fetching source overlay configmap %s/%s: %w", up.Namespace, ref.Name, err)
	}

	value, ok := src.Data[ref.Key]
	if !ok {
		return fmt.Errorf("key %q not found in configmap %s/%s", ref.Key, up.Namespace, ref.Name)
	}

	destName := proxy.Name + "-overlays"
	fileKey := fmt.Sprintf("%s_%s.yaml", up.Namespace, up.Name)
	return r.upsertConfigMap(ctx, proxy, destName, proxy.Namespace, fileKey, value)
}

func (r *MCPProxyReconciler) upsertConfigMap(ctx context.Context, proxy *v1alpha1.MCPProxy, name, namespace, key, value string) error {
	cm := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cm)
	if apierrors.IsNotFound(err) {
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Data: map[string]string{key: value},
		}
		if setErr := ctrl.SetControllerReference(proxy, cm, r.Scheme); setErr != nil {
			return fmt.Errorf("setting owner reference on configmap %s: %w", name, setErr)
		}
		if createErr := r.Create(ctx, cm); createErr != nil {
			return fmt.Errorf("creating configmap %s: %w", name, createErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("fetching configmap %s: %w", name, err)
	}

	if cm.Data != nil && cm.Data[key] == value {
		slog.Debug("ConfigMap key unchanged, skipping update", "name", name, "key", key)
		return nil
	}
	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	cm.Data[key] = value
	if updateErr := r.Update(ctx, cm); updateErr != nil {
		return fmt.Errorf("updating configmap %s: %w", name, updateErr)
	}
	return nil
}

// reconcileDeployment creates or updates the proxy Deployment.
func (r *MCPProxyReconciler) reconcileDeployment(ctx context.Context, proxy *v1alpha1.MCPProxy, upstreams []v1alpha1.MCPUpstream) error {
	desired := r.buildDeployment(proxy, upstreams)

	existing := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: proxy.Name, Namespace: proxy.Namespace}, existing)
	if apierrors.IsNotFound(err) {
		if createErr := r.Create(ctx, desired); createErr != nil {
			return fmt.Errorf("creating deployment: %w", createErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("fetching deployment: %w", err)
	}

	if equality.Semantic.DeepEqual(existing.Spec, desired.Spec) {
		slog.Debug("Deployment spec unchanged, skipping update", "name", proxy.Name)
		return nil
	}
	existing.Spec = desired.Spec
	if updateErr := r.Update(ctx, existing); updateErr != nil {
		return fmt.Errorf("updating deployment: %w", updateErr)
	}
	return nil
}

func (r *MCPProxyReconciler) buildDeployment(proxy *v1alpha1.MCPProxy, upstreams []v1alpha1.MCPUpstream) *appsv1.Deployment {
	image := proxy.Spec.Image
	if image == "" {
		image = defaultProxyImage
	}

	port := proxy.Spec.Server.Port
	if port == 0 {
		port = defaultProxyPort
	}

	replicas := int32(1)
	if proxy.Spec.Replicas != nil {
		replicas = *proxy.Spec.Replicas
	}

	podLabels := map[string]string{
		"app.kubernetes.io/name":      "mcp-anything",
		"app.kubernetes.io/instance":  proxy.Name,
		"app.kubernetes.io/component": "proxy",
	}

	// Build volumes and mounts.
	volumes := []corev1.Volume{
		{
			Name: configVolumeName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: proxy.Name + "-config",
					},
				},
			},
		},
	}
	volumeMounts := []corev1.VolumeMount{
		{
			Name:      configVolumeName,
			MountPath: "/etc/mcp-anything",
			SubPath:   "",
		},
	}

	// Add specs volume if any upstream uses a configMapRef for OpenAPI.
	hasSpecs := false
	hasOverlays := false
	for i := range upstreams {
		if upstreams[i].Spec.OpenAPI.ConfigMapRef != nil {
			hasSpecs = true
		}
		if upstreams[i].Spec.Overlay != nil && upstreams[i].Spec.Overlay.ConfigMapRef != nil {
			hasOverlays = true
		}
	}

	if hasSpecs {
		volumes = append(volumes, corev1.Volume{
			Name: specsVolumeName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: proxy.Name + "-specs",
					},
				},
			},
		})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      specsVolumeName,
			MountPath: specsMountPath,
		})
	}

	if hasOverlays {
		volumes = append(volumes, corev1.Volume{
			Name: overlaysVolumeName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: proxy.Name + "-overlays",
					},
				},
			},
		})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      overlaysVolumeName,
			MountPath: overlaysMountPath,
		})
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proxy.Name,
			Namespace: proxy.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: podLabels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: podLabels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "proxy",
							Image: image,
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: port,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							Env: []corev1.EnvVar{
								{
									Name:  "CONFIG_PATH",
									Value: configMountPath,
								},
							},
							VolumeMounts: volumeMounts,
							Resources:    proxy.Spec.Resources,
						},
					},
					Volumes: volumes,
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(proxy, dep, r.Scheme); err != nil {
		slog.Error("setting owner reference on deployment", "error", err)
	}
	return dep
}

// reconcileService creates or updates the proxy Service.
func (r *MCPProxyReconciler) reconcileService(ctx context.Context, proxy *v1alpha1.MCPProxy) error {
	desired := r.buildService(proxy)

	existing := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: proxy.Name, Namespace: proxy.Namespace}, existing)
	if apierrors.IsNotFound(err) {
		if createErr := r.Create(ctx, desired); createErr != nil {
			return fmt.Errorf("creating service: %w", createErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("fetching service: %w", err)
	}

	if equality.Semantic.DeepEqual(existing.Spec.Ports, desired.Spec.Ports) &&
		reflect.DeepEqual(existing.Spec.Selector, desired.Spec.Selector) {
		slog.Debug("Service spec unchanged, skipping update", "name", proxy.Name)
		return nil
	}
	existing.Spec.Ports = desired.Spec.Ports
	existing.Spec.Selector = desired.Spec.Selector
	if updateErr := r.Update(ctx, existing); updateErr != nil {
		return fmt.Errorf("updating service: %w", updateErr)
	}
	return nil
}

func (r *MCPProxyReconciler) buildService(proxy *v1alpha1.MCPProxy) *corev1.Service {
	port := proxy.Spec.Server.Port
	if port == 0 {
		port = defaultProxyPort
	}

	selectorLabels := map[string]string{
		"app.kubernetes.io/name":      "mcp-anything",
		"app.kubernetes.io/instance":  proxy.Name,
		"app.kubernetes.io/component": "proxy",
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proxy.Name,
			Namespace: proxy.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: selectorLabels,
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       port,
					TargetPort: intstr.FromString("http"),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(proxy, svc, r.Scheme); err != nil {
		slog.Error("setting owner reference on service", "error", err)
	}
	return svc
}

// updateStatus writes all status fields and conditions back to the API server in one call,
// avoiding "object has been modified" conflicts from multiple Status().Update() calls.
func (r *MCPProxyReconciler) updateStatus(ctx context.Context, proxy *v1alpha1.MCPProxy, crdUpstreams, svcUpstreams []v1alpha1.MCPUpstream, conflicts []string) error {
	totalCount := len(crdUpstreams) + len(svcUpstreams)
	reconciledMsg := fmt.Sprintf("proxy configured with %d upstream(s) (%d CRD, %d annotated service)",
		totalCount, len(crdUpstreams), len(svcUpstreams))

	// Build the desired conflict condition.
	var conflictCond metav1.Condition
	if len(conflicts) > 0 {
		conflictCond = metav1.Condition{
			Type:               conditionTypePrefixConflict,
			Status:             metav1.ConditionTrue,
			Reason:             "PrefixConflict",
			Message:            fmt.Sprintf("prefix conflicts: %s", strings.Join(conflicts, "; ")),
			ObservedGeneration: proxy.Generation,
		}
	} else {
		conflictCond = metav1.Condition{
			Type:               conditionTypePrefixConflict,
			Status:             metav1.ConditionFalse,
			Reason:             "NoPrefixConflict",
			Message:            "no prefix conflicts",
			ObservedGeneration: proxy.Generation,
		}
	}

	currentReconciled := apimeta.FindStatusCondition(proxy.Status.Conditions, conditionTypeReconciled)
	currentConflict := apimeta.FindStatusCondition(proxy.Status.Conditions, conditionTypePrefixConflict)

	unchanged := proxy.Status.UpstreamCount == len(crdUpstreams) &&
		proxy.Status.AnnotatedServiceCount == len(svcUpstreams) &&
		proxy.Status.ObservedGeneration == proxy.Generation &&
		currentReconciled != nil &&
		currentReconciled.Status == metav1.ConditionTrue &&
		currentReconciled.Message == reconciledMsg &&
		currentConflict != nil &&
		currentConflict.Status == conflictCond.Status &&
		currentConflict.Message == conflictCond.Message

	if unchanged {
		slog.Debug("MCPProxy status unchanged, skipping update", "name", proxy.Name)
		return nil
	}

	proxy.Status.UpstreamCount = len(crdUpstreams)
	proxy.Status.AnnotatedServiceCount = len(svcUpstreams)
	proxy.Status.ObservedGeneration = proxy.Generation

	apimeta.SetStatusCondition(&proxy.Status.Conditions, metav1.Condition{
		Type:               conditionTypeReconciled,
		Status:             metav1.ConditionTrue,
		Reason:             "Reconciled",
		Message:            reconciledMsg,
		ObservedGeneration: proxy.Generation,
	})
	apimeta.SetStatusCondition(&proxy.Status.Conditions, conflictCond)

	if err := r.Status().Update(ctx, proxy); err != nil {
		return fmt.Errorf("updating MCPProxy status: %w", err)
	}
	return nil
}

func (r *MCPProxyReconciler) setCondition(ctx context.Context, proxy *v1alpha1.MCPProxy, condType string, status metav1.ConditionStatus, reason, msg string) error {
	apimeta.SetStatusCondition(&proxy.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: proxy.Generation,
	})
	if err := r.Status().Update(ctx, proxy); err != nil {
		return fmt.Errorf("updating status condition: %w", err)
	}
	return nil
}

// listAnnotatedServices returns synthetic MCPUpstream objects built from Services
// that carry the mcp-anything.ai/enabled=true annotation and are matched by the
// proxy's serviceDiscovery configuration.
func (r *MCPProxyReconciler) listAnnotatedServices(ctx context.Context, proxy *v1alpha1.MCPProxy) ([]v1alpha1.MCPUpstream, error) {
	if proxy.Spec.ServiceDiscovery == nil || !proxy.Spec.ServiceDiscovery.Enabled {
		return nil, nil
	}

	namespaces := serviceDiscoveryNamespaces(proxy)

	var synthetic []v1alpha1.MCPUpstream
	for _, ns := range namespaces {
		svcList := &corev1.ServiceList{}
		if err := r.List(ctx, svcList, client.InNamespace(ns)); err != nil {
			return nil, fmt.Errorf("listing services in namespace %s: %w", ns, err)
		}
		for i := range svcList.Items {
			svc := &svcList.Items[i]
			if svc.Annotations[AnnotationEnabled] != "true" {
				continue
			}
			if !serviceMatchesProxy(svc, proxy) {
				continue
			}
			up, err := serviceToMCPUpstream(svc)
			if err != nil {
				slog.Warn("skipping annotated service",
					"service", svc.Namespace+"/"+svc.Name, "error", err)
				continue
			}
			synthetic = append(synthetic, *up)
		}
	}
	return synthetic, nil
}

// serviceDiscoveryNamespaces returns the list of namespaces to scan for annotated Services.
func serviceDiscoveryNamespaces(proxy *v1alpha1.MCPProxy) []string {
	if proxy.Spec.ServiceDiscovery == nil || proxy.Spec.ServiceDiscovery.NamespaceSelector == nil {
		// Fall back to the upstream namespace selector.
		if len(proxy.Spec.NamespaceSelector.MatchNames) > 0 {
			return proxy.Spec.NamespaceSelector.MatchNames
		}
		return []string{proxy.Namespace}
	}
	if len(proxy.Spec.ServiceDiscovery.NamespaceSelector.MatchNames) > 0 {
		return proxy.Spec.ServiceDiscovery.NamespaceSelector.MatchNames
	}
	return []string{proxy.Namespace}
}

// serviceMatchesProxy returns true if the Service should be picked up by the given proxy.
// A Service matches if it has no mcp-anything.ai/proxy annotation (picked up by any proxy)
// OR if the annotation value equals the proxy name.
func serviceMatchesProxy(svc *corev1.Service, proxy *v1alpha1.MCPProxy) bool {
	proxyAnnotation, hasProxyAnnotation := svc.Annotations[AnnotationProxy]
	if !hasProxyAnnotation {
		return true
	}
	return proxyAnnotation == proxy.Name
}

// detectPrefixConflicts returns a list of human-readable conflict descriptions for any
// tool prefix that is claimed by more than one upstream (across CRD and annotated sources).
func detectPrefixConflicts(crdUpstreams, svcUpstreams []v1alpha1.MCPUpstream) []string {
	type sourceInfo struct {
		id     string
		source string
	}
	seen := make(map[string]sourceInfo)
	var conflicts []string

	record := func(up *v1alpha1.MCPUpstream, source string) {
		if up.Spec.ToolPrefix == "" {
			return
		}
		key := up.Spec.ToolPrefix
		id := up.Namespace + "/" + up.Name
		if existing, ok := seen[key]; ok {
			conflicts = append(conflicts, fmt.Sprintf("prefix %q: %s (%s) vs %s (%s)",
				key, existing.id, existing.source, id, source))
		} else {
			seen[key] = sourceInfo{id: id, source: source}
		}
	}

	for i := range crdUpstreams {
		record(&crdUpstreams[i], "crd")
	}
	for i := range svcUpstreams {
		record(&svcUpstreams[i], "service")
	}
	return conflicts
}

// proxiesForAnnotatedService returns reconcile.Requests for all MCPProxy instances that
// should discover the given annotated Service.
func (r *MCPProxyReconciler) proxiesForAnnotatedService(ctx context.Context, svc *corev1.Service) []reconcile.Request {
	proxyList := &v1alpha1.MCPProxyList{}
	if err := r.List(ctx, proxyList); err != nil {
		slog.Error("listing MCPProxy for service trigger", "error", err)
		return nil
	}

	var requests []reconcile.Request
	for i := range proxyList.Items {
		proxy := &proxyList.Items[i]
		if proxy.Spec.ServiceDiscovery == nil || !proxy.Spec.ServiceDiscovery.Enabled {
			continue
		}
		namespaces := serviceDiscoveryNamespaces(proxy)
		for _, ns := range namespaces {
			if ns != svc.Namespace {
				continue
			}
			if !serviceMatchesProxy(svc, proxy) {
				break
			}
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      proxy.Name,
					Namespace: proxy.Namespace,
				},
			})
			break
		}
	}
	return requests
}

// SetupWithManager registers the reconciler with a controller-runtime Manager.
func (r *MCPProxyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// When an MCPUpstream changes, enqueue its owning proxies.
	mapUpstreamToProxy := handler.MapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		upstream, ok := obj.(*v1alpha1.MCPUpstream)
		if !ok {
			return nil
		}
		return r.proxiesForUpstream(ctx, upstream)
	})

	// When an annotated Service changes, enqueue the proxies that should discover it.
	mapServiceToProxy := handler.MapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		svc, ok := obj.(*corev1.Service)
		if !ok {
			return nil
		}
		if svc.Annotations[AnnotationEnabled] != "true" {
			return nil
		}
		return r.proxiesForAnnotatedService(ctx, svc)
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.MCPProxy{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Service{}).
		Watches(&v1alpha1.MCPUpstream{},
			handler.EnqueueRequestsFromMapFunc(mapUpstreamToProxy),
			builder.WithPredicates(predicate.GenerationChangedPredicate{}),
		).
		Watches(&corev1.Service{},
			handler.EnqueueRequestsFromMapFunc(mapServiceToProxy),
		).
		Complete(r)
}

// proxiesForUpstream returns reconcile.Requests for all MCPProxy instances whose
// upstreamSelector matches the given upstream's labels.
func (r *MCPProxyReconciler) proxiesForUpstream(ctx context.Context, upstream *v1alpha1.MCPUpstream) []reconcile.Request {
	proxyList := &v1alpha1.MCPProxyList{}
	if err := r.List(ctx, proxyList); err != nil {
		slog.Error("listing MCPProxy for upstream trigger", "error", err)
		return nil
	}

	var requests []reconcile.Request
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
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      proxy.Name,
						Namespace: proxy.Namespace,
					},
				})
				break
			}
		}
	}
	return requests
}
