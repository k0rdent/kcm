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

package conditions

import (
	"fmt"
	"regexp"
	"strings"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// sveltosTempKubeconfigRe matches the temporary file sveltoscluster-manager writes the kubeconfig to
// before loading it. The numeric suffix is random per probe, so it has to be stripped: it is the only
// varying part of an otherwise identical failure message, and leaving it in makes the condition
// message differ on every probe, which in turn produces an endless stream of status writes,
// ClusterDeployment reconciles and duplicate events for a cluster whose state never actually changed.
var sveltosTempKubeconfigRe = regexp.MustCompile(`(/tmp/kubeconfig)\d+`)

// stableFailureMessage strips the per-probe noise out of a SveltosCluster failure message so that an
// unchanged failure yields an unchanged message.
func stableFailureMessage(msg string) string {
	return sveltosTempKubeconfigRe.ReplaceAllString(strings.TrimSpace(msg), "$1")
}

// GetSveltosClusterReadyCondition builds the [github.com/K0rdent/kcm/api/v1beta1.SveltosClusterReadyCondition]
// for cd from the health of its underlying SveltosCluster.
//
// Templates that adopt an already existing cluster (e.g. adopted-cluster) produce a SveltosCluster
// instead of a CAPI Cluster, so [GetCAPIClusterSummaryCondition] has nothing to summarize and the
// reachability of the adopted cluster would otherwise never be reflected on the ClusterDeployment.
//
// The condition is derived from spec.connectionStatus and spec.failureMessage, which
// sveltoscluster-manager refreshes on every health probe:
//
//   - ConnectionDown: the probe has failed at least spec.consecutiveFailureThreshold times in a row.
//     Reported as a failure.
//   - a failureMessage without ConnectionDown: the probe is failing but is still within the
//     configured threshold. Reported as progressing so that a transient blip does not flap the
//     ClusterDeployment out of its Ready state; spec.consecutiveFailureThreshold is what tunes
//     how forgiving this is.
//   - ConnectionHealthy: the cluster is reachable.
//   - anything else: the cluster has not been probed yet, so its health is genuinely unknown.
//
// NOTE: status.ready is deliberately NOT consulted. When sveltoscluster-manager cannot even build
// a client from the kubeconfig it returns before touching status.ready, which leaves the previously
// observed (or default) value behind. An adopted cluster whose kubeconfig is malformed therefore
// reports connectionStatus=Down alongside a stale ready=true.
func GetSveltosClusterReadyCondition(cd *kcmv1.ClusterDeployment, sveltosCluster *libsveltosv1beta1.SveltosCluster) *metav1.Condition {
	cond := &metav1.Condition{
		Type:               kcmv1.SveltosClusterReadyCondition,
		ObservedGeneration: cd.Generation,
	}

	failureMessage := ""
	if sveltosCluster.Status.FailureMessage != nil {
		failureMessage = stableFailureMessage(*sveltosCluster.Status.FailureMessage)
	}

	switch {
	case sveltosCluster.Status.ConnectionStatus == libsveltosv1beta1.ConnectionDown:
		cond.Status = metav1.ConditionFalse
		cond.Reason = kcmv1.ConnectionDownReason
		cond.Message = "Connection to the cluster is down."
		if failureMessage != "" {
			cond.Message += " " + failureMessage
		}

	case failureMessage != "":
		cond.Status = metav1.ConditionFalse
		cond.Reason = kcmv1.ProgressingReason
		cond.Message = fmt.Sprintf("Connection to the cluster has failed %d time(s) in a row, retrying: %s",
			sveltosCluster.Status.ConnectionFailures, failureMessage)

	case sveltosCluster.Status.ConnectionStatus == libsveltosv1beta1.ConnectionHealthy:
		cond.Status = metav1.ConditionTrue
		cond.Reason = kcmv1.SucceededReason
		cond.Message = "Connection to the cluster is healthy"

	default:
		cond.Status = metav1.ConditionUnknown
		cond.Reason = kcmv1.ConnectionProbePendingReason
		cond.Message = "Waiting for the connection to the cluster to be probed"
	}

	return cond
}
