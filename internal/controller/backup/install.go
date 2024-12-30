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

package backup

import (
	"context"
	"fmt"
	"io"
	"slices"
	"time"

	velerov1api "github.com/zerospiel/velero/pkg/apis/velero/v1"
	velerobuilder "github.com/zerospiel/velero/pkg/builder"
	veleroclient "github.com/zerospiel/velero/pkg/client"
	veleroinstall "github.com/zerospiel/velero/pkg/install"
	"github.com/zerospiel/velero/pkg/uploader"
	"github.com/zerospiel/velero/pkg/util/kube"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kcmv1alpha1 "github.com/K0rdent/kcm/api/v1alpha1"
)

// ReconcileVeleroInstallation reconciles installation of velero stack within a management cluster.
func (c *Config) ReconcileVeleroInstallation(ctx context.Context, mgmt *kcmv1alpha1.Management) (ctrl.Result, error) {
	requeueResult := ctrl.Result{Requeue: true, RequeueAfter: c.installRequeueAfter}

	veleroDeploy, err := c.fetchVeleroDeploy(ctx)
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed to get velero deploy: %w", err)
	}

	if apierrors.IsNotFound(err) {
		if err := c.installVelero(ctx); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to perform velero stack installation: %w", err)
		}

		return requeueResult, nil
	}

	originalDeploy := veleroDeploy.DeepCopy()

	installedProperly, err := c.isDeployProperlyInstalled(ctx, veleroDeploy)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check if velero deploy is properly installed: %w", err)
	}

	if !installedProperly {
		if err := c.installVelero(ctx); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to perform velero stack installation: %w", err)
		}

		return requeueResult, nil
	}

	isPatchRequired, err := c.normalizeDeploy(ctx, veleroDeploy, mgmt)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check if velero deploy required patch: %w", err)
	}

	l := ctrl.LoggerFrom(ctx)
	if isPatchRequired {
		l.Info("Patching the deployment")
		if err := c.cl.Patch(ctx, veleroDeploy, client.MergeFrom(originalDeploy)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to patch velero deploy: %w", err)
		}

		l.Info("Successfully patched the deploy")
	}

	if !isDeploymentReady(veleroDeploy) {
		l.Info("Deployment is not ready yet, will requeue")
		return requeueResult, nil
	}

	l.Info("Deployment is in the expected state")
	return ctrl.Result{}, nil
}

// InstallVeleroCRDs install all Velero CRDs.
func (c *Config) InstallVeleroCRDs(cl client.Client) error {
	dc, err := dynamic.NewForConfig(c.kubeRestConfig)
	if err != nil {
		return fmt.Errorf("failed to construct dynamic client: %w", err)
	}

	return veleroinstall.Install(veleroclient.NewDynamicFactory(dc), cl, veleroinstall.AllCRDs(), io.Discard)
}

// installVelero installs velero stack with all the required components.
func (c *Config) installVelero(ctx context.Context) error {
	ctrl.LoggerFrom(ctx).Info("Installing velero stack")

	if err := c.ensureVeleroRBAC(ctx); err != nil {
		return fmt.Errorf("failed to ensure velero RBAC: %w", err)
	}

	options := &veleroinstall.VeleroOptions{
		Namespace: c.systemNamespace,
		Image:     c.image,
		Features:  c.features,
		Plugins:   c.pluginImages,

		ServiceAccountName:      c.getServiceAccountName(),
		NoDefaultBackupLocation: true, // no need (explicit BSL)

		DefaultRepoMaintenanceFrequency: time.Hour,          // default
		GarbageCollectionFrequency:      time.Hour,          // default
		PodVolumeOperationTimeout:       4 * time.Hour,      // default
		UploaderType:                    uploader.KopiaType, // the only supported

		// TODO: skip null params?
		ProviderName: "", // no need, provided through the explicit BSL object
		Bucket:       "", // no need, provided through the explicit BSL object

		Prefix:                    "",  // no need when out-of-tree
		PodAnnotations:            nil, // no need, default comes from velero
		PodLabels:                 nil, // no need, default comes from velero
		ServiceAccountAnnotations: nil, // customizable through the config?

		VeleroPodResources:    corev1.ResourceRequirements{}, // unbounded
		NodeAgentPodResources: corev1.ResourceRequirements{}, // not used
		PodResources:          kube.PodResources{},           // maintenance job resources, unlimited ok

		SecretData:                  nil,   // no need, provided through the explicit BSL object
		UseNodeAgent:                false, // no need
		RestoreOnly:                 false, // no need
		PrivilegedNodeAgent:         false, // no need
		UseVolumeSnapshots:          false, // no need
		BSLConfig:                   nil,   // backupstoragelocation
		VSLConfig:                   nil,   // volumesnapshotlocation
		CACertData:                  nil,   // no need (explicit BSL)
		DefaultVolumesToFsBackup:    false, // no volume backups, no need
		DefaultSnapshotMoveData:     false, // no snapshots, no need
		DisableInformerCache:        false, // dangerous
		ScheduleSkipImmediately:     false, // might be useful, but easy to customize directly through the deploy
		KeepLatestMaintenanceJobs:   0,     // optional
		BackupRepoConfigMap:         "",    // no need, backup config through a CM
		RepoMaintenanceJobConfigMap: "",    // no need, main job config through a CM
		NodeAgentConfigMap:          "",    // no need, node-agent config through a CM
	}

	resources := veleroinstall.AllResources(options)

	dc, err := dynamic.NewForConfig(c.kubeRestConfig)
	if err != nil {
		return fmt.Errorf("failed to construct dynamic client: %w", err)
	}

	return veleroinstall.Install(veleroclient.NewDynamicFactory(dc), c.cl, resources, io.Discard)
}

func (c *Config) installCustomPlugins(ctx context.Context, veleroDeploy *appsv1.Deployment, mgmt *kcmv1alpha1.Management) (isPatchRequired bool, _ error) {
	if mgmt == nil || len(mgmt.Spec.Backup.CustomPlugins) == 0 {
		return false, nil
	}

	l := ctrl.LoggerFrom(ctx)

	bsls := new(velerov1api.BackupStorageLocationList)
	if err := c.cl.List(ctx, bsls, client.InNamespace(c.systemNamespace)); err != nil {
		return false, fmt.Errorf("failed to list velero backup storage locations: %w", err)
	}

	// NOTE: we do not care about removing the init containers (plugins), it might be managed by the velero CLI directly
	// TODO: process absent containers?
	initContainers := slices.Clone(veleroDeploy.Spec.Template.Spec.InitContainers)
	preLen := len(initContainers)
	for _, bsl := range bsls.Items {
		image, ok := mgmt.Spec.Backup.CustomPlugins[bsl.Spec.Provider]
		if !ok {
			l.Info("Custom plugin is set but no BackupStorageLocation with such provider exists", "provider", bsl.Spec.Provider, "bsl_name", bsl.Name, "velero_namespace", c.systemNamespace)
			continue
		}

		cont := *velerobuilder.ForPluginContainer(image, corev1.PullIfNotPresent).Result()
		if !slices.ContainsFunc(initContainers, hasContainer(cont)) {
			initContainers = append(initContainers, cont)
		}
	}

	postLen := len(initContainers)

	if preLen == postLen { // nothing to do
		return false, nil
	}

	l.Info("Adding new plugins to the Velero deployment", "new_plugins_count", postLen-preLen)
	veleroDeploy.Spec.Template.Spec.InitContainers = initContainers
	return true, nil
}

func (c *Config) normalizeDeploy(ctx context.Context, veleroDeploy *appsv1.Deployment, mgmt *kcmv1alpha1.Management) (bool, error) {
	l := ctrl.LoggerFrom(ctx)

	isPatchRequired, err := c.installCustomPlugins(ctx, veleroDeploy, mgmt)
	if err != nil {
		return false, fmt.Errorf("failed to check if custom plugins are in place: %w", err)
	}

	// process 2 invariants beforehand since velero installation does not manage those if they has been changed
	cont := veleroDeploy.Spec.Template.Spec.Containers[0]
	if cont.Image != c.image {
		l.Info("Deployment container has unexpected image", "current_image", cont.Image, "expected_image", c.image)
		cont.Image = c.image
		veleroDeploy.Spec.Template.Spec.Containers[0] = cont
		isPatchRequired = true
	}

	if veleroDeploy.Spec.Replicas == nil || *veleroDeploy.Spec.Replicas == 0 {
		l.Info("Deployment is scaled to 0, scaling up to 1")
		*veleroDeploy.Spec.Replicas = 1
		isPatchRequired = true
	}

	return isPatchRequired, nil
}

func (c *Config) isDeployProperlyInstalled(ctx context.Context, veleroDeploy *appsv1.Deployment) (bool, error) {
	l := ctrl.LoggerFrom(ctx)

	l.Info("Checking if Velero deployment is properly installed")

	missingPlugins := []string{}
	for _, pluginImage := range c.pluginImages {
		if slices.ContainsFunc(veleroDeploy.Spec.Template.Spec.InitContainers, func(c corev1.Container) bool {
			return pluginImage == c.Image
		}) {
			continue
		}

		missingPlugins = append(missingPlugins, pluginImage)
	}

	if len(veleroDeploy.Spec.Template.Spec.Containers) > 0 &&
		veleroDeploy.Spec.Template.Spec.Containers[0].Name == VeleroName &&
		veleroDeploy.Spec.Template.Spec.ServiceAccountName == c.getServiceAccountName() &&
		len(missingPlugins) == 0 {
		return true, nil
	}

	if len(missingPlugins) > 0 {
		l.Info("There are missing init containers in the velero deployment", "missing_images", missingPlugins)
	} else {
		l.Info("Deployment has unexpected container name or referenced service account, considering to reinstall the deployment again")
	}

	// the deploy is "corrupted", remove only it and then reinstall
	if err := c.cl.Delete(ctx, veleroDeploy); err != nil {
		return false, fmt.Errorf("failed to delete velero deploy: %w", err)
	}

	removalCtx, cancel := context.WithCancel(ctx)
	var checkErr error
	checkFn := func(ctx context.Context) {
		key := client.ObjectKeyFromObject(veleroDeploy)
		ll := l.V(1).WithValues("velero_deploy", key.String())
		ll.Info("Checking if the deployment has been removed")
		if checkErr = c.cl.Get(ctx, key, veleroDeploy); checkErr != nil {
			if apierrors.IsNotFound(checkErr) {
				ll.Info("Removed successfully")
				checkErr = nil
			}
			cancel()
			return
		}
		ll.Info("Not removed yet")
	}

	wait.UntilWithContext(removalCtx, checkFn, time.Millisecond*500)
	if checkErr != nil {
		return false, fmt.Errorf("failed to wait for velero deploy removal: %w", checkErr)
	}

	return false, nil // require install
}

func hasContainer(container corev1.Container) func(c corev1.Container) bool {
	return func(c corev1.Container) bool {
		// if container names, images, or volume mounts (name/mount) differ
		// than consider that the slice does not a given container
		if c.Name != container.Name ||
			c.Image != container.Image ||
			len(c.VolumeMounts) != len(container.VolumeMounts) {
			return false
		}

		for i := range c.VolumeMounts {
			if c.VolumeMounts[i].Name != container.VolumeMounts[i].Name ||
				c.VolumeMounts[i].MountPath != container.VolumeMounts[i].MountPath {
				return false
			}
		}

		return true
	}
}

// ensureVeleroRBAC creates required RBAC objects for velero to be functional
// with the minimal required set of permissions.
// Returns the name of created ServiceAccount referenced by created bindings.
func (c *Config) ensureVeleroRBAC(ctx context.Context) error {
	crbName, clusterRoleName, rbName, roleName := VeleroName, VeleroName, VeleroName, VeleroName
	if c.systemNamespace != VeleroName {
		vns := VeleroName + "-" + c.systemNamespace
		crbName, clusterRoleName = vns+"-clusterrolebinding", vns+"-clusterrole"
		rbName, roleName = vns+"-rolebinding", vns+"-role"
	}
	saName := c.getServiceAccountName()

	systemNS := new(corev1.Namespace)
	if err := c.cl.Get(ctx, client.ObjectKey{Name: c.systemNamespace}, systemNS); apierrors.IsNotFound(err) {
		systemNS.Name = c.systemNamespace
		if err := c.cl.Create(ctx, systemNS); err != nil {
			return fmt.Errorf("failed to create %s namespace for velero: %w", c.systemNamespace, err)
		}
	}

	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: saName, Namespace: c.systemNamespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, c.cl, sa, func() error {
		sa.Labels = veleroinstall.Labels()
		return nil
	}); err != nil {
		return fmt.Errorf("failed to create or update velero service account: %w", err)
	}

	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: roleName, Namespace: c.systemNamespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, c.cl, role, func() error {
		role.Labels = veleroinstall.Labels()
		role.Rules = []rbacv1.PolicyRule{
			{
				APIGroups: []string{velerov1api.SchemeGroupVersion.Group},
				Resources: []string{"*"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{corev1.GroupName},
				Resources: []string{"secrets"},
				Verbs:     []string{"create"},
			},
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to create or update velero role: %w", err)
	}

	roleBinding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: rbName, Namespace: c.systemNamespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, c.cl, roleBinding, func() error {
		roleBinding.Labels = veleroinstall.Labels()
		if roleBinding.ObjectMeta.CreationTimestamp.IsZero() {
			roleBinding.RoleRef = rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "Role",
				Name:     roleName,
			}
		}
		roleBinding.Subjects = []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      saName,
				Namespace: c.systemNamespace,
			},
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to create or update velero role binding: %w", err)
	}

	cr := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: clusterRoleName}}
	if _, err := controllerutil.CreateOrUpdate(ctx, c.cl, cr, func() error {
		cr.Labels = veleroinstall.Labels()
		cr.Rules = []rbacv1.PolicyRule{
			{
				APIGroups: []string{"*"},
				Resources: []string{"*"},
				Verbs:     []string{"list", "get"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"namespaces"},
				Verbs:     []string{"list", "get"},
			},
			{
				APIGroups: []string{apiextv1.GroupName},
				Resources: []string{"customresourcedefinitions"},
				Verbs:     []string{"get"},
			},
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to create or update velero cluster role: %w", err)
	}

	crb := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: crbName}}
	if _, err := controllerutil.CreateOrUpdate(ctx, c.cl, crb, func() error {
		crb.Labels = veleroinstall.Labels()
		if crb.ObjectMeta.CreationTimestamp.IsZero() {
			crb.RoleRef = rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "ClusterRole",
				Name:     clusterRoleName,
			}
		}
		crb.Subjects = []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      saName,
				Namespace: c.systemNamespace,
			},
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to create or update velero cluster role binding: %w", err)
	}

	return nil
}

func (c *Config) fetchVeleroDeploy(ctx context.Context) (*appsv1.Deployment, error) {
	veleroDeploy := new(appsv1.Deployment)
	return veleroDeploy, c.cl.Get(ctx, client.ObjectKey{Namespace: c.systemNamespace, Name: VeleroName}, veleroDeploy)
}

// https://github.com/kubernetes/kubernetes/blob/master/staging/src/k8s.io/kubectl/pkg/polymorphichelpers/rollout_status.go#L76-L89

// isDeploymentReady checks if the given Deployment instance is ready.
func isDeploymentReady(d *appsv1.Deployment) bool {
	if d.Generation > d.Status.ObservedGeneration {
		return false
	}

	const timedOutReason = "ProgressDeadlineExceeded" // avoid dependency
	var cond *appsv1.DeploymentCondition
	for _, c := range d.Status.Conditions {
		if c.Type == appsv1.DeploymentProgressing {
			cond = &c
			break
		}
	}

	if cond != nil && cond.Reason == timedOutReason {
		return false
	}

	if d.Spec.Replicas != nil && d.Status.UpdatedReplicas < *d.Spec.Replicas {
		return false
	}

	if d.Status.Replicas > d.Status.UpdatedReplicas {
		return false
	}

	if d.Status.AvailableReplicas < d.Status.UpdatedReplicas {
		return false
	}

	return true
}
