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
	"fmt"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	conditionsutil "github.com/K0rdent/kcm/internal/util/conditions"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	schemeutil "github.com/K0rdent/kcm/internal/util/scheme"
)

// capiClusterPollEnqueue is the [pollerutil.EnqueueFunc] driving the
// periodic CAPI Cluster status check. For each non-deleted, non-dry-run
// ClusterDeployment it fetches the CAPI Cluster from the cluster where it
// lives (management or regional) and only emits the ClusterDeployment when
// the computed CAPIClusterSummary condition would differ from the one
// already set.
//
// Regional clients are cached per region for the duration of a single tick.
func (r *ClusterDeploymentReconciler) capiClusterPollEnqueue(ctx context.Context) ([]*kcmv1.ClusterDeployment, error) {
	l := ctrl.LoggerFrom(ctx)

	cds := new(kcmv1.ClusterDeploymentList)
	if err := r.MgmtClient.List(ctx, cds); err != nil {
		return nil, fmt.Errorf("failed to list ClusterDeployments: %w", err)
	}
	if len(cds.Items) == 0 {
		return nil, nil
	}

	// region name -> regional client, reused within a single tick
	rgnClients := make(map[string]client.Client)

	enqueue := make([]*kcmv1.ClusterDeployment, 0, len(cds.Items))
	for i := range cds.Items {
		cd := &cds.Items[i]
		if !cd.DeletionTimestamp.IsZero() || cd.Spec.DryRun {
			// dry-run CDs never reconcile infrastructure, so nothing to poll
			continue
		}

		cl, err := r.resolveCAPIClusterClient(ctx, cd, rgnClients)
		if err != nil {
			l.V(1).Error(err, "skipping CD in CAPI poll: cannot resolve client", "clusterdeployment", client.ObjectKeyFromObject(cd))
			continue
		}

		changed, err := r.capiClusterConditionDrifted(ctx, cl, cd)
		if err != nil {
			l.V(1).Error(err, "skipping CD in CAPI poll: cannot evaluate condition", "clusterdeployment", client.ObjectKeyFromObject(cd))
			continue
		}

		if changed {
			enqueue = append(enqueue, cd)
		}
	}

	return enqueue, nil
}

// resolveCAPIClusterClient returns the client for the cluster where cd's CAPI
// Cluster lives, reusing entries in cache (keyed by region name) so the
// kubeconfig Secret is fetched at most once per region per tick.
func (r *ClusterDeploymentReconciler) resolveCAPIClusterClient(ctx context.Context, cd *kcmv1.ClusterDeployment, cache map[string]client.Client) (client.Client, error) {
	cred := new(kcmv1.Credential)
	credKey := client.ObjectKey{Namespace: cd.Namespace, Name: cd.Spec.Credential}
	if err := r.MgmtClient.Get(ctx, credKey, cred); err != nil {
		return nil, fmt.Errorf("failed to get Credential %s: %w", credKey, err)
	}

	if cred.Spec.Region == "" {
		return r.MgmtClient, nil
	}

	if cl, ok := cache[cred.Spec.Region]; ok {
		return cl, nil
	}

	cl, err := kubeutil.GetRegionalClientByRegionName(ctx, r.MgmtClient, r.SystemNamespace, cred.Spec.Region, schemeutil.GetRegionalScheme)
	if err != nil {
		return nil, fmt.Errorf("failed to get regional client for region %s: %w", cred.Spec.Region, err)
	}

	cache[cred.Spec.Region] = cl

	return cl, nil
}

// capiClusterConditionDrifted reports whether the CAPIClusterSummary
// condition computed from the CAPI Cluster fetched via cl differs from the
// one currently set on cd. When the CAPI Cluster is missing, drift is
// reported so the controller can surface the disappearance (promote
// Deleting -> DeletionCompleted, or mark previously-known Clusters as
// Missing). A CD that never had a CAPIClusterSummary condition is treated
// as legitimately Cluster-less (e.g., a template that does not produce one).
func (*ClusterDeploymentReconciler) capiClusterConditionDrifted(ctx context.Context, cl client.Client, cd *kcmv1.ClusterDeployment) (bool, error) {
	clusters := new(clusterapiv1.ClusterList)
	if err := cl.List(
		ctx, clusters,
		client.MatchingLabels{kcmv1.FluxHelmChartNameKey: cd.Name},
		client.Limit(1),
		client.InNamespace(cd.Namespace),
	); err != nil {
		return false, fmt.Errorf("failed to list CAPI Clusters for %s: %w", client.ObjectKeyFromObject(cd), err)
	}

	curr := apimeta.FindStatusCondition(cd.Status.Conditions, kcmv1.CAPIClusterSummaryCondition)

	if len(clusters.Items) == 0 {
		if curr == nil {
			// never observed a CAPI Cluster; nothing to surface
			return false, nil
		}
		switch curr.Reason {
		case kcmv1.CAPIClusterMissingReason, kcmv1.DeletionCompletedReason:
			// disappearance already reflected
			return false, nil
		default:
			// either Deleting (promote to DeletionCompleted) or previously-known Cluster vanished
			return true, nil
		}
	}

	next, err := conditionsutil.GetCAPIClusterSummaryCondition(cd, &clusters.Items[0])
	if err != nil {
		return false, fmt.Errorf("failed to compute CAPI summary condition: %w", err)
	}

	if curr == nil {
		return true, nil
	}

	return curr.Status != next.Status || curr.Reason != next.Reason || curr.Message != next.Message, nil
}
