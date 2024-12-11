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
	"time"

	velerov1api "github.com/zerospiel/velero/pkg/apis/velero/v1"
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
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type Config struct {
	kubeRestConfig *rest.Config
	cl             client.Client

	image           string
	systemNamespace string
	features        []string

	requeueAfter time.Duration
}

const veleroName = "velero"

type ConfigOpt func(*Config)

func NewConfig(cl client.Client, kc *rest.Config, opts ...ConfigOpt) *Config {
	c := newWithDefaults()

	for _, o := range opts {
		o(c)
	}

	c.cl = cl
	c.kubeRestConfig = kc

	return c
}

func WithRequeueAfter(d time.Duration) ConfigOpt {
	return func(c *Config) {
		if d == 0 {
			return
		}
		c.requeueAfter = d
	}
}

func WithVeleroSystemNamespace(ns string) ConfigOpt {
	return func(c *Config) {
		if len(ns) == 0 {
			return
		}
		c.systemNamespace = ns
	}
}

func WithVeleroImage(image string) ConfigOpt {
	return func(c *Config) {
		if len(image) == 0 {
			return
		}
		c.image = image
	}
}

func WithFeatures(features ...string) ConfigOpt {
	return func(c *Config) {
		if len(features) == 0 {
			return
		}
		c.features = features
	}
}

func newWithDefaults() *Config {
	return &Config{
		requeueAfter:    5 * time.Second,
		systemNamespace: veleroName,
		image:           fmt.Sprintf("%s/%s:%s", veleroName, veleroName, "v1.15.0"), // velero/velero:v1.15.0
	}
}

// ReconcileVeleroInstallation reconciles installation of velero stack within a management cluster.
func (c *Config) ReconcileVeleroInstallation(ctx context.Context) (ctrl.Result, error) {
	deployState, err := c.checkVeleroDeployIsInstalled(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to determine if velero is installed: %w", err)
	}

	if deployState.needInstallation {
		ctrl.LoggerFrom(ctx).Info("Installing velero stack")
		if err := c.installVelero(ctx); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to perform velero stack installation: %w", err)
		}

		return ctrl.Result{}, nil
	}

	if deployState.needRequeue || deployState.needInstallation {
		return ctrl.Result{Requeue: true, RequeueAfter: c.requeueAfter}, nil // either the installation has happened or direct requeue is required
	}

	return ctrl.Result{}, nil
}

// installVelero installs velero stack with all the required components.
func (c *Config) installVelero(ctx context.Context) error {
	saName, err := c.ensureVeleroRBAC(ctx)
	if err != nil {
		return fmt.Errorf("failed to ensure velero RBAC: %w", err)
	}

	options := &veleroinstall.VeleroOptions{
		Namespace: c.systemNamespace,
		Image:     c.image,
		Features:  c.features,

		ServiceAccountName:      saName,
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
		Plugins:                     nil,   // should be installed on-demand (BSL object)
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

// ensureVeleroRBAC creates required RBAC objects for velero to be functional
// with the minimal required set of permissions.
// Returns the name of created ServiceAccount referenced by created bindings.
func (c *Config) ensureVeleroRBAC(ctx context.Context) (string, error) {
	crbName, clusterRoleName, rbName, roleName, saName := veleroName, veleroName, veleroName, veleroName, veleroName
	if c.systemNamespace != veleroName {
		vns := veleroName + "-" + c.systemNamespace
		crbName, clusterRoleName, saName = vns+"-clusterrolebinding", vns+"-clusterrole", crbName+"-sa"
		rbName, roleName = vns+"-rolebinding", vns+"-role"
	}

	systemNS := new(corev1.Namespace)
	if err := c.cl.Get(ctx, client.ObjectKey{Name: c.systemNamespace}, systemNS); apierrors.IsNotFound(err) {
		systemNS.Name = c.systemNamespace
		if err := c.cl.Create(ctx, systemNS); err != nil {
			return "", fmt.Errorf("failed to create %s namespace for velero: %w", c.systemNamespace, err)
		}
	}

	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: saName, Namespace: c.systemNamespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, c.cl, sa, func() error {
		sa.Labels = veleroinstall.Labels()
		return nil
	}); err != nil {
		return "", fmt.Errorf("failed to create or update velero service account: %w", err)
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
		return "", fmt.Errorf("failed to create or update velero role: %w", err)
	}

	roleBinding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: rbName, Namespace: c.systemNamespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, c.cl, roleBinding, func() error {
		roleBinding.Labels = veleroinstall.Labels()
		roleBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     roleName,
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
		return "", fmt.Errorf("failed to create or update velero role binding: %w", err)
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
		return "", fmt.Errorf("failed to create or update velero cluster role: %w", err)
	}

	crb := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: crbName}}
	if _, err := controllerutil.CreateOrUpdate(ctx, c.cl, crb, func() error {
		crb.Labels = veleroinstall.Labels()
		crb.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     clusterRoleName,
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
		return "", fmt.Errorf("failed to create or update velero cluster role binding: %w", err)
	}

	return saName, nil
}

type deployState struct {
	needRequeue      bool
	needInstallation bool
}

// checkVeleroDeployIsInstalled check whether the velero deploy is already installed:
//   - the deployment is presented;
//   - is in ready state;
//   - the only container has the expected image and replicas.
//
// If image or replica count are not expected, the deploy will be patched regardingly.
// If the deploy has unexpected container name, the deploy will be deleted.
func (c *Config) checkVeleroDeployIsInstalled(ctx context.Context) (deployState, error) {
	l := ctrl.LoggerFrom(ctx).WithName("velero-deploy-checker")

	l.Info("Checking if Velero deployment is already installed")

	veleroDeploy := new(appsv1.Deployment)
	err := c.cl.Get(ctx, client.ObjectKey{Namespace: c.systemNamespace, Name: veleroName}, veleroDeploy)
	if err != nil && !apierrors.IsNotFound(err) {
		return deployState{}, fmt.Errorf("failed to get velero deploy: %w", err)
	}

	if apierrors.IsNotFound(err) {
		l.Info("Deployment is not found, considering the stack has not been (yet) installed")
		return deployState{needInstallation: true}, nil
	}

	if len(veleroDeploy.Spec.Template.Spec.Containers) == 0 ||
		veleroDeploy.Spec.Template.Spec.Containers[0].Name != veleroName {
		l.Info("Deployment has unexpected container name, considering to reinstall the deployment again")
		// the deploy is "corrupted", remove only it and then reinstall
		if err := c.cl.Delete(ctx, veleroDeploy); err != nil {
			return deployState{}, fmt.Errorf("failed to delete velero deploy: %w", err)
		}

		removalCtx, cancel := context.WithCancel(ctx)
		var checkErr error
		checkFn := func(ctx context.Context) {
			key := client.ObjectKeyFromObject(veleroDeploy)
			ll := l.V(1).WithValues("velero_deploy", key.String())
			ll.Info("Checking if the deployment has been removed")
			if checkErr = c.cl.Get(ctx, client.ObjectKeyFromObject(veleroDeploy), veleroDeploy); checkErr != nil {
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
			return deployState{}, fmt.Errorf("failed to wait for velero deploy removal: %w", checkErr)
		}

		return deployState{needInstallation: true}, nil
	}

	isPatchRequired := false
	// process 2 invariants beforehand
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

	if isPatchRequired {
		l.Info("Patching the deployment")
		if err := c.cl.Patch(ctx, veleroDeploy, client.Merge); err != nil {
			return deployState{}, fmt.Errorf("failed to patch velero deploy: %w", err)
		}

		l.Info("Need to requeue after the successful patch")
		return deployState{needRequeue: true}, nil
	}

	r := isDeploymentReady(veleroDeploy) // if no invariants then just check the readiness
	if !r {
		l.Info("Deployment is not ready yet, will requeue")
		return deployState{needRequeue: true}, nil
	}

	l.Info("Deployment is in the expected state")
	return deployState{}, nil
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
