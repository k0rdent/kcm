// Copyright 2025
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package flux implements the KSM adapter backed by Flux: it translates a
// [github.com/K0rdent/kcm/api/v1beta1.ServiceSet] into a set of
// [github.com/fluxcd/helm-controller/api/v2.HelmRelease] objects, one per
// Helm service, and collects their state back onto the ServiceSet.
//
// The HelmRelease objects always live on the management cluster next to the
// Flux sources they consume; a service that targets a child cluster is
// reconciled remotely through .spec.kubeConfig.
package flux

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	corev1 "k8s.io/api/core/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/json"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/yaml"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/record"
	"github.com/K0rdent/kcm/internal/serviceset"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	ratelimitutil "github.com/K0rdent/kcm/internal/util/ratelimit"
)

const (
	// serviceSetNameLabel and serviceSetNamespaceLabel tie a HelmRelease back
	// to the ServiceSet it was produced from. The owner reference alone is not
	// enough: we list the releases of one ServiceSet to prune and to collect
	// state, and a label selector is the only indexed way to do that.
	serviceSetNameLabel      = "ksm.k0rdent.mirantis.com/service-set-name"
	serviceSetNamespaceLabel = "ksm.k0rdent.mirantis.com/service-set-namespace"
	serviceNameLabel         = "ksm.k0rdent.mirantis.com/service-name"
	serviceNamespaceLabel    = "ksm.k0rdent.mirantis.com/service-namespace"

	// defaultInterval is how often helm-controller re-reconciles a release.
	defaultInterval = 5 * time.Minute

	// nudgeInterval is how often a HelmRelease stuck mid-uninstall is asked to
	// reconcile again. Short enough to keep a teardown moving, long enough not
	// to patch the same object on every requeue.
	nudgeInterval = 30 * time.Second

	// defaultConcurrentReconciles matches the --max-concurrent-reconciles
	// default. It is a fallback: cmd/main.go never populates the config field
	// the flag writes to, so the reconciler would otherwise run single-worker.
	defaultConcurrentReconciles = 10
)

var errUnsupportedServiceType = errors.New("the flux adapter only supports Helm-backed ServiceTemplates")

// providerConfig is the flux-specific part of ServiceSet.spec.provider.config.
// Unknown fields are ignored on purpose: KCM fills the config with the legacy
// sveltos-shaped keys whenever the user did not set one explicitly.
type providerConfig struct {
	// Interval at which helm-controller reconciles the produced HelmReleases.
	Interval *metav1.Duration `json:"interval,omitempty"`

	// ServiceAccountName to impersonate on the target cluster.
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// MaxHistory is the number of Helm revisions kept per release.
	MaxHistory *int `json:"maxHistory,omitempty"`

	// DriftDetectionMode is one of "enabled", "warn" or "disabled".
	DriftDetectionMode string `json:"driftDetectionMode,omitempty"`
}

// ServiceSetReconciler reconciles a ServiceSet object into HelmRelease objects.
type ServiceSetReconciler struct {
	client.Client

	timeFunc func() time.Time

	SystemNamespace string

	// AdapterName is the name of the workload running the controller; it is
	// what identifies this adapter in the StateManagementProvider spec.
	AdapterName      string
	AdapterNamespace string

	MaxConcurrentReconciles int
	requeueInterval         time.Duration
}

func (r *ServiceSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	start := time.Now()
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling ServiceSet")

	serviceSet := new(kcmv1.ServiceSet)
	err = r.Get(ctx, req.NamespacedName, serviceSet)
	if apierrors.IsNotFound(err) {
		l.Info("ServiceSet not found, skipping")
		return ctrl.Result{}, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	smp := new(kcmv1.StateManagementProvider)
	if err = r.Get(ctx, client.ObjectKey{Name: serviceSet.Spec.Provider.Name}, smp); err != nil {
		// A ServiceSet of another adapter is none of our business, and neither
		// is one whose provider does not exist (yet).
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if smp.AdapterKind() != kcmv1.AdapterKindFlux {
		return ctrl.Result{}, nil
	}

	if !serviceSet.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, serviceSet)
	}

	if controllerutil.AddFinalizer(serviceSet, kcmv1.ServiceSetFinalizer) {
		return ctrl.Result{}, r.Update(ctx, serviceSet)
	}

	continueReconciliation, err := labelsMatchSelector(serviceSet.Labels, smp.Spec.Selector)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check ServiceSet labels: %w", err)
	}
	if !continueReconciliation {
		l.V(1).Info("ServiceSet labels do not match provider selector, skipping")
		return ctrl.Result{}, nil
	}

	clone := serviceSet.DeepCopy()
	defer func() {
		if err == nil {
			fillNotDeployedServices(serviceSet, r.timeFunc)
		}
		// The status is written even when reconciliation failed, so that the
		// failure is visible on the object and not only in the logs. The
		// original error is still returned, so the object is requeued with
		// respect to the rate limiter.
		if !equality.Semantic.DeepEqual(clone.Status, serviceSet.Status) {
			if updateErr := r.Status().Update(ctx, serviceSet); updateErr != nil {
				err = errors.Join(err, updateErr)
			}
		}
		l.Info("Finished reconciling ServiceSet", "duration", time.Since(start), "err", err)
	}()

	serviceSet.Status.Cluster = clusterReference(serviceSet)
	serviceSet.Status.Provider = kcmv1.ProviderState{
		Ready:     smp.Status.Ready,
		Suspended: smp.Spec.Suspend,
	}
	if !smp.Status.Ready {
		if clone.Status.Provider.Ready {
			record.Eventf(serviceSet, smp, kcmv1.StateManagementProviderNotReadyEvent, kcmv1.ServiceSetReconcileEventAction,
				"StateManagementProvider %s not ready, skipping ServiceSet %s reconciliation", smp.Name, serviceSet.Name)
		}
		l.Info("StateManagementProvider is not ready, skipping", "provider", serviceSet.Spec.Provider)
		return ctrl.Result{}, nil
	}
	if smp.Spec.Suspend {
		if !clone.Status.Provider.Suspended {
			record.Eventf(serviceSet, smp, kcmv1.StateManagementProviderSuspendedEvent, kcmv1.ServiceSetReconcileEventAction,
				"StateManagementProvider %s suspended, skipping ServiceSet %s reconciliation", smp.Name, serviceSet.Name)
		}
		l.Info("StateManagementProvider is suspended, skipping", "provider", serviceSet.Spec.Provider)
		return ctrl.Result{}, nil
	}

	if err = r.ensureHelmReleases(ctx, serviceSet); err != nil {
		conditionOldState := apimeta.FindStatusCondition(clone.Status.Conditions, kcmv1.ServiceSetProfileCondition)
		conditionNewState := apimeta.FindStatusCondition(serviceSet.Status.Conditions, kcmv1.ServiceSetProfileCondition)
		if conditionStatusChangedToFalse(conditionOldState, conditionNewState) {
			record.Warnf(serviceSet, nil, kcmv1.ServiceSetEnsureProfileFailedEvent, kcmv1.ServiceSetEnsureProfileEventAction,
				"Failed to ensure HelmReleases for ServiceSet %s: %v", serviceSet.Name, err)
		}
		return ctrl.Result{}, err
	}

	if err = r.collectServiceStatuses(ctx, serviceSet); err != nil {
		conditionOldState := apimeta.FindStatusCondition(clone.Status.Conditions, kcmv1.ServiceSetStatusesCollectedCondition)
		conditionNewState := apimeta.FindStatusCondition(serviceSet.Status.Conditions, kcmv1.ServiceSetStatusesCollectedCondition)
		if conditionStatusChangedToFalse(conditionOldState, conditionNewState) {
			record.Warnf(serviceSet, nil, kcmv1.ServiceSetCollectServiceStatusesFailedEvent, kcmv1.ServiceSetCollectServiceStatusesEventAction,
				"Failed to collect Service statuses for ServiceSet %s: %v", serviceSet.Name, err)
		}
		return ctrl.Result{}, err
	}

	// A HelmRelease that is still rolling out does not always produce a status
	// event we would see (an unchanged object is not re-sent), so poll until
	// everything settles.
	if !serviceSet.Status.Deployed {
		return ctrl.Result{RequeueAfter: r.requeueInterval}, nil
	}

	return ctrl.Result{}, nil
}

func (r *ServiceSetReconciler) reconcileDelete(ctx context.Context, serviceSet *kcmv1.ServiceSet) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling ServiceSet deletion")

	if slices.ContainsFunc(serviceSet.Status.Services, func(state kcmv1.ServiceState) bool {
		return state.State != kcmv1.ServiceStateDeleting
	}) {
		serviceStates := make([]kcmv1.ServiceState, 0, len(serviceSet.Status.Services))
		for _, state := range serviceSet.Status.Services {
			newState := state.DeepCopy()
			newState.State = kcmv1.ServiceStateDeleting
			newState.LastStateTransitionTime = new(metav1.NewTime(r.timeFunc()))
			serviceStates = append(serviceStates, *newState)
		}
		serviceSet.Status.Services = serviceStates
		return ctrl.Result{}, r.Status().Update(ctx, serviceSet)
	}

	releases, err := r.ownedHelmReleases(ctx, serviceSet)
	if err != nil {
		return ctrl.Result{}, err
	}

	if len(releases) > 0 {
		for i := range releases {
			hr := &releases[i]
			if hr.DeletionTimestamp.IsZero() {
				l.Info("Deleting HelmRelease", "helmrelease", client.ObjectKeyFromObject(hr))
				if err := r.Delete(ctx, hr); client.IgnoreNotFound(err) != nil {
					return ctrl.Result{}, fmt.Errorf("failed to delete HelmRelease %s: %w", client.ObjectKeyFromObject(hr), err)
				}
				continue
			}
			if err := r.nudgeStalledUninstall(ctx, hr); err != nil {
				return ctrl.Result{}, err
			}
		}
		l.V(1).Info("Waiting for HelmReleases to be deleted", "count", len(releases))
		return ctrl.Result{RequeueAfter: r.requeueInterval}, nil
	}

	if controllerutil.RemoveFinalizer(serviceSet, kcmv1.ServiceSetFinalizer) {
		if err := r.Update(ctx, serviceSet); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServiceSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	if r.timeFunc == nil {
		r.timeFunc = time.Now
	}
	r.requeueInterval = 10 * time.Second
	if r.MaxConcurrentReconciles <= 0 {
		r.MaxConcurrentReconciles = defaultConcurrentReconciles
	}

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			MaxConcurrentReconciles: r.MaxConcurrentReconciles,
			RateLimiter:             ratelimitutil.DefaultFastSlow(),
		}).
		Named("ksm-flux-adapter").
		For(&kcmv1.ServiceSet{}).
		Owns(&helmcontrollerv2.HelmRelease{}).
		Watches(&kcmv1.StateManagementProvider{}, kubeutil.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) ([]ctrl.Request, error) {
			provider, ok := o.(*kcmv1.StateManagementProvider)
			if !ok || provider.AdapterKind() != kcmv1.AdapterKindFlux {
				return nil, nil
			}

			selector := fields.OneTermEqualSelector(kcmv1.ServiceSetProviderIndexKey, provider.Name)
			serviceSets := new(kcmv1.ServiceSetList)
			if err := r.List(ctx, serviceSets, client.MatchingFieldsSelector{Selector: selector}); err != nil {
				return nil, fmt.Errorf("failed to list ServiceSets for StateManagementProvider %s: %w", provider.Name, err)
			}
			requests := make([]ctrl.Request, 0, len(serviceSets.Items))
			for _, serviceSet := range serviceSets.Items {
				requests = append(requests, ctrl.Request{Name: serviceSet.Name, Namespace: serviceSet.Namespace})
			}
			return requests, nil
		})).
		Complete(r)
}

// ensureHelmReleases creates, updates or removes a HelmRelease per service of
// the ServiceSet and prunes the ones that are no longer wanted.
func (r *ServiceSetReconciler) ensureHelmReleases(ctx context.Context, serviceSet *kcmv1.ServiceSet) (err error) {
	start := time.Now()
	l := ctrl.LoggerFrom(ctx)
	l.Info("Ensuring HelmReleases")

	profileCondition := findCondition(serviceSet, kcmv1.ServiceSetProfileCondition)
	initialConditionStatus := profileCondition.Status

	status := metav1.ConditionFalse
	reason := kcmv1.ServiceSetProfileNotReadyReason
	message := kcmv1.ServiceSetProfileNotReadyMessage

	defer func(initialStatus metav1.ConditionStatus) {
		updated := updateCondition(serviceSet, profileCondition, status, reason, message, r.timeFunc())
		if initialStatus != status && status == metav1.ConditionTrue && updated {
			record.Eventf(serviceSet, nil, kcmv1.ServiceSetEnsureProfileSuccessEvent, kcmv1.ServiceSetEnsureProfileEventAction,
				"Successfully ensured HelmReleases for ServiceSet %s", serviceSet.Name)
		}
		l.V(1).Info("Finished ensuring HelmReleases", "duration", time.Since(start))
	}(initialConditionStatus)

	cfg, err := providerConfigFromJSON(serviceSet.Spec.Provider.Config)
	if err != nil {
		reason, message = kcmv1.ServiceSetProfileBuildFailedReason, err.Error()
		return err
	}

	kubeConfigRef, err := r.kubeConfigReference(ctx, serviceSet)
	if err != nil {
		reason, message = kcmv1.ServiceSetProfileBuildFailedReason, err.Error()
		return err
	}

	wanted := make(map[string]struct{}, len(serviceSet.Spec.Services))
	for _, svc := range serviceSet.Spec.Services {
		tmpl := new(kcmv1.ServiceTemplate)
		key := client.ObjectKey{Namespace: serviceSet.Namespace, Name: svc.Template}
		if err := r.Get(ctx, key, tmpl); err != nil {
			reason, message = kcmv1.ServiceSetHelmChartsBuildFailedReason, err.Error()
			return fmt.Errorf("failed to get ServiceTemplate %s: %w", key, err)
		}

		if tmpl.Spec.Helm == nil {
			reason = kcmv1.ServiceSetHelmChartsBuildFailedReason
			message = fmt.Sprintf("ServiceTemplate %s: %v", key, errUnsupportedServiceType)
			return fmt.Errorf("ServiceTemplate %s: %w", key, errUnsupportedServiceType)
		}
		if !tmpl.Status.Valid {
			reason = kcmv1.ServiceSetHelmChartsBuildFailedReason
			message = fmt.Sprintf("ServiceTemplate %s is invalid: %s", key, tmpl.Status.ValidationError)
			return fmt.Errorf("ServiceTemplate %s is invalid: %s", key, tmpl.Status.ValidationError)
		}

		name := helmReleaseName(serviceSet, svc)

		// An explicit Uninstall action is expressed by removing the release:
		// Flux has no equivalent of "keep the entry but uninstall the chart".
		if svc.HelmAction == string(helmActionUninstall) {
			if err := r.deleteHelmRelease(ctx, serviceSet.Namespace, name); err != nil {
				return err
			}
			continue
		}

		desired, err := buildHelmRelease(serviceSet, svc, tmpl, cfg, kubeConfigRef, name)
		if err != nil {
			reason, message = kcmv1.ServiceSetHelmChartsBuildFailedReason, err.Error()
			return err
		}
		if err := controllerutil.SetControllerReference(serviceSet, desired, r.Scheme()); err != nil {
			reason, message = kcmv1.ServiceSetProfileBuildFailedReason, err.Error()
			return fmt.Errorf("failed to set controller reference on HelmRelease %s: %w", name, err)
		}

		if err := r.createOrUpdateHelmRelease(ctx, desired); err != nil {
			reason, message = kcmv1.ServiceSetProfileNotReadyReason, err.Error()
			return err
		}
		wanted[name] = struct{}{}
	}

	// Drop the releases of services that were removed from the spec.
	existing, err := r.ownedHelmReleases(ctx, serviceSet)
	if err != nil {
		return err
	}
	for _, hr := range existing {
		if _, ok := wanted[hr.Name]; ok {
			continue
		}
		if !hr.DeletionTimestamp.IsZero() {
			continue
		}
		l.Info("Deleting orphaned HelmRelease", "helmrelease", client.ObjectKeyFromObject(&hr))
		if err := r.Delete(ctx, &hr); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete HelmRelease %s: %w", client.ObjectKeyFromObject(&hr), err)
		}
	}

	status = metav1.ConditionTrue
	reason = kcmv1.ServiceSetProfileReadyReason
	message = kcmv1.ServiceSetProfileReadyMessage
	return nil
}

// nudgeStalledUninstall asks helm-controller to reconcile a HelmRelease that
// has been marked for deletion but is still sitting there. helm-controller does
// not always act on the deletion itself, and its next scheduled reconcile is
// spec.interval away -- minutes during which the ServiceSet, and the
// MultiClusterService above it, cannot finish deleting. A reconcile request is
// a watch event it cannot miss.
func (r *ServiceSetReconciler) nudgeStalledUninstall(ctx context.Context, hr *helmcontrollerv2.HelmRelease) error {
	now := r.timeFunc()
	if requested, err := time.Parse(time.RFC3339Nano, hr.Annotations[fluxmeta.ReconcileRequestAnnotation]); err == nil &&
		now.Sub(requested) < nudgeInterval {
		return nil
	}

	patch := client.MergeFrom(hr.DeepCopy())
	if hr.Annotations == nil {
		hr.Annotations = make(map[string]string, 1)
	}
	hr.Annotations[fluxmeta.ReconcileRequestAnnotation] = now.Format(time.RFC3339Nano)
	if err := r.Patch(ctx, hr, patch); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to request reconcile of HelmRelease %s: %w", client.ObjectKeyFromObject(hr), err)
	}
	return nil
}

func (r *ServiceSetReconciler) createOrUpdateHelmRelease(ctx context.Context, desired *helmcontrollerv2.HelmRelease) error {
	existing := new(helmcontrollerv2.HelmRelease)
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, desired); err != nil {
			return fmt.Errorf("failed to create HelmRelease %s: %w", client.ObjectKeyFromObject(desired), err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get HelmRelease %s: %w", client.ObjectKeyFromObject(desired), err)
	}

	if equality.Semantic.DeepEqual(existing.Spec, desired.Spec) &&
		equality.Semantic.DeepDerivative(desired.Labels, existing.Labels) {
		return nil
	}

	patched := existing.DeepCopy()
	patched.Spec = desired.Spec
	patched.Labels = desired.Labels
	patched.OwnerReferences = desired.OwnerReferences
	if err := r.Update(ctx, patched); err != nil {
		return fmt.Errorf("failed to update HelmRelease %s: %w", client.ObjectKeyFromObject(desired), err)
	}
	return nil
}

func (r *ServiceSetReconciler) deleteHelmRelease(ctx context.Context, namespace, name string) error {
	hr := new(helmcontrollerv2.HelmRelease)
	hr.Name, hr.Namespace = name, namespace
	if err := r.Delete(ctx, hr); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to delete HelmRelease %s/%s: %w", namespace, name, err)
	}
	return nil
}

func (r *ServiceSetReconciler) ownedHelmReleases(ctx context.Context, serviceSet *kcmv1.ServiceSet) ([]helmcontrollerv2.HelmRelease, error) {
	list := new(helmcontrollerv2.HelmReleaseList)
	err := r.List(ctx, list,
		client.InNamespace(serviceSet.Namespace),
		client.MatchingLabels{
			serviceSetNameLabel:      serviceSet.Name,
			serviceSetNamespaceLabel: serviceSet.Namespace,
		})
	if err != nil {
		return nil, fmt.Errorf("failed to list HelmReleases of ServiceSet %s: %w", client.ObjectKeyFromObject(serviceSet), err)
	}
	return list.Items, nil
}

// kubeConfigReference returns the kubeconfig Flux must use to reach the
// cluster the services are deployed to, or nil for the management cluster.
//
//nolint:nilnil // a nil reference is the answer: it means the management cluster
func (r *ServiceSetReconciler) kubeConfigReference(ctx context.Context, serviceSet *kcmv1.ServiceSet) (*fluxmeta.KubeConfigReference, error) {
	if serviceSet.Spec.Provider.SelfManagement || serviceSet.Spec.Cluster == "" {
		return nil, nil
	}

	clusterKey := client.ObjectKey{Namespace: serviceSet.Namespace, Name: serviceSet.Spec.Cluster}
	cd := new(kcmv1.ClusterDeployment)
	if err := r.Get(ctx, clusterKey, cd); err != nil {
		return nil, fmt.Errorf("failed to get ClusterDeployment %s: %w", clusterKey, err)
	}

	cred := new(kcmv1.Credential)
	credKey := client.ObjectKey{Namespace: cd.Namespace, Name: cd.Spec.Credential}
	if err := r.Get(ctx, credKey, cred); err != nil {
		return nil, fmt.Errorf("failed to get Credential %s: %w", credKey, err)
	}
	if cred.Spec.Region != "" {
		// The HelmRelease has to live next to the Flux source it consumes, and
		// those only exist on the management cluster. A regional cluster would
		// need its own copy of the sources first.
		return nil, fmt.Errorf("regional cluster %q is not supported by the flux adapter", cred.Spec.Region)
	}

	secretKey := kubeutil.GetKubeconfigSecretKey(clusterKey)
	return &fluxmeta.KubeConfigReference{
		SecretRef: &fluxmeta.SecretKeyReference{Name: secretKey.Name, Key: "value"},
	}, nil
}

// collectServiceStatuses translates the state of every produced HelmRelease
// back into the ServiceSet status.
func (r *ServiceSetReconciler) collectServiceStatuses(ctx context.Context, serviceSet *kcmv1.ServiceSet) (err error) {
	start := time.Now()
	l := ctrl.LoggerFrom(ctx)
	l.Info("Collecting Service statuses")

	statusesCollectedCondition := findCondition(serviceSet, kcmv1.ServiceSetStatusesCollectedCondition)
	initialConditionStatus := statusesCollectedCondition.Status

	status := metav1.ConditionFalse
	reason := kcmv1.ServiceSetStatusesNotCollectedReason
	message := kcmv1.ServiceSetStatusesNotCollectedMessage

	defer func(initialStatus metav1.ConditionStatus) {
		updated := updateCondition(serviceSet, statusesCollectedCondition, status, reason, message, r.timeFunc())
		if initialStatus != status && status == metav1.ConditionTrue && updated {
			record.Eventf(serviceSet, nil, kcmv1.ServiceSetCollectServiceStatusesSuccessEvent, kcmv1.ServiceSetCollectServiceStatusesEventAction,
				"Successfully collected service statuses for ServiceSet %s", serviceSet.Name)
		}
		l.V(1).Info("Finished services status collection", "duration", time.Since(start))
	}(initialConditionStatus)

	releases, err := r.ownedHelmReleases(ctx, serviceSet)
	if err != nil {
		return err
	}

	byService := make(map[client.ObjectKey]*helmcontrollerv2.HelmRelease, len(releases))
	for i := range releases {
		hr := &releases[i]
		byService[client.ObjectKey{
			Namespace: hr.Labels[serviceNamespaceLabel],
			Name:      hr.Labels[serviceNameLabel],
		}] = hr
	}

	previous := make(map[client.ObjectKey]kcmv1.ServiceState, len(serviceSet.Status.Services))
	for _, state := range serviceSet.Status.Services {
		previous[client.ObjectKey{Namespace: state.Namespace, Name: state.Name}] = state
	}

	// Built from the spec, not from the previous status: a service dropped from
	// the spec has to leave the status with it, or the ServiceSet never counts
	// as deployed again.
	states := make([]kcmv1.ServiceState, 0, len(serviceSet.Spec.Services))
	for _, svc := range serviceSet.Spec.Services {
		key := client.ObjectKey{Namespace: svc.Namespace, Name: svc.Name}
		state := previous[key]
		newState := *state.DeepCopy()
		newState.Name, newState.Namespace, newState.Template = svc.Name, svc.Namespace, svc.Template
		newState.Type = kcmv1.ServiceTypeHelm

		if hr, found := byService[key]; found {
			newState.State, newState.FailureMessage = serviceStateFromHelmRelease(hr)
			if version := deployedChartVersion(hr); version != "" {
				newState.Version = version
			}
		} else {
			// Either the release has not been created yet or the service was
			// uninstalled on purpose; both read as "nothing deployed".
			newState.State = kcmv1.ServiceStateNotDeployed
			newState.FailureMessage = ""
		}

		if newState.State != state.State || newState.LastStateTransitionTime == nil {
			newState.LastStateTransitionTime = new(metav1.NewTime(r.timeFunc()))
		}
		states = append(states, newState)
	}
	serviceSet.Status.Services = states

	serviceSet.Status.Deployed = !slices.ContainsFunc(serviceSet.Status.Services, func(s kcmv1.ServiceState) bool {
		return s.State != kcmv1.ServiceStateDeployed
	})

	r.updateServicesInReadyStateCondition(serviceSet)

	status = metav1.ConditionTrue
	reason = kcmv1.ServiceSetStatusesCollectedReason
	message = kcmv1.ServiceSetStatusesCollectedMessage
	return nil
}

// updateServicesInReadyStateCondition updates the "ServicesInReadyState" condition.
func (r *ServiceSetReconciler) updateServicesInReadyStateCondition(serviceSet *kcmv1.ServiceSet) {
	deployedCount := 0
	for _, svc := range serviceSet.Status.Services {
		if svc.State == kcmv1.ServiceStateDeployed {
			deployedCount++
		}
	}
	totalCount := len(serviceSet.Spec.Services)
	svcReadyCond := findCondition(serviceSet, kcmv1.ServicesInReadyStateCondition)
	svcReadyStatus := metav1.ConditionTrue
	if deployedCount != totalCount {
		svcReadyStatus = metav1.ConditionFalse
	}

	if deployedCount <= totalCount {
		updateCondition(serviceSet, svcReadyCond, svcReadyStatus, kcmv1.ServicesInReadyStateCondition,
			fmt.Sprintf("%d/%d", deployedCount, totalCount), r.timeFunc())
	}
}

// serviceStateFromHelmRelease maps the Flux conditions of a HelmRelease onto
// the KCM service state.
func serviceStateFromHelmRelease(hr *helmcontrollerv2.HelmRelease) (state, failureMessage string) {
	if !hr.DeletionTimestamp.IsZero() {
		return kcmv1.ServiceStateDeleting, ""
	}

	if stalled := apimeta.FindStatusCondition(hr.Status.Conditions, fluxmeta.StalledCondition); stalled != nil &&
		stalled.Status == metav1.ConditionTrue {
		return kcmv1.ServiceStateFailed, stalled.Message
	}

	ready := apimeta.FindStatusCondition(hr.Status.Conditions, fluxmeta.ReadyCondition)
	if ready != nil && ready.Status == metav1.ConditionTrue {
		return kcmv1.ServiceStateDeployed, ""
	}

	// An atomic upgrade that was rolled back leaves Ready false with a reason
	// about the remediation rather than the failure. Without this the service
	// would sit in Provisioning forever, retried every interval.
	if remediated := apimeta.FindStatusCondition(hr.Status.Conditions, helmcontrollerv2.RemediatedCondition); remediated != nil &&
		remediated.Status == metav1.ConditionTrue {
		return kcmv1.ServiceStateFailed, releaseFailureMessage(hr, remediated)
	}

	if ready != nil && ready.Status == metav1.ConditionFalse && isTerminalFailureReason(ready.Reason) {
		return kcmv1.ServiceStateFailed, ready.Message
	}

	return kcmv1.ServiceStateProvisioning, ""
}

// releaseFailureMessage prefers what the release attempt said over what the
// remediation did: "Helm rollback succeeded" is not why the service failed.
func releaseFailureMessage(hr *helmcontrollerv2.HelmRelease, fallback *metav1.Condition) string {
	if released := apimeta.FindStatusCondition(hr.Status.Conditions, helmcontrollerv2.ReleasedCondition); released != nil &&
		released.Status == metav1.ConditionFalse {
		return released.Message
	}
	return fallback.Message
}

// isTerminalFailureReason reports whether a not-ready reason describes an
// attempt that actually failed, as opposed to one still in flight.
func isTerminalFailureReason(reason string) bool {
	switch reason {
	case helmcontrollerv2.InstallFailedReason,
		helmcontrollerv2.UpgradeFailedReason,
		helmcontrollerv2.TestFailedReason,
		helmcontrollerv2.RollbackFailedReason,
		helmcontrollerv2.UninstallFailedReason,
		helmcontrollerv2.ArtifactFailedReason:
		return true
	default:
		return false
	}
}

func deployedChartVersion(hr *helmcontrollerv2.HelmRelease) string {
	if len(hr.Status.History) == 0 {
		return ""
	}
	return hr.Status.History[0].ChartVersion
}

type helmAction string

const helmActionUninstall helmAction = "Uninstall"

// helmReleaseName is the name of the HelmRelease produced for a service. The
// ServiceSet name is part of it so that two ServiceSets in one namespace
// cannot collide on a service of the same name.
func helmReleaseName(serviceSet *kcmv1.ServiceSet, svc kcmv1.ServiceWithValues) string {
	name := fmt.Sprintf("%s-%s", serviceSet.Name, svc.Name)
	if svc.Namespace != "" {
		name = fmt.Sprintf("%s-%s-%s", serviceSet.Name, svc.Namespace, svc.Name)
	}
	const maxNameLength = 253
	if len(name) <= maxNameLength {
		return name
	}
	// Keep the name unique when it has to be cut: a digest of the full name
	// replaces the tail.
	sum := sha256.Sum256([]byte(name))
	suffix := "-" + hex.EncodeToString(sum[:])[:8]
	return name[:maxNameLength-len(suffix)] + suffix
}

// buildHelmRelease renders the HelmRelease for a single service.
func buildHelmRelease(
	serviceSet *kcmv1.ServiceSet,
	svc kcmv1.ServiceWithValues,
	tmpl *kcmv1.ServiceTemplate,
	cfg providerConfig,
	kubeConfigRef *fluxmeta.KubeConfigReference,
	name string,
) (*helmcontrollerv2.HelmRelease, error) {
	values, err := valuesFromYAML(svc.Values)
	if err != nil {
		return nil, fmt.Errorf("failed to parse values of service %s: %w", svc.Name, err)
	}

	releaseNamespace := svc.Namespace
	if releaseNamespace == "" {
		releaseNamespace = svc.Name
	}

	interval := metav1.Duration{Duration: defaultInterval}
	if cfg.Interval != nil {
		interval = *cfg.Interval
	}

	hr := &helmcontrollerv2.HelmRelease{
		Name:      name,
		Namespace: serviceSet.Namespace,
		Labels: map[string]string{
			// Both Flux controllers are started with
			// --watch-label-selector=k0rdent.mirantis.com/managed=true.
			kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue,
			serviceSetNameLabel:      serviceSet.Name,
			serviceSetNamespaceLabel: serviceSet.Namespace,
			serviceNameLabel:         svc.Name,
			serviceNamespaceLabel:    svc.Namespace,
		},
		Spec: helmcontrollerv2.HelmReleaseSpec{
			Interval:           interval,
			KubeConfig:         kubeConfigRef,
			ReleaseName:        svc.Name,
			TargetNamespace:    releaseNamespace,
			StorageNamespace:   releaseNamespace,
			ServiceAccountName: cfg.ServiceAccountName,
			MaxHistory:         cfg.MaxHistory,
			Values:             values,
			ValuesFrom:         valuesReferences(svc.ValuesFrom),
			Suspend:            isPaused(serviceSet),
			DriftDetection:     driftDetection(cfg.DriftDetectionMode),
		},
	}

	if err := setChartSource(hr, tmpl); err != nil {
		return nil, err
	}

	applyHelmOptions(hr, helmOptionsFor(svc, tmpl))

	return hr, nil
}

// setChartSource points the HelmRelease at the Flux source KCM already
// maintains for the ServiceTemplate.
func setChartSource(hr *helmcontrollerv2.HelmRelease, tmpl *kcmv1.ServiceTemplate) error {
	key := client.ObjectKeyFromObject(tmpl)

	// Charts defined by a chart spec or ref: KCM reconciles a flux HelmChart
	// for them, which the HelmRelease can consume directly.
	if chartRef := tmpl.GetCommonStatus().ChartRef; chartRef != nil {
		hr.Spec.ChartRef = &helmcontrollerv2.CrossNamespaceSourceReference{
			APIVersion: chartRef.APIVersion,
			Kind:       chartRef.Kind,
			Name:       chartRef.Name,
			Namespace:  chartRef.Namespace,
		}
		return nil
	}

	// Charts coming from a Flux source object declared on the template.
	source := tmpl.Status.SourceStatus
	if source == nil {
		return fmt.Errorf("status for ServiceTemplate %s has not been updated yet", key)
	}

	chartSource := tmpl.Spec.Helm.ChartSource
	if chartSource == nil {
		return fmt.Errorf("ServiceTemplate %s has no Helm chart defined", key)
	}

	if source.Kind == "OCIRepository" {
		// An OCIRepository artifact is the chart itself, so it is referenced
		// directly; source-controller cannot wrap it in a HelmChart.
		hr.Spec.ChartRef = &helmcontrollerv2.CrossNamespaceSourceReference{
			Kind:      source.Kind,
			Name:      source.Name,
			Namespace: source.Namespace,
		}
		return nil
	}

	hr.Spec.Chart = &helmcontrollerv2.HelmChartTemplate{
		ObjectMeta: &helmcontrollerv2.HelmChartTemplateObjectMeta{
			// The HelmChart is created by helm-controller, and source-controller
			// only watches labelled objects.
			Labels: map[string]string{kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue},
		},
		Spec: helmcontrollerv2.HelmChartTemplateSpec{
			Chart: strings.TrimPrefix(strings.TrimPrefix(chartSource.Path, "."), "/"),
			SourceRef: &helmcontrollerv2.CrossNamespaceObjectReference{
				Kind:      source.Kind,
				Name:      source.Name,
				Namespace: source.Namespace,
			},
		},
	}
	return nil
}

// helmOptionsFor returns the options of the template, falling back to the ones
// of the service, mirroring what the sveltos adapter does.
func helmOptionsFor(svc kcmv1.ServiceWithValues, tmpl *kcmv1.ServiceTemplate) *kcmv1.ServiceHelmOptions {
	if tmpl.Spec.HelmOptions != nil {
		return tmpl.Spec.HelmOptions
	}
	return svc.HelmOptions
}

// applyHelmOptions translates the subset of the KCM Helm options that has a
// Flux counterpart. Options without one (client cache, dependency update,
// release description) are silently dropped: helm-controller has no knob for
// them.
func applyHelmOptions(hr *helmcontrollerv2.HelmRelease, options *kcmv1.ServiceHelmOptions) {
	install := &helmcontrollerv2.Install{CreateNamespace: true}
	upgrade := new(helmcontrollerv2.Upgrade)

	if options != nil {
		if options.Timeout != nil {
			hr.Spec.Timeout = options.Timeout
		}
		if options.Wait != nil && !*options.Wait {
			install.DisableWait, upgrade.DisableWait = true, true
		}
		if options.WaitForJobs != nil && !*options.WaitForJobs {
			install.DisableWaitForJobs, upgrade.DisableWaitForJobs = true, true
		}
		if options.DisableHooks != nil {
			install.DisableHooks, upgrade.DisableHooks = *options.DisableHooks, *options.DisableHooks
		}
		if options.DisableOpenAPIValidation != nil {
			install.DisableOpenAPIValidation = *options.DisableOpenAPIValidation
			upgrade.DisableOpenAPIValidation = *options.DisableOpenAPIValidation
		}
		if options.SkipSchemaValidation != nil {
			install.DisableSchemaValidation = *options.SkipSchemaValidation
			upgrade.DisableSchemaValidation = *options.SkipSchemaValidation
		}
		if options.SkipCRDs != nil && *options.SkipCRDs {
			install.CRDs = helmcontrollerv2.Skip
			upgrade.CRDs = helmcontrollerv2.Skip
		}
		//nolint:staticcheck // deprecated but still honoured for compatibility
		if options.CreateNamespace != nil {
			install.CreateNamespace = *options.CreateNamespace
		}
		//nolint:staticcheck // deprecated but still honoured for compatibility
		if options.Replace != nil {
			install.Replace = *options.Replace
		}
		if options.InstallOptions != nil && options.InstallOptions.CreateNamespace != nil {
			install.CreateNamespace = *options.InstallOptions.CreateNamespace
		}
		if options.InstallOptions != nil && options.InstallOptions.Replace != nil {
			install.Replace = *options.InstallOptions.Replace
		}
		// Helm's --atomic rolls back a failed release; in Flux that is
		// expressed as a remediation strategy.
		if options.Atomic != nil && *options.Atomic {
			install.Remediation = &helmcontrollerv2.InstallRemediation{Retries: 1}
			strategy := helmcontrollerv2.RollbackRemediationStrategy
			upgrade.Remediation = &helmcontrollerv2.UpgradeRemediation{Retries: 1, Strategy: &strategy}
		}
		if options.UpgradeOptions != nil {
			upgrade.Force = options.UpgradeOptions.Force
		}
		if options.Labels != nil && len(*options.Labels) > 0 {
			hr.Spec.CommonMetadata = &helmcontrollerv2.CommonMetadata{Labels: *options.Labels}
		}
	}

	hr.Spec.Install = install
	hr.Spec.Upgrade = upgrade
}

func driftDetection(mode string) *helmcontrollerv2.DriftDetection {
	switch mode {
	case "enabled":
		return &helmcontrollerv2.DriftDetection{Mode: helmcontrollerv2.DriftDetectionEnabled}
	case "warn":
		return &helmcontrollerv2.DriftDetection{Mode: helmcontrollerv2.DriftDetectionWarn}
	default:
		return nil
	}
}

func valuesReferences(src []kcmv1.ValuesFrom) []helmcontrollerv2.ValuesReference {
	if len(src) == 0 {
		return nil
	}
	refs := make([]helmcontrollerv2.ValuesReference, 0, len(src))
	for _, item := range src {
		refs = append(refs, helmcontrollerv2.ValuesReference{Kind: item.Kind, Name: item.Name})
	}
	return refs
}

//nolint:nilnil // a nil value is the answer: the service passes no values
func valuesFromYAML(values string) (*apiextv1.JSON, error) {
	if strings.TrimSpace(values) == "" {
		return nil, nil
	}
	raw, err := yaml.YAMLToJSON([]byte(values))
	if err != nil {
		return nil, err
	}
	return &apiextv1.JSON{Raw: raw}, nil
}

func providerConfigFromJSON(raw *apiextv1.JSON) (providerConfig, error) {
	cfg := providerConfig{}
	if raw == nil || len(raw.Raw) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(raw.Raw, &cfg); err != nil {
		return cfg, fmt.Errorf("failed to parse provider config: %w", err)
	}
	return cfg, nil
}

func isPaused(serviceSet *kcmv1.ServiceSet) bool {
	_, paused := serviceSet.GetAnnotations()[kcmv1.ServiceSetPausedAnnotation]
	return paused
}

func clusterReference(serviceSet *kcmv1.ServiceSet) *corev1.ObjectReference {
	if serviceSet.Spec.Provider.SelfManagement {
		return serviceset.SelfManagementClusterReference()
	}
	return &corev1.ObjectReference{
		Kind:       kcmv1.ClusterDeploymentKind,
		Name:       serviceSet.Spec.Cluster,
		Namespace:  serviceSet.Namespace,
		APIVersion: kcmv1.GroupVersion.WithKind(kcmv1.ClusterDeploymentKind).GroupVersion().String(),
	}
}

func fillNotDeployedServices(serviceSet *kcmv1.ServiceSet, now func() time.Time) {
	known := make(map[client.ObjectKey]struct{}, len(serviceSet.Status.Services))
	for _, service := range serviceSet.Status.Services {
		known[client.ObjectKey{Namespace: service.Namespace, Name: service.Name}] = struct{}{}
	}

	for _, service := range serviceSet.Spec.Services {
		key := client.ObjectKey{Namespace: service.Namespace, Name: service.Name}
		if _, ok := known[key]; ok {
			continue
		}
		serviceSet.Status.Services = append(serviceSet.Status.Services, kcmv1.ServiceState{
			Name:                    service.Name,
			Namespace:               service.Namespace,
			Template:                service.Template,
			Type:                    kcmv1.ServiceTypeHelm,
			State:                   kcmv1.ServiceStateNotDeployed,
			LastStateTransitionTime: new(metav1.NewTime(now())),
		})
	}
}

func labelsMatchSelector(serviceSetLabels map[string]string, selector *metav1.LabelSelector) (bool, error) {
	if selector == nil {
		return true, nil
	}
	labelSelector, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return false, fmt.Errorf("failed to convert selector: %w", err)
	}
	return labelSelector.Matches(labels.Set(serviceSetLabels)), nil
}

func findCondition(serviceSet *kcmv1.ServiceSet, conditionType string) metav1.Condition {
	condition := apimeta.FindStatusCondition(serviceSet.Status.Conditions, conditionType)
	if condition == nil {
		return metav1.Condition{
			Type:   conditionType,
			Status: metav1.ConditionUnknown,
		}
	}
	return *condition
}

func updateCondition(
	serviceSet *kcmv1.ServiceSet,
	condition metav1.Condition,
	status metav1.ConditionStatus,
	reason, message string,
	now time.Time,
) bool {
	condition.Status = status
	condition.Reason = reason
	condition.Message = message
	condition.ObservedGeneration = serviceSet.Generation
	condition.LastTransitionTime = metav1.NewTime(now)
	return apimeta.SetStatusCondition(&serviceSet.Status.Conditions, condition)
}

func conditionStatusChangedToFalse(conditionOldState, conditionNewState *metav1.Condition) bool {
	if conditionNewState == nil {
		return false
	}
	if conditionOldState == nil {
		return conditionNewState.Status == metav1.ConditionFalse
	}
	return conditionOldState.Status != conditionNewState.Status && conditionNewState.Status == metav1.ConditionFalse
}
