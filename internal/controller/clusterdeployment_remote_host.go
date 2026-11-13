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
	"maps"
	"slices"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
)

// k0smotron identifiers, copied from github.com/k0sproject/k0smotron to avoid the dependency.
const (
	// k0smotronExternalOwnerSuffix names the ConfigMap k0smotron creates on a remote host
	// cluster as the owner of everything it puts there (util.getExternalOwnerName), and deletes
	// with the cluster.
	k0smotronExternalOwnerSuffix = "-root-owner"
	// k0smotronDefaultKubeconfigKey is the default of spec.remoteHostCluster.kubeconfigRef.key.
	k0smotronDefaultKubeconfigKey = "value"
)

var k0smotronControlPlaneGVK = schema.GroupVersionKind{
	Group:   "controlplane.cluster.x-k8s.io",
	Version: "v1beta2",
	Kind:    "K0smotronControlPlane",
}

// remoteHostCopyLabel marks a Secret or ConfigMap that KCM copied onto the remote host cluster of
// a hosted control plane. Objects without it are never changed or deleted there.
const remoteHostCopyLabel = "k0rdent.mirantis.com/remote-host-copy"

var errRemoteHostObjectNotManaged = errors.New("object exists on the remote host cluster and was not created by KCM")

// k0smotronControlPlaneSpec is the subset of the K0smotronControlPlane v1beta2 spec
// (k0smotron.io/v1beta2 ClusterSpec) that names objects the control plane pods read.
type k0smotronControlPlaneSpec struct {
	RemoteHostCluster *k0smotronRemoteHostCluster `json:"remoteHostCluster,omitempty"`
	Storage           k0smotronStorage            `json:"storage"`
	Mounts            []k0smotronMount            `json:"mounts,omitempty"`
}

type k0smotronRemoteHostCluster struct {
	KubeconfigRef *k0smotronKubeconfigRef `json:"kubeconfigRef,omitempty"`
}

type k0smotronKubeconfigRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Key       string `json:"key,omitempty"`
}

type k0smotronStorage struct {
	Kine k0smotronKine `json:"kine"`
}

type k0smotronKine struct {
	DataSourceSecretName string `json:"dataSourceSecretName,omitempty"`
}

type k0smotronMount struct {
	corev1.VolumeSource `json:",inline"`
}

// referencedNames returns the sorted names of the Secrets and ConfigMaps the control plane pods
// mount or read from their own namespace.
func (s *k0smotronControlPlaneSpec) referencedNames() (secrets, configMaps []string) {
	secretSet, configMapSet := make(map[string]struct{}), make(map[string]struct{})
	add := func(set map[string]struct{}, name string) {
		if name != "" {
			set[name] = struct{}{}
		}
	}

	for _, m := range s.Mounts {
		if m.Secret != nil {
			add(secretSet, m.Secret.SecretName)
		}
		if m.ConfigMap != nil {
			add(configMapSet, m.ConfigMap.Name)
		}
		if m.Projected != nil {
			for _, src := range m.Projected.Sources {
				if src.Secret != nil {
					add(secretSet, src.Secret.Name)
				}
				if src.ConfigMap != nil {
					add(configMapSet, src.ConfigMap.Name)
				}
			}
		}
	}
	add(secretSet, s.Storage.Kine.DataSourceSecretName)

	return slices.Sorted(maps.Keys(secretSet)), slices.Sorted(maps.Keys(configMapSet))
}

// getK0smotronControlPlane returns cd's K0smotronControlPlane and its decoded spec. It returns a
// nil object when cd has no CAPI Cluster, the Cluster's control plane is not a
// K0smotronControlPlane, or k0smotron v1beta2 is not served. requeue reports a Cluster naming a
// K0smotronControlPlane that does not exist yet.
func getK0smotronControlPlane(ctx context.Context, cl client.Client, cd *kcmv1.ClusterDeployment) (_ *unstructured.Unstructured, _ *k0smotronControlPlaneSpec, requeue bool, _ error) {
	cluster := new(clusterapiv1.Cluster)
	if err := cl.Get(ctx, client.ObjectKeyFromObject(cd), cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil, false, nil
		}
		return nil, nil, false, fmt.Errorf("failed to get CAPI Cluster %s: %w", client.ObjectKeyFromObject(cd), err)
	}

	ref := cluster.Spec.ControlPlaneRef
	if ref.APIGroup != k0smotronControlPlaneGVK.Group || ref.Kind != k0smotronControlPlaneGVK.Kind || ref.Name == "" {
		return nil, nil, false, nil
	}

	kcp := new(unstructured.Unstructured)
	kcp.SetGroupVersionKind(k0smotronControlPlaneGVK)
	kcpKey := client.ObjectKey{Namespace: cluster.Namespace, Name: ref.Name}
	if err := cl.Get(ctx, kcpKey, kcp); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil, true, nil
		}
		if apimeta.IsNoMatchError(err) {
			ctrl.LoggerFrom(ctx).V(1).Info("K0smotronControlPlane v1beta2 is not served, skipping remote host sync", "controlPlane", kcpKey)
			return nil, nil, false, nil
		}
		return nil, nil, false, fmt.Errorf("failed to get K0smotronControlPlane %s: %w", kcpKey, err)
	}

	spec := new(k0smotronControlPlaneSpec)
	if rawSpec, ok := kcp.Object["spec"].(map[string]any); ok {
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(rawSpec, spec); err != nil {
			return nil, nil, false, fmt.Errorf("failed to decode the spec of K0smotronControlPlane %s: %w", kcpKey, err)
		}
	}

	return kcp, spec, false, nil
}

// syncRemoteHostObjects copies the Secrets and ConfigMaps a K0smotronControlPlane's pods reference
// onto its remote host cluster (spec.remoteHostCluster). k0smotron runs the pods there but looks
// the referenced objects up by name in their own namespace on that cluster, while KCM creates
// them only on the cluster that holds the ClusterDeployment.
//
// Every referenced object that exists in the control plane's namespace on the source cluster is
// created or updated on the host. A referenced name absent from the source is assumed to be
// provided on the host and is left alone, and so is a host object without remoteHostCopyLabel.
// Each copy carries an owner reference to k0smotron's external owner ConfigMap of every
// ClusterDeployment using it, so it is garbage-collected once the last such cluster is deleted.
// Copies this cluster no longer references lose its owner reference, and are deleted when no
// other cluster owns them.
func (r *ClusterDeploymentReconciler) syncRemoteHostObjects(ctx context.Context, scope *clusterScope) (ctrl.Result, error) {
	cd := scope.cd
	l := ctrl.LoggerFrom(ctx).WithName("remote-host-sync")

	kcp, spec, requeue, err := getK0smotronControlPlane(ctx, scope.rgnClient, cd)
	if err != nil {
		return ctrl.Result{}, err
	}
	if requeue {
		return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, nil
	}
	if kcp == nil || spec.RemoteHostCluster == nil || spec.RemoteHostCluster.KubeconfigRef == nil || spec.RemoteHostCluster.KubeconfigRef.Name == "" {
		return ctrl.Result{}, nil
	}

	ref := spec.RemoteHostCluster.KubeconfigRef
	kubeconfigKey := client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}
	if kubeconfigKey.Namespace == "" {
		kubeconfigKey.Namespace = kcp.GetNamespace()
	}
	dataKey := ref.Key
	if dataKey == "" {
		dataKey = k0smotronDefaultKubeconfigKey
	}

	hostClient, err := kubeutil.GetChildClient(ctx, scope.rgnClient, kubeconfigKey, dataKey, scope.rgnClient.Scheme(), r.childClientFactory)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get a client for the remote host cluster of K0smotronControlPlane %s: %w", client.ObjectKeyFromObject(kcp), err)
	}

	namespace := kcp.GetNamespace()
	owner := new(corev1.ConfigMap)
	ownerKey := client.ObjectKey{Namespace: namespace, Name: cd.Name + k0smotronExternalOwnerSuffix}
	if err := hostClient.Get(ctx, ownerKey, owner); err != nil {
		if apierrors.IsNotFound(err) {
			l.V(1).Info("Waiting for k0smotron to create its external owner on the remote host cluster", "owner", ownerKey)
			return ctrl.Result{RequeueAfter: r.defaultRequeueTime}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get ConfigMap %s on the remote host cluster: %w", ownerKey, err)
	}

	secrets, configMaps := spec.referencedNames()
	copier := remoteHostCopier{source: scope.rgnClient, host: hostClient, namespace: namespace, owner: owner}

	var errs []error
	for _, name := range secrets {
		op, err := copier.copySecret(ctx, name)
		errs = append(errs, r.reportRemoteCopy(cd, "Secret", namespace, name, op, err))
	}
	for _, name := range configMaps {
		op, err := copier.copyConfigMap(ctx, name)
		errs = append(errs, r.reportRemoteCopy(cd, "ConfigMap", namespace, name, op, err))
	}
	errs = append(errs, copier.prune(ctx, secrets, configMaps))

	return ctrl.Result{}, errors.Join(errs...)
}

// reportRemoteCopy emits the event for one copy's outcome and returns the error to propagate.
func (r *ClusterDeploymentReconciler) reportRemoteCopy(cd *kcmv1.ClusterDeployment, kind, namespace, name string, op controllerutil.OperationResult, err error) error {
	switch {
	case errors.Is(err, errRemoteHostObjectNotManaged):
		r.warnf(cd, "RemoteHostObjectConflict", "%s %s/%s already exists on the remote host cluster and is not managed by KCM, leaving it unchanged", kind, namespace, name)
		return nil
	case err != nil:
		return fmt.Errorf("failed to copy %s %s/%s to the remote host cluster: %w", kind, namespace, name, err)
	case op == controllerutil.OperationResultCreated || op == controllerutil.OperationResultUpdated:
		r.eventf(cd, "RemoteHostObjectSynced", "Successfully %s %s %s/%s on the remote host cluster", op, kind, namespace, name)
	}
	return nil
}

// remoteHostCopier copies objects from the control plane's namespace on the source cluster to the
// same namespace on the remote host cluster.
type remoteHostCopier struct {
	source    client.Client
	host      client.Client
	owner     *corev1.ConfigMap
	namespace string
}

func (c *remoteHostCopier) copySecret(ctx context.Context, name string) (controllerutil.OperationResult, error) {
	src := new(corev1.Secret)
	if err := c.source.Get(ctx, client.ObjectKey{Namespace: c.namespace, Name: name}, src); err != nil {
		return controllerutil.OperationResultNone, client.IgnoreNotFound(err)
	}

	dst := &corev1.Secret{Name: name, Namespace: c.namespace}
	return c.createOrUpdate(ctx, dst, func() {
		if dst.ResourceVersion == "" { // the type is immutable
			dst.Type = src.Type
		}
		dst.Data = src.Data
	})
}

func (c *remoteHostCopier) copyConfigMap(ctx context.Context, name string) (controllerutil.OperationResult, error) {
	src := new(corev1.ConfigMap)
	if err := c.source.Get(ctx, client.ObjectKey{Namespace: c.namespace, Name: name}, src); err != nil {
		return controllerutil.OperationResultNone, client.IgnoreNotFound(err)
	}

	dst := &corev1.ConfigMap{Name: name, Namespace: c.namespace}
	return c.createOrUpdate(ctx, dst, func() {
		dst.Data = src.Data
		dst.BinaryData = src.BinaryData
	})
}

func (c *remoteHostCopier) createOrUpdate(ctx context.Context, dst client.Object, copyData func()) (controllerutil.OperationResult, error) {
	return controllerutil.CreateOrUpdate(ctx, c.host, dst, func() error {
		if dst.GetResourceVersion() != "" && dst.GetLabels()[remoteHostCopyLabel] != kcmv1.KCMManagedLabelValue {
			return errRemoteHostObjectNotManaged
		}

		labels := dst.GetLabels()
		if labels == nil {
			labels = make(map[string]string, 1)
		}
		labels[remoteHostCopyLabel] = kcmv1.KCMManagedLabelValue
		dst.SetLabels(labels)

		copyData()

		return controllerutil.SetOwnerReference(c.owner, dst, c.host.Scheme())
	})
}

// prune drops this cluster's owner reference from the copies it no longer references, and deletes
// those no other cluster owns.
func (c *remoteHostCopier) prune(ctx context.Context, secrets, configMaps []string) error {
	selector := []client.ListOption{client.InNamespace(c.namespace), client.MatchingLabels{remoteHostCopyLabel: kcmv1.KCMManagedLabelValue}}

	secretList := new(corev1.SecretList)
	if err := c.host.List(ctx, secretList, selector...); err != nil {
		return fmt.Errorf("failed to list Secrets on the remote host cluster: %w", err)
	}
	configMapList := new(corev1.ConfigMapList)
	if err := c.host.List(ctx, configMapList, selector...); err != nil {
		return fmt.Errorf("failed to list ConfigMaps on the remote host cluster: %w", err)
	}

	var stale []client.Object
	for i := range secretList.Items {
		if !slices.Contains(secrets, secretList.Items[i].Name) {
			stale = append(stale, &secretList.Items[i])
		}
	}
	for i := range configMapList.Items {
		if !slices.Contains(configMaps, configMapList.Items[i].Name) {
			stale = append(stale, &configMapList.Items[i])
		}
	}

	var errs []error
	for _, obj := range stale {
		owned, err := controllerutil.HasOwnerReference(obj.GetOwnerReferences(), c.owner, c.host.Scheme())
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !owned {
			continue
		}

		if len(obj.GetOwnerReferences()) == 1 {
			if err := c.host.Delete(ctx, obj); client.IgnoreNotFound(err) != nil {
				errs = append(errs, fmt.Errorf("failed to delete %s/%s on the remote host cluster: %w", obj.GetNamespace(), obj.GetName(), err))
			}
			continue
		}

		if err := controllerutil.RemoveOwnerReference(c.owner, obj, c.host.Scheme()); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := c.host.Update(ctx, obj); client.IgnoreNotFound(err) != nil {
			errs = append(errs, fmt.Errorf("failed to update %s/%s on the remote host cluster: %w", obj.GetNamespace(), obj.GetName(), err))
		}
	}

	return errors.Join(errs...)
}
