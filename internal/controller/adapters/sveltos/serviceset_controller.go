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

package sveltos

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"dario.cat/mergo"
	"github.com/Masterminds/semver/v3"
	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/go-logr/logr"
	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	"github.com/projectsveltos/addon-controller/lib/clusterops"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/utils/ptr"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/record"
	"github.com/K0rdent/kcm/internal/serviceset"
	helmutil "github.com/K0rdent/kcm/internal/util/helm"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	pointerutil "github.com/K0rdent/kcm/internal/util/pointer"
	ratelimitutil "github.com/K0rdent/kcm/internal/util/ratelimit"
	schemeutil "github.com/K0rdent/kcm/internal/util/scheme"
)

const (
	sveltosDriftIgnorePatch = `- op: add
  path: /metadata/annotations/projectsveltos.io~1driftDetectionIgnore
  value: ok`

	managementSveltosCluster = "mgmt"
)

var (
	errEmptyConfig                  = errors.New("empty config")
	errBuildProfileFromConfigFailed = errors.New("failed to build profile from config")
	errBuildHelmChartsFailed        = errors.New("failed to build helm charts")
	errBuildKustomizationRefsFailed = errors.New("failed to build kustomization refs")
	errBuildPolicyRefsFailed        = errors.New("failed to build policy refs")
	errNoMatchingClusters           = errors.New("no matching clusters for ServiceSet")
)

type profileConfig struct {
	// KSM specific configuration
	// Priority is the priority of the Profile.
	Priority *int32 `json:"priority,omitempty"`
	// DriftIgnore is a list of [github.com/projectsveltos/libsveltos/api/v1beta1.PatchSelector] to ignore
	// when checking for drift.
	DriftIgnore []libsveltosv1beta1.PatchSelector `json:"driftIgnore,omitempty"`

	SyncMode             string                                       `json:"syncMode,omitempty"`
	TemplateResourceRefs []addoncontrollerv1beta1.TemplateResourceRef `json:"templateResourceRefs,omitempty"`
	PolicyRefs           []addoncontrollerv1beta1.PolicyRef           `json:"policyRefs,omitempty"`
	DriftExclusions      []libsveltosv1beta1.DriftExclusion           `json:"driftExclusions,omitempty"`
	Patches              []libsveltosv1beta1.Patch                    `json:"patches,omitempty"`
	ContinueOnError      bool                                         `json:"continueOnError,omitempty"`
	Reloader             bool                                         `json:"reloader,omitempty"`
	StopOnConflict       bool                                         `json:"stopOnConflict,omitempty"`
}

// ServiceSetReconciler reconciles a ServiceSet object and produces
// [github.com/projectsveltos/addon-controller/api/v1beta1.Profile] objects.
type ServiceSetReconciler struct {
	client.Client

	timeFunc  func() time.Time
	eventChan chan event.GenericEvent

	SystemNamespace string

	// AdapterName is the name of the workload running the controller
	// effectively this name is used to identify adapter in the
	// [github.com/k0rdent/kcm/api/v1beta1.StateManagementProvider] spec.
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

	rgnClient, err := getRegionalClient(ctx, r.Client, serviceSet, r.SystemNamespace)
	if err != nil {
		l.Error(err, "failed to get regional client")
		return ctrl.Result{}, err
	}

	if !serviceSet.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, rgnClient, serviceSet)
	}

	if controllerutil.AddFinalizer(serviceSet, kcmv1.ServiceSetFinalizer) {
		return ctrl.Result{}, r.Update(ctx, serviceSet)
	}

	smp := new(kcmv1.StateManagementProvider)
	if err = r.Get(ctx, client.ObjectKey{Name: serviceSet.Spec.Provider.Name}, smp); err != nil {
		return ctrl.Result{}, err
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
		// we won't update serviceSet anyhow in case of any error
		// occurred during reconciliation, by returning error
		// serviceSet object being reconciled will be requeued
		// with respect of rate limits
		if err != nil {
			return
		}

		fillNotDeployedServices(serviceSet, r.timeFunc)
		// if serviceSet status changed we'll update object's
		// status, so object being reconciled will be requeued,
		// otherwise we'll do nothing since the poller will
		// enqueue serviceSet object in case any changes in
		// corresponding ClusterSummary object.
		if !equality.Semantic.DeepEqual(clone.Status, serviceSet.Status) {
			err = r.Status().Update(ctx, serviceSet)
		}
		l.Info("ServiceSet reconciled", "duration", time.Since(start))
	}()

	serviceSet.Status.Provider = kcmv1.ProviderState{
		Ready:     smp.Status.Ready,
		Suspended: smp.Spec.Suspend,
	}
	if !smp.Status.Ready {
		record.Eventf(serviceSet, serviceSet.Generation, kcmv1.StateManagementProviderNotReadyEvent,
			"StateManagementProvider %s not ready, skipping ServiceSet %s reconciliation", smp.Name, serviceSet.Name)
		l.Info("StateManagementProvider is not ready, skipping", "provider", serviceSet.Spec.Provider)
		return ctrl.Result{}, nil
	}
	if smp.Spec.Suspend {
		record.Eventf(serviceSet, serviceSet.Generation, kcmv1.StateManagementProviderSuspendedEvent,
			"StateManagementProvider %s suspended, skipping ServiceSet %s reconciliation", smp.Name, serviceSet.Name)
		l.Info("StateManagementProvider is suspended, skipping", "provider", serviceSet.Spec.Provider)
		return ctrl.Result{}, nil
	}

	// first we'll ensure the profile exists and up-to-date
	if err = r.ensureProfile(ctx, rgnClient, serviceSet); err != nil {
		record.Warnf(serviceSet, serviceSet.Generation, kcmv1.ServiceSetEnsureProfileFailedEvent,
			"Failed to ensure Profile for ServiceSet %s: %v", serviceSet.Name, err)
		return ctrl.Result{}, err
	}
	// then we'll collect the statuses of the services
	err = r.collectServiceStatuses(ctx, rgnClient, serviceSet)
	if err != nil {
		record.Warnf(serviceSet, serviceSet.Generation, kcmv1.ServiceSetCollectServiceStatusesFailedEvent,
			"Failed to collect Service statuses for ServiceSet %s: %v", serviceSet.Name, err)
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ServiceSetReconciler) reconcileDelete(ctx context.Context, rgnClient client.Client, serviceSet *kcmv1.ServiceSet) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling ServiceSet deletion")

	// we'll update the ServiceSet status to reflect the deletion of the services
	if slices.ContainsFunc(serviceSet.Status.Services, func(state kcmv1.ServiceState) bool {
		return state.State != kcmv1.ServiceStateDeleting
	}) {
		serviceStates := make([]kcmv1.ServiceState, 0, len(serviceSet.Status.Services))
		for _, state := range serviceSet.Status.Services {
			if state.State == kcmv1.ServiceStateDeleting {
				continue
			}
			newState := state.DeepCopy()
			newState.State = kcmv1.ServiceStateDeleting
			newState.LastStateTransitionTime = pointerutil.To(metav1.NewTime(r.timeFunc()))
			serviceStates = append(serviceStates, *newState)
		}
		serviceSet.Status.Services = serviceStates
		return ctrl.Result{}, r.Status().Update(ctx, serviceSet)
	}

	var profile client.Object
	if serviceSet.Spec.Provider.SelfManagement {
		profile = new(addoncontrollerv1beta1.ClusterProfile)
	} else {
		profile = new(addoncontrollerv1beta1.Profile)
	}

	key := client.ObjectKeyFromObject(serviceSet)
	err := rgnClient.Get(ctx, key, profile)
	// if IgnoreNotFound returns non-nil error, it means that the error
	// occurred while sending the request to the kube-apiserver.
	if client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Profile: %w", err)
	}
	// if error is nil, it means that the Profile was found and we need
	// to initiate the deletion of the Profile or wait for it to be deleted.
	if err == nil {
		if profile.GetDeletionTimestamp().IsZero() {
			l.Info("Deleting Profile", "profile", profile.GetName())
			if err = rgnClient.Delete(ctx, profile); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete Profile: %w", err)
			}
			return ctrl.Result{RequeueAfter: r.requeueInterval}, nil
		}
		l.V(1).Info("Waiting for Profile to be deleted", "profile", profile.GetName())
		return ctrl.Result{RequeueAfter: r.requeueInterval}, nil
	}

	// otherwise the Profile was not found, so we can remove the finalizer
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

	// in case reconciliation will slowdown and occasionally poller will produce
	// events faster than controller will reconcile objects, we will have a 10-fold
	// capacity reserve for event channel.
	r.eventChan = make(chan event.GenericEvent, r.MaxConcurrentReconciles*10)

	poller := &Poller{
		Client:          r.Client,
		requeueInterval: r.requeueInterval,
		eventChan:       r.eventChan,
		systemNamespace: r.SystemNamespace,
	}

	if err := mgr.Add(poller); err != nil {
		return fmt.Errorf("failed to add poller to manager: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			MaxConcurrentReconciles: 10,
			RateLimiter:             ratelimitutil.DefaultFastSlow(),
		}).
		Named("ksm-sveltos-adapter").
		Watches(&kcmv1.ServiceSet{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []ctrl.Request {
			serviceSet, ok := o.(*kcmv1.ServiceSet)
			if !ok {
				return nil
			}
			provider := new(kcmv1.StateManagementProvider)
			if err := r.Get(ctx, client.ObjectKey{Name: serviceSet.Spec.Provider.Name}, provider); err != nil {
				return nil
			}
			if provider.Spec.Adapter.Name != r.AdapterName && provider.Spec.Adapter.Namespace != r.AdapterNamespace {
				return nil
			}
			return []ctrl.Request{
				{
					NamespacedName: client.ObjectKey{
						Name:      serviceSet.Name,
						Namespace: serviceSet.Namespace,
					},
				},
			}
		})).
		Watches(&kcmv1.StateManagementProvider{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []ctrl.Request {
			provider, ok := o.(*kcmv1.StateManagementProvider)
			if !ok {
				return nil
			}
			if provider.Spec.Adapter.Name != r.AdapterName && provider.Spec.Adapter.Namespace != r.AdapterNamespace {
				return nil
			}

			selector := fields.OneTermEqualSelector(kcmv1.ServiceSetProviderIndexKey, provider.Name)
			serviceSets := new(kcmv1.ServiceSetList)
			if err := r.List(ctx, serviceSets, client.MatchingFieldsSelector{Selector: selector}); err != nil {
				return nil
			}
			requests := make([]ctrl.Request, 0, len(serviceSets.Items))
			for _, serviceSet := range serviceSets.Items {
				requests = append(requests, ctrl.Request{
					NamespacedName: client.ObjectKey{
						Name:      serviceSet.Name,
						Namespace: serviceSet.Namespace,
					},
				})
			}
			return requests
		})).
		WatchesRawSource(source.Channel(r.eventChan, &handler.EnqueueRequestForObject{})).
		Complete(r)
}

// ensureProfile ensures that a [github.com/projectsveltos/addon-controller/api/v1beta1.Profile]
// object exists for a given [github.com/K0rdent/kcm/api/v1beta1.ServiceSet].
func (r *ServiceSetReconciler) ensureProfile(ctx context.Context, rgnClient client.Client, serviceSet *kcmv1.ServiceSet) error {
	start := time.Now()
	l := ctrl.LoggerFrom(ctx)
	l.Info("Ensuring ProjectSveltos Profile")
	profileCondition, _ := findCondition(serviceSet, kcmv1.ServiceSetProfileCondition)

	status := metav1.ConditionFalse
	reason := kcmv1.ServiceSetProfileNotReadyReason
	message := kcmv1.ServiceSetProfileNotReadyMessage

	defer func() {
		if updateCondition(serviceSet, profileCondition, status, reason, message, r.timeFunc()) && status == metav1.ConditionTrue {
			l.Info("Successfully ensured ProjectSveltos Profile")
			record.Eventf(serviceSet, serviceSet.Generation, kcmv1.ServiceSetEnsureProfileSuccessEvent,
				"Successfully ensured ProjectSveltos Profile for ServiceSet %s", serviceSet.Name)
		}
		l.V(1).Info("Finished ensuring ProjectSveltos Profile", "duration", time.Since(start))
	}()

	spec, err := r.profileSpec(ctx, rgnClient, serviceSet)
	if errors.Is(err, errBuildProfileFromConfigFailed) {
		reason = kcmv1.ServiceSetProfileBuildFailedReason
		message = fmt.Sprintf("Failed to build Profile from ServiceSet %s configuration: %v", serviceSet.Name, err)
	}
	if errors.Is(err, errBuildHelmChartsFailed) {
		reason = kcmv1.ServiceSetHelmChartsBuildFailedReason
		message = fmt.Sprintf("Failed to build Helm Charts from ServiceSet %s configuration: %v", serviceSet.Name, err)
	}
	if errors.Is(err, errBuildKustomizationRefsFailed) {
		reason = kcmv1.ServiceSetKustomizationRefsBuildFailedReason
		message = fmt.Sprintf("Failed to build KustomizationRefs from ServiceSet %s configuration: %v", serviceSet.Name, err)
	}
	if errors.Is(err, errBuildPolicyRefsFailed) {
		reason = kcmv1.ServiceSetPolicyRefsBuildFailedReason
		message = fmt.Sprintf("Failed to build PolicyRefs from ServiceSet %s configuration: %v", serviceSet.Name, err)
	}
	if err != nil {
		return fmt.Errorf("failed to build Profile: %w", err)
	}

	if serviceSet.Spec.Provider.SelfManagement {
		if err = r.createOrUpdateClusterProfile(ctx, rgnClient, serviceSet, spec); err != nil {
			return fmt.Errorf("failed to create or update ClusterProfile: %w", err)
		}
	} else {
		if err = r.createOrUpdateProfile(ctx, rgnClient, serviceSet, spec); err != nil {
			return fmt.Errorf("failed to create or update Profile: %w", err)
		}
	}

	status = metav1.ConditionTrue
	reason = kcmv1.ServiceSetProfileReadyReason
	message = kcmv1.ServiceSetProfileReadyMessage
	return nil
}

func (*ServiceSetReconciler) createOrUpdateProfile(ctx context.Context, rgnClient client.Client, serviceSet *kcmv1.ServiceSet, spec *addoncontrollerv1beta1.Spec) error {
	ownerReference := metav1.NewControllerRef(serviceSet, kcmv1.GroupVersion.WithKind(kcmv1.ServiceSetKind))

	profile := new(addoncontrollerv1beta1.Profile)
	key := client.ObjectKeyFromObject(serviceSet)
	err := rgnClient.Get(ctx, key, profile)
	// if IgnoreNotFound returns non-nil error, this means that
	// an error occurred while trying to request kube-apiserver
	if client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to get Profile: %w", err)
	}

	annotationsUpdated := handlePauseAnnotations(&profile.ObjectMeta, serviceSet)

	switch {
	// we already excluded all errors except NotFound
	// hence if the error is not nil, it means that the object was not found
	case err != nil:
		profile.Name = serviceSet.Name
		profile.Namespace = serviceSet.Namespace
		profile.Labels = map[string]string{
			kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue,
		}
		profile.OwnerReferences = []metav1.OwnerReference{*ownerReference}
		profile.Spec = *spec
		if err = rgnClient.Create(ctx, profile); err != nil {
			return fmt.Errorf("failed to create Profile for ServiceSet %s: %w", serviceSet.Name, err)
		}
	// if profile spec is not equal to the spec we just created,
	// we need to update it
	case annotationsUpdated || !equality.Semantic.DeepEqual(profile.Spec, *spec):
		profile.OwnerReferences = []metav1.OwnerReference{*ownerReference}
		profile.Spec = *spec
		if err = rgnClient.Update(ctx, profile); err != nil {
			return fmt.Errorf("failed to update Profile for ServiceSet %s: %w", serviceSet.Name, err)
		}
	}
	return nil
}

func (*ServiceSetReconciler) createOrUpdateClusterProfile(ctx context.Context, rgnClient client.Client, serviceSet *kcmv1.ServiceSet, spec *addoncontrollerv1beta1.Spec) error {
	ownerReference := metav1.NewControllerRef(serviceSet, kcmv1.GroupVersion.WithKind(kcmv1.ServiceSetKind))

	profile := new(addoncontrollerv1beta1.ClusterProfile)
	key := client.ObjectKeyFromObject(serviceSet)
	err := rgnClient.Get(ctx, key, profile)
	// if IgnoreNotFound returns non-nil error, this means that
	// an error occurred while trying to request kube-apiserver
	if client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to get Profile: %w", err)
	}

	annotationsUpdated := handlePauseAnnotations(&profile.ObjectMeta, serviceSet)

	switch {
	// we already excluded all errors except NotFound
	// hence if the error is not nil, it means that the object was not found
	case err != nil:
		profile.Name = serviceSet.Name
		profile.Labels = map[string]string{
			kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue,
		}
		profile.OwnerReferences = []metav1.OwnerReference{*ownerReference}
		profile.Spec = *spec
		if err = rgnClient.Create(ctx, profile); err != nil {
			return fmt.Errorf("failed to create Profile for ServiceSet %s: %w", serviceSet.Name, err)
		}
	// if profile spec is not equal to the spec we just created,
	// we need to update it
	case annotationsUpdated || !equality.Semantic.DeepEqual(profile.Spec, *spec):
		profile.OwnerReferences = []metav1.OwnerReference{*ownerReference}
		profile.Spec = *spec
		if err = rgnClient.Update(ctx, profile); err != nil {
			return fmt.Errorf("failed to update Profile for ServiceSet %s: %w", serviceSet.Name, err)
		}
	}
	return nil
}

func handlePauseAnnotations(profile *metav1.ObjectMeta, serviceSet *kcmv1.ServiceSet) (annoUpdated bool) {
	profileAnnotations := profile.GetAnnotations()
	if profileAnnotations == nil {
		profileAnnotations = make(map[string]string)
	}
	_, internalPaused := serviceSet.GetAnnotations()[kcmv1.ServiceSetPausedAnnotation]
	_, externalPaused := profileAnnotations[addoncontrollerv1beta1.ProfilePausedAnnotation]

	if internalPaused == externalPaused {
		return false
	}
	switch {
	case internalPaused:
		profileAnnotations[addoncontrollerv1beta1.ProfilePausedAnnotation] = "true"
	case externalPaused:
		delete(profileAnnotations, addoncontrollerv1beta1.ProfilePausedAnnotation)
	}
	profile.SetAnnotations(profileAnnotations)
	return true
}

func (r *ServiceSetReconciler) profileSpec(ctx context.Context, rgnClient client.Client, serviceSet *kcmv1.ServiceSet) (*addoncontrollerv1beta1.Spec, error) {
	var (
		clusterSelector             libsveltosv1beta1.Selector
		clusterReference            corev1.ObjectReference
		clusterTemplateResourceRefs []addoncontrollerv1beta1.TemplateResourceRef
		clusterPolicyRefs           []addoncontrollerv1beta1.PolicyRef
		err                         error
	)
	if serviceSet.Spec.Provider.SelfManagement {
		clusterSelector = libsveltosv1beta1.Selector{
			LabelSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					kcmv1.K0rdentManagementClusterLabelKey: kcmv1.K0rdentManagementClusterLabelValue,
					"sveltos-agent":                        "present",
				},
			},
		}
		clusterReference = corev1.ObjectReference{
			Kind:       libsveltosv1beta1.SveltosClusterKind,
			Namespace:  managementSveltosCluster,
			Name:       managementSveltosCluster,
			APIVersion: libsveltosv1beta1.GroupVersion.WithKind(libsveltosv1beta1.SveltosClusterKind).GroupVersion().String(),
		}
	} else {
		cd := new(kcmv1.ClusterDeployment)
		key := client.ObjectKey{
			Namespace: serviceSet.Namespace,
			Name:      serviceSet.Spec.Cluster,
		}
		if err := r.Get(ctx, key, cd); err != nil {
			return nil, fmt.Errorf("failed to get ClusterDeployment: %w", err)
		}
		cred := new(kcmv1.Credential)
		key = client.ObjectKey{
			Namespace: cd.Namespace,
			Name:      cd.Spec.Credential,
		}
		if err := r.Get(ctx, key, cred); err != nil {
			return nil, fmt.Errorf("failed to get Credential: %w", err)
		}
		clusterReference, err = r.getClusterReference(ctx, rgnClient, client.ObjectKeyFromObject(cd))
		if err != nil {
			return nil, fmt.Errorf("failed to get ClusterReference for ClusterDeployment %s/%s: %w", cd.Namespace, cd.Name, err)
		}
		// we need to propagate credentials if the ServiceSet was produced by the ClusterDeployment controller only,
		// otherwise, every MultiClusterService-related ServiceSet will try to propagate credentials which will lead
		// to failing deployments.
		if serviceSet.Spec.MultiClusterService == "" {
			clusterTemplateResourceRefs = projectTemplateResourceRefs(cd, cred)
			clusterPolicyRefs = projectPolicyRefs(cd, cred)
		}
	}

	spec, err := buildProfileSpec(serviceSet.Spec.Provider.Config)
	if err != nil && !errors.Is(err, errEmptyConfig) {
		record.Warnf(serviceSet, serviceSet.Generation, kcmv1.ServiceSetProfileBuildFailedEvent,
			"Failed to build Profile for ServiceSet %s: %v", serviceSet.Name, err)
		return nil, errors.Join(errBuildProfileFromConfigFailed, err)
	}
	spec.ClusterSelector = clusterSelector
	spec.ClusterRefs = []corev1.ObjectReference{clusterReference}
	spec.TemplateResourceRefs = append(spec.TemplateResourceRefs, clusterTemplateResourceRefs...)

	helmCharts, err := getHelmCharts(ctx, r.Client, serviceSet)
	if err != nil {
		record.Warnf(serviceSet, serviceSet.Generation, kcmv1.ServiceSetHelmChartsBuildFailedEvent,
			"Failed to get Helm charts for ServiceSet %s: %v", serviceSet.Name, err)
		return nil, errors.Join(errBuildHelmChartsFailed, err)
	}
	kustomizationRefs, err := getKustomizationRefs(ctx, r.Client, serviceSet)
	if err != nil {
		record.Warnf(serviceSet, serviceSet.Generation, kcmv1.ServiceSetKustomizationRefsBuildFailedEvent,
			"Failed to get KustomizationRefs for ServiceSet %s: %v", serviceSet.Name, err)
		return nil, errors.Join(errBuildKustomizationRefsFailed, err)
	}
	policyRefs, err := getPolicyRefs(ctx, r.Client, serviceSet)
	if err != nil {
		record.Warnf(serviceSet, serviceSet.Generation, kcmv1.ServiceSetPolicyRefsBuildFailedEvent,
			"Failed to get PolicyRefs for ServiceSet %s: %v", serviceSet.Name, err)
		return nil, errors.Join(errBuildPolicyRefsFailed, err)
	}
	policyRefs = append(policyRefs, clusterPolicyRefs...)
	spec.HelmCharts = helmCharts
	spec.KustomizationRefs = kustomizationRefs
	spec.PolicyRefs = append(spec.PolicyRefs, policyRefs...)
	return spec, nil
}

// getClusterReference returns the v1.ObjectReference to the underlying cluster object. It might be either CAPI Cluster
// or ProjectSveltos SveltosCluster.
func (*ServiceSetReconciler) getClusterReference(ctx context.Context, rgnClient client.Client, key client.ObjectKey) (corev1.ObjectReference, error) {
	// we'll try to find underlying CAPI Cluster object, in case of success we'll return object
	// reference to it
	capiCluster := new(clusterapiv1.Cluster)
	err := rgnClient.Get(ctx, key, capiCluster)
	// in this case an error should be returned
	if client.IgnoreNotFound(err) != nil {
		return corev1.ObjectReference{}, err
	}
	// if no error occurred we'll return reference to discovered object
	if err == nil {
		return corev1.ObjectReference{
			Kind:       clusterapiv1.ClusterKind,
			Namespace:  key.Namespace,
			Name:       key.Name,
			APIVersion: clusterapiv1.GroupVersion.WithKind(clusterapiv1.ClusterKind).GroupVersion().String(),
		}, nil
	}
	// otherwise we'll try to get underlying ProjectSveltos SveltosCluster object.
	sveltosCluster := new(libsveltosv1beta1.SveltosCluster)
	err = rgnClient.Get(ctx, key, sveltosCluster)
	if err == nil {
		return corev1.ObjectReference{
			Kind:       libsveltosv1beta1.SveltosClusterKind,
			Namespace:  key.Namespace,
			Name:       key.Name,
			APIVersion: libsveltosv1beta1.GroupVersion.WithKind(libsveltosv1beta1.SveltosClusterKind).GroupVersion().String(),
		}, nil
	}
	return corev1.ObjectReference{}, err
}

func (*ServiceSetReconciler) collectServiceStatuses(ctx context.Context, rgnClient client.Client, serviceSet *kcmv1.ServiceSet) (err error) {
	start := time.Now()
	l := ctrl.LoggerFrom(ctx)
	l.Info("Collecting Service statuses")

	if serviceSet.Spec.Provider.SelfManagement {
		clusterProfile := new(addoncontrollerv1beta1.ClusterProfile)
		key := client.ObjectKeyFromObject(serviceSet)
		if err := rgnClient.Get(ctx, key, clusterProfile); err != nil {
			return fmt.Errorf("failed to get ClusterProfile: %w", err)
		}

		l.V(1).Info("Found matching ClusterProfile", "ClusterProfile", client.ObjectKeyFromObject(clusterProfile))
		err = collectServiceStatusesFromProfileOrClusterProfile(ctx, rgnClient, serviceSet, clusterProfile)
	} else {
		profile := new(addoncontrollerv1beta1.Profile)
		key := client.ObjectKeyFromObject(serviceSet)
		if err := rgnClient.Get(ctx, key, profile); err != nil {
			return fmt.Errorf("failed to get Profile: %w", err)
		}

		l.V(1).Info("Found matching Profile", "Profile", client.ObjectKeyFromObject(profile))
		err = collectServiceStatusesFromProfileOrClusterProfile(ctx, rgnClient, serviceSet, profile)
	}

	l.Info("Collecting Service statuses completed", "duration", time.Since(start))
	return err
}

func getClusterSummaryForServiceSet(ctx context.Context, rgnClient client.Client, serviceSet *kcmv1.ServiceSet, profileObj client.Object) (*addoncontrollerv1beta1.ClusterSummary, error) {
	l := ctrl.LoggerFrom(ctx)

	var (
		matchingRefs []corev1.ObjectReference
		profileKind  string
		profileName  string
	)

	switch p := profileObj.(type) {
	case *addoncontrollerv1beta1.Profile:
		matchingRefs = p.Status.MatchingClusterRefs
		profileKind = addoncontrollerv1beta1.ProfileKind
		profileName = p.Name
		l.V(1).Info("Processing Profile", "profile", client.ObjectKeyFromObject(p))
	case *addoncontrollerv1beta1.ClusterProfile:
		matchingRefs = p.Status.MatchingClusterRefs
		profileKind = addoncontrollerv1beta1.ClusterProfileKind
		profileName = p.Name
		l.V(1).Info("Processing ClusterProfile", "clusterProfile", client.ObjectKeyFromObject(p))
	default:
		return nil, fmt.Errorf("unsupported profile type: %T", profileObj)
	}

	if len(matchingRefs) == 0 {
		l.Info("No matching clusters found for ServiceSet")
		serviceSet.Status.Deployed = false
		return nil, errNoMatchingClusters
	}

	// Use the first matching cluster reference for both types because:
	// 1. For Profile type we expect that it will match only a single cluster.
	// 2. While ClusterProfile can match multiple clusters, we are using it
	// only for self-management of the management cluster.
	//
	// TODO(https://github.com/k0rdent/kcm/issues/2083):
	// It seems like there is no reason to use ClusterProfile instead of Profile for self-management
	// other than backwards compatibility. Even then users should simply be able to delete
	// the ClusterProfile created by previous versions. We already broke backward compatibility
	// for MCS by creating Profiles instead of ClusterProfiles via ServiceSet.
	// If we use Profile for self-management, a lot of the code in this file can be simplified.
	// Also note that the status of ServiceSet is not designed to capture statuses from multiple clusters.
	obj := matchingRefs[0]
	isSveltosCluster := obj.APIVersion == libsveltosv1beta1.GroupVersion.WithKind(libsveltosv1beta1.SveltosClusterKind).GroupVersion().String()
	summaryName := clusterops.GetClusterSummaryName(profileKind, profileName, obj.Name, isSveltosCluster)

	summary := new(addoncontrollerv1beta1.ClusterSummary)
	summaryRef := client.ObjectKey{Name: summaryName, Namespace: obj.Namespace}
	if err := rgnClient.Get(ctx, summaryRef, summary); err != nil {
		return nil, fmt.Errorf("failed to get ClusterSummary %s to fetch status: %w", summaryRef.String(), err)
	}
	return summary, nil
}

func collectServiceStatusesFromProfileOrClusterProfile(ctx context.Context, rgnClient client.Client, serviceSet *kcmv1.ServiceSet, profileObj client.Object) (_ error) {
	l := ctrl.LoggerFrom(ctx)

	summary, err := getClusterSummaryForServiceSet(ctx, rgnClient, serviceSet, profileObj)
	if errors.Is(err, errNoMatchingClusters) {
		return nil
	}
	if err != nil {
		return err
	}

	l.V(1).Info("Found matching ClusterSummary", "summary", client.ObjectKeyFromObject(summary))
	serviceSet.Status.Services = servicesStateFromSummary(l, summary, serviceSet)
	serviceSet.Status.Deployed = !slices.ContainsFunc(serviceSet.Status.Services, func(s kcmv1.ServiceState) bool {
		return s.State != kcmv1.ServiceStateDeployed
	})

	return nil
}

// getHelmCharts returns slice of helm chart options to use with Sveltos.
// Namespace is the namespace of the referred templates in services slice.
func getHelmCharts(ctx context.Context, c client.Client, serviceSet *kcmv1.ServiceSet) ([]addoncontrollerv1beta1.HelmChart, error) {
	helmCharts := make([]addoncontrollerv1beta1.HelmChart, 0)
	namespace := serviceSet.Namespace
	for _, svc := range serviceSet.Spec.Services {
		tmpl, err := serviceTemplateObjectFromService(ctx, c, svc, namespace)
		if err != nil {
			return nil, err
		}

		if tmpl.Spec.Helm == nil {
			continue
		}

		if !tmpl.Status.Valid {
			continue
		}

		var helmChart addoncontrollerv1beta1.HelmChart
		switch {
		case tmpl.Spec.Helm.ChartRef != nil, tmpl.Spec.Helm.ChartSpec != nil:
			helmChart, err = helmChartFromSpecOrRef(ctx, c, namespace, svc, tmpl)
		case tmpl.Spec.Helm.ChartSource != nil:
			helmChart, err = helmChartFromFluxSource(ctx, svc, tmpl, namespace)
		default:
			return nil, fmt.Errorf("ServiceTemplate %s/%s has no Helm chart defined", tmpl.Namespace, tmpl.Name)
		}

		if err != nil {
			return nil, err
		}

		helmCharts = append(helmCharts, helmChart)

		if !slices.ContainsFunc(serviceSet.Status.Services, func(s kcmv1.ServiceState) bool {
			return s.Name == svc.Name && s.Namespace == svc.Namespace
		}) {
			serviceStatus := kcmv1.ServiceState{
				LastStateTransitionTime: pointerutil.To(metav1.Now()),
				Type:                    kcmv1.ServiceTypeHelm,
				Name:                    svc.Name,
				Namespace:               svc.Namespace,
				Template:                svc.Template,
				Version:                 svc.Template,
				State:                   kcmv1.ServiceStateProvisioning,
			}
			serviceSet.Status.Services = append(serviceSet.Status.Services, serviceStatus)
		}
	}

	return helmCharts, nil
}

// helmChartFromSpecOrRef returns a HelmChart object from a ServiceTemplate.
func helmChartFromSpecOrRef(
	ctx context.Context,
	c client.Client,
	namespace string,
	svc kcmv1.ServiceWithValues,
	template *kcmv1.ServiceTemplate,
) (addoncontrollerv1beta1.HelmChart, error) {
	var helmChart addoncontrollerv1beta1.HelmChart
	if template.GetCommonStatus() == nil || template.GetCommonStatus().ChartRef == nil {
		return helmChart, fmt.Errorf("status for ServiceTemplate %s/%s has not been updated yet", template.Namespace, template.Name)
	}
	templateRef := client.ObjectKeyFromObject(template)
	chart := &sourcev1.HelmChart{}
	chartRef := client.ObjectKey{
		Namespace: template.GetCommonStatus().ChartRef.Namespace,
		Name:      template.GetCommonStatus().ChartRef.Name,
	}
	if err := c.Get(ctx, chartRef, chart); err != nil {
		return helmChart, fmt.Errorf("failed to get HelmChart %s referenced by ServiceTemplate %s: %w", chartRef.String(), templateRef.String(), err)
	}

	chartVersion := chart.Spec.Version
	if chart.Status.Artifact != nil && chart.Status.Artifact.Revision != "" {
		if _, err := semver.NewVersion(chart.Status.Artifact.Revision); err == nil {
			// Try to get the HelmChart version from status.artifact.revision
			// It contains the valid chart version for charts from a GitRepository
			chartVersion = chart.Status.Artifact.Revision
		}
	}
	_, err := semver.NewVersion(chartVersion)
	if err != nil {
		return helmChart, fmt.Errorf("failed to determine version %s of HelmChart %s referenced by ServiceTemplate %s: %w", chartVersion, chartRef.String(), templateRef.String(), err)
	}

	repoRef := client.ObjectKey{
		// Using chart's namespace because it's source
		// should be within the same namespace.
		Namespace: chart.Namespace,
		Name:      chart.Spec.SourceRef.Name,
	}

	var repoURL string
	var repoChartName string
	var registryCredentialsConfig *addoncontrollerv1beta1.RegistryCredentialsConfig
	chartName := chart.Spec.Chart

	switch chart.Spec.SourceRef.Kind {
	case sourcev1.HelmRepositoryKind:
		repo := &sourcev1.HelmRepository{}
		if err := c.Get(ctx, repoRef, repo); err != nil {
			return helmChart, fmt.Errorf("failed to get %s: %w", repoRef.String(), err)
		}
		repoURL = repo.Spec.URL
		repoChartName = func() string {
			if repo.Spec.Type == helmutil.RegistryTypeOCI {
				return chartName
			}
			// Sveltos accepts ChartName in <repository>/<chart> format for non-OCI.
			// We don't have a repository name, so we can use <chart>/<chart> instead.
			// See: https://projectsveltos.github.io/sveltos/addons/helm_charts/.
			return fmt.Sprintf("%s/%s", chartName, chartName)
		}()
		if repo.Spec.Insecure || repo.Spec.SecretRef != nil || repo.Spec.CertSecretRef != nil {
			registryCredentialsConfig = generateRegistryCredentialsConfig(namespace, repo.Spec.Insecure, repo.Spec.SecretRef, repo.Spec.CertSecretRef)
		}
	case sourcev1.GitRepositoryKind:
		repo := &sourcev1.GitRepository{}
		if err := c.Get(ctx, repoRef, repo); err != nil {
			return helmChart, fmt.Errorf("failed to get %s: %w", repoRef.String(), err)
		}
		repoURL = fmt.Sprintf("gitrepository://%s/%s/%s", chart.Namespace, chart.Spec.SourceRef.Name, chartName)
		// Sveltos accepts ChartName in <repository>/<chart> format for non-OCI.
		// We don't have a repository name, so we can use <chart>/<chart> instead.
		// See: https://projectsveltos.github.io/sveltos/addons/helm_charts/.
		repoChartName = chartName
		registryCredentialsConfig = generateRegistryCredentialsConfig(namespace, false, repo.Spec.SecretRef, nil)
	default:
		return helmChart, fmt.Errorf("unsupported HelmChart source kind %s", repoRef.String())
	}

	helmOptions := template.Spec.HelmOptions
	if template.Spec.HelmOptions == nil {
		helmOptions = &kcmv1.ServiceHelmOptions{}
	}

	if svc.HelmOptions != nil {
		err = mergo.Merge(&helmOptions, svc.HelmOptions, mergo.WithAppendSlice)
		if err != nil {
			return addoncontrollerv1beta1.HelmChart{}, err
		}
	}

	helmChart = addoncontrollerv1beta1.HelmChart{
		Values:        svc.Values,
		ValuesFrom:    convertValuesFrom(svc.ValuesFrom, namespace),
		RepositoryURL: repoURL,
		// We don't have repository name so chart name becomes repository name.
		RepositoryName: chartName,
		ChartName:      repoChartName,
		ChartVersion:   chartVersion,
		ReleaseName:    svc.Name,
		ReleaseNamespace: func() string {
			if svc.Namespace != "" {
				return svc.Namespace
			}
			return svc.Name
		}(),
		RegistryCredentialsConfig: registryCredentialsConfig,
		Options:                   convertHelmOptions(*helmOptions),
	}
	return helmChart, nil
}

// generateRegistryCredentialsConfig returns a RegistryCredentialsConfig object.
func generateRegistryCredentialsConfig(
	namespace string,
	insecure bool,
	secretRef *fluxmeta.LocalObjectReference,
	certSecretRef *fluxmeta.LocalObjectReference,
) *addoncontrollerv1beta1.RegistryCredentialsConfig {
	c := new(addoncontrollerv1beta1.RegistryCredentialsConfig)

	// The reason it is passed to PlainHTTP instead of InsecureSkipTLSVerify is because
	// the source.Spec.Insecure field is meant to be used for connecting to repositories
	// over plain HTTP, which is different than what InsecureSkipTLSVerify is meant for.
	// See: https://github.com/fluxcd/source-controller/pull/1288
	c.PlainHTTP = insecure
	if c.PlainHTTP {
		// InsecureSkipTLSVerify is redundant in this case.
		// At the time of implementation, Sveltos would return an error when PlainHTTP
		// and InsecureSkipTLSVerify were both set, so verify before removing.
		c.InsecureSkipTLSVerify = false
	}

	if secretRef != nil {
		c.CredentialsSecretRef = &corev1.SecretReference{
			Name:      secretRef.Name,
			Namespace: namespace,
		}
	}

	if certSecretRef != nil {
		c.CASecretRef = &corev1.SecretReference{
			Name:      certSecretRef.Name,
			Namespace: namespace,
		}
	}

	return c
}

// helmChartFromFluxSource returns a HelmChart object from a Flux source.
func helmChartFromFluxSource(
	_ context.Context,
	svc kcmv1.ServiceWithValues,
	template *kcmv1.ServiceTemplate,
	namespace string,
) (addoncontrollerv1beta1.HelmChart, error) {
	var helmChart addoncontrollerv1beta1.HelmChart
	if template.Status.SourceStatus == nil {
		return helmChart, fmt.Errorf("status for ServiceTemplate %s/%s has not been updated yet", template.Namespace, template.Name)
	}

	chartSource := template.Spec.Helm.ChartSource
	status := template.Status.SourceStatus
	sanitizedPath := strings.TrimPrefix(strings.TrimPrefix(chartSource.Path, "."), "/")
	url := fmt.Sprintf("%s://%s/%s/%s", status.Kind, status.Namespace, status.Name, sanitizedPath)

	helmOptions := template.Spec.HelmOptions
	if template.Spec.HelmOptions == nil {
		helmOptions = &kcmv1.ServiceHelmOptions{}
	}

	if svc.HelmOptions != nil {
		err := mergo.Merge(&helmOptions, svc.HelmOptions, mergo.WithAppendSlice)
		if err != nil {
			return addoncontrollerv1beta1.HelmChart{}, err
		}
	}

	helmChart = addoncontrollerv1beta1.HelmChart{
		RepositoryURL:    url,
		ReleaseName:      svc.Name,
		ReleaseNamespace: svc.Namespace,
		Values:           svc.Values,
		ValuesFrom:       convertValuesFrom(svc.ValuesFrom, namespace),
		Options:          convertHelmOptions(*helmOptions),
	}

	return helmChart, nil
}

// getKustomizationRefs returns a list of KustomizationRefs for the given services.
func getKustomizationRefs(ctx context.Context, c client.Client, serviceSet *kcmv1.ServiceSet) ([]addoncontrollerv1beta1.KustomizationRef, error) {
	kustomizationRefs := make([]addoncontrollerv1beta1.KustomizationRef, 0)
	namespace := serviceSet.Namespace
	for _, svc := range serviceSet.Spec.Services {
		tmpl, err := serviceTemplateObjectFromService(ctx, c, svc, namespace)
		if err != nil {
			return nil, err
		}

		if tmpl.Spec.Kustomize == nil {
			continue
		}

		if !tmpl.Status.Valid {
			continue
		}

		kustomization := addoncontrollerv1beta1.KustomizationRef{
			Namespace:       tmpl.Status.SourceStatus.Namespace,
			Name:            tmpl.Status.SourceStatus.Name,
			Kind:            tmpl.Status.SourceStatus.Kind,
			Path:            tmpl.Spec.Kustomize.Path,
			TargetNamespace: svc.Namespace,
			DeploymentType:  addoncontrollerv1beta1.DeploymentType(tmpl.Spec.Kustomize.DeploymentType),
			ValuesFrom:      convertValuesFrom(svc.ValuesFrom, namespace),
		}

		kustomizationRefs = append(kustomizationRefs, kustomization)

		if !slices.ContainsFunc(serviceSet.Status.Services, func(s kcmv1.ServiceState) bool {
			return s.Name == svc.Name && s.Namespace == svc.Namespace
		}) {
			serviceStatus := kcmv1.ServiceState{
				LastStateTransitionTime: pointerutil.To(metav1.Now()),
				Type:                    kcmv1.ServiceTypeKustomize,
				Name:                    svc.Name,
				Namespace:               svc.Namespace,
				Template:                svc.Template,
				Version:                 svc.Template,
				State:                   kcmv1.ServiceStateProvisioning,
			}
			serviceSet.Status.Services = append(serviceSet.Status.Services, serviceStatus)
		}
	}
	return kustomizationRefs, nil
}

// getPolicyRefs returns a list of PolicyRefs for the given services.
func getPolicyRefs(ctx context.Context, c client.Client, serviceSet *kcmv1.ServiceSet) ([]addoncontrollerv1beta1.PolicyRef, error) {
	policyRefs := make([]addoncontrollerv1beta1.PolicyRef, 0)
	namespace := serviceSet.Namespace
	for _, svc := range serviceSet.Spec.Services {
		tmpl, err := serviceTemplateObjectFromService(ctx, c, svc, namespace)
		if err != nil {
			return nil, err
		}

		if tmpl.Spec.Resources == nil {
			continue
		}

		if !tmpl.Status.Valid {
			continue
		}

		policyRef := addoncontrollerv1beta1.PolicyRef{
			Namespace:      tmpl.Status.SourceStatus.Namespace,
			Name:           tmpl.Status.SourceStatus.Name,
			Kind:           tmpl.Status.SourceStatus.Kind,
			Path:           tmpl.Spec.Resources.Path,
			DeploymentType: addoncontrollerv1beta1.DeploymentType(tmpl.Spec.Resources.DeploymentType),
		}

		policyRefs = append(policyRefs, policyRef)

		if !slices.ContainsFunc(serviceSet.Status.Services, func(s kcmv1.ServiceState) bool {
			return s.Name == svc.Name && s.Namespace == svc.Namespace
		}) {
			serviceStatus := kcmv1.ServiceState{
				LastStateTransitionTime: pointerutil.To(metav1.Now()),
				Type:                    kcmv1.ServiceTypeResource,
				Name:                    svc.Name,
				Namespace:               svc.Namespace,
				Template:                svc.Template,
				Version:                 svc.Template,
				State:                   kcmv1.ServiceStateProvisioning,
			}
			serviceSet.Status.Services = append(serviceSet.Status.Services, serviceStatus)
		}
	}
	return policyRefs, nil
}

// serviceTemplateObjectFromService returns the [github.com/K0rdent/kcm/api/v1beta1.ServiceTemplate]
// object found either by direct reference or in [github.com/K0rdent/kcm/api/v1beta1.ServiceTemplateChain] by defined version.
func serviceTemplateObjectFromService(
	ctx context.Context,
	cl client.Client,
	svc kcmv1.ServiceWithValues,
	namespace string,
) (*kcmv1.ServiceTemplate, error) {
	template := new(kcmv1.ServiceTemplate)
	key := client.ObjectKey{Name: svc.Template, Namespace: namespace}
	if err := cl.Get(ctx, key, template); err != nil {
		return nil, fmt.Errorf("failed to get ServiceTemplate %s: %w", key.String(), err)
	}
	return template, nil
}

// convertValuesFrom converts [github.com/K0rdent/kcm/api/v1beta1.ValuesFrom]
// to [github.com/projectsveltos/addon-controller/api/v1beta1.ValueFrom].
func convertValuesFrom(src []kcmv1.ValuesFrom, namespace string) []addoncontrollerv1beta1.ValueFrom {
	valueFrom := make([]addoncontrollerv1beta1.ValueFrom, 0, len(src))
	for _, item := range src {
		valueFrom = append(valueFrom, addoncontrollerv1beta1.ValueFrom{
			Kind:      item.Kind,
			Name:      item.Name,
			Namespace: namespace,
		})
	}
	return valueFrom
}

func convertHelmOptions(options kcmv1.ServiceHelmOptions) *addoncontrollerv1beta1.HelmOptions {
	toReturn := addoncontrollerv1beta1.HelmOptions{
		Timeout: options.Timeout,
	}

	if options.SkipCRDs != nil {
		toReturn.SkipCRDs = *options.SkipCRDs
	}

	if options.SkipSchemaValidation != nil {
		toReturn.SkipSchemaValidation = *options.SkipSchemaValidation
	}

	if options.Wait != nil {
		toReturn.Wait = *options.Wait
	}

	if options.CreateNamespace != nil {
		toReturn.InstallOptions.CreateNamespace = *options.CreateNamespace
	}

	if options.WaitForJobs != nil {
		toReturn.WaitForJobs = *options.WaitForJobs
	}

	if options.DisableHooks != nil {
		toReturn.DisableHooks = *options.DisableHooks
	}

	if options.DisableOpenAPIValidation != nil {
		toReturn.DisableOpenAPIValidation = *options.DisableOpenAPIValidation
	}

	if options.Atomic != nil {
		toReturn.Atomic = *options.Atomic
	}

	if options.DependencyUpdate != nil {
		toReturn.DependencyUpdate = *options.DependencyUpdate
	}

	if options.Labels != nil {
		toReturn.Labels = *options.Labels
	}

	if options.EnableClientCache != nil {
		toReturn.EnableClientCache = *options.EnableClientCache
	}

	if options.Description != nil {
		toReturn.Description = *options.Description
	}

	if options.Replace != nil {
		toReturn.InstallOptions.Replace = *options.Replace
	}
	if options.DisableHooks != nil {
		toReturn.InstallOptions.DisableHooks = *options.DisableHooks
	}

	return &toReturn
}

// buildProfileSpec converts raw JSON configuration to [github.com/projectsveltos/addon-controller/api/v1beta1.Spec].
// This conversion is done using intermediate structure [profileConfig] which defines additional configuration fields
// along with mirroring [github.com/projectsveltos/addon-controller/api/v1beta1.Spec] fields. This is done to,
// first, support [github.com/projectsveltos/addon-controller/api/v1beta1.Spec] fields and, second, allow additional
// configuration.
func buildProfileSpec(config *apiextv1.JSON) (*addoncontrollerv1beta1.Spec, error) {
	spec := new(addoncontrollerv1beta1.Spec)
	if config == nil {
		return spec, errEmptyConfig
	}
	params := new(profileConfig)
	err := json.Unmarshal(config.Raw, params)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal raw config to profile configuration: %w", err)
	}

	tier, err := priorityToTier(ptr.Deref(params.Priority, int32(100)))
	if err != nil {
		return nil, fmt.Errorf("failed to convert priority to tier: %w", err)
	}

	spec.Tier = tier
	spec.SyncMode = addoncontrollerv1beta1.SyncMode(params.SyncMode)
	spec.ContinueOnConflict = !params.StopOnConflict
	spec.ContinueOnError = params.ContinueOnError
	spec.Reloader = params.Reloader
	spec.TemplateResourceRefs = params.TemplateResourceRefs
	spec.PolicyRefs = params.PolicyRefs
	spec.Patches = params.Patches
	spec.DriftExclusions = params.DriftExclusions

	for _, target := range params.DriftIgnore {
		spec.Patches = append(spec.Patches, libsveltosv1beta1.Patch{
			Target: &target,
			Patch:  sveltosDriftIgnorePatch,
		})
	}
	return spec, nil
}

// priorityToTier converts priority value to Sveltos tier value.
func priorityToTier(priority int32) (int32, error) {
	var mini int32 = 1
	maxi := math.MaxInt32 - mini

	// This check is needed because Sveltos asserts a min value of 1 on tier.
	if priority >= mini && priority <= maxi {
		return math.MaxInt32 - priority, nil
	}

	return 0, fmt.Errorf("invalid value %d, priority has to be between %d and %d", priority, mini, maxi)
}

// fillNotDeployedServices fills [github.com/K0rdent/kcm/api/v1beta1.ServiceSet] status for
// services that are not deployed.
func fillNotDeployedServices(serviceSet *kcmv1.ServiceSet, now func() time.Time) {
	deployedServicesMap := make(map[client.ObjectKey]struct{})
	for _, service := range serviceSet.Status.Services {
		key := client.ObjectKey{
			Namespace: service.Namespace,
			Name:      service.Name,
		}
		deployedServicesMap[key] = struct{}{}
	}

	for _, service := range serviceSet.Spec.Services {
		key := client.ObjectKey{
			Namespace: service.Namespace,
			Name:      service.Name,
		}
		if _, ok := deployedServicesMap[key]; ok {
			continue
		}
		serviceSet.Status.Services = append(serviceSet.Status.Services, kcmv1.ServiceState{
			Name:                    service.Name,
			Namespace:               service.Namespace,
			Template:                service.Template,
			State:                   kcmv1.ServiceStateNotDeployed,
			LastStateTransitionTime: pointerutil.To(metav1.NewTime(now())),
		})
	}
}

// TODO: refactor, see: https://github.com/k0rdent/kcm/issues/1586
func projectTemplateResourceRefs(cd *kcmv1.ClusterDeployment, cred *kcmv1.Credential) []addoncontrollerv1beta1.TemplateResourceRef {
	if !cd.Spec.PropagateCredentials || cred.Spec.IdentityRef == nil {
		return nil
	}

	refs := []addoncontrollerv1beta1.TemplateResourceRef{
		{
			Resource:   *cred.Spec.IdentityRef,
			Identifier: "InfrastructureProviderIdentity",
		},
	}

	if !strings.EqualFold(cred.Spec.IdentityRef.Kind, "Secret") {
		refs = append(refs, addoncontrollerv1beta1.TemplateResourceRef{
			Resource: corev1.ObjectReference{
				APIVersion: "v1",
				Kind:       "Secret",
				Namespace:  cred.Spec.IdentityRef.Namespace,
				Name:       cred.Spec.IdentityRef.Name + "-secret",
			},
			Identifier: "InfrastructureProviderIdentitySecret",
		})
	}

	return refs
}

// TODO: refactor, see: https://github.com/k0rdent/kcm/issues/1586
func projectPolicyRefs(cd *kcmv1.ClusterDeployment, cred *kcmv1.Credential) []addoncontrollerv1beta1.PolicyRef {
	if !cd.Spec.PropagateCredentials || cred.Spec.IdentityRef == nil {
		return nil
	}

	return []addoncontrollerv1beta1.PolicyRef{
		{
			Kind:           "ConfigMap",
			Namespace:      cred.Spec.IdentityRef.Namespace,
			Name:           cred.Spec.IdentityRef.Name + "-resource-template",
			DeploymentType: addoncontrollerv1beta1.DeploymentTypeRemote,
		},
	}
}

// labelsMatchSelector returns true if the labels of the ServiceSet match the selector.
func labelsMatchSelector(serviceSetLabels map[string]string, selector *metav1.LabelSelector) (bool, error) {
	if selector == nil {
		return true, nil
	}

	labelSelector, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return false, fmt.Errorf("failed to convert LabelSelector to Selector: %w", err)
	}
	return labelSelector.Matches(labels.Set(serviceSetLabels)), nil
}

// findCondition finds the condition of the given type in the ServiceSet.
// If no condition is found, a new condition of given type is created.
func findCondition(serviceSet *kcmv1.ServiceSet, conditionType string) (metav1.Condition, bool) {
	var created bool
	condition := apimeta.FindStatusCondition(serviceSet.Status.Conditions, conditionType)
	if condition == nil {
		condition = &metav1.Condition{Type: conditionType, ObservedGeneration: serviceSet.Generation}
		created = true
	}
	return *condition, created
}

// updateCondition updates the given condition of the ServiceSet.
func updateCondition(
	serviceSet *kcmv1.ServiceSet,
	condition metav1.Condition,
	status metav1.ConditionStatus,
	reason string,
	message string,
	transitionTime time.Time,
) bool {
	if condition.Status != status || condition.Reason != reason || condition.Message != message {
		condition.LastTransitionTime = metav1.NewTime(transitionTime)
	}
	condition.ObservedGeneration = serviceSet.Generation
	condition.Status = status
	condition.Reason = reason
	condition.Message = message
	return apimeta.SetStatusCondition(&serviceSet.Status.Conditions, condition)
}

func servicesStateFromSummary(
	logger logr.Logger,
	summary *addoncontrollerv1beta1.ClusterSummary,
	serviceSet *kcmv1.ServiceSet,
) []kcmv1.ServiceState {
	logger.Info("Collecting services state from ClusterSummary", "cluster_summary", client.ObjectKeyFromObject(summary))
	// we'll recreate service states list according to the desired services
	states := make([]kcmv1.ServiceState, 0, len(serviceSet.Spec.Services))
	servicesMap := make(map[client.ObjectKey]kcmv1.ServiceState)
	for _, service := range serviceSet.Spec.Services {
		servicesMap[client.ObjectKey{
			Namespace: service.Namespace,
			Name:      service.Name,
		}] = kcmv1.ServiceState{
			Type:                    "",
			LastStateTransitionTime: nil,
			Name:                    service.Name,
			Namespace:               service.Namespace,
			Template:                service.Template,
			Version:                 service.Template,
			State:                   kcmv1.ServiceStateProvisioning,
			FailureMessage:          "",
		}
	}

	hasKustomizations := len(summary.Spec.ClusterProfileSpec.KustomizationRefs) > 0
	hasPolicies := len(summary.Spec.ClusterProfileSpec.PolicyRefs) > 0

	// in case feature is absent we'll treat it as deployed
	helmChartsFailureMessage := ""
	kustomizationsDeployed := !hasKustomizations
	kustomizationsFailed := false
	kustomizationsFailureMessage := ""
	policiesDeployed := !hasPolicies
	policiesFailed := false
	policiesFailureMessage := ""

	for _, feature := range summary.Status.FeatureSummaries {
		switch feature.FeatureID {
		// we'll only lookup for failure message related to helm charts,
		// because we have better mechanism to determine whether helm release was deployed or not
		case libsveltosv1beta1.FeatureHelm:
			if feature.FailureMessage != nil {
				helmChartsFailureMessage = *feature.FailureMessage
			}
		// we cannot determine which kustomizations or policies were failed, hence we'll treat them as failed
		// in case feature summary contains failure message. This message will be copied to the ServiceSet status
		// thus user will be able to see the reason of failure.
		// this is a temporary solution, we'll work with projectsveltos maintainers to improve observability.
		case libsveltosv1beta1.FeatureKustomize:
			kustomizationsDeployed = feature.Status == libsveltosv1beta1.FeatureStatusProvisioned
			if feature.FailureMessage != nil {
				kustomizationsFailed = true
				kustomizationsFailureMessage = *feature.FailureMessage
			}
		case libsveltosv1beta1.FeatureResources:
			policiesDeployed = feature.Status == libsveltosv1beta1.FeatureStatusProvisioned
			if feature.FailureMessage != nil {
				policiesFailed = true
				policiesFailureMessage = *feature.FailureMessage
			}
		}
	}

	helmReleaseMap := make(map[client.ObjectKey]bool)
	for _, helmRelease := range summary.Status.HelmReleaseSummaries {
		// we'll save true to the helmReleaseMap if values hash is not empty.
		// This means that the helm release was successfully deployed.
		helmReleaseMap[client.ObjectKey{
			Namespace: helmRelease.ReleaseNamespace,
			Name:      helmRelease.ReleaseName,
		}] = len(helmRelease.ValuesHash) > 0
	}

	for _, s := range serviceSet.Status.Services {
		// we won't save state if the service absent in desired ones
		newState, ok := servicesMap[client.ObjectKey{
			Namespace: s.Namespace,
			Name:      s.Name,
		}]
		if !ok {
			continue
		}
		newState.Type = s.Type
		newState.LastStateTransitionTime = s.LastStateTransitionTime

		switch s.Type {
		case kcmv1.ServiceTypeHelm:
			deployed, found := helmReleaseMap[serviceset.ServiceKey(s.Namespace, s.Name)]
			if !found {
				newState.State = kcmv1.ServiceStateNotDeployed
			}
			if deployed {
				newState.State = kcmv1.ServiceStateDeployed
			}
			if helmChartsFailureMessage != "" {
				newState.State = kcmv1.ServiceStateFailed
				newState.FailureMessage = helmChartsFailureMessage
			}
			if newState.State != s.State {
				newState.LastStateTransitionTime = pointerutil.To(metav1.Now())
			}
		case kcmv1.ServiceTypeKustomize:
			if kustomizationsDeployed {
				newState.State = kcmv1.ServiceStateDeployed
			}
			if kustomizationsFailed {
				newState.State = kcmv1.ServiceStateFailed
				newState.FailureMessage = "One or more Kustomizations failed to deploy:" + kustomizationsFailureMessage
			}
		case kcmv1.ServiceTypeResource:
			if policiesDeployed {
				newState.State = kcmv1.ServiceStateDeployed
			}
			if policiesFailed {
				newState.State = kcmv1.ServiceStateFailed
				newState.FailureMessage = "One or more Resources failed to deploy: " + policiesFailureMessage
			}
		}
		states = append(states, newState)
	}
	logger.V(1).Info("Collected services state from summary", "states", states)
	return states
}

// getRegionalClient returns local or regional kubernetes client depending on the target cluster type.
func getRegionalClient(ctx context.Context, cl client.Client, serviceSet *kcmv1.ServiceSet, systemNamespace string) (client.Client, error) {
	if serviceSet.Spec.Cluster == "" {
		// The ServiceSet created for self-managing the management cluster has
		// empty .spec.cluster because it isn't matching any ClusterDeployment.
		// So we return the management cluster client in this case.
		return cl, nil
	}

	cd := new(kcmv1.ClusterDeployment)
	cdKey := client.ObjectKey{Namespace: serviceSet.Namespace, Name: serviceSet.Spec.Cluster}
	if err := cl.Get(ctx, cdKey, cd); err != nil {
		return nil, fmt.Errorf("failed to get %s ClusterDeployment: %w", cdKey, err)
	}

	cred := new(kcmv1.Credential)
	credKey := client.ObjectKey{Namespace: cd.Namespace, Name: cd.Spec.Credential}
	if err := cl.Get(ctx, credKey, cred); err != nil {
		return nil, fmt.Errorf("failed to get %s Credential: %w", credKey, err)
	}

	rgnClient, err := kubeutil.GetRegionalClientByRegionName(ctx, cl, systemNamespace, cred.Spec.Region, schemeutil.GetRegionalSchemeWithSveltos)
	if err != nil {
		return nil, fmt.Errorf("failed to get regional client: %w", err)
	}

	return rgnClient, nil
}
