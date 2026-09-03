// Copyright 2024
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

package controller

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/metrics"
	"github.com/K0rdent/kcm/internal/record"
	"github.com/K0rdent/kcm/internal/serviceset"
	conditionsutil "github.com/K0rdent/kcm/internal/util/conditions"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	labelsutil "github.com/K0rdent/kcm/internal/util/labels"
	ratelimitutil "github.com/K0rdent/kcm/internal/util/ratelimit"
	validationutil "github.com/K0rdent/kcm/internal/util/validation"
)

type MultiClusterServiceCommonReconciler struct {
	Client client.Client

	timeFunc func() time.Time

	SystemNamespace        string
	IsDisabledValidationWH bool // is webhook disabled set via the controller flags

	defaultRequeueTime time.Duration
}

// MultiClusterServiceReconciler reconciles a MultiClusterService object
type MultiClusterServiceReconciler struct {
	MultiClusterServiceCommonReconciler
}

// Reconcile reconciles a MultiClusterService object.
//
//nolint:dupl // intentionally mirrors NamespacedMultiClusterServiceReconciler.Reconcile - see its doc comment.
func (r *MultiClusterServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling MultiClusterService")

	mcs := &kcmv1.MultiClusterService{}
	err = r.Client.Get(ctx, req.NamespacedName, mcs)
	if apierrors.IsNotFound(err) {
		l.Info("MultiClusterService not found, ignoring since object must be deleted")
		return ctrl.Result{}, nil
	}
	if err != nil {
		l.Error(err, "Failed to get MultiClusterService")
		return ctrl.Result{}, err
	}

	clone := mcs.DeepCopy()
	defer func() {
		// we need to explicitly requeue MultiClusterService object,
		// otherwise we'll miss if some ClusterDeployment will be updated
		// with matching labels.
		requeue, e := r.updateStatus(ctx, clone, mcs)
		if requeue {
			result = ctrl.Result{RequeueAfter: r.defaultRequeueTime}
		}
		err = errors.Join(err, e)
	}()

	if !mcs.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, mcs)
	}

	management := &kcmv1.Management{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: kcmv1.ManagementName}, management); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Management: %w", err)
	}
	if !management.DeletionTimestamp.IsZero() {
		l.Info("Management is being deleted, skipping MultiClusterService reconciliation")
		return ctrl.Result{}, nil
	}

	return r.reconcileUpdate(ctx, mcs)
}

func (r *MultiClusterServiceCommonReconciler) reconcileUpdate(ctx context.Context, mcs kcmv1.MultiClusterServiceCommon) (result ctrl.Result, err error) {
	if kcmv1.IsMCSNil(mcs) {
		return ctrl.Result{}, nil
	}

	l := ctrl.LoggerFrom(ctx)

	mcsKind := kcmv1.MultiClusterServiceKind
	finalizer := kcmv1.MultiClusterServiceFinalizer
	indexKey := kcmv1.ServiceSetMultiClusterServiceIndexKey
	if _, ok := mcs.(*kcmv1.NamespacedMultiClusterService); ok {
		finalizer = kcmv1.NamespacedMultiClusterServiceFinalizer
		mcsKind = kcmv1.NamespacedMultiClusterServiceKind
		indexKey = kcmv1.ServiceSetNamespacedMultiClusterServiceIndexKey
	}

	if controllerutil.AddFinalizer(mcs, finalizer) {
		if err = r.Client.Update(ctx, mcs); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update %s %s with finalizer %s: %w", mcsKind, mcs.GetFullname(), finalizer, err)
		}
		// Requeuing to make sure that ClusterProfile is reconciled in subsequent runs.
		// Without the requeue, we would be depending on an external re-trigger after
		// the 1st run for the ClusterProfile object to be reconciled.
		return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, nil
	}

	if updated, err := labelsutil.AddKCMComponentLabel(ctx, r.Client, mcs); err != nil {
		l.Error(err, "adding component label")
		return ctrl.Result{}, err
	} else if updated {
		// generation has not changed, so an explicit requeue is needed.
		return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, nil
	}

	// None of the spec validations are retriggered by returning an error here: the
	// controller already watches the objects they read, and until the spec or one of
	// those objects changes there is nothing to retry.
	if !r.validateSpec(ctx, mcs) {
		return ctrl.Result{}, nil
	}

	l.V(1).Info("Cleaning up ServiceSets for ClusterDeployments that no longer match")
	if err = r.cleanupServiceSets(ctx, mcs); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile cleanup: %w", err)
	}

	l.V(1).Info("Ensuring ServiceSets for matching ClusterDeployments")
	ensured, err := r.ensureServiceSets(ctx, mcs)
	if err != nil {
		return ctrl.Result{}, err
	}

	serviceSetList := new(kcmv1.ServiceSetList)
	if err := r.Client.List(ctx, serviceSetList, client.MatchingFields{indexKey: mcs.GetFullname()}, client.InNamespace(mcs.GetNamespace())); err != nil {
		return ctrl.Result{}, errors.Join(ensured.errs, fmt.Errorf("failed to list ServiceSets for %s %s: %w", mcsKind, mcs.GetFullname(), err))
	}
	l.V(1).Info("ServiceSets matching "+mcsKind+" found", "count", len(serviceSetList.Items))

	currentlyMatchingServiceSets := filterMatchingServiceSets(mcs, serviceSetList.Items, ensured.matchingClusterKeys)

	r.setClustersCondition(ctx, mcs, ensured.totalMatchingClusters, currentlyMatchingServiceSets, ensured.blocked)
	r.setDependencyReadyCondition(mcs, ensured.blocked, ensured.dependencyCheckErrs)

	// setMatchingClusters must run even when ensured.errs is non-nil. A single reconcile can both
	// hit a real error on one cluster/dependency and find another cluster blocked (see
	// okToReconcileServiceSet), and the conditions set above already reflect `blocked` - while
	// Reconcile's deferred updateStatus persists the status regardless of the error returned here.
	// Returning before this ran would therefore persist conditions announcing that N clusters are
	// waiting on a dependency alongside a .status.matchingClusters that is stale (or empty on a
	// first reconcile), and it would stay that way for as long as the error keeps recurring.
	clustersErr := r.setMatchingClusters(ctx, mcs, currentlyMatchingServiceSets, ensured.blocked)
	if ensured.errs != nil {
		return ctrl.Result{}, errors.Join(ensured.errs, clustersErr)
	}

	var (
		upgradePaths []kcmv1.ServiceUpgradePaths
		servicesErr  error
	)
	upgradePaths, servicesErr = serviceset.ServicesUpgradePaths(ctx, r.Client, mcs.GetMultiClusterServiceSpec().ServiceSpec.Services, r.SystemNamespace)
	mcs.GetMultiClusterServiceStatus().ServicesUpgradePaths = upgradePaths

	return result, errors.Join(servicesErr, clustersErr)
}

// validateSpec runs the spec-level validations for the given MultiClusterService, setting the
// corresponding condition for each of them. It reports whether all of them passed; if it returns
// false the caller must stop reconciling, since none of these failures are worth retrying until
// the spec or one of the objects it references changes.
func (r *MultiClusterServiceCommonReconciler) validateSpec(ctx context.Context, mcs kcmv1.MultiClusterServiceCommon) bool {
	if kcmv1.IsMCSNil(mcs) {
		return false
	}

	l := ctrl.LoggerFrom(ctx)

	mcsKind := kcmv1.MultiClusterServiceKind
	mcsNamespace := r.SystemNamespace
	if _, ok := mcs.(*kcmv1.NamespacedMultiClusterService); ok {
		mcsKind = kcmv1.NamespacedMultiClusterServiceKind
		mcsNamespace = mcs.GetNamespace()
	}
	mcsSpec := mcs.GetMultiClusterServiceSpec()

	l.Info("Validating service templates")
	if err := validationutil.ServicesHaveValidTemplates(ctx, r.Client, mcsSpec.ServiceSpec.Services, mcsNamespace); err != nil {
		if r.setCondition(mcs, kcmv1.ServicesReferencesValidationCondition, err) {
			record.Warnf(mcs, nil, kcmv1.ServicesReferencesValidationCondition, "ValidateServiceTemplates", err.Error())
		}
		l.Error(err, "failed to validate service template references")
		// Will not retrigger this error because the MCS controller is
		// already configured to watch for changes in ServiceTemplates.
		return false
	}
	r.setCondition(mcs, kcmv1.ServicesReferencesValidationCondition, nil)

	l.Info("Validating service dependencies")
	if err := validationutil.ValidateServiceDependencyOverall(mcsSpec.ServiceSpec.Services); err != nil {
		if r.setCondition(mcs, kcmv1.ServicesDependencyValidationCondition, err) {
			record.Warnf(mcs, nil, kcmv1.ServicesDependencyValidationCondition, "ValidateServiceDependencies", err.Error())
		}
		l.Error(err, "failed to validate service dependencies of services defined in spec, will not retrigger")
		// Will not retrigger this error because nothing to do until spec is changed.
		return false
	}
	r.setCondition(mcs, kcmv1.ServicesDependencyValidationCondition, nil)

	l.Info("Validating " + mcsKind + " dependencies")
	if err := validationutil.ValidateMCSDependencyOverall(ctx, r.Client, mcs); err != nil {
		if r.setCondition(mcs, kcmv1.MultiClusterServiceDependencyValidationCondition, err) {
			record.Warnf(mcs, nil, kcmv1.MultiClusterServiceDependencyValidationCondition, "ValidateMCSDependencies", err.Error())
		}
		l.Error(err, "failed to validate "+mcsKind+" dependencies, will not retrigger")
		// Will not retrigger this error because nothing to do until spec is changed.
		return false
	}
	r.setCondition(mcs, kcmv1.MultiClusterServiceDependencyValidationCondition, nil)

	return true
}

// ensuredServiceSets is what a single ensureServiceSets pass learned about the clusters a
// MultiClusterService targets. Everything in it feeds the status conditions set afterwards,
// which is why the two error buckets are kept apart rather than joined into one.
type ensuredServiceSets struct {
	// errs accumulates the per-cluster failures that must be reported as a reconcile error,
	// both createOrUpdateServiceSet failures and the real errors from okToReconcileServiceSet.
	errs error

	// dependencyCheckErrs collects only the errors okToReconcileServiceSet returns via its own
	// err return value (real, unexpected failures - never the blocked state). It is kept separate
	// from errs, which also accumulates createOrUpdateServiceSet failures unrelated to dependency
	// readiness, so that setDependencyReadyCondition reports Unknown precisely when this MCS's
	// dependency readiness could not be established - not whenever any error occurred.
	dependencyCheckErrs error

	// matchingClusterKeys holds the keys of the matching ClusterDeployments that are not being
	// deleted, used afterwards to tell the ServiceSets of currently-matching clusters apart from
	// those preserved on clusters that no longer match.
	matchingClusterKeys map[client.ObjectKey]struct{}

	// blocked collects, for each matching cluster whose ServiceSet could not be created
	// or updated because a MultiClusterService this one depends on hasn't finished
	// deploying its services there yet, a reference to that cluster and a message
	// describing what it's waiting on. Unlike other errors, this is an expected,
	// self-resolving state rather than a failure, so it's surfaced on mcs.Status
	// instead of being returned as a reconcile error.
	blocked []blockedCluster

	// totalMatchingClusters tracks how many clusters we expect ServiceSets to be deployed to.
	// Sourcing the total from the matching ClusterDeployments (plus selfManagement) - rather
	// than from the existing ServiceSets - ensures that clusters whose ServiceSet failed to be
	// created (e.g. due to unsatisfied MCS dependencies or a transient error) are still
	// counted in the denominator of the ClusterInReadyState condition.
	totalMatchingClusters int
}

// ensureServiceSets creates or updates the ServiceSet of every cluster the given
// MultiClusterService currently matches, skipping the ones still waiting on one of its
// MultiClusterService dependencies. A non-nil error is fatal for the reconcile and already
// carries whatever had been accumulated in the returned value's errs; the per-cluster failures
// reported through that field are not.
func (r *MultiClusterServiceCommonReconciler) ensureServiceSets(ctx context.Context, mcs kcmv1.MultiClusterServiceCommon) (ensuredServiceSets, error) {
	if kcmv1.IsMCSNil(mcs) {
		return ensuredServiceSets{}, nil
	}

	l := ctrl.LoggerFrom(ctx)

	mcsKind := kcmv1.MultiClusterServiceKind
	_, isNamespacedMCS := mcs.(*kcmv1.NamespacedMultiClusterService)
	if isNamespacedMCS {
		mcsKind = kcmv1.NamespacedMultiClusterServiceKind
	}
	mcsSpec := mcs.GetMultiClusterServiceSpec()

	selector, err := metav1.LabelSelectorAsSelector(&mcsSpec.ClusterSelector)
	if err != nil {
		return ensuredServiceSets{}, fmt.Errorf("failed to convert ClusterSelector to selector: %w", err)
	}

	res := ensuredServiceSets{}
	// deps resolves mcs.Spec.DependsOn once for all targets checked below - see resolveDependencies.
	deps := r.resolveDependencies(ctx, mcs)

	// if selfManagement flag is set, then we'll need to create serviceSet which does not refer
	// any clusterDeployment, but also has selfManagement flag set to true.
	// We ignore selfManagement for NamespacedMultiClusterService.
	if !isNamespacedMCS && mcsSpec.ServiceSpec.Provider.SelfManagement {
		res.totalMatchingClusters++

		l.V(1).Info("Checking if creation of ServiceSet for the management cluster is blocked by another MultiClusterService")
		ok, err := r.okToReconcileServiceSet(ctx, nil, deps, &res.blocked)
		if err != nil {
			// Real, unexpected failures only. A blocked (waiting-on-dependency) state
			// is surfaced via `blocked`/status, not propagated as a reconcile error.
			res.errs = errors.Join(res.errs, err)
			res.dependencyCheckErrs = errors.Join(res.dependencyCheckErrs, err)
		}
		if ok {
			l.V(1).Info("Ensuring ServiceSet for the management cluster")
			res.errs = errors.Join(res.errs, r.createOrUpdateServiceSet(ctx, mcs, nil))
		}
	}

	clusters := new(kcmv1.ClusterDeploymentList)
	if !selector.Empty() {
		if err := r.Client.List(ctx, clusters, client.MatchingLabelsSelector{Selector: selector}, client.InNamespace(mcs.GetNamespace())); err != nil {
			return res, errors.Join(res.errs, fmt.Errorf("failed to list ClusterDeployments: %w", err))
		}
	}

	l.V(1).Info("Matching ClusterDeployments found", "count", len(clusters.Items))
	res.matchingClusterKeys = make(map[client.ObjectKey]struct{}, len(clusters.Items))
	for _, cluster := range clusters.Items {
		clusterKey := client.ObjectKeyFromObject(&cluster)
		if !cluster.DeletionTimestamp.IsZero() {
			continue
		}
		res.totalMatchingClusters++
		res.matchingClusterKeys[clusterKey] = struct{}{}

		l.V(1).Info("Checking if creation of ServiceSet for matching ClusterDeployment is blocked by another "+mcsKind, "CD", clusterKey)
		ok, err := r.okToReconcileServiceSet(ctx, &cluster, deps, &res.blocked)
		if err != nil {
			// Real, unexpected failures only. A blocked (waiting-on-dependency) state
			// is surfaced via `blocked`/status, not propagated as a reconcile error.
			res.errs = errors.Join(res.errs, err)
			res.dependencyCheckErrs = errors.Join(res.dependencyCheckErrs, err)
		}
		if ok {
			l.V(1).Info("Ensuring ServiceSet for the matching ClusterDeployment", "CD", clusterKey)
			res.errs = errors.Join(res.errs, r.createOrUpdateServiceSet(ctx, mcs, &cluster))
		}
	}

	return res, nil
}

// filterMatchingServiceSets filters the given ServiceSets down to the ones whose target
// cluster currently matches the selector (or the self-management ServiceSet when SelfManagement
// is on). With KeepServicesOnSelectorMismatch=true the full list includes ServiceSets we
// intentionally preserved on clusters that no longer match; those should not be counted in
// ClusterInReadyState (numerator) nor surfaced in `.status.matchingClusters`, both of which are
// defined as scoped to currently-matching clusters. The preserved ServiceSets still exist
// on cluster and continue running their services - they're just not reflected in MCS status
// until their cluster matches again.
func filterMatchingServiceSets(mcs kcmv1.MultiClusterServiceCommon, serviceSets []kcmv1.ServiceSet, matchingClusterKeys map[client.ObjectKey]struct{}) []kcmv1.ServiceSet {
	if kcmv1.IsMCSNil(mcs) {
		return nil
	}

	matching := make([]kcmv1.ServiceSet, 0, len(serviceSets))
	for _, ss := range serviceSets {
		if ss.Spec.Cluster == "" {
			// We ignore SelfManagement field for NamespacedMultiClusterService.
			if _, ok := mcs.(*kcmv1.MultiClusterService); ok &&
				mcs.GetMultiClusterServiceSpec().ServiceSpec.Provider.SelfManagement {
				matching = append(matching, ss)
			}
			continue
		}
		if _, ok := matchingClusterKeys[client.ObjectKey{Namespace: ss.Namespace, Name: ss.Spec.Cluster}]; ok {
			matching = append(matching, ss)
		}
	}

	return matching
}

// setClustersCondition updates MultiClusterService's condition which shows number of clusters where services were
// successfully deployed out of total number of matching clusters.
//
// totalClusters is the number of clusters the MCS is expected to target (matching
// ClusterDeployments that are not being deleted, plus one for selfManagement when
// enabled). It must be sourced from the matching ClusterDeployments rather than from
// the ServiceSets list, otherwise clusters whose ServiceSet was not created yet
// (e.g. due to unsatisfied dependencies or transient errors) would be silently
// dropped from the denominator and the condition would misrepresent reality.
//
// blocked lists the clusters this reconcile found waiting on a MultiClusterService dependency;
// they are excluded from the numerator so that this condition agrees with the same clusters'
// entries in .status.matchingClusters, which setMatchingClusters reports as not deployed.
func (*MultiClusterServiceCommonReconciler) setClustersCondition(ctx context.Context, mcs kcmv1.MultiClusterServiceCommon, totalClusters int, serviceSets []kcmv1.ServiceSet, blocked []blockedCluster) {
	if kcmv1.IsMCSNil(mcs) {
		return
	}

	mcsKind := kcmv1.MultiClusterServiceKind
	if _, ok := mcs.(*kcmv1.NamespacedMultiClusterService); ok {
		mcsKind = kcmv1.NamespacedMultiClusterServiceKind
	}

	l := ctrl.LoggerFrom(ctx)
	l.V(1).Info("Reconciling " + mcsKind + " conditions")

	var readyDeployments int

	c := metav1.Condition{
		Type:   kcmv1.ClusterInReadyStateCondition,
		Status: metav1.ConditionTrue,
		Reason: kcmv1.SucceededReason,
	}

	// Keyed by clusterTargetKey (Kind/APIVersion/namespace/name), not namespace/name alone: the
	// self-management pseudo-target is a SveltosCluster always named mgmt/mgmt, and a real,
	// unrelated ClusterDeployment coincidentally also named mgmt in namespace mgmt may exist at
	// the same time - a namespace/name-only key would let a blocked self-management entry wrongly
	// exclude that ClusterDeployment's own, unrelated ServiceSet below.
	blockedKeys := make(map[clusterTargetKey]struct{}, len(blocked))
	for _, b := range blocked {
		blockedKeys[clusterTargetKeyFromRef(b.ref)] = struct{}{}
	}

	for _, serviceSet := range serviceSets {
		// We won't count serviceSets being deleted in the ready deployments count.
		// If the serviceSet is being deleted, this means that either corresponding
		// cluster is being deleted or corresponding cluster has labels which don't
		// match selector anymore. Hence all services defined in the service set
		// will be removed from cluster and there is no reason to count them anyhow.
		if !serviceSet.DeletionTimestamp.IsZero() {
			continue
		}
		// A ServiceSet created during an earlier, unblocked reconcile is not deleted when the
		// dependency becomes unsatisfied again (already deployed services are never torn down),
		// so it can still report Deployed while this reconcile finds its cluster blocked. Since
		// it is no longer being kept in sync with the spec, its Deployed flag is stale: counting
		// it here would let this condition claim e.g. 1/1 ready for a cluster that
		// setMatchingClusters simultaneously reports as not deployed and dependency-blocked.
		if _, isBlocked := blockedKeys[clusterTargetKeyFromRef(serviceset.ClusterReference(&serviceSet))]; isBlocked {
			continue
		}
		if serviceSet.Status.Deployed {
			readyDeployments++
		}
	}

	if readyDeployments < totalClusters {
		c.Status = metav1.ConditionFalse
		c.Reason = kcmv1.FailedReason
	}

	c.Message = fmt.Sprintf("%d/%d", readyDeployments, totalClusters)
	apimeta.SetStatusCondition(&mcs.GetMultiClusterServiceStatus().Conditions, c)
}

// blockedCluster describes a matching cluster whose ServiceSet could not be created or
// updated because a MultiClusterService this one depends on has not yet deployed all of
// its services there.
type blockedCluster struct {
	ref *corev1.ObjectReference
	msg string
}

// clusterTargetKey uniquely identifies a matching-cluster target for .status.matchingClusters
// bookkeeping. Namespace/name alone is not sufficient: the self-management pseudo-target is a
// SveltosCluster named mgmt/mgmt, and a real, unrelated ClusterDeployment coincidentally also
// named mgmt in namespace mgmt may exist at the same time - both count toward the readiness
// denominator, so they must not collide into a single map entry.
type clusterTargetKey struct {
	kind, apiVersion, namespace, name string
}

func clusterTargetKeyFromRef(ref *corev1.ObjectReference) clusterTargetKey {
	return clusterTargetKey{kind: ref.Kind, apiVersion: ref.APIVersion, namespace: ref.Namespace, name: ref.Name}
}

// setDependencyReadyCondition updates the MultiClusterServiceDependencyReady condition, which
// reflects whether every MultiClusterService this one depends on has finished deploying its
// services to all clusters this MultiClusterService matches.
//
// checkErr is the joined set of real, unexpected errors okToReconcileServiceSet returned while
// checking dependencies (e.g. failing to Get a dependency MultiClusterService/ServiceSet, a
// malformed ClusterSelector) - never the expected blocked state, which is carried by blocked
// instead. When checkErr is non-nil, dependency readiness could not be established for at least
// one matching cluster: that cluster is neither confirmed ready nor confirmed blocked, so
// reporting True or False would assert something we don't actually know, even if other clusters
// were genuinely found blocked in the same reconcile.
func (*MultiClusterServiceCommonReconciler) setDependencyReadyCondition(mcs kcmv1.MultiClusterServiceCommon, blocked []blockedCluster, checkErr error) {
	if kcmv1.IsMCSNil(mcs) {
		return
	}

	mcsKind := kcmv1.MultiClusterServiceKind
	if _, ok := mcs.(*kcmv1.NamespacedMultiClusterService); ok {
		mcsKind = kcmv1.NamespacedMultiClusterServiceKind
	}

	c := metav1.Condition{
		Type:               kcmv1.MultiClusterServiceDependencyReadyCondition,
		Status:             metav1.ConditionTrue,
		Reason:             kcmv1.SucceededReason,
		ObservedGeneration: mcs.GetGeneration(),
	}
	switch {
	case checkErr != nil:
		c.Status = metav1.ConditionUnknown
		c.Reason = kcmv1.MultiClusterServiceDependencyCheckFailedReason
		// checkErr itself is not bounded here - the caller still returns and thus logs it in full
		// (controller-runtime logs any non-nil error returned from Reconcile) - only what gets
		// persisted onto mcs.Status is capped. See dependencyCheckMessage.
		c.Message = dependencyCheckMessage(checkErr)
	case len(blocked) > 0:
		c.Status = metav1.ConditionFalse
		c.Reason = kcmv1.MultiClusterServiceDependencyNotReadyReason
		c.Message = fmt.Sprintf("waiting for %s dependencies to be ready on %d matching cluster(s)", mcsKind, len(blocked))
	}
	apimeta.SetStatusCondition(&mcs.GetMultiClusterServiceStatus().Conditions, c)
}

// maxDependencyCheckMessageBytes bounds the DependencyReady condition's Message when reporting
// checkErr. checkErr accumulates one wrapped error per (target, dependency) pair across the whole
// reconcile - proportional to matching-cluster count x len(DependsOn), neither of which is
// bounded - and this message is persisted on mcs.Status, unlike checkErr itself, which the caller
// still returns (and controller-runtime thus logs) in full. Without this cap, a persistent
// failure on a large deployment could grow the condition message toward API object size limits.
const maxDependencyCheckMessageBytes = 1024

// dependencyCheckMessage renders checkErr into a message bounded to maxDependencyCheckMessageBytes,
// noting the total number of underlying errors and, when truncated, how much detail was omitted.
func dependencyCheckMessage(checkErr error) string {
	msg := fmt.Sprintf("failed to determine MultiClusterService dependency readiness (%d error(s)): %s",
		countJoinedErrors(checkErr), checkErr.Error())
	if len(msg) <= maxDependencyCheckMessageBytes {
		return msg
	}
	omittedBytes := len(msg) - maxDependencyCheckMessageBytes
	truncated := strings.ToValidUTF8(msg[:maxDependencyCheckMessageBytes], "")
	return fmt.Sprintf("%s... (%d bytes omitted, see reconcile logs for the full error)", truncated, omittedBytes)
}

// countJoinedErrors returns the number of leaf errors joined into err via errors.Join (recursively,
// since errors.Join trees can nest), 1 for a non-joined error, or 0 for nil.
func countJoinedErrors(err error) int {
	if err == nil {
		return 0
	}
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return 1
	}
	n := 0
	for _, e := range joined.Unwrap() {
		n += countJoinedErrors(e)
	}
	return n
}

// setMatchingClusters collects service deployments status on matching clusters from ServiceSet objects and
// updates MultiClusterService object's status. blocked provides an entry for each matching cluster whose
// ServiceSet does not exist yet because it is waiting on a MultiClusterService dependency, so that such
// clusters are still surfaced in the status instead of silently missing from it.
func (r *MultiClusterServiceCommonReconciler) setMatchingClusters(ctx context.Context, mcs kcmv1.MultiClusterServiceCommon, serviceSets []kcmv1.ServiceSet, blocked []blockedCluster) error {
	if kcmv1.IsMCSNil(mcs) {
		return nil
	}

	mcsKey := kcmv1.MultiClusterServiceKind
	if _, ok := mcs.(*kcmv1.NamespacedMultiClusterService); ok {
		mcsKey = kcmv1.NamespacedMultiClusterServiceKind
	}

	l := ctrl.LoggerFrom(ctx)
	l.V(1).Info("Reconciling " + mcsKey + " matching clusters")
	now := metav1.NewTime(r.timeFunc())
	// clusterEntries is keyed by clusterTargetKey rather than appended to a plain slice, because
	// a cluster can appear in both serviceSets and blocked at the same time: its ServiceSet may have been
	// created during an earlier, unblocked reconcile and is still around (we don't delete the services
	// already deployed before dependency changed), while the current reconcile now finds it blocked again.
	// Keying by cluster ensures exactly one entry per cluster instead of one from each source.
	clusterEntries := make(map[clusterTargetKey]kcmv1.MatchingCluster, len(serviceSets)+len(blocked))

	var errs error
	for _, serviceSet := range serviceSets {
		// we'll skip service sets being deleted
		if !serviceSet.DeletionTimestamp.IsZero() {
			continue
		}
		// we'll skip service sets which does not have cluster reference set yet
		if serviceSet.Status.Cluster == nil {
			continue
		}

		cluster := kcmv1.MatchingCluster{
			ObjectReference:    serviceSet.Status.Cluster.DeepCopy(),
			LastTransitionTime: &now,
			Deployed:           serviceSet.Status.Deployed,
		}
		regional, err := r.clusterRegional(ctx, cluster.ObjectReference)
		if err != nil {
			errs = errors.Join(errs, err)
			continue
		}
		cluster.Regional = regional
		clusterEntries[clusterTargetKeyFromRef(cluster.ObjectReference)] = cluster
	}

	// blocked is applied after serviceSets and overwrites any entry for the same cluster - it
	// reflects this reconcile's up-to-date view of whether the dependency is satisfied, whereas a
	// pre-existing ServiceSet-derived entry may be stale (e.g. still Deployed from before the
	// dependency became unsatisfied again, even though it is no longer being kept in sync).
	for _, b := range blocked {
		// Unlike the serviceSets loop above, a failure here is not joined into errs and does not
		// skip the entry: we already know for certain that this cluster is blocked on a dependency,
		// and that is the important fact to surface - it must not depend on the Credential a
		// blocked cluster's ClusterDeployment references already existing (dependency-blocked and
		// Credential-not-yet-created are independent, unrelated states). A failure to compute
		// Regional here just means it's best-effort reported as false for this reconcile; it self-
		// corrects once the Credential is resolvable, via the merge below.
		regional, _ := r.clusterRegional(ctx, b.ref)
		clusterEntries[clusterTargetKeyFromRef(b.ref)] = kcmv1.MatchingCluster{
			ObjectReference:    b.ref,
			LastTransitionTime: &now,
			Regional:           regional,
			Deployed:           false,
			Reason:             kcmv1.MultiClusterServiceDependencyNotReadyReason,
			Message:            b.msg,
		}
	}

	mcsStatus := mcs.GetMultiClusterServiceStatus()
	observedClustersMap := make(map[clusterTargetKey]kcmv1.MatchingCluster, len(mcsStatus.MatchingClusters))
	for _, cluster := range mcsStatus.MatchingClusters {
		observedClustersMap[clusterTargetKeyFromRef(cluster.ObjectReference)] = cluster
	}

	resultingClusters := make([]kcmv1.MatchingCluster, 0, len(clusterEntries))
	for _, cluster := range clusterEntries {
		observedCluster, ok := observedClustersMap[clusterTargetKeyFromRef(cluster.ObjectReference)]
		if !ok {
			resultingClusters = append(resultingClusters, cluster)
			continue
		}
		if observedCluster.Deployed != cluster.Deployed {
			observedCluster.Deployed = cluster.Deployed
			observedCluster.LastTransitionTime = cluster.LastTransitionTime.DeepCopy()
		}
		observedCluster.Reason = cluster.Reason
		observedCluster.Message = cluster.Message
		// Regional and the object reference are recomputed every reconcile (unlike Deployed, which
		// intentionally preserves its prior LastTransitionTime unless it actually changed), so they
		// must always be copied onto the observed entry - otherwise a cluster first observed while
		// blocked (Regional defaulted false, since it isn't known yet) would keep that stale false
		// forever, even once it unblocks and its true Regional value has been computed.
		observedCluster.Regional = cluster.Regional
		observedCluster.ObjectReference = cluster.ObjectReference.DeepCopy()
		resultingClusters = append(resultingClusters, observedCluster)
	}

	// We need to sort the slice of matching clusters in order to avoid any
	// unnecessary reconciles when the status is compared in the `updateStatus` func.
	slices.SortStableFunc(resultingClusters, func(a, b kcmv1.MatchingCluster) int {
		if n := cmp.Compare(a.Kind, b.Kind); n != 0 {
			return n
		}
		if n := cmp.Compare(a.Namespace, b.Namespace); n != 0 {
			return n
		}
		return cmp.Compare(a.Name, b.Name)
	})
	mcsStatus.MatchingClusters = resultingClusters

	return errs
}

// clusterRegional determines whether ref - a ClusterDeployment or the self-management mgmt
// pseudo-cluster - is regional. Only a ClusterDeployment reference carries region information
// (via its Credential); the mgmt pseudo-cluster is never regional.
func (r *MultiClusterServiceCommonReconciler) clusterRegional(ctx context.Context, ref *corev1.ObjectReference) (bool, error) {
	if ref.Kind != kcmv1.ClusterDeploymentKind {
		return false, nil
	}
	cd := new(kcmv1.ClusterDeployment)
	key := client.ObjectKey{Name: ref.Name, Namespace: ref.Namespace}
	if err := r.Client.Get(ctx, key, cd); err != nil {
		return false, fmt.Errorf("failed to get ClusterDeployment %s: %w", key, err)
	}
	cred := new(kcmv1.Credential)
	key = client.ObjectKey{Namespace: cd.Namespace, Name: cd.Spec.Credential}
	if err := r.Client.Get(ctx, key, cred); err != nil {
		return false, fmt.Errorf("failed to get Credential %s: %w", key, err)
	}
	return cred.Spec.Region != "", nil
}

// updateStatus check whether status needs to be updated, if so updates the status for the MultiClusterService object
// and returns a flag whether requeue should happen and an error.
func (r *MultiClusterServiceCommonReconciler) updateStatus(ctx context.Context, oldObj, newObj kcmv1.MultiClusterServiceCommon) (bool, error) {
	oldStatus := oldObj.GetMultiClusterServiceStatus()
	newStatus := newObj.GetMultiClusterServiceStatus()

	// we'll requeue if no changes were applied to keep tracking ClusterDeployments
	// which were created or updated.
	if equality.Semantic.DeepEqual(oldStatus, newStatus) {
		return true, nil
	}

	newStatus.ObservedGeneration = newObj.GetGeneration()
	newStatus.Conditions = conditionsutil.UpdateReadyCondition(newStatus.Conditions, newObj.GetGeneration(), handleMultiClusterServiceFailedCondition)

	// we'll requeue in case of successful status update due to existing GenerationChangePredicate.
	// Otherwise we'll return an error.
	if err := r.Client.Status().Update(ctx, newObj); err != nil {
		var msg string
		if _, ok := newObj.(*kcmv1.MultiClusterService); ok {
			msg = "failed to update status for MultiClusterService " + newObj.GetFullname()
		} else {
			msg = "failed to update status for NamespacedMultiClusterService " + newObj.GetFullname()
		}
		return false, fmt.Errorf("%s: %w", msg, err)
	}
	return true, nil
}

func handleMultiClusterServiceFailedCondition(cond metav1.Condition) (errMsg, warning string) {
	switch cond.Type {
	case kcmv1.ClusterInReadyStateCondition:
		errMsg = cond.Message + " Clusters are ready."
	case kcmv1.ServicesInReadyStateCondition:
		errMsg = cond.Message + " Services are ready."
	default:
		errMsg = cond.Message
	}
	return errMsg, ""
}

func (r *MultiClusterServiceCommonReconciler) reconcileDelete(ctx context.Context, mcs kcmv1.MultiClusterServiceCommon) (result ctrl.Result, err error) {
	if kcmv1.IsMCSNil(mcs) {
		return ctrl.Result{}, nil
	}

	l := ctrl.LoggerFrom(ctx)

	mcsKind := kcmv1.MultiClusterServiceKind
	indexKey := kcmv1.ServiceSetMultiClusterServiceIndexKey
	finalizer := kcmv1.MultiClusterServiceFinalizer
	if _, ok := mcs.(*kcmv1.NamespacedMultiClusterService); ok {
		mcsKind = kcmv1.NamespacedMultiClusterServiceKind
		indexKey = kcmv1.ServiceSetNamespacedMultiClusterServiceIndexKey
		finalizer = kcmv1.NamespacedMultiClusterServiceFinalizer
	}

	l.Info("Deleting " + mcsKind)

	mcs.GetName()

	defer func() {
		if err == nil {
			for _, svc := range mcs.GetMultiClusterServiceSpec().ServiceSpec.Services {
				metrics.TrackMetricTemplateUsage(ctx, kcmv1.ServiceTemplateKind, svc.Template, mcsKind, mcs.GetObjectMeta(), false)
			}
		}
	}()

	l.Info("Validating " + mcsKind + " dependencies for delete")
	if err := validationutil.ValidateMCSDelete(ctx, r.Client, mcs); err != nil {
		if r.setCondition(mcs, kcmv1.MultiClusterServiceDependencyValidationCondition, err) {
			record.Warnf(mcs, nil, kcmv1.MultiClusterServiceDependencyValidationCondition, "ValidateDelete", err.Error())
		}
		l.Error(err, "failed validation for "+mcsKind+" deletion, will retrigger")
		// Will retrigger this error because we want this MCS to be deleted once:
		// 1. Either the MCS this one depends on is deleted.
		// 2. Or the dependency is removed.
		return ctrl.Result{}, err
	}
	r.setCondition(mcs, kcmv1.MultiClusterServiceDependencyValidationCondition, nil)

	serviceSets := new(kcmv1.ServiceSetList)
	if err := r.Client.List(ctx, serviceSets, client.MatchingFields{indexKey: mcs.GetFullname()}, client.InNamespace(mcs.GetNamespace())); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list ServiceSets for %s %s: %w", mcsKind, mcs.GetFullname(), err)
	}
	l.V(1).Info("Found ServiceSets", "count", len(serviceSets.Items))
	for _, serviceSet := range serviceSets.Items {
		if !serviceSet.DeletionTimestamp.IsZero() {
			continue
		}
		if err := r.Client.Delete(ctx, &serviceSet); err != nil {
			l.Error(err, "failed to delete ServiceSet", "ServiceSet.Name", serviceSet.Name)
		}
		l.V(1).Info("Deleting ServiceSet", "namespaced_name", client.ObjectKeyFromObject(&serviceSet))
	}
	if len(serviceSets.Items) > 0 {
		return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, nil
	}

	if ok := controllerutil.RemoveFinalizer(mcs, finalizer); ok {
		if err := r.Client.Update(ctx, mcs); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to remove finalizer %s from %s %s: %w", finalizer, mcsKind, mcs.GetFullname(), err)
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MultiClusterServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	if r.timeFunc == nil {
		r.timeFunc = time.Now
	}
	r.defaultRequeueTime = 10 * time.Second

	managedController := ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimitutil.DefaultFastSlow(),
		}).
		For(&kcmv1.MultiClusterService{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&kcmv1.ServiceSet{},
			kubeutil.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) ([]ctrl.Request, error) {
				serviceSet, ok := o.(*kcmv1.ServiceSet)
				if !ok {
					return nil, nil
				}
				if serviceSet.Spec.MultiClusterService == "" {
					return nil, nil
				}
				mcs := new(kcmv1.MultiClusterService)
				if err := r.Client.Get(ctx, client.ObjectKey{Name: serviceSet.Spec.MultiClusterService}, mcs); err != nil {
					if apierrors.IsNotFound(err) {
						return nil, nil
					}
					return nil, fmt.Errorf("failed to get MultiClusterService %s: %w", serviceSet.Spec.MultiClusterService, err)
				}
				return []ctrl.Request{{NamespacedName: client.ObjectKeyFromObject(mcs)}}, nil
			}),
		)

	if r.IsDisabledValidationWH {
		// MultiClusterService is cluster-scoped, so its cache index stores every entry under the
		// no-namespace key. Passing client.InNamespace here would ask for the entry under the
		// ServiceTemplate's own namespace instead, which never matches - so, unlike the
		// NamespacedMultiClusterService watch below, this List must not be namespace-scoped.
		watchServiceTemplates(mgr, managedController, "multiclusterservice_ctrl_setup",
			func(ctx context.Context, _, templateName string) ([]ctrl.Request, error) {
				mcss := new(kcmv1.MultiClusterServiceList)
				if err := mgr.GetClient().List(ctx, mcss, client.MatchingFields{kcmv1.MultiClusterServiceTemplatesIndexKey: templateName}); err != nil {
					return nil, fmt.Errorf("failed to list MultiClusterServices by ServiceTemplate %s: %w", templateName, err)
				}

				resp := make([]ctrl.Request, 0, len(mcss.Items))
				for _, v := range mcss.Items {
					resp = append(resp, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&v)})
				}

				return resp, nil
			})
	}

	return managedController.Complete(r)
}

// watchServiceTemplates registers the ServiceTemplate watch that MultiClusterServiceReconciler
// and NamespacedMultiClusterServiceReconciler enable identically when webhook validation is
// disabled - everything about the watch is shared except how to turn a ServiceTemplate name into
// the matching MultiClusterService/NamespacedMultiClusterService reconcile requests, which listMCS
// supplies.
func watchServiceTemplates(
	mgr ctrl.Manager,
	managedController *builder.Builder,
	setupName string,
	listMCS func(ctx context.Context, namespace, templateName string) ([]ctrl.Request, error),
) {
	managedController.Watches(&kcmv1.ServiceTemplate{}, kubeutil.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) ([]ctrl.Request, error) {
		return listMCS(ctx, o.GetNamespace(), o.GetName())
	}), builder.WithPredicates(predicate.Funcs{
		GenericFunc: func(event.TypedGenericEvent[client.Object]) bool { return false },
		DeleteFunc:  func(event.TypedDeleteEvent[client.Object]) bool { return false },
		UpdateFunc: func(tue event.TypedUpdateEvent[client.Object]) bool {
			sto, ok := tue.ObjectOld.(*kcmv1.ServiceTemplate)
			if !ok {
				return false
			}
			stn, ok := tue.ObjectNew.(*kcmv1.ServiceTemplate)
			if !ok {
				return false
			}
			return stn.Status.Valid && !sto.Status.Valid
		},
	}))
	mgr.GetLogger().WithName(setupName).Info("Validations are disabled, watcher for ServiceTemplate objects is set")
}

// createOrUpdateServiceSet creates or updates the ServiceSet for the provided mcs and cd (cd is
// nil for the self-management ServiceSet).
func (r *MultiClusterServiceCommonReconciler) createOrUpdateServiceSet(
	ctx context.Context,
	mcs kcmv1.MultiClusterServiceCommon,
	cd *kcmv1.ClusterDeployment,
) error {
	serviceSetObjectKey := serviceset.ObjectKey(r.SystemNamespace, cd, mcs)
	opRequisites := serviceset.OperationRequisites{
		ServiceSetKey:   serviceSetObjectKey,
		MCS:             mcs,
		CD:              cd,
		SystemNamespace: r.SystemNamespace,
	}

	serviceSet, op, err := serviceset.GetServiceSetWithOperation(ctx, r.Client, opRequisites)
	if err != nil {
		return fmt.Errorf("failed to get ServiceSet %s: %w", serviceSetObjectKey.String(), err)
	}
	if op == kcmv1.ServiceSetOperationNone {
		return nil
	}

	return serviceset.NewProcessor(r.Client).CreateOrUpdateServiceSet(ctx, op, serviceSet)
}

func (r *MultiClusterServiceCommonReconciler) cleanupServiceSets(ctx context.Context, mcs kcmv1.MultiClusterServiceCommon) error {
	if kcmv1.IsMCSNil(mcs) {
		return nil
	}

	mcsKind := kcmv1.MultiClusterServiceKind
	indexKey := kcmv1.ServiceSetMultiClusterServiceIndexKey
	_, isNamespacedMCS := mcs.(*kcmv1.NamespacedMultiClusterService)
	if isNamespacedMCS {
		mcsKind = kcmv1.NamespacedMultiClusterServiceKind
		indexKey = kcmv1.ServiceSetNamespacedMultiClusterServiceIndexKey
	}
	mcsSpec := mcs.GetMultiClusterServiceSpec()

	if mcsSpec.KeepServicesOnSelectorMismatch {
		return nil
	}

	serviceSets := new(kcmv1.ServiceSetList)
	// we'll list all ServiceSets which have .spec.multiClusterService or spec.namespacedMultiClusterService
	// defined and match current MultiClusterService/NamespacedMultiClusterService object being reconciled.
	if err := r.Client.List(ctx, serviceSets, client.MatchingFields{indexKey: mcs.GetFullname()}, client.InNamespace(mcs.GetNamespace())); err != nil {
		return fmt.Errorf("failed to list ServiceSets for %s %s: %w", mcsKind, mcs.GetFullname(), err)
	}

	selector, err := metav1.LabelSelectorAsSelector(&mcsSpec.ClusterSelector)
	if err != nil {
		return fmt.Errorf("failed to convert ClusterSelector to label selector: %w", err)
	}

	var errs error
	for _, serviceSet := range serviceSets.Items {
		// this will happen in case the corresponding ClusterDeployment was deleted,
		// which triggered ServiceSet deletion as
		if !serviceSet.DeletionTimestamp.IsZero() {
			continue
		}

		// this is a selfManagement ServiceSet: keep it only if selfManagement
		// is still enabled, otherwise it no longer matches and must be deleted.
		// We ignore selfManagement for NamespacedMultiClusterServices.
		if !isNamespacedMCS && serviceSet.Spec.Cluster == "" {
			if mcsSpec.ServiceSpec.Provider.SelfManagement {
				continue
			}
			if err := r.Client.Delete(ctx, &serviceSet); client.IgnoreNotFound(err) != nil {
				errs = errors.Join(errs, fmt.Errorf("failed to delete ServiceSet %s/%s: %w", serviceSet.Namespace, serviceSet.Name, err))
			}
			continue
		}

		if selector.Empty() {
			// since selector is empty it will not match any ServiceSet so deleting the
			// ServiceSet without checking if its ClusterDeployment's labels match the selector
			if err := r.Client.Delete(ctx, &serviceSet); client.IgnoreNotFound(err) != nil {
				errs = errors.Join(errs, fmt.Errorf("failed to delete ServiceSet %s/%s: %w", serviceSet.Namespace, serviceSet.Name, err))
			}
			continue
		}

		cd := new(kcmv1.ClusterDeployment)
		key := client.ObjectKey{Namespace: serviceSet.Namespace, Name: serviceSet.Spec.Cluster}
		if err := r.Client.Get(ctx, key, cd); err != nil {
			return fmt.Errorf("failed to get ClusterDeployment %s: %w", key.String(), err)
		}

		if !selector.Matches(labels.Set(cd.Labels)) {
			// delete the ServiceSet since it's ClusterDeployment's labels don't match selector anymore
			if err := r.Client.Delete(ctx, &serviceSet); client.IgnoreNotFound(err) != nil {
				errs = errors.Join(errs, fmt.Errorf("failed to delete ServiceSet %s/%s: %w", serviceSet.Namespace, serviceSet.Name, err))
			}
		}
	}

	return errs
}

func (*MultiClusterServiceCommonReconciler) setCondition(mcs kcmv1.MultiClusterServiceCommon, typ string, err error) bool {
	reason, cstatus, msg := kcmv1.SucceededReason, metav1.ConditionTrue, ""
	if err != nil {
		reason, cstatus, msg = kcmv1.FailedReason, metav1.ConditionFalse, err.Error()
	}

	return apimeta.SetStatusCondition(&mcs.GetMultiClusterServiceStatus().Conditions, metav1.Condition{
		Type:               typ,
		Status:             cstatus,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: mcs.GetGeneration(),
	})
}

// resolvedDependency is one entry of mcs.Spec.DependsOn, resolved once per reconcile: the
// dependency MultiClusterService fetched, its ClusterSelector compiled, and its desired-service
// key set built. None of that depends on which target (self-management or a specific matching
// ClusterDeployment) is being checked, so resolveDependencies computes it once up front instead
// of okToReconcileServiceSet repeating it for every target.
type resolvedDependency struct {
	// getErr is set instead of mcs when the dependency MultiClusterService itself could not be
	// fetched; every other field is unset in that case.
	getErr error

	// selectorErr is set instead of selector when mcs.Spec.ClusterSelector fails to compile.
	selectorErr error

	selector labels.Selector

	// svcToCheck is the set of service keys from mcs.Spec.ServiceSpec.Services, used to check
	// which of a target ServiceSet's reported services belong to this dependency.
	svcToCheck map[client.ObjectKey]struct{}

	// mcsSpec *kcmv1.MultiClusterServiceSpec
	mcs kcmv1.MultiClusterServiceCommon

	mcsKey client.ObjectKey

	// kind is the Kind of this dependency: the same kind as the object being reconciled, since
	// a MultiClusterService depends only on MultiClusterServices and a
	// NamespacedMultiClusterService only on NamespacedMultiClusterServices. It is recorded here
	// rather than derived from mcs on demand because mcs is nil whenever getErr is set, which is
	// precisely the case whose message most needs to name the kind.
	kind string
}

// resolveDependencies resolves every entry of mcs.Spec.DependsOn once - see resolvedDependency's
// doc comment for why this must happen only once per reconcile rather than once per target.
func (r *MultiClusterServiceCommonReconciler) resolveDependencies(ctx context.Context, mcs kcmv1.MultiClusterServiceCommon) []resolvedDependency {
	if kcmv1.IsMCSNil(mcs) {
		return nil
	}

	mcsSpec := mcs.GetMultiClusterServiceSpec()
	deps := make([]resolvedDependency, 0, len(mcsSpec.DependsOn))

	_, isNamespacedMCS := mcs.(*kcmv1.NamespacedMultiClusterService)
	depKind := kcmv1.MCSKind(mcs)

	for _, dep := range mcsSpec.DependsOn {
		// If the mcs is NOT namespaced then mcs.GetNamespace() will be empty
		// which is what we want for a cluster-wide MultiClusterService.
		rd := resolvedDependency{
			kind:   depKind,
			mcsKey: client.ObjectKey{Name: dep, Namespace: mcs.GetNamespace()},
		}

		var depMCS kcmv1.MultiClusterServiceCommon
		if isNamespacedMCS {
			x := new(kcmv1.NamespacedMultiClusterService)
			if getErr := r.Client.Get(ctx, rd.mcsKey, x); getErr != nil {
				rd.getErr = getErr
				deps = append(deps, rd)
				continue
			}
			depMCS = x
		} else {
			x := new(kcmv1.MultiClusterService)
			if getErr := r.Client.Get(ctx, rd.mcsKey, x); getErr != nil {
				rd.getErr = getErr
				deps = append(deps, rd)
				continue
			}
			depMCS = x
		}

		if kcmv1.IsMCSNil(depMCS) {
			continue
		}

		spec := depMCS.GetMultiClusterServiceSpec()
		rd.mcs = depMCS
		rd.selector, rd.selectorErr = metav1.LabelSelectorAsSelector(&spec.ClusterSelector)

		svcToCheck := make(map[client.ObjectKey]struct{}, len(spec.ServiceSpec.Services))
		for _, svc := range spec.ServiceSpec.Services {
			svcToCheck[serviceset.ServiceKey(svc.Namespace, svc.Name)] = struct{}{}
		}
		rd.svcToCheck = svcToCheck
		deps = append(deps, rd)
	}

	return deps
}

// okToReconcileServiceSet verifies if it is ok to reconcile a serviceset for the provided
// cd by verifying, for each of deps, that all of that dependency's services have been
// successfully deployed on the cluster represented by cd (cd is nil for the self-management
// ServiceSet).
//
// SelfManagement is ignored for NamespacedMultiClusterService, and that is already handled
// by the caller rather than here: ensureServiceSets only makes the cd == nil call for a
// cluster-scoped MultiClusterService, so for a NamespacedMultiClusterService this function
// is always reached with a real cd. That matters because the cd == nil branch below is the
// only place any SelfManagement flag is read - the cd != nil branch decides purely on the
// dependency's ClusterSelector - so no NamespacedMultiClusterService can reach a
// SelfManagement check, whether as the subject or as one of its deps.
//
// It reports its result through three channels, kept deliberately separate so the caller can
// treat an expected "waiting on dependency" state differently from a real failure:
//
//   - ok is true only when the ServiceSet may be created/updated, i.e. there is neither a real
//     error nor a blocked state. The caller should create/update the ServiceSet only when ok.
//   - err is non-nil only for unexpected failures (e.g. a Get or label-selector error). The
//     caller should propagate it as a real reconcile error so controller-runtime retries it with
//     backoff and it's logged as an actual failure. It never carries the blocked state.
//   - blocked is appended to (one entry for cd, or the mgmt pseudo-cluster when cd is nil) when
//     the ServiceSet must not be created/updated yet because a MultiClusterService this one
//     depends on hasn't finished deploying its services to this cluster - an expected,
//     self-resolving state the caller should surface on mcs.Status rather than treat as a
//     failure. A blocked state leaves err nil (and ok false).
//
// A single call may both hit a real error on one dependency and be blocked on another: err is
// then non-nil and blocked has an entry, so the failure is propagated while the blocked cluster
// is still surfaced on status.
//
// deps is mcs.Spec.DependsOn already resolved once per reconcile by resolveDependencies - see its
// doc comment for why that must not be redone per target.
func (r *MultiClusterServiceCommonReconciler) okToReconcileServiceSet(ctx context.Context, cd *kcmv1.ClusterDeployment, deps []resolvedDependency, blocked *[]blockedCluster) (ok bool, err error) {
	clusterRef := client.ObjectKey{Namespace: "mgmt", Name: "mgmt"}
	clusterLabels := make(map[string]string)
	// cd is nil only for the self-management (mothership) ServiceSet. A MultiClusterService
	// can both self-manage and match a ClusterSelector at the same time, in which case this
	// function is called once with cd == nil (mgmt) and once per matching cd - so cd's
	// presence, not mcs's own SelfManagement flag, is what tells us which target this call
	// is checking.
	if cd != nil {
		clusterRef = client.ObjectKeyFromObject(cd)
		clusterLabels = cd.Labels
	}

	// blockingDeps collects one short, bounded entry per DependsOn entry found blocking (never a
	// full wrapped error) - DependsOn is not meaningfully bounded, and this ends up stored on
	// mcs.Status once per matching cluster, so an unbounded per-dependency message here would let
	// status size grow as O(cluster count x DependsOn length) instead of O(cluster count).
	var blockingDeps []string

	defer func() {
		if len(blockingDeps) > 0 && blocked != nil { // To avoid panic by dereferencing a nil pointer
			msg := blockedMessage(clusterRef, blockingDeps)
			if cd != nil {
				*blocked = append(*blocked, blockedCluster{
					ref: &corev1.ObjectReference{
						Kind:       kcmv1.ClusterDeploymentKind,
						Name:       cd.Name,
						Namespace:  cd.Namespace,
						APIVersion: kcmv1.GroupVersion.WithKind(kcmv1.ClusterDeploymentKind).GroupVersion().String(),
					},
					msg: msg,
				})
			} else {
				*blocked = append(*blocked, blockedCluster{
					ref: serviceset.SelfManagementClusterReference(),
					msg: msg,
				})
			}
		}
		// ok to create/update the ServiceSet only when there is neither a real,
		// unexpected error nor an expected blocked state. blockingDeps is surfaced via
		// *blocked (status) rather than folded into err, so a blocked-but-not-errored
		// call returns err == nil and is not propagated as a reconcile error.
		ok = err == nil && len(blockingDeps) == 0
	}()

	for _, dep := range deps {
		if dep.getErr != nil {
			// Unexpected: ValidateMCSDependencyOverall already confirmed depMCS exists earlier
			// in this same reconcile, so a Get failure here is a real (likely transient) error,
			// not a normal "waiting on dependency" state.
			err = errors.Join(err, fmt.Errorf("failed to get %s %s which this depends on: %w", dep.kind, dep.mcsKey, dep.getErr))
			continue
		}

		if kcmv1.IsMCSNil(dep.mcs) {
			continue
		}

		depSpec := dep.mcs.GetMultiClusterServiceSpec()
		// Check if depMCS applies to the cluster represented by clusterRef. Self-management and
		// ClusterSelector are independent, mutually exclusive-in-relevance mechanisms: whether depMCS
		// targets the mgmt pseudo-cluster depends solely on depMCS's own SelfManagement flag (a
		// ClusterSelector never applies to the mothership itself), while whether depMCS targets a real
		// ClusterDeployment depends solely on whether its ClusterSelector matches that cluster's labels
		// - depMCS's SelfManagement flag has no bearing on that (a MultiClusterService can self-manage
		// and independently match other ClusterDeployments via ClusterSelector at the same time).
		// Reached only for a cluster-scoped MultiClusterService - see this function's doc
		// comment - so dep here is a MultiClusterService too, and its SelfManagement flag
		// is meaningful rather than ignored.
		if cd == nil {
			if !depSpec.ServiceSpec.Provider.SelfManagement {
				// depMCS does not target the mgmt cluster, so there is no dependency here.
				continue
			}
		} else {
			if dep.selectorErr != nil {
				// Unexpected: a malformed ClusterSelector is a configuration/validation
				// problem, not the dependency simply not being ready yet.
				err = errors.Join(err, fmt.Errorf("failed to determine if %s %s which this depends on matches cluster %s: %w", dep.kind, dep.mcsKey, clusterRef, dep.selectorErr))
				continue
			}
			// An empty ClusterSelector converts to labels.Everything(), which Matches() treats as
			// matching every cluster. reconcileUpdate instead treats an empty selector as matching no
			// ClusterDeployment (it only lists matching ClusterDeployments when !selector.Empty()), so
			// mirror that here - otherwise a depMCS with a blank ClusterSelector would appear to depend
			// against every ClusterDeployment, even ones its own reconcile never targets.
			if dep.selector.Empty() || !dep.selector.Matches(labels.Set(clusterLabels)) {
				continue
			}
		}

		// Get the ServiceSet associated with provided CD and depMCS.
		sset := new(kcmv1.ServiceSet)
		ssetKey := serviceset.ObjectKey(r.SystemNamespace, cd, dep.mcs)
		getErr := r.Client.Get(ctx, ssetKey, sset)
		if apierrors.IsNotFound(getErr) {
			// Expected: depMCS simply hasn't created its ServiceSet for this cluster yet.
			//
			// NOTE: We can safely retrigger here by adding error to return value because
			// we already return earlier if depMCS does not match either the cluster
			// represented by CD or the mgmt cluster. If that check is removed then a
			// bug may be introduced where the ServiceSet for this MCS and cluster is
			// never created if any one of the depMCS has a set of selector labels that
			// don't match either the cluster represented by CD or the mgmt cluster.
			// In such a scenario, the execution will always add error and continue because
			// it is trying to fetch the ServiceSet for depMCS and cluster which will never exist.
			// getErr (a NotFound error) is deliberately not embedded here - it adds nothing
			// actionable beyond "not yet created" and would make this entry's size depend on the
			// underlying API error's formatting.
			blockingDeps = append(blockingDeps, dep.mcsKey.String()+" (ServiceSet not yet created)")
			continue
		}
		if getErr != nil {
			// Unexpected: any error other than NotFound is a real (likely transient) failure.
			err = errors.Join(err, fmt.Errorf("failed to get serviceSet %s (owned by %s %s): %w", ssetKey, dep.kind, dep.mcsKey, getErr))
			continue
		}

		// To check if all services for depMCS have been deployed, we use dep.svcToCheck (built
		// once from depMCS's spec, not the ServiceSet's) because the ServiceSet may not have the
		// full list of services in its spec or status due to inter-service dependencies.
		deployed := 0
		for _, svc := range sset.Status.Services {
			if _, found := dep.svcToCheck[serviceset.ServiceKey(svc.Namespace, svc.Name)]; found {
				if svc.State == kcmv1.ServiceStateDeployed {
					deployed++
				}
			}
		}

		if deployed != len(depSpec.ServiceSpec.Services) {
			// Expected: depMCS's ServiceSet exists but hasn't finished deploying yet.
			blockingDeps = append(blockingDeps, fmt.Sprintf("%s (%d/%d services deployed)", dep.mcsKey.String(), deployed, len(depSpec.ServiceSpec.Services)))
			continue
		}
	}

	return ok, err
}

// maxBlockingDependenciesInMessage caps how many blocking dependencies are named individually in
// a blocked cluster's status message; DependsOn is not meaningfully bounded, so beyond this the
// remainder is summarized by count instead, keeping the message (and thus mcs.Status) a bounded
// size regardless of how many dependencies are actually blocking.
const maxBlockingDependenciesInMessage = 3

// blockedMessage renders a bounded summary of blockingDeps (see maxBlockingDependenciesInMessage)
// explaining why clusterRef's ServiceSet is being skipped this reconcile.
func blockedMessage(clusterRef client.ObjectKey, blockingDeps []string) string {
	shown := blockingDeps
	var omitted int
	if len(shown) > maxBlockingDependenciesInMessage {
		omitted = len(shown) - maxBlockingDependenciesInMessage
		shown = shown[:maxBlockingDependenciesInMessage]
	}
	msg := fmt.Sprintf("skipping create/update of ServiceSet for matching cluster %s: waiting on %s", clusterRef, strings.Join(shown, ", "))
	if omitted > 0 {
		msg += fmt.Sprintf(" and %d more", omitted)
	}
	return msg
}
