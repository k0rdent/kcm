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

package clusterdeployment

import (
	"context"
	"errors"
	"fmt"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/K0rdent/kcm/test/e2e/kubeclient"
	"github.com/K0rdent/kcm/test/utils"
)

// resourceValidationFunc is intended to validate a specific kubernetes
// resource.
type resourceValidationFunc func(context.Context, *kubeclient.KubeClient, string) error

func validateCluster(ctx context.Context, kc *kubeclient.KubeClient, clusterName string) error {
	cluster, err := kc.GetCluster(ctx, clusterName)
	if err != nil {
		return err
	}

	phase, _, err := unstructured.NestedString(cluster.Object, "status", "phase")
	if err != nil {
		return fmt.Errorf("failed to get status.phase for %s: %v", cluster.GetName(), err)
	}

	if phase == "Deleting" {
		Fail(fmt.Sprintf("%s is in 'Deleting' phase", cluster.GetName()))
	}

	if err := utils.ValidateObjectNamePrefix(cluster, clusterName); err != nil {
		Fail(err.Error())
	}

	return utils.ValidateConditionsTrue(cluster)
}

func validateMachines(ctx context.Context, kc *kubeclient.KubeClient, clusterName string) error {
	machines, err := kc.ListMachines(ctx, clusterName)
	if err != nil {
		return err
	}

	if len(machines) == 0 {
		// No machines have been created yet, check for MachineDeployments to
		// provide some debug information as to why no machines are present.
		md, err := kc.ListMachineDeployments(ctx, clusterName)
		if err != nil {
			return fmt.Errorf("failed to list machine deployments: %w", err)
		}

		for _, md := range md {
			_, _ = fmt.Fprintf(GinkgoWriter, "No machines found, validating MachineDeployment %s\n", md.GetName())

			if err := utils.ValidateObjectNamePrefix(&md, clusterName); err != nil {
				Fail(err.Error())
			}

			if err := utils.ValidateConditionsTrue(&md); err != nil {
				return err
			}
		}
	}

	for _, machine := range machines {
		if err := utils.ValidateObjectNamePrefix(&machine, clusterName); err != nil {
			Fail(err.Error())
		}

		if err := utils.ValidateConditionsTrue(&machine); err != nil {
			return err
		}
	}

	return nil
}

func validateRemoteMachines(ctx context.Context, kc *kubeclient.KubeClient, clusterName string) error {
	machines, err := kc.ListRemoteMachines(ctx, clusterName)
	if err != nil {
		return err
	}
	for _, machine := range machines {
		if err := validateReadyStatus(machine); err != nil {
			return err
		}
	}
	return nil
}

func validateK0sControlPlanes(ctx context.Context, kc *kubeclient.KubeClient, clusterName string) error {
	controlPlanes, err := kc.ListK0sControlPlanes(ctx, clusterName)
	if err != nil {
		return err
	}

	var errs error
	for _, controlPlane := range controlPlanes {
		if err := utils.ValidateObjectNamePrefix(&controlPlane, clusterName); err != nil {
			errs = errors.Join(errs, err)
			continue
		}

		// k0s does not use the metav1.Condition type for status.conditions,
		// instead it uses a custom type so we can't use
		// ValidateConditionsTrue here, instead we'll check for "ready: true".
		errs = errors.Join(errs, validateReadyStatus(controlPlane))
	}

	return errs
}

func validateK0smotronControlPlanes(ctx context.Context, kc *kubeclient.KubeClient, clusterName string) error {
	controlPlanes, err := kc.ListK0smotronControlPlanes(ctx, clusterName)
	if err != nil {
		return err
	}
	var errs error
	for _, controlPlane := range controlPlanes {
		errs = errors.Join(errs, validateReadyStatus(controlPlane))
	}
	return errs
}

func validateAWSManagedControlPlanes(ctx context.Context, kc *kubeclient.KubeClient, clusterName string) error {
	controlPlanes, err := kc.ListAWSManagedControlPlanes(ctx, clusterName)
	if err != nil {
		return err
	}
	var errs error
	for _, controlPlane := range controlPlanes {
		errs = errors.Join(errs, validateReadyStatus(controlPlane))
	}
	return errs
}

func validateAzureASOManagedCluster(ctx context.Context, kc *kubeclient.KubeClient, clusterName string) error {
	cluster, err := kc.GetAzureASOManagedCluster(ctx, clusterName)
	if err != nil {
		return err
	}
	return validateReadyStatus(*cluster)
}

func validateGCPManagedCluster(ctx context.Context, kc *kubeclient.KubeClient, clusterName string) error {
	cluster, err := kc.GetGCPManagedCluster(ctx, clusterName)
	if err != nil {
		return err
	}
	return validateReadyStatus(*cluster)
}

func validateAzureASOManagedControlPlane(ctx context.Context, kc *kubeclient.KubeClient, clusterName string) error {
	controlPlane, err := kc.GetAzureASOManagedControlPlane(ctx, clusterName)
	if err != nil {
		return err
	}
	return validateReadyStatus(*controlPlane)
}

func validateAzureASOManagedMachinePools(ctx context.Context, kc *kubeclient.KubeClient, clusterName string) error {
	machinePools, err := kc.ListAzureASOManagedMachinePools(ctx, clusterName)
	if err != nil {
		return err
	}
	var errs error
	for _, machinePool := range machinePools {
		errs = errors.Join(errs, validateReadyStatus(machinePool))
	}
	return errs
}

func validateGCPManagedControlPlane(ctx context.Context, kc *kubeclient.KubeClient, clusterName string) error {
	controlPlanes, err := kc.GetGCPManagedControlPlanes(ctx, clusterName)
	if err != nil {
		return err
	}
	var errs error
	for _, cp := range controlPlanes {
		errs = errors.Join(errs, validateReadyStatus(cp))
	}
	return errs
}

func validateGCPManagedMachinePools(ctx context.Context, kc *kubeclient.KubeClient, clusterName string) error {
	machinePools, err := kc.ListGCPManagedMachinePools(ctx, clusterName)
	if err != nil {
		return err
	}
	var errs error
	for _, machinePool := range machinePools {
		errs = errors.Join(errs, validateReadyStatus(machinePool))
	}
	return errs
}

// validateReadyStatus validates if the provided object has ready status
func validateReadyStatus(obj unstructured.Unstructured) error {
	name := obj.GetName()
	kind := obj.GetKind()
	objStatus, found, err := unstructured.NestedFieldCopy(obj.Object, "status")
	if err != nil {
		return fmt.Errorf("failed to get status conditions for %s: %s: %w", kind, name, err)
	}
	if !found {
		return fmt.Errorf("no status found for %s: %s", kind, name)
	}
	st, ok := objStatus.(map[string]any)
	if !ok {
		return fmt.Errorf("expected %s condition to be type map[string]any, got: %T", kind, objStatus)
	}
	if v, ok := st["ready"].(bool); !ok || !v {
		return fmt.Errorf("%s %s is not yet ready, status: %+v", kind, name, st)
	}
	return nil
}

// validateCSIDriver validates that the provider CSI driver is functioning
// by creating a PVC and verifying it enters "Bound" status.
func validateCSIDriver(ctx context.Context, kc *kubeclient.KubeClient, clusterName string) error {
	clusterKC := kc.NewFromCluster(ctx, "default", clusterName)

	pvcName := clusterName + "-csi-test-pvc"

	_, err := clusterKC.Client.CoreV1().PersistentVolumeClaims(clusterKC.Namespace).
		Create(ctx, &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name: pvcName,
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{
					corev1.ReadWriteOnce,
				},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("1Gi"),
					},
				},
			},
		}, metav1.CreateOptions{})
	if err != nil {
		// Since these resourceValidationFuncs are intended to be used in
		// Eventually we should ensure a follow-up PVCreate is a no-op.
		if !apierrors.IsAlreadyExists(err) {
			Fail(fmt.Sprintf("failed to create test PVC: %v", err))
		}
	}

	// Create a pod that uses the PVC so that the PVC enters "Bound" status.
	_, err = clusterKC.Client.CoreV1().Pods(clusterKC.Namespace).Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: pvcName + "-pod",
		},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					Name: "test-pvc-vol",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: pvcName,
						},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:  "test-pvc-container",
					Image: "nginx",
					VolumeMounts: []corev1.VolumeMount{
						{
							MountPath: "/storage",
							Name:      "test-pvc-vol",
						},
					},
				},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		if !apierrors.IsAlreadyExists(err) {
			Fail(fmt.Sprintf("failed to create test Pod: %v", err))
		}
	}

	// Verify the PVC enters "Bound" status and inherits the CSI driver
	// storageClass without us having to specify it.
	pvc, err := clusterKC.Client.CoreV1().PersistentVolumeClaims(clusterKC.Namespace).
		Get(ctx, pvcName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get test PVC: %w", err)
	}

	if pvc.Spec.StorageClassName == nil {
		Fail(fmt.Sprintf("%s PersistentVolumeClaim does not have a storageClass defined", pvcName))
	}

	if pvc.Status.Phase != corev1.ClaimBound {
		return fmt.Errorf("%s PersistentVolume not yet 'Bound', current phase: %q", pvcName, pvc.Status.Phase)
	}

	return nil
}

// validateCCM validates that the provider's cloud controller manager is
// functional by creating a LoadBalancer service and verifying it is assigned
// an external IP.
func validateCCM(ctx context.Context, kc *kubeclient.KubeClient, clusterName string) error {
	clusterKC := kc.NewFromCluster(ctx, "default", clusterName)

	createdServiceName := "loadbalancer-" + clusterName

	_, err := clusterKC.Client.CoreV1().Services(clusterKC.Namespace).Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: createdServiceName,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"some": "selector",
			},
			Ports: []corev1.ServicePort{
				{
					Port:       8765,
					TargetPort: intstr.FromInt(9376),
				},
			},
			Type: corev1.ServiceTypeLoadBalancer,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		// Since these resourceValidationFuncs are intended to be used in
		// Eventually we should ensure a follow-up ServiceCreate is a no-op.
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create test Service: %w", err)
		}
	}

	// Verify the Service is assigned an external IP.
	service, err := clusterKC.Client.CoreV1().Services(clusterKC.Namespace).
		Get(ctx, createdServiceName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get test Service: %w", err)
	}

	for _, i := range service.Status.LoadBalancer.Ingress {
		if i.Hostname != "" || i.IP != "" {
			return nil
		}
	}

	return fmt.Errorf("%s Service does not yet have an external hostname", service.Name)
}

// validateSveltosCluster validates that the sveltos cluster is ready
func validateSveltosCluster(ctx context.Context, kc *kubeclient.KubeClient, clusterName string) error {
	sveltosCluster, err := kc.GetSveltosCluster(ctx, clusterName)
	if err != nil {
		return fmt.Errorf("error getting sveltos cluster: %v", err)
	}

	ready, found, err := unstructured.NestedBool(sveltosCluster.Object, "status", "ready")
	if err != nil {
		return fmt.Errorf("error checking sveltos cluster ready: %v", err)
	}

	if !found || !ready {
		return fmt.Errorf("sveltos cluster %s is not ready", clusterName)
	}

	return nil
}

func ValidateService(ctx context.Context, kc *kubeclient.KubeClient, name string) error {
	_, err := kc.Client.CoreV1().Services(kc.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	return nil
}

func ValidateDeployment(ctx context.Context, kc *kubeclient.KubeClient, name string) error {
	dep, err := kc.Client.AppsV1().Deployments(kc.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	if dep.Generation > dep.Status.ObservedGeneration {
		return fmt.Errorf("deployment %s has observed generation equals to %d, expected %d", name, dep.Status.ObservedGeneration, dep.Generation)
	}

	const timedOutReason = "ProgressDeadlineExceeded" // avoid dependency

	if slices.ContainsFunc(dep.Status.Conditions, func(c appsv1.DeploymentCondition) bool {
		return c.Type == appsv1.DeploymentProgressing && c.Reason == timedOutReason
	}) {
		return fmt.Errorf("deployment %s has timed out", name)
	}

	if dep.Spec.Replicas != nil && dep.Status.UpdatedReplicas < *dep.Spec.Replicas {
		return fmt.Errorf("deployment %s has %d updated replicas, desired replicas %d", name, dep.Status.UpdatedReplicas, *dep.Spec.Replicas)
	}

	if dep.Status.Replicas > dep.Status.UpdatedReplicas {
		return fmt.Errorf("deployment %s has %d updated replicas, expected %d", name, dep.Status.UpdatedReplicas, dep.Status.Replicas)
	}

	if dep.Status.AvailableReplicas < dep.Status.UpdatedReplicas {
		return fmt.Errorf("deployment %s has %d available replicas, expected %d", name, dep.Status.AvailableReplicas, dep.Status.UpdatedReplicas)
	}

	if *dep.Spec.Replicas != dep.Status.ReadyReplicas {
		return fmt.Errorf("deployment %s has %d ready replicas, desired replicas %d", name, dep.Status.ReadyReplicas, *dep.Spec.Replicas)
	}

	return nil
}
