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

// Package argocd implements the KSM adapter backed by Argo CD: it translates a
// [github.com/K0rdent/kcm/api/v1beta1.ServiceSet] into Argo CD Application
// objects, one per Helm service, and collects their state back onto the
// ServiceSet.
//
// The Applications live in Argo CD's own namespace on the management cluster,
// which is where Argo CD looks for them. They therefore cannot carry an owner
// reference to the ServiceSet - owner references do not cross namespaces - so
// the adapter labels them with it instead and deletes them itself.
package argocd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	corev1 "k8s.io/api/core/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/json"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/yaml"

	argocdv1 "github.com/K0rdent/kcm/api/argocd/v1alpha1"
	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/controller/adapters/adapterutil"
	"github.com/K0rdent/kcm/internal/record"
	helmutil "github.com/K0rdent/kcm/internal/util/helm"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	ratelimitutil "github.com/K0rdent/kcm/internal/util/ratelimit"
)

const (
	// serviceSetNameLabel and the three below tie an Application back to the
	// ServiceSet and the service it was produced from. An owner reference
	// cannot: the Application lives in Argo CD's namespace, the ServiceSet in
	// its own.
	serviceSetNameLabel      = "ksm.k0rdent.mirantis.com/service-set-name"
	serviceSetNamespaceLabel = "ksm.k0rdent.mirantis.com/service-set-namespace"
	serviceNameLabel         = "ksm.k0rdent.mirantis.com/service-name"
	serviceNamespaceLabel    = "ksm.k0rdent.mirantis.com/service-namespace"

	// argoSecretTypeLabel is how Argo CD finds its repository secrets.
	argoSecretTypeLabel = "argocd.argoproj.io/secret-type"
	argoSecretTypeRepo  = "repository"

	// DefaultNamespace is where Argo CD is installed by default, and so where
	// it looks for Applications.
	DefaultNamespace = "argocd"

	// defaultProject is the Argo CD project every Application lands in unless
	// the provider config names another.
	defaultProject = "default"

	// inClusterServer is Argo CD's name for the cluster it runs in.
	inClusterServer = "https://kubernetes.default.svc"

	// defaultConcurrentReconciles matches the --max-concurrent-reconciles
	// default. It is a fallback: cmd/main.go never populates the config field
	// the flag writes to, so the reconciler would otherwise run single-worker.
	defaultConcurrentReconciles = 10
)

var (
	errUnsupportedServiceType = errors.New("the argocd adapter only supports Helm-backed ServiceTemplates")
	errUnsupportedSource      = errors.New("the argocd adapter only supports Helm repository and Git chart sources")
)

// providerConfig is the Argo CD specific part of ServiceSet.spec.provider.config.
// Unknown fields are ignored on purpose: KCM fills the config with the legacy
// sveltos-shaped keys whenever the user did not set one explicitly.
type providerConfig struct {
	// SelfHeal reverts changes made to the deployed resources outside of
	// Argo CD. Defaults to true.
	SelfHeal *bool `json:"selfHeal,omitempty"`

	// Prune deletes resources dropped from the chart. Defaults to true.
	Prune *bool `json:"prune,omitempty"`

	// RetryLimit is how many times a failed sync is retried; negative means
	// unlimited. Defaults to 5.
	RetryLimit *int64 `json:"retryLimit,omitempty"`

	// Project is the Argo CD project of the produced Applications.
	Project string `json:"project,omitempty"`

	// SyncOptions are passed to Argo CD verbatim, on top of the ones the
	// adapter sets itself.
	SyncOptions []string `json:"syncOptions,omitempty"`
}

// ServiceSetReconciler reconciles a ServiceSet object into Argo CD Applications.
type ServiceSetReconciler struct {
	client.Client

	timeFunc func() time.Time

	SystemNamespace string

	// ArgoNamespace is where Argo CD runs, and so where its Applications and
	// repository secrets have to be created.
	ArgoNamespace string

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
	if smp.AdapterKind() != kcmv1.AdapterKindArgoCD {
		return ctrl.Result{}, nil
	}

	if !serviceSet.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, serviceSet)
	}

	if controllerutil.AddFinalizer(serviceSet, kcmv1.ServiceSetFinalizer) {
		return ctrl.Result{}, r.Update(ctx, serviceSet)
	}

	continueReconciliation, err := adapterutil.LabelsMatchSelector(serviceSet.Labels, smp.Spec.Selector)
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
			adapterutil.FillNotDeployedServices(serviceSet, r.timeFunc)
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

	serviceSet.Status.Cluster = adapterutil.ClusterReference(serviceSet)
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

	if err = r.ensureApplications(ctx, serviceSet); err != nil {
		conditionOldState := apimeta.FindStatusCondition(clone.Status.Conditions, kcmv1.ServiceSetProfileCondition)
		conditionNewState := apimeta.FindStatusCondition(serviceSet.Status.Conditions, kcmv1.ServiceSetProfileCondition)
		if adapterutil.ConditionStatusChangedToFalse(conditionOldState, conditionNewState) {
			record.Warnf(serviceSet, nil, kcmv1.ServiceSetEnsureProfileFailedEvent, kcmv1.ServiceSetEnsureProfileEventAction,
				"Failed to ensure Applications for ServiceSet %s: %v", serviceSet.Name, err)
		}
		return ctrl.Result{}, err
	}

	if err = r.collectServiceStatuses(ctx, serviceSet); err != nil {
		conditionOldState := apimeta.FindStatusCondition(clone.Status.Conditions, kcmv1.ServiceSetStatusesCollectedCondition)
		conditionNewState := apimeta.FindStatusCondition(serviceSet.Status.Conditions, kcmv1.ServiceSetStatusesCollectedCondition)
		if adapterutil.ConditionStatusChangedToFalse(conditionOldState, conditionNewState) {
			record.Warnf(serviceSet, nil, kcmv1.ServiceSetCollectServiceStatusesFailedEvent, kcmv1.ServiceSetCollectServiceStatusesEventAction,
				"Failed to collect Service statuses for ServiceSet %s: %v", serviceSet.Name, err)
		}
		return ctrl.Result{}, err
	}

	// An Application that is still syncing does not always produce a status
	// event we would see, so poll until everything settles.
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

	applications, err := r.ownedApplications(ctx, serviceSet)
	if err != nil {
		return ctrl.Result{}, err
	}

	if len(applications) > 0 {
		for i := range applications {
			app := &applications[i]
			if !app.DeletionTimestamp.IsZero() {
				continue
			}
			l.Info("Deleting argocdv1.Application", "application", client.ObjectKeyFromObject(app))
			if err := r.Delete(ctx, app); client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete argocdv1.Application %s: %w", client.ObjectKeyFromObject(app), err)
			}
		}
		l.V(1).Info("Waiting for Applications to be deleted", "count", len(applications))
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
	if r.ArgoNamespace == "" {
		r.ArgoNamespace = DefaultNamespace
	}
	if r.MaxConcurrentReconciles <= 0 {
		r.MaxConcurrentReconciles = defaultConcurrentReconciles
	}
	r.requeueInterval = 10 * time.Second

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			MaxConcurrentReconciles: r.MaxConcurrentReconciles,
			RateLimiter:             ratelimitutil.DefaultFastSlow(),
		}).
		Named("ksm-argocd-adapter").
		For(&kcmv1.ServiceSet{}).
		// Not Owns(): an Application lives in Argo CD's namespace, so it
		// cannot hold an owner reference to the ServiceSet.
		Watches(&argocdv1.Application{}, kubeutil.EnqueueRequestsFromMapFunc(func(_ context.Context, o client.Object) ([]ctrl.Request, error) {
			labels := o.GetLabels()
			name, namespace := labels[serviceSetNameLabel], labels[serviceSetNamespaceLabel]
			if name == "" || namespace == "" {
				return nil, nil
			}
			return []ctrl.Request{{Name: name, Namespace: namespace}}, nil
		})).
		Watches(&kcmv1.StateManagementProvider{}, kubeutil.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) ([]ctrl.Request, error) {
			provider, ok := o.(*kcmv1.StateManagementProvider)
			if !ok || provider.AdapterKind() != kcmv1.AdapterKindArgoCD {
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

// ensureApplications creates, updates or removes an Application per service of
// the ServiceSet and prunes the ones that are no longer wanted.
func (r *ServiceSetReconciler) ensureApplications(ctx context.Context, serviceSet *kcmv1.ServiceSet) (err error) {
	start := time.Now()
	l := ctrl.LoggerFrom(ctx)
	l.Info("Ensuring Applications")

	profileCondition := adapterutil.FindCondition(serviceSet, kcmv1.ServiceSetProfileCondition)
	initialConditionStatus := profileCondition.Status

	status := metav1.ConditionFalse
	reason := kcmv1.ServiceSetProfileNotReadyReason
	message := kcmv1.ServiceSetProfileNotReadyMessage

	defer func(initialStatus metav1.ConditionStatus) {
		updated := adapterutil.UpdateCondition(serviceSet, profileCondition, status, reason, message, r.timeFunc())
		if initialStatus != status && status == metav1.ConditionTrue && updated {
			record.Eventf(serviceSet, nil, kcmv1.ServiceSetEnsureProfileSuccessEvent, kcmv1.ServiceSetEnsureProfileEventAction,
				"Successfully ensured Applications for ServiceSet %s", serviceSet.Name)
		}
		l.V(1).Info("Finished ensuring Applications", "duration", time.Since(start))
	}(initialConditionStatus)

	cfg, err := providerConfigFromJSON(serviceSet.Spec.Provider.Config)
	if err != nil {
		reason, message = kcmv1.ServiceSetProfileBuildFailedReason, err.Error()
		return err
	}

	destination, err := r.destination(ctx, serviceSet)
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

		name := applicationName(serviceSet, svc)

		// Argo CD has no "keep the Application but uninstall the release", so
		// an explicit Uninstall is the removal of the Application.
		if svc.HelmAction == helmActionUninstall {
			if err := r.deleteApplication(ctx, name); err != nil {
				return err
			}
			continue
		}

		source, err := r.chartSource(ctx, serviceSet.Namespace, tmpl)
		if err != nil {
			reason, message = kcmv1.ServiceSetHelmChartsBuildFailedReason, err.Error()
			return err
		}

		values, err := r.resolveValues(ctx, serviceSet.Namespace, svc)
		if err != nil {
			reason, message = kcmv1.ServiceSetHelmChartsBuildFailedReason, err.Error()
			return err
		}

		if err := r.ensureRepositorySecret(ctx, serviceSet, source); err != nil {
			reason, message = kcmv1.ServiceSetProfileNotReadyReason, err.Error()
			return err
		}

		desired := buildApplication(serviceSet, svc, tmpl, source.source, values, destination, cfg, r.ArgoNamespace, name)
		if err := r.createOrUpdateApplication(ctx, desired); err != nil {
			reason, message = kcmv1.ServiceSetProfileNotReadyReason, err.Error()
			return err
		}
		wanted[name] = struct{}{}
	}

	// Drop the Applications of services that were removed from the spec.
	existing, err := r.ownedApplications(ctx, serviceSet)
	if err != nil {
		return err
	}
	for i := range existing {
		app := &existing[i]
		if _, ok := wanted[app.Name]; ok {
			continue
		}
		if !app.DeletionTimestamp.IsZero() {
			continue
		}
		l.Info("Deleting orphaned argocdv1.Application", "application", client.ObjectKeyFromObject(app))
		if err := r.Delete(ctx, app); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete argocdv1.Application %s: %w", client.ObjectKeyFromObject(app), err)
		}
	}

	status = metav1.ConditionTrue
	reason = kcmv1.ServiceSetProfileReadyReason
	message = kcmv1.ServiceSetProfileReadyMessage
	return nil
}

func (r *ServiceSetReconciler) createOrUpdateApplication(ctx context.Context, desired *argocdv1.Application) error {
	existing := new(argocdv1.Application)
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, desired); err != nil {
			return fmt.Errorf("failed to create argocdv1.Application %s: %w", client.ObjectKeyFromObject(desired), err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get argocdv1.Application %s: %w", client.ObjectKeyFromObject(desired), err)
	}

	if equality.Semantic.DeepEqual(existing.Spec, desired.Spec) &&
		equality.Semantic.DeepDerivative(desired.Labels, existing.Labels) {
		return nil
	}

	patched := existing.DeepCopy()
	patched.Spec = desired.Spec
	patched.Labels = desired.Labels
	if err := r.Update(ctx, patched); err != nil {
		return fmt.Errorf("failed to update argocdv1.Application %s: %w", client.ObjectKeyFromObject(desired), err)
	}
	return nil
}

func (r *ServiceSetReconciler) deleteApplication(ctx context.Context, name string) error {
	app := new(argocdv1.Application)
	app.Name, app.Namespace = name, r.ArgoNamespace
	if err := r.Delete(ctx, app); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to delete argocdv1.Application %s/%s: %w", r.ArgoNamespace, name, err)
	}
	return nil
}

func (r *ServiceSetReconciler) ownedApplications(ctx context.Context, serviceSet *kcmv1.ServiceSet) ([]argocdv1.Application, error) {
	list := new(argocdv1.ApplicationList)
	err := r.List(ctx, list,
		client.InNamespace(r.ArgoNamespace),
		client.MatchingLabels{
			serviceSetNameLabel:      serviceSet.Name,
			serviceSetNamespaceLabel: serviceSet.Namespace,
		})
	if err != nil {
		return nil, fmt.Errorf("failed to list Applications of ServiceSet %s: %w", client.ObjectKeyFromObject(serviceSet), err)
	}
	return list.Items, nil
}

// destination is the cluster and namespace Argo CD deploys to.
func (r *ServiceSetReconciler) destination(ctx context.Context, serviceSet *kcmv1.ServiceSet) (argocdv1.ApplicationDestination, error) {
	if serviceSet.Spec.Provider.SelfManagement || serviceSet.Spec.Cluster == "" {
		return argocdv1.ApplicationDestination{Server: inClusterServer}, nil
	}

	clusterKey := client.ObjectKey{Namespace: serviceSet.Namespace, Name: serviceSet.Spec.Cluster}
	cd := new(kcmv1.ClusterDeployment)
	if err := r.Get(ctx, clusterKey, cd); err != nil {
		return argocdv1.ApplicationDestination{}, fmt.Errorf("failed to get ClusterDeployment %s: %w", clusterKey, err)
	}

	cred := new(kcmv1.Credential)
	credKey := client.ObjectKey{Namespace: cd.Namespace, Name: cd.Spec.Credential}
	if err := r.Get(ctx, credKey, cred); err != nil {
		return argocdv1.ApplicationDestination{}, fmt.Errorf("failed to get Credential %s: %w", credKey, err)
	}
	if cred.Spec.Region != "" {
		return argocdv1.ApplicationDestination{}, fmt.Errorf("regional cluster %q is not supported by the argocd adapter", cred.Spec.Region)
	}

	// By name, not by URL: the cluster has to be registered with Argo CD
	// anyway, and a name survives an API server address that changes.
	return argocdv1.ApplicationDestination{Name: serviceSet.Spec.Cluster}, nil
}

// collectServiceStatuses translates the state of every produced Application
// back into the ServiceSet status.
func (r *ServiceSetReconciler) collectServiceStatuses(ctx context.Context, serviceSet *kcmv1.ServiceSet) (err error) {
	start := time.Now()
	l := ctrl.LoggerFrom(ctx)
	l.Info("Collecting Service statuses")

	statusesCollectedCondition := adapterutil.FindCondition(serviceSet, kcmv1.ServiceSetStatusesCollectedCondition)
	initialConditionStatus := statusesCollectedCondition.Status

	status := metav1.ConditionFalse
	reason := kcmv1.ServiceSetStatusesNotCollectedReason
	message := kcmv1.ServiceSetStatusesNotCollectedMessage

	defer func(initialStatus metav1.ConditionStatus) {
		updated := adapterutil.UpdateCondition(serviceSet, statusesCollectedCondition, status, reason, message, r.timeFunc())
		if initialStatus != status && status == metav1.ConditionTrue && updated {
			record.Eventf(serviceSet, nil, kcmv1.ServiceSetCollectServiceStatusesSuccessEvent, kcmv1.ServiceSetCollectServiceStatusesEventAction,
				"Successfully collected service statuses for ServiceSet %s", serviceSet.Name)
		}
		l.V(1).Info("Finished services status collection", "duration", time.Since(start))
	}(initialConditionStatus)

	applications, err := r.ownedApplications(ctx, serviceSet)
	if err != nil {
		return err
	}

	byService := make(map[client.ObjectKey]*argocdv1.Application, len(applications))
	for i := range applications {
		app := &applications[i]
		byService[client.ObjectKey{
			Namespace: app.Labels[serviceNamespaceLabel],
			Name:      app.Labels[serviceNameLabel],
		}] = app
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

		if app, found := byService[key]; found {
			newState.State, newState.FailureMessage = serviceStateFromApplication(app)
			if version := deployedChartVersion(app); version != "" {
				newState.Version = version
			}
		} else {
			// Either the Application has not been created yet or the service
			// was uninstalled on purpose; both read as "nothing deployed".
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

	adapterutil.UpdateServicesInReadyStateCondition(serviceSet, r.timeFunc())

	status = metav1.ConditionTrue
	reason = kcmv1.ServiceSetStatusesCollectedReason
	message = kcmv1.ServiceSetStatusesCollectedMessage
	return nil
}

// serviceStateFromApplication maps Argo CD's health and sync status onto the
// KCM service state.
func serviceStateFromApplication(app *argocdv1.Application) (state, failureMessage string) {
	if !app.DeletionTimestamp.IsZero() {
		return kcmv1.ServiceStateDeleting, ""
	}

	// A failed sync is the provider's own verdict on the last attempt and
	// outranks health, which may still describe the previous revision.
	if op := app.Status.OperationState; op != nil &&
		(op.Phase == argocdv1.OperationFailed || op.Phase == argocdv1.OperationError) {
		return kcmv1.ServiceStateFailed, firstNonEmpty(op.Message, failedConditionMessage(app))
	}

	switch app.Status.Health.Status {
	case argocdv1.HealthStatusDegraded:
		return kcmv1.ServiceStateFailed, firstNonEmpty(app.Status.Health.Message, failedConditionMessage(app))
	case argocdv1.HealthStatusHealthy:
		if app.Status.Sync.Status == argocdv1.SyncStatusSynced {
			return kcmv1.ServiceStateDeployed, ""
		}
		return kcmv1.ServiceStateProvisioning, ""
	default:
		return kcmv1.ServiceStateProvisioning, ""
	}
}

// failedConditionMessage falls back to the Application's own conditions, which
// is where Argo CD puts a comparison or sync error that never became an
// operation, such as a chart it could not pull.
func failedConditionMessage(app *argocdv1.Application) string {
	for _, c := range app.Status.Conditions {
		if strings.Contains(c.Type, "Error") {
			return c.Message
		}
	}
	return ""
}

func deployedChartVersion(app *argocdv1.Application) string {
	if app.Status.Sync.Status != argocdv1.SyncStatusSynced {
		return ""
	}
	// For a Helm chart source the synced revision is the chart version.
	if _, err := semver.NewVersion(app.Status.Sync.Revision); err != nil {
		return ""
	}
	return app.Status.Sync.Revision
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

const helmActionUninstall = "Uninstall"

// applicationName is the name of the Application produced for a service. Argo
// CD keeps all of them in one namespace, so the ServiceSet's own namespace has
// to be part of it.
func applicationName(serviceSet *kcmv1.ServiceSet, svc kcmv1.ServiceWithValues) string {
	parts := []string{serviceSet.Namespace, serviceSet.Name}
	if svc.Namespace != "" {
		parts = append(parts, svc.Namespace)
	}
	parts = append(parts, svc.Name)
	return boundedName(strings.Join(parts, "-"))
}

// boundedName keeps a name within what Kubernetes accepts, uniquely.
func boundedName(name string) string {
	const maxNameLength = 253
	if len(name) <= maxNameLength {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	suffix := "-" + hex.EncodeToString(sum[:])[:8]
	return name[:maxNameLength-len(suffix)] + suffix
}

// buildApplication renders the Application for a single service.
func buildApplication(
	serviceSet *kcmv1.ServiceSet,
	svc kcmv1.ServiceWithValues,
	tmpl *kcmv1.ServiceTemplate,
	source *argocdv1.ApplicationSource,
	values string,
	destination argocdv1.ApplicationDestination,
	cfg providerConfig,
	argoNamespace, name string,
) *argocdv1.Application {
	releaseNamespace := svc.Namespace
	if releaseNamespace == "" {
		releaseNamespace = svc.Name
	}
	destination.Namespace = releaseNamespace

	source.Helm = &argocdv1.ApplicationSourceHelm{
		ReleaseName: svc.Name,
		Namespace:   releaseNamespace,
		Values:      values,
	}
	applyHelmOptions(source.Helm, helmOptionsFor(svc, tmpl))

	app := &argocdv1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: argoNamespace,
			Labels: map[string]string{
				kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue,
				serviceSetNameLabel:      serviceSet.Name,
				serviceSetNamespaceLabel: serviceSet.Namespace,
				serviceNameLabel:         svc.Name,
				serviceNamespaceLabel:    svc.Namespace,
			},
			// Without it Argo CD orphans the deployed resources when the
			// Application goes, and a ServiceSet teardown would leave the
			// services running.
			Finalizers: []string{argocdv1.ResourcesFinalizer},
		},
		Spec: argocdv1.ApplicationSpec{
			Project:     firstNonEmpty(cfg.Project, defaultProject),
			Source:      source,
			Destination: destination,
			SyncPolicy:  syncPolicy(cfg, serviceSet),
		},
	}
	return app
}

// syncPolicy is how Argo CD is told to keep the service as the ServiceSet
// describes it. A paused ServiceSet drops the automated part, which is Argo
// CD's equivalent of suspending.
func syncPolicy(cfg providerConfig, serviceSet *kcmv1.ServiceSet) *argocdv1.SyncPolicy {
	options := make([]string, 0, 2+len(cfg.SyncOptions))
	options = append(options,
		// Helm's --create-namespace equivalent: the target namespace is the
		// service's own and nothing else creates it.
		"CreateNamespace=true",
		// A chart whose CRDs and CRs land in one sync needs the CRDs first.
		"RespectIgnoreDifferences=true",
	)
	options = append(options, cfg.SyncOptions...)

	policy := &argocdv1.SyncPolicy{
		SyncOptions: options,
		Retry: &argocdv1.RetryStrategy{
			Limit:   retryLimit(cfg),
			Backoff: &argocdv1.Backoff{Duration: "5s", MaxDuration: "3m"},
		},
	}

	if _, paused := serviceSet.GetAnnotations()[kcmv1.ServiceSetPausedAnnotation]; paused {
		return policy
	}

	policy.Automated = &argocdv1.SyncPolicyAutomated{
		Prune:    cfg.Prune == nil || *cfg.Prune,
		SelfHeal: cfg.SelfHeal == nil || *cfg.SelfHeal,
	}
	return policy
}

func retryLimit(cfg providerConfig) int64 {
	const defaultRetryLimit = 5
	if cfg.RetryLimit != nil {
		return *cfg.RetryLimit
	}
	return defaultRetryLimit
}

// helmOptionsFor returns the options of the template, falling back to the ones
// of the service, as the other adapters do.
func helmOptionsFor(svc kcmv1.ServiceWithValues, tmpl *kcmv1.ServiceTemplate) *kcmv1.ServiceHelmOptions {
	if tmpl.Spec.HelmOptions != nil {
		return tmpl.Spec.HelmOptions
	}
	return svc.HelmOptions
}

// applyHelmOptions translates the two KCM Helm options Argo CD has a knob for.
// The rest describe how Helm itself installs, which Argo CD does not expose:
// it renders the chart and applies the result.
func applyHelmOptions(helm *argocdv1.ApplicationSourceHelm, options *kcmv1.ServiceHelmOptions) {
	if options == nil {
		return
	}
	if options.SkipCRDs != nil {
		helm.SkipCrds = *options.SkipCRDs
	}
	if options.SkipSchemaValidation != nil {
		helm.SkipSchemaValidation = *options.SkipSchemaValidation
	}
}

// chartSource is where an Application's chart comes from, and whether that
// repository has to be registered with Argo CD as an OCI one.
type chartSource struct {
	source        *argocdv1.ApplicationSource
	ociRepository bool
}

// chartSource points the Application at the chart the ServiceTemplate names,
// reading it off the Flux source objects KCM maintains for it.
func (r *ServiceSetReconciler) chartSource(ctx context.Context, namespace string, tmpl *kcmv1.ServiceTemplate) (chartSource, error) {
	key := client.ObjectKeyFromObject(tmpl)

	chartRef := tmpl.GetCommonStatus().ChartRef
	if chartRef == nil {
		return chartSource{}, fmt.Errorf("ServiceTemplate %s: %w", key, errUnsupportedSource)
	}

	chart := new(sourcev1.HelmChart)
	chartKey := client.ObjectKey{Namespace: chartRef.Namespace, Name: chartRef.Name}
	if chartKey.Namespace == "" {
		chartKey.Namespace = namespace
	}
	if err := r.Get(ctx, chartKey, chart); err != nil {
		return chartSource{}, fmt.Errorf("failed to get HelmChart %s of ServiceTemplate %s: %w", chartKey, key, err)
	}

	repoKey := client.ObjectKey{Namespace: chart.Namespace, Name: chart.Spec.SourceRef.Name}
	switch chart.Spec.SourceRef.Kind {
	case sourcev1.HelmRepositoryKind:
		repo := new(sourcev1.HelmRepository)
		if err := r.Get(ctx, repoKey, repo); err != nil {
			return chartSource{}, fmt.Errorf("failed to get HelmRepository %s: %w", repoKey, err)
		}
		return chartSource{
			source: &argocdv1.ApplicationSource{
				// Argo CD takes an OCI registry path without the scheme and
				// marks the repository itself as OCI; see ensureRepositorySecret.
				RepoURL:        strings.TrimPrefix(repo.Spec.URL, "oci://"),
				Chart:          chart.Spec.Chart,
				TargetRevision: chartVersion(chart),
			},
			ociRepository: repo.Spec.Type == helmutil.RegistryTypeOCI,
		}, nil
	case sourcev1.GitRepositoryKind:
		repo := new(sourcev1.GitRepository)
		if err := r.Get(ctx, repoKey, repo); err != nil {
			return chartSource{}, fmt.Errorf("failed to get GitRepository %s: %w", repoKey, err)
		}
		return chartSource{source: &argocdv1.ApplicationSource{
			RepoURL:        repo.Spec.URL,
			Path:           chart.Spec.Chart,
			TargetRevision: gitRevision(repo),
		}}, nil
	default:
		return chartSource{}, fmt.Errorf("ServiceTemplate %s has a %s chart source: %w",
			key, chart.Spec.SourceRef.Kind, errUnsupportedSource)
	}
}

// chartVersion prefers what the source controller actually resolved, which for
// a Git-backed chart is the only place the version appears.
func chartVersion(chart *sourcev1.HelmChart) string {
	if chart.Status.Artifact != nil && chart.Status.Artifact.Revision != "" {
		if _, err := semver.NewVersion(chart.Status.Artifact.Revision); err == nil {
			return chart.Status.Artifact.Revision
		}
	}
	return chart.Spec.Version
}

func gitRevision(repo *sourcev1.GitRepository) string {
	ref := repo.Spec.Reference
	if ref == nil {
		return "HEAD"
	}
	return firstNonEmpty(ref.Tag, ref.SemVer, ref.Branch, ref.Commit, "HEAD")
}

// ensureRepositorySecret registers the chart's repository with Argo CD. It is
// needed for an OCI registry, which Argo CD otherwise reads as a plain Helm
// repository, and for anything with credentials.
func (r *ServiceSetReconciler) ensureRepositorySecret(ctx context.Context, serviceSet *kcmv1.ServiceSet, source chartSource) error {
	if !source.ociRepository {
		// A plain HTTP(S) Helm repository or a public Git one needs no entry:
		// Argo CD reaches it from the Application's repoURL alone.
		return nil
	}

	url := source.source.RepoURL
	sum := sha256.Sum256([]byte(url))
	name := "kcm-repo-" + hex.EncodeToString(sum[:8])

	desired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: r.ArgoNamespace,
			Labels: map[string]string{
				argoSecretTypeLabel:      argoSecretTypeRepo,
				kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue,
			},
		},
		StringData: map[string]string{
			"name":      name,
			"url":       url,
			"type":      "helm",
			"enableOCI": "true",
		},
	}

	existing := new(corev1.Secret)
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, desired); client.IgnoreAlreadyExists(err) != nil {
			return fmt.Errorf("failed to create Argo CD repository secret %s for ServiceSet %s: %w",
				client.ObjectKeyFromObject(desired), client.ObjectKeyFromObject(serviceSet), err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get Argo CD repository secret %s: %w", client.ObjectKeyFromObject(desired), err)
	}

	if string(existing.Data["url"]) == url && existing.Labels[argoSecretTypeLabel] == argoSecretTypeRepo {
		return nil
	}

	patched := existing.DeepCopy()
	patched.StringData = desired.StringData
	if patched.Labels == nil {
		patched.Labels = make(map[string]string, len(desired.Labels))
	}
	maps.Copy(patched.Labels, desired.Labels)
	if err := r.Update(ctx, patched); err != nil {
		return fmt.Errorf("failed to update Argo CD repository secret %s: %w", client.ObjectKeyFromObject(desired), err)
	}
	return nil
}

// resolveValues merges the values the service references with the ones it
// carries, the latter winning - the order the other adapters hand to Helm.
// Argo CD has no valuesFrom, so the merge happens here.
func (r *ServiceSetReconciler) resolveValues(ctx context.Context, namespace string, svc kcmv1.ServiceWithValues) (string, error) {
	documents := make([]string, 0, len(svc.ValuesFrom)+1)

	for _, ref := range svc.ValuesFrom {
		key := client.ObjectKey{Namespace: namespace, Name: ref.Name}
		switch ref.Kind {
		case "ConfigMap":
			cm := new(corev1.ConfigMap)
			if err := r.Get(ctx, key, cm); err != nil {
				return "", fmt.Errorf("failed to get ConfigMap %s referenced by service %s: %w", key, svc.Name, err)
			}
			documents = append(documents, slices.Sorted(maps.Values(cm.Data))...)
		case "Secret":
			secret := new(corev1.Secret)
			if err := r.Get(ctx, key, secret); err != nil {
				return "", fmt.Errorf("failed to get Secret %s referenced by service %s: %w", key, svc.Name, err)
			}
			values := make([]string, 0, len(secret.Data))
			for _, k := range slices.Sorted(maps.Keys(secret.Data)) {
				values = append(values, string(secret.Data[k]))
			}
			documents = append(documents, values...)
		default:
			return "", fmt.Errorf("service %s references unsupported valuesFrom kind %s", svc.Name, ref.Kind)
		}
	}

	documents = append(documents, svc.Values)
	return mergeValues(documents)
}

// mergeValues merges YAML documents left to right, the way Helm merges -f
// files: later keys win, maps are merged, everything else is replaced.
func mergeValues(documents []string) (string, error) {
	merged := make(map[string]any)
	for _, document := range documents {
		if strings.TrimSpace(document) == "" {
			continue
		}
		values := make(map[string]any)
		if err := yaml.Unmarshal([]byte(document), &values); err != nil {
			return "", fmt.Errorf("failed to parse values: %w", err)
		}
		merged = mergeMaps(merged, values)
	}
	if len(merged) == 0 {
		return "", nil
	}
	out, err := yaml.Marshal(merged)
	if err != nil {
		return "", fmt.Errorf("failed to render values: %w", err)
	}
	return string(out), nil
}

func mergeMaps(base, overlay map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(overlay))
	maps.Copy(out, base)
	for k, v := range overlay {
		if overlayMap, ok := v.(map[string]any); ok {
			if baseMap, ok := out[k].(map[string]any); ok {
				out[k] = mergeMaps(baseMap, overlayMap)
				continue
			}
		}
		out[k] = v
	}
	return out
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
