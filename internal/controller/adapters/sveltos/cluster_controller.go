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
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/libsveltos/lib/clusterproxy"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	apiv1 "k8s.io/client-go/tools/clientcmd/api/v1"
	"k8s.io/kubectl/pkg/scheme"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	ratelimitutil "github.com/K0rdent/kcm/internal/util/ratelimit"
)

// ClusterReconciler reconciles a SveltosCluster object.
type ClusterReconciler struct {
	client.Client

	config *rest.Config
}

func (r *ClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling")

	sveltosCluster := new(libsveltosv1beta1.SveltosCluster)
	if err := r.Get(ctx, req.NamespacedName, sveltosCluster); err != nil {
		l.Error(err, "unable to fetch SveltosCluster")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	patchHelper, err := patch.NewHelper(sveltosCluster, r.Client)
	if err != nil {
		l.Error(err, "failed to construct patch helper for SveltosCluster")
		return ctrl.Result{}, err
	}

	caData, err := getInClusterCAData()
	if err != nil {
		l.Error(err, "failed to fetch CA data")
		return ctrl.Result{}, err
	}

	libsveltosLogger := ctrl.LoggerFrom(ctx).WithName("libsveltos")
	data, err := clusterproxy.GetSveltosSecretData(ctx, libsveltosLogger, r.Client, sveltosCluster.Namespace, sveltosCluster.Name)
	if err != nil {
		l.Error(err, "failed to get sveltos secret data")
		return ctrl.Result{}, fmt.Errorf("failed to get sveltos secret data: %w", err)
	}

	unstructuredConfig := &unstructured.Unstructured{}
	if _, _, err := scheme.Codecs.UniversalDeserializer().Decode(data, nil, unstructuredConfig); err != nil {
		l.Error(err, "failed to decode \"k8s.io/client-go/tools/clientcmd/api/v1\".Config data")
		return ctrl.Result{}, fmt.Errorf("failed to decode \"k8s.io/client-go/tools/clientcmd/api/v1\".Config (%.50s): %w", string(data), err)
	}

	config := new(apiv1.Config)
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredConfig.UnstructuredContent(), config); err != nil {
		l.Error(err, "failed to convert unstructured data to \"k8s.io/client-go/tools/clientcmd/api/v1\".Config")
		return ctrl.Result{}, fmt.Errorf("failed to convert unstructured data to \"k8s.io/client-go/tools/clientcmd/api/v1\".Config: %w", err)
	}

	const renewalConfigKey = "re-kubeconfig"
	for i := range config.Contexts {
		cc := &config.Contexts[i]

		saNamespace := cc.Context.Namespace
		saName := cc.Context.AuthInfo

		if sveltosCluster.Spec.TokenRequestRenewalOption.SANamespace != "" && sveltosCluster.Spec.TokenRequestRenewalOption.SAName != "" {
			saNamespace = sveltosCluster.Spec.TokenRequestRenewalOption.SANamespace
			saName = sveltosCluster.Spec.TokenRequestRenewalOption.SAName
		}

		// https://github.com/projectsveltos/sveltoscluster-manager/blob/31ff86a077fb6cb4d4c426440ef2fe4f2ff578ce/controllers/sveltoscluster_controller.go#L331

		saExpirationInSecond := sveltosCluster.Spec.TokenRequestRenewalOption.RenewTokenRequestInterval.Seconds()
		// Minimum duration for a TokenRequest is 10 minutes. SveltosCluster reconciler always set the expiration to be
		// sveltosCluster.Spec.TokenRequestRenewalOption.RenewTokenRequestInterval plus 30 minutes. That will also allow
		// reconciler to renew it again before it current tokenRequest expires
		const secondsToAddToTokenRequest = 30 * 60 // 30 minutes
		saExpirationInSecond += float64(secondsToAddToTokenRequest)

		tokenRequest, err := r.getServiceAccountTokenRequest(ctx, saNamespace, saName, saExpirationInSecond)
		if err != nil {
			l.Error(err, "failed to get TokenRequest")
			return ctrl.Result{}, fmt.Errorf("failed to get TokenRequest: %w", err)
		}

		// it's sveltoscluster-manager controller responsibility to update renew token request interval
		// and we want to interfere with this controller as little as possible

		l.V(1).Info("Get Kubeconfig from TokenRequest")
		data := r.getKubeconfigFromToken(saNamespace, saName, tokenRequest.Token, caData)

		if err := clusterproxy.UpdateSveltosSecretData(ctx, libsveltosLogger, r.Client, sveltosCluster.Namespace, sveltosCluster.Name, data, renewalConfigKey); err != nil {
			l.Error(err, "failed to update Secret")
			return ctrl.Result{}, fmt.Errorf("failed to update Secret: %w", err)
		}

		// set updates to spec/status
		// WARN: interferes with another controller
		if sveltosCluster.Spec.KubeconfigKeyName != renewalConfigKey {
			sveltosCluster.Spec.KubeconfigKeyName = renewalConfigKey
		}
		sveltosCluster.Status.LastReconciledTokenRequestAt = time.Now().Format(time.RFC3339)
	}

	// NOTE: set neither failure messages nor health status, it's sveltoscluster-manager controller responsibility
	// we only update the config with a new token and set (if not) the new key along with the last update time

	// patch both spec/status
	// WARN: interferes with another controller
	if err := patchHelper.Patch(ctx, sveltosCluster); err != nil {
		l.Error(err, "failed to patch SveltosCluster")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ClusterReconciler) getServiceAccountTokenRequest(ctx context.Context, saNs, saName string, saExpirationInSecond float64) (*authenticationv1.TokenRequestStatus, error) {
	clientset, err := kubernetes.NewForConfig(r.config)
	if err != nil {
		return nil, fmt.Errorf("failed to construct ClientSet from manager's config: %w", err)
	}

	expiration := int64(saExpirationInSecond)
	tokenRequest, err := clientset.CoreV1().ServiceAccounts(saNs).CreateToken(ctx, saName, &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			ExpirationSeconds: &expiration,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create token for ServiceAccount '%s/%s': %w", saNs, saName, err)
	}

	return &tokenRequest.Status, nil
}

func (r *ClusterReconciler) getKubeconfigFromToken(namespace, serviceAccountName, token string, caData []byte) string {
	const template = `apiVersion: v1
kind: Config
clusters:
- name: local
  cluster:
    server: %s
    certificate-authority-data: "%s"
users:
- name: %s
  user:
    token: %s
contexts:
- name: sveltos-context
  context:
    cluster: local
    namespace: %s
    user: %s
current-context: sveltos-context`

	data := fmt.Sprintf(template,
		r.config.Host,
		base64.StdEncoding.EncodeToString(caData),
		serviceAccountName,
		token,
		namespace,
		serviceAccountName)

	return data
}

func getInClusterCAData() ([]byte, error) {
	fi, err := os.Stat("/var/run/secrets/kubernetes.io/serviceaccount/token")
	inCluster := os.Getenv("KUBERNETES_SERVICE_HOST") != "" &&
		os.Getenv("KUBERNETES_SERVICE_PORT") != "" &&
		err == nil && !fi.IsDir()

	if !inCluster { // sanity check
		return []byte{}, nil
	}

	const rootCAFile = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	content, err := os.ReadFile(rootCAFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA file: %w", err)
	}

	return content, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.config = mgr.GetConfig()

	return ctrl.NewControllerManagedBy(mgr).
		Named("sveltos_cluster_controller").
		For(&libsveltosv1beta1.SveltosCluster{}).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimitutil.DefaultFastSlow(),
		}).
		WithEventFilter(predicate.Funcs{
			GenericFunc: func(event.TypedGenericEvent[client.Object]) bool { return false },
			CreateFunc:  func(event.TypedCreateEvent[client.Object]) bool { return false },
			DeleteFunc:  func(event.TypedDeleteEvent[client.Object]) bool { return false },
			UpdateFunc: func(tue event.TypedUpdateEvent[client.Object]) bool {
				// consider only new incoming object
				sveltosCluster, ok := tue.ObjectNew.(*libsveltosv1beta1.SveltosCluster)
				if !ok {
					return false
				}

				// consider only mgmt sveltos cluster
				if v, ok := sveltosCluster.Labels[kcmv1.K0rdentManagementClusterLabelKey]; !ok || v != kcmv1.K0rdentManagementClusterLabelValue {
					return false
				}

				// consider only those clusters that have auto-renewal
				if sveltosCluster.Spec.TokenRequestRenewalOption == nil {
					return false
				}

				// consider only clusters that have been updated at least once
				if sveltosCluster.Status.LastReconciledTokenRequestAt == "" {
					return false
				}

				// consider only if new status is down (healthy/empty is not the case)
				if sveltosCluster.Status.ConnectionStatus == libsveltosv1beta1.ConnectionHealthy ||
					sveltosCluster.Status.ConnectionStatus == "" {
					return false
				}

				// consider only clusters with failures
				if sveltosCluster.Status.FailureMessage == nil {
					return false
				}

				// consider only auth issues, since we have only strings, then no chance to check properly as error instance
				if !strings.Contains(*sveltosCluster.Status.FailureMessage, "the server has asked for the client to provide credentials") &&
					!strings.Contains(*sveltosCluster.Status.FailureMessage, "Unauthorized") {
					return false
				}

				// the logic below is referenced from
				// https://github.com/projectsveltos/sveltoscluster-manager/blob/31ff86a077fb6cb4d4c426440ef2fe4f2ff578ce/controllers/sveltoscluster_controller.go#L289
				currentTime := time.Now()

				lastRenewal := sveltosCluster.CreationTimestamp
				parsedTime, err := time.Parse(time.RFC3339, sveltosCluster.Status.LastReconciledTokenRequestAt)
				if err == nil { // if NO error
					lastRenewal = metav1.Time{Time: parsedTime}
				}

				renewalInterval := sveltosCluster.Spec.TokenRequestRenewalOption.RenewTokenRequestInterval.Seconds()
				const tenMinutes = 10 * 60
				var renewalThreshold time.Duration
				if renewalInterval > tenMinutes {
					renewalThreshold = time.Duration(renewalInterval-tenMinutes) * time.Second
				} else {
					renewalThreshold = time.Duration(renewalInterval) * time.Second
				}

				elapsed := currentTime.Sub(lastRenewal.Time)
				return elapsed.Seconds() > renewalThreshold.Seconds()
			},
		}).
		Complete(r)
}
