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
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	v1beta12 "github.com/k0sproject/k0smotron/api/infrastructure/v1beta1"
	sveltosv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	"github.com/projectsveltos/libsveltos/lib/clusterproxy"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/kubeconfig"
	"sigs.k8s.io/cluster-api/util/secret"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	hmc "github.com/Mirantis/hmc/api/v1alpha1"
	"github.com/Mirantis/hmc/internal/sveltos"
)

// UnmanagedClusterReconciler reconciles a UnmanagedCluster object
type UnmanagedClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=hmc.mirantis.com.hmc.mirantis.com,resources=unmanagedclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=hmc.mirantis.com.hmc.mirantis.com,resources=unmanagedclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=hmc.mirantis.com.hmc.mirantis.com,resources=unmanagedclusters/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the UnmanagedCluster object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *UnmanagedClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling UnmanagedCluster")

	if err := v1beta12.AddToScheme(r.Client.Scheme()); err != nil {
		return ctrl.Result{}, err
	}

	if err := v1beta1.AddToScheme(r.Client.Scheme()); err != nil {
		return ctrl.Result{}, err
	}

	unmanagedCluster := new(hmc.UnmanagedCluster)
	if err := r.Get(ctx, req.NamespacedName, unmanagedCluster); err != nil {
		if apierrors.IsNotFound(err) {
			l.Info("UnmanagedCluster not found, ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		l.Error(err, "Failed to get UnmanagedCluster")
		return ctrl.Result{}, err
	}

	if controllerutil.AddFinalizer(unmanagedCluster, hmc.UnmanagedClusterFinalizer) {
		if err := r.Client.Update(ctx, unmanagedCluster); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update UnmanagedCluster %s with finalizer %s: %w", unmanagedCluster.Name, hmc.UnmanagedClusterFinalizer, err)
		}
	}
	return r.reconcileUnmanagedCluster(ctx, unmanagedCluster)
}

// SetupWithManager sets up the controller with the Manager.
func (r *UnmanagedClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hmc.UnmanagedCluster{}).
		Complete(r)
}

func (r *UnmanagedClusterReconciler) getControlPlaneEndpoint(ctx context.Context, unmanagedCluster *hmc.UnmanagedCluster) (v1beta1.APIEndpoint, error) {
	bytes, err := kubeconfig.FromSecret(ctx, r.Client, client.ObjectKey{
		Namespace: unmanagedCluster.Namespace,
		Name:      unmanagedCluster.Name,
	})
	if err != nil {
		return v1beta1.APIEndpoint{}, fmt.Errorf("failed to get cluster kubeconfig secret: %w", err)
	}

	config, err := clientcmd.RESTConfigFromKubeConfig(bytes)
	if err != nil {
		return v1beta1.APIEndpoint{}, fmt.Errorf("failed to get rest config from kube config secret: %w", err)
	}

	hostURL, err := url.Parse(config.Host)
	if err != nil {
		return v1beta1.APIEndpoint{}, fmt.Errorf("kube config secret contains invalid host: %w", err)
	}

	portNumber, err := strconv.Atoi(hostURL.Port())
	if err != nil {
		return v1beta1.APIEndpoint{}, fmt.Errorf("kube config secret contains invalid port: %w", err)
	}
	return v1beta1.APIEndpoint{Host: hostURL.Hostname(), Port: int32(portNumber)}, nil
}

func (r *UnmanagedClusterReconciler) createCluster(ctx context.Context, unmanagedCluster *hmc.UnmanagedCluster) error {
	controlPlaneEndPoint, err := r.getControlPlaneEndpoint(ctx, unmanagedCluster)
	if err != nil {
		return fmt.Errorf("failed to get control plane endpoint: %w", err)
	}

	clusterObject := &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      unmanagedCluster.Name,
			Namespace: unmanagedCluster.Namespace,
			Labels: map[string]string{
				"helm.toolkit.fluxcd.io/name":      unmanagedCluster.Name,
				"helm.toolkit.fluxcd.io/namespace": unmanagedCluster.Namespace,
			},
		},
		Spec: v1beta1.ClusterSpec{
			ControlPlaneEndpoint: controlPlaneEndPoint,
			InfrastructureRef: &corev1.ObjectReference{
				Kind:       "UnmanagedCluster",
				Namespace:  unmanagedCluster.Namespace,
				Name:       unmanagedCluster.Name,
				APIVersion: unmanagedCluster.APIVersion,
			},
		},
	}
	clusterObject.Status.SetTypedPhase(v1beta1.ClusterPhaseUnknown)
	err = r.Client.Create(ctx, clusterObject)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create unmanagedCluster object %s/%s: %s", unmanagedCluster.Namespace, unmanagedCluster.Name, err)
	}

	return nil
}

func (r *UnmanagedClusterReconciler) createMachines(ctx context.Context, unmanagedCluster *hmc.UnmanagedCluster) error {
	l := ctrl.LoggerFrom(ctx)

	nodelist, err := r.getNodeList(ctx, unmanagedCluster)
	if err != nil {
		return err
	}

	kubeConfigSecretName := secret.Name(unmanagedCluster.Name, secret.Kubeconfig)

	// find any existing unmanaged machines for the cluster to see if any need to be cleaned up because
	// the underlying node was removed
	existingMachines := &hmc.UnmanagedMachineList{}
	if err := r.List(ctx, existingMachines, &client.ListOptions{
		Namespace:     unmanagedCluster.Namespace,
		LabelSelector: labels.SelectorFromSet(map[string]string{v1beta1.ClusterNameLabel: unmanagedCluster.Name}),
	}); err != nil {
		return fmt.Errorf("failed to list existing unmanaged machines: %w", err)
	}

	existingMachinesByName := map[string]*hmc.UnmanagedMachine{}
	for _, existingMachine := range existingMachines.Items {
		existingMachinesByName[existingMachine.GetName()] = &existingMachine
	}

	for _, node := range nodelist.Items {
		delete(existingMachinesByName, node.Name)

		unmanagedMachine := hmc.UnmanagedMachine{
			TypeMeta: metav1.TypeMeta{
				Kind:       "UnmanagedMachine",
				APIVersion: hmc.GroupVersion.Identifier(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      node.Name,
				Namespace: unmanagedCluster.Namespace,
				Labels: map[string]string{
					v1beta1.ClusterNameLabel: unmanagedCluster.Name,
				},
			},
			Spec: hmc.UnmanagedMachineSpec{
				ProviderID:  node.Spec.ProviderID,
				ClusterName: unmanagedCluster.Name,
			},
			Status: hmc.UnmanagedMachineStatus{
				Ready: true,
			},
		}

		err := r.Create(ctx, &unmanagedMachine)
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create machine: %w", err)
		}

		ref := types.NamespacedName{Name: unmanagedMachine.Name, Namespace: unmanagedMachine.Namespace}
		if err := r.Get(ctx, ref, &unmanagedMachine); err != nil {
			return fmt.Errorf("failed to get unmanaged machine: %w", err)
		}
		unmanagedMachine.Status = hmc.UnmanagedMachineStatus{
			Ready: true,
		}
		if err := r.Status().Update(ctx, &unmanagedMachine); err != nil {
			return fmt.Errorf("failed to update unmanaged machine status: %w", err)
		}

		l.Info("Create machine", "node", node.Name)
		machine := v1beta1.Machine{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Machine",
				APIVersion: v1beta1.GroupVersion.Identifier(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      node.Name,
				Namespace: unmanagedCluster.Namespace,
				Labels:    map[string]string{v1beta1.GroupVersion.Identifier(): hmc.GroupVersion.Version, v1beta1.ClusterNameLabel: unmanagedCluster.Name},
			},
			Spec: v1beta1.MachineSpec{
				ClusterName: unmanagedCluster.Name,
				Bootstrap: v1beta1.Bootstrap{
					DataSecretName: &kubeConfigSecretName,
				},
				InfrastructureRef: corev1.ObjectReference{
					Kind:       "UnmanagedMachine",
					Namespace:  unmanagedCluster.Namespace,
					Name:       node.Name,
					APIVersion: hmc.GroupVersion.Identifier(),
				},
				ProviderID: &node.Spec.ProviderID,
			},
			Status: v1beta1.MachineStatus{
				NodeRef: &corev1.ObjectReference{
					Kind:       "Node",
					Name:       node.Name,
					APIVersion: "v1",
				},
				NodeInfo:               &corev1.NodeSystemInfo{},
				CertificatesExpiryDate: nil,
				BootstrapReady:         true,
				InfrastructureReady:    true,
			},
		}

		if _, ok := node.Labels[v1beta1.NodeRoleLabelPrefix+"/control-plane"]; ok {
			if machine.Labels == nil {
				machine.Labels = make(map[string]string)
			}
			machine.Labels[v1beta1.MachineControlPlaneLabel] = "true"
		}
		err = r.Create(ctx, &machine)
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create machine: %w", err)
		}
	}

	// cleanup any orphaned unmanaged machines and capi machines
	for _, existingUnmanagedMachine := range existingMachinesByName {
		if err := r.Delete(ctx, existingUnmanagedMachine); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete orphaned unmanaged machine: %w", err)
		}

		if err := r.Delete(ctx, &v1beta1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      existingUnmanagedMachine.Name,
				Namespace: unmanagedCluster.Namespace,
			},
		}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete orphaned machine: %w", err)
		}
	}
	return nil
}

func (r *UnmanagedClusterReconciler) getNodeList(ctx context.Context, unmanagedCluster *hmc.UnmanagedCluster) (*corev1.NodeList, error) {
	l := ctrl.LoggerFrom(ctx)
	clusterClient, err := clusterproxy.GetCAPIKubernetesClient(ctx, l, r.Client, r.Client.Scheme(), unmanagedCluster.Namespace, unmanagedCluster.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to remote cluster: %w", err)
	}

	nodelist := &corev1.NodeList{}
	if err := clusterClient.List(ctx, nodelist); err != nil {
		return nil, fmt.Errorf("failed to list cluster nodes: %w", err)
	}
	return nodelist, nil
}

func (r *UnmanagedClusterReconciler) reconcileUnmanagedCluster(ctx context.Context, unmanagedCluster *hmc.UnmanagedCluster) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	if !unmanagedCluster.DeletionTimestamp.IsZero() {
		l.Info("Deleting UnmanagedCluster")
		return r.reconcileDeletion(ctx, unmanagedCluster)
	}

	if err := r.createCluster(ctx, unmanagedCluster); err != nil {
		return ctrl.Result{RequeueAfter: DefaultRequeueInterval}, err
	}

	if err := r.createServices(ctx, unmanagedCluster); err != nil {
		return ctrl.Result{RequeueAfter: DefaultRequeueInterval}, err
	}

	if err := r.createMachines(ctx, unmanagedCluster); err != nil {
		return ctrl.Result{RequeueAfter: DefaultRequeueInterval}, err
	}

	requeue, err := r.updateStatus(ctx, unmanagedCluster)
	if err != nil {
		if requeue {
			return ctrl.Result{RequeueAfter: DefaultRequeueInterval}, err
		}
		return ctrl.Result{RequeueAfter: DefaultRequeueInterval}, err
	}

	if requeue {
		return ctrl.Result{RequeueAfter: DefaultRequeueInterval}, nil
	}
	return ctrl.Result{}, nil
}

func (r *UnmanagedClusterReconciler) createServices(ctx context.Context, mc *hmc.UnmanagedCluster) error {
	opts, err := helmChartOpts(ctx, r.Client, mc.Namespace, mc.Spec.Services)
	if err != nil {
		return err
	}

	if _, err := sveltos.ReconcileProfile(ctx, r.Client, mc.Namespace, mc.Name,
		sveltos.ReconcileProfileOpts{
			OwnerReference: &metav1.OwnerReference{
				APIVersion: hmc.GroupVersion.String(),
				Kind:       hmc.UnmanagedClusterKind,
				Name:       mc.Name,
				UID:        mc.UID,
			},
			LabelSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					hmc.FluxHelmChartNamespaceKey: mc.Namespace,
					hmc.FluxHelmChartNameKey:      mc.Name,
				},
			},
			HelmChartOpts:  opts,
			Priority:       mc.Spec.ServicesPriority,
			StopOnConflict: mc.Spec.StopOnConflict,
		}); err != nil {
		return fmt.Errorf("failed to reconcile Profile: %w", err)
	}

	return nil
}

func (r *UnmanagedClusterReconciler) reconcileDeletion(ctx context.Context, unmanagedCluster *hmc.UnmanagedCluster) (ctrl.Result, error) {
	clusterLabel := map[string]string{v1beta1.ClusterNameLabel: unmanagedCluster.Name}
	deleteAllOpts := []client.DeleteAllOfOption{
		client.InNamespace(unmanagedCluster.Namespace),
		client.MatchingLabels(clusterLabel),
	}

	if err := r.DeleteAllOf(
		ctx,
		&hmc.UnmanagedMachine{},
		deleteAllOpts...,
	); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{RequeueAfter: DefaultRequeueInterval}, fmt.Errorf("failed to delete unmanaged machines: %w", err)
	}

	if err := r.DeleteAllOf(
		ctx,
		&v1beta1.Machine{},
		deleteAllOpts...,
	); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{RequeueAfter: DefaultRequeueInterval}, fmt.Errorf("failed to delete unmanaged machines: %w", err)
	}

	if err := r.Delete(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: unmanagedCluster.Namespace,
			Name:      secret.Name(unmanagedCluster.Name, secret.Kubeconfig),
			Labels: map[string]string{
				v1beta1.ClusterNameLabel: unmanagedCluster.Name,
			},
		},
	}); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{RequeueAfter: DefaultRequeueInterval}, fmt.Errorf("failed to delete cluster secret: %w", err)
	}

	if err := r.Delete(ctx, &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: unmanagedCluster.Namespace,
			Name:      unmanagedCluster.Name,
			Labels: map[string]string{
				v1beta1.ClusterNameLabel: unmanagedCluster.Name,
			},
		},
	}); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{RequeueAfter: DefaultRequeueInterval}, fmt.Errorf("failed to delete cluster: %w", err)
	}

	if controllerutil.RemoveFinalizer(unmanagedCluster, hmc.UnmanagedClusterFinalizer) {
		if err := r.Client.Update(ctx, unmanagedCluster); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to remove finalizer %s from UnmanagedCluster %s: %w",
				hmc.UnmanagedClusterFinalizer, unmanagedCluster.Name, err)
		}
	}
	return ctrl.Result{}, nil
}

func (r *UnmanagedClusterReconciler) updateStatus(ctx context.Context, cluster *hmc.UnmanagedCluster) (bool, error) {
	requeue := false
	nodelist, err := r.getNodeList(ctx, cluster)
	if err != nil {
		return true, err
	}

	allNodeCondition := metav1.Condition{
		Type:    hmc.AllNodesCondition,
		Status:  "True",
		Message: "All nodes are ready",
		Reason:  hmc.SucceededReason,
	}

	cluster.Status.Ready = true
	var nonReadyNodes []string
	for _, node := range nodelist.Items {
		for _, nodeCondition := range node.Status.Conditions {
			if nodeCondition.Type == corev1.NodeReady {
				if nodeCondition.Status != corev1.ConditionTrue {
					allNodeCondition.Status = metav1.ConditionFalse
					allNodeCondition.Reason = hmc.FailedReason
					nonReadyNodes = append(nonReadyNodes, node.Name)
					requeue = true
					cluster.Status.Ready = false
				}
			}
		}
	}

	if len(nonReadyNodes) > 0 {
		allNodeCondition.Message = fmt.Sprintf("Nodes %s are not ready", strings.Join(nonReadyNodes, ","))
	}
	apimeta.SetStatusCondition(cluster.GetConditions(), allNodeCondition)

	if len(cluster.Spec.Services) > 0 {
		sveltosClusterSummaries := &sveltosv1beta1.ClusterSummaryList{}
		if err := r.List(ctx, sveltosClusterSummaries, &client.ListOptions{
			Namespace:     cluster.Namespace,
			LabelSelector: labels.SelectorFromSet(map[string]string{sveltosv1beta1.ClusterNameLabel: cluster.Name}),
		}); err != nil {
			return true, fmt.Errorf("failed to list sveltos cluster summary: %w", err)
		}

		if len(sveltosClusterSummaries.Items) > 0 {
			var failedCharts []string

			helmCondition := metav1.Condition{
				Type:   hmc.HelmChart,
				Reason: hmc.SucceededReason,
				Status: metav1.ConditionTrue,
			}

			for _, clusterSummary := range sveltosClusterSummaries.Items {
				for _, helmReleaseSummary := range clusterSummary.Status.HelmReleaseSummaries {
					if helmReleaseSummary.Status != sveltosv1beta1.HelmChartStatusManaging {
						helmCondition.Reason = hmc.FailedReason
						helmCondition.Status = metav1.ConditionFalse
						requeue = true
						failedCharts = append(failedCharts, helmReleaseSummary.ReleaseName)
					}
				}
			}

			if len(failedCharts) > 0 {
				helmCondition.Message = "Charts failed to deploy " + strings.Join(failedCharts, ",")
			}
			apimeta.SetStatusCondition(cluster.GetConditions(), helmCondition)
		} else {
			requeue = true
		}
	}

	if err := r.Status().Update(ctx, cluster); err != nil {
		return true, fmt.Errorf("failed to update unmanaged cluster status: %w", err)
	}

	return requeue, nil
}