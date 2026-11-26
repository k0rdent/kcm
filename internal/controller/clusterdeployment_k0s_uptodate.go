// Copyright 2026
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
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	runtimev1 "sigs.k8s.io/cluster-api/api/runtime/v1beta2"
	"sigs.k8s.io/cluster-api/util/annotations"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	schemeutil "github.com/K0rdent/kcm/internal/util/scheme"
)

// =============================================================================
// TODO(KLUDGE): DELETE THIS FILE once Cluster API reports k0s workers as up to
// date by itself. Track https://github.com/kubernetes-sigs/cluster-api/issues/14086
//
// This is a temporary, KCM-only kludge, NOT a mechanism supported or
// recommended by the Cluster API community. Delete together with it the
// addK0sWorkerUpToDateNudger call in SetupWithManager, this file's test, and
// everything listed in templates/provider/kcm-regional/files/k0s-worker-uptodate-policy.yaml.
// =============================================================================
//
// Cluster API >= v1.14.0 never reports k0s MachineDeployment workers as up to
// date, because their kubelet version (vX.Y.Z+k0s) lacks the k0s build number
// of Machine.spec.version (vX.Y.Z+k0s.N).
//
// The kcm-regional chart installs the MutatingAdmissionPolicy
// [k0sWorkerUpToDatePolicyName], which corrects the UpToDate condition whenever
// Cluster API writes the status of such a Machine. Machines that were already
// stuck when the policy appeared get no write while nothing changes, so the
// nudger below periodically sends them an empty status patch, which runs the
// policy against the stored object.

const (
	// k0sWorkerUpToDatePolicyName must match the policy in templates/provider/kcm-regional/files/k0s-worker-uptodate-policy.yaml.
	k0sWorkerUpToDatePolicyName = "kcm-k0s-worker-uptodate"
	// k0sWorkerUpToDateNudgeInterval is how often the nudger looks for stuck k0s workers.
	k0sWorkerUpToDateNudgeInterval = 5 * time.Minute
	// k0sWorkerUpToDateStepTimeout bounds each step of a run, resolving a region or nudging one
	// ClusterDeployment, so an unresponsive apiserver holds up the rest of the run for that long at most.
	k0sWorkerUpToDateStepTimeout = 30 * time.Second
	// k0sWorkerUpToDateRenudgeAfter is how long the nudger leaves an unchanged Machine alone after
	// the policy declined to correct it, in case the apiserver had not loaded the policy yet.
	k0sWorkerUpToDateRenudgeAfter = time.Hour

	k0sWorkerConfigKind = "K0sWorkerConfig"
	k0sKubeletSuffix    = "+k0s"
)

// k0sCoreVersion matches the vX.Y.Z the policy accepts in front of the k0s kubelet suffix.
var k0sCoreVersion = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

// k0sWorkerUpToDateNudger sends empty status patches to k0s worker Machines stuck
// as not up to date, so the admission policy can correct them. It covers the
// management cluster and every region.
type k0sWorkerUpToDateNudger struct {
	mgmtClient client.Client
	// mgmtReader reads Machines and the policy without caching them in the manager
	mgmtReader client.Reader
	// regionalClient returns the client of a region
	regionalClient func(context.Context, *kcmv1.Region) (client.Client, error)

	// nudged remembers every Machine nudged so far, so a Machine the policy declined is
	// not patched again until it changes or the memo expires; only the nudger's goroutine
	// touches it
	nudged map[types.UID]k0sNudge
	now    func() time.Time

	stepTimeout time.Duration
}

// k0sNudge is when a Machine was nudged and the resourceVersion it had before the patch.
type k0sNudge struct {
	at              time.Time
	resourceVersion string
}

// k0sNudgeTarget is the cluster (management or a region) where a ClusterDeployment's CAPI objects live.
type k0sNudgeTarget struct {
	reader client.Reader
	writer client.Client
	// active is false when the policy is not installed there, or the region is paused, failed to
	// resolve or timed out earlier in the run
	active bool
}

var _ manager.Runnable = (*k0sWorkerUpToDateNudger)(nil)

// addK0sWorkerUpToDateNudger registers the nudger with the manager.
//
// TODO(KLUDGE): DELETE once https://github.com/kubernetes-sigs/cluster-api/issues/14086 is fixed, see the file header.
func (r *ClusterDeploymentReconciler) addK0sWorkerUpToDateNudger(mgr ctrl.Manager) error {
	if err := mgr.Add(newK0sWorkerUpToDateNudger(r.MgmtClient, mgr.GetAPIReader(), r.SystemNamespace)); err != nil {
		return fmt.Errorf("failed to add k0s worker UpToDate nudger: %w", err)
	}
	return nil
}

func newK0sWorkerUpToDateNudger(mgmtClient client.Client, mgmtReader client.Reader, systemNamespace string) *k0sWorkerUpToDateNudger {
	return &k0sWorkerUpToDateNudger{
		mgmtClient: mgmtClient,
		mgmtReader: mgmtReader,
		regionalClient: func(ctx context.Context, rgn *kcmv1.Region) (client.Client, error) {
			cl, _, err := kubeutil.GetRegionalClient(ctx, mgmtClient, systemNamespace, rgn, schemeutil.GetRegionalScheme)
			return cl, err
		},
		nudged:      make(map[types.UID]k0sNudge),
		now:         time.Now,
		stepTimeout: k0sWorkerUpToDateStepTimeout,
	}
}

// Start implements [manager.Runnable]. It runs only on the leader.
func (n *k0sWorkerUpToDateNudger) Start(ctx context.Context) error {
	l := ctrl.LoggerFrom(ctx).WithName("k0s_worker_uptodate_nudger")
	ctx = ctrl.LoggerInto(ctx, l)

	l.Info("Starting k0s worker UpToDate nudger", "interval", k0sWorkerUpToDateNudgeInterval)
	wait.UntilWithContext(ctx, n.nudge, k0sWorkerUpToDateNudgeInterval)
	return nil
}

func (n *k0sWorkerUpToDateNudger) nudge(ctx context.Context) {
	l := ctrl.LoggerFrom(ctx)

	cds := new(kcmv1.ClusterDeploymentList)
	if err := n.mgmtClient.List(ctx, cds); err != nil {
		l.Error(err, "failed to list ClusterDeployments")
		return
	}

	// region name ("" for the management cluster) -> target, resolved once per run
	targets := make(map[string]*k0sNudgeTarget)
	seen := make(map[types.UID]k0sNudge)
	for i := range cds.Items {
		if ctx.Err() != nil {
			// the manager is stopping
			return
		}

		cd := &cds.Items[i]
		if !cd.DeletionTimestamp.IsZero() || cd.Spec.DryRun {
			continue
		}

		key := client.ObjectKeyFromObject(cd)
		t, err := n.targetFor(ctx, cd, targets)
		switch {
		case apierrors.IsNotFound(err):
			// the Credential does not exist (yet), the ClusterDeployment reconciler reports that
			l.V(1).Info("skipping ClusterDeployment", "clusterdeployment", key, "reason", err.Error())
			continue
		case err != nil:
			l.Error(err, "skipping ClusterDeployment", "clusterdeployment", key)
			continue
		case !t.active:
			continue
		}

		stepCtx, cancel := context.WithTimeout(ctx, n.stepTimeout)
		err = n.nudgeClusterDeployment(stepCtx, t, cd, seen)
		cancel()
		if err != nil {
			l.Error(err, "failed to nudge stuck k0s workers", "clusterdeployment", key)
			// an unresponsive apiserver costs a run one step, not one per ClusterDeployment
			if errors.Is(stepCtx.Err(), context.DeadlineExceeded) {
				t.active = false
			}
		}
	}

	// drops Machines that are gone or no longer stuck
	n.nudged = seen
}

// targetFor returns the target of cd's region, resolving every region once per run. It
// returns only the failure to read cd's Credential: a region that cannot be resolved is
// logged once and skipped for the rest of the run.
func (n *k0sWorkerUpToDateNudger) targetFor(ctx context.Context, cd *kcmv1.ClusterDeployment, targets map[string]*k0sNudgeTarget) (*k0sNudgeTarget, error) {
	cred := new(kcmv1.Credential)
	credKey := client.ObjectKey{Namespace: cd.Namespace, Name: cd.Spec.Credential}
	if err := n.mgmtClient.Get(ctx, credKey, cred); err != nil {
		return nil, fmt.Errorf("failed to get Credential %s: %w", credKey, err)
	}

	region := cred.Spec.Region
	if t, ok := targets[region]; ok {
		return t, nil
	}

	stepCtx, cancel := context.WithTimeout(ctx, n.stepTimeout)
	t, err := n.resolveTarget(stepCtx, region)
	cancel()
	if err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "skipping the ClusterDeployments of the region in this run", "region", region)
		t = new(k0sNudgeTarget)
	}
	targets[region] = t
	return t, nil
}

// resolveTarget returns the target of region, "" being the management cluster.
func (n *k0sWorkerUpToDateNudger) resolveTarget(ctx context.Context, region string) (*k0sNudgeTarget, error) {
	t := &k0sNudgeTarget{reader: n.mgmtReader, writer: n.mgmtClient}
	if region != "" {
		rgn := new(kcmv1.Region)
		if err := n.mgmtClient.Get(ctx, client.ObjectKey{Name: region}, rgn); err != nil {
			return nil, fmt.Errorf("failed to get Region %s: %w", region, err)
		}
		// like the ClusterDeployment reconciler, leave a paused region alone (e.g. while it is restored)
		if _, ok := rgn.Annotations[kcmv1.RegionPauseAnnotation]; ok {
			return new(k0sNudgeTarget), nil
		}

		cl, err := n.regionalClient(ctx, rgn)
		if err != nil {
			return nil, fmt.Errorf("failed to get regional client for region %s: %w", region, err)
		}
		t.reader, t.writer = cl, cl
	}

	err := t.reader.Get(ctx, client.ObjectKey{Name: k0sWorkerUpToDatePolicyName}, new(admissionregistrationv1.MutatingAdmissionPolicy))
	switch {
	case err == nil:
		t.active = true
	case apierrors.IsNotFound(err) || apimeta.IsNoMatchError(err):
		// disabled in the chart, or MutatingAdmissionPolicy v1 is not served
	default:
		return nil, fmt.Errorf("failed to get MutatingAdmissionPolicy %s: %w", k0sWorkerUpToDatePolicyName, err)
	}
	return t, nil
}

func (n *k0sWorkerUpToDateNudger) nudgeClusterDeployment(ctx context.Context, t *k0sNudgeTarget, cd *kcmv1.ClusterDeployment, seen map[types.UID]k0sNudge) error {
	clusters := new(clusterapiv1.ClusterList)
	if err := t.reader.List(ctx, clusters,
		client.InNamespace(cd.Namespace),
		client.MatchingLabels{kcmv1.FluxHelmChartNameKey: cd.Name},
		client.Limit(1),
	); err != nil {
		return fmt.Errorf("failed to list CAPI Clusters: %w", err)
	}
	if len(clusters.Items) == 0 {
		return nil
	}

	cluster := &clusters.Items[0]
	// like the capi controllers, leave paused clusters and Machines alone (e.g. during clusterctl move)
	if annotations.IsPaused(cluster, cluster) {
		return nil
	}
	if !apimeta.IsStatusConditionFalse(cluster.Status.Conditions, clusterapiv1.ClusterWorkerMachinesUpToDateCondition) {
		return nil
	}

	machines := new(clusterapiv1.MachineList)
	if err := t.reader.List(ctx, machines,
		client.InNamespace(cluster.Namespace),
		client.MatchingLabels{clusterapiv1.ClusterNameLabel: cluster.Name},
		client.HasLabels{clusterapiv1.MachineDeploymentNameLabel},
	); err != nil {
		return fmt.Errorf("failed to list Machines of CAPI Cluster %s: %w", client.ObjectKeyFromObject(cluster), err)
	}

	nudged := 0
	for i := range machines.Items {
		m := &machines.Items[i]
		if annotations.HasPaused(m) || !isStuckK0sWorker(m) {
			continue
		}

		rv := m.ResourceVersion
		if prev, ok := n.nudged[m.UID]; ok && prev.resourceVersion == rv && n.now().Sub(prev.at) < k0sWorkerUpToDateRenudgeAfter {
			seen[m.UID] = prev
			continue
		}

		// an empty patch writes nothing by itself, the admission policy makes the change
		if err := t.writer.Status().Patch(ctx, m, client.RawPatch(types.MergePatchType, []byte("{}"))); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("failed to patch status of Machine %s: %w", client.ObjectKeyFromObject(m), err)
		}
		// keyed by the version read before the patch: a declined patch leaves it unchanged
		seen[m.UID] = k0sNudge{at: n.now(), resourceVersion: rv}
		nudged++
	}

	if nudged > 0 {
		ctrl.LoggerFrom(ctx).Info("Nudged stuck k0s worker Machines", "cluster", client.ObjectKeyFromObject(cluster), "count", nudged)
	}
	return nil
}

// isStuckK0sWorker reports whether m is a k0s MachineDeployment worker whose only reason to be
// not up to date is the k0s build in its version, mirroring the admission policy conditions.
// Unlike the policy it reads the kubelet version only: Machine.status.version (Cluster API
// >= v1.14.5) is not in the Cluster API version KCM builds against yet, and Cluster API sets
// it to the kubelet version anyway.
func isStuckK0sWorker(m *clusterapiv1.Machine) bool {
	if m.Spec.Bootstrap.ConfigRef.Kind != k0sWorkerConfigKind {
		return false
	}
	if _, ok := m.Labels[clusterapiv1.MachineDeploymentNameLabel]; !ok {
		return false
	}
	if _, ok := m.Annotations[clusterapiv1.UpdateInProgressAnnotation]; ok {
		return false
	}
	if strings.Contains(m.Annotations[runtimev1.PendingHooksAnnotation], "UpdateMachine") {
		return false
	}
	if apimeta.IsStatusConditionTrue(m.Status.Conditions, clusterapiv1.MachineUpdatingCondition) {
		return false
	}

	c := apimeta.FindStatusCondition(m.Status.Conditions, clusterapiv1.MachineUpToDateCondition)
	if c == nil || c.Status != metav1.ConditionFalse || c.Reason != clusterapiv1.MachineUpToDateUpdatingReason {
		return false
	}

	if m.Status.NodeInfo == nil || !strings.HasSuffix(m.Status.NodeInfo.KubeletVersion, k0sKubeletSuffix) {
		return false
	}
	kubelet := m.Status.NodeInfo.KubeletVersion
	core := strings.TrimSuffix(kubelet, k0sKubeletSuffix)
	if !k0sCoreVersion.MatchString(core) {
		return false
	}
	if m.Spec.Version == core || m.Spec.Version == kubelet {
		return true
	}
	// the kubelet version plus a numeric k0s build, e.g. v1.36.3+k0s.2 for kubelet v1.36.3+k0s
	build, ok := strings.CutPrefix(m.Spec.Version, kubelet+".")
	return ok && build != "" && !strings.ContainsFunc(build, func(r rune) bool { return r < '0' || r > '9' })
}
