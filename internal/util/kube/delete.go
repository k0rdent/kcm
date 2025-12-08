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

package kube

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// DeletionExcludeFn returns true for objects that must be preserved.
type DeletionExcludeFn[T client.Object] func(T) bool

// DeleteAllExceptAndWait deletes collection of the given object
// across all of the namespaces available, and wait for the given
// timeout to ensure all of the objects have actually been deleted.
func DeleteAllExceptAndWait[T client.Object](
	ctx context.Context,
	c client.Client,
	obj T,
	list client.ObjectList,
	timeout time.Duration,
	exclude DeletionExcludeFn[T],
	extraOpts ...client.DeleteAllOfOption,
) error {
	namespaces := new(corev1.NamespaceList)
	if err := c.List(ctx, namespaces); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to list namespaces: %w", err)
	}

	for _, ns := range namespaces.Items {
		if exclude == nil {
			opts := append([]client.DeleteAllOfOption{client.InNamespace(ns.Name)}, extraOpts...)
			if err := c.DeleteAllOf(ctx, obj, opts...); client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("failed to delete collection: %w", err)
			}

			continue
		}

		if err := c.List(ctx, list, client.InNamespace(ns.Name)); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to list in %q: %w", ns.Name, err)
		}

		items, err := apimeta.ExtractList(list)
		if err != nil {
			return fmt.Errorf("failed to extract list: %w", err)
		}

		for _, v := range items {
			o, ok := v.(T)
			if !ok {
				return fmt.Errorf("unexpected list item type %T", v)
			}

			if exclude != nil && exclude(o) {
				continue
			}

			if err := c.Delete(ctx, o); client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("failed to delete %s %s: %w", o.GetObjectKind().GroupVersionKind().String(), client.ObjectKeyFromObject(o), err)
			}
		}
	}

	interval := 500 * time.Millisecond
	return wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		if err := c.List(ctx, list); client.IgnoreNotFound(err) != nil {
			return false, fmt.Errorf("failed to list during wait: %w", err)
		}

		objs, err := apimeta.ExtractList(list)
		if err != nil {
			return false, fmt.Errorf("failed to extract list during wait: %w", err)
		}

		for _, v := range objs {
			o, ok := v.(T)
			if !ok {
				return true, fmt.Errorf("unexpected list item type %T", v) // NOTE: return without wait
			}

			if exclude != nil && exclude(o) {
				continue
			}

			return false, nil
		}

		return true, nil
	})
}

// allowedOwnerKinds lists the allowed top-level controller group+kind we accept.
var allowedOwnerKinds = map[string]struct{}{
	"apps/Deployment":  {},
	"apps/StatefulSet": {},
	"apps/ReplicaSet":  {},
	"apps/DaemonSet":   {},
	"batch/Job":        {},
	"batch/CronJob":    {},
}

// DeletePVCsAndOwnersAndWait deletes PVCs (respecting exclude), removing pod owners
// when necessary so PVCs can be released. This also removes finalizers from objects
// before deleting them. It waits up to timeout for matching PVCs to disappear.
func DeletePVCsAndOwnersAndWait(
	ctx context.Context,
	c client.Client,
	timeout time.Duration,
	exclude DeletionExcludeFn[*corev1.PersistentVolumeClaim],
) error {
	namespaces := new(corev1.NamespaceList)
	if err := c.List(ctx, namespaces); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to list namespaces: %w", err)
	}

	l := log.FromContext(ctx).WithName("pvc-deleter")

	for _, ns := range namespaces.Items {
		pvcList := new(corev1.PersistentVolumeClaimList)
		if err := c.List(ctx, pvcList, client.InNamespace(ns.Name)); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to list PVCs in %q: %w", ns.Name, err)
		}

		for _, pvc := range pvcList.Items {
			if exclude != nil && exclude(&pvc) {
				continue
			}

			pods, err := findPodsUsingPVC(ctx, c, ns.Name, pvc.Name)
			if err != nil {
				return fmt.Errorf("failed to find pods using pvc %s/%s: %w", ns.Name, pvc.Name, err)
			}

			for _, pod := range pods {
				topOwner, err := findTopLevelAllowedController(ctx, c, &pod.ObjectMeta)
				if err != nil {
					return fmt.Errorf("failed to resolve owners for pod %s/%s: %w", pod.Namespace, pod.Name, err)
				}

				if topOwner == nil {
					// delete plain pod
					if err := removeFinalizers(ctx, c, &pod); err != nil {
						l.V(1).Error(err, "failed to remove finalizers from pod, yet trying to delete the object", "pod ns", pod.Namespace, "pod name", pod.Name)
					}

					if err := c.Delete(ctx, &pod); client.IgnoreNotFound(err) != nil {
						return fmt.Errorf("failed to delete pod %s/%s: %w", pod.Namespace, pod.Name, err)
					}

					continue
				}

				// delete top owner of the pod (pod should be gc-ed)
				if err := removeFinalizers(ctx, c, topOwner); err != nil {
					l.V(1).Error(err, "failed to remove finalizers from owner, yet trying to delete the object", "owner ns", topOwner.GetNamespace(), "owner name", topOwner.GetName(), "owner kind", topOwner.GetKind())
				}

				if err := c.Delete(ctx, topOwner); client.IgnoreNotFound(err) != nil {
					return fmt.Errorf("failed to delete owner %s/%s (kind=%s): %w",
						topOwner.GetNamespace(), topOwner.GetName(), topOwner.GetKind(), err)
				}
			}

			// delete the PVC itself
			if err := removeFinalizers(ctx, c, &pvc); err != nil {
				l.V(1).Error(err, "failed to remove finalizers from PVC, yet trying to delete the object", "pvc ns", pvc.Namespace, "pvc name", pvc.Name)
			}

			if err := c.Delete(ctx, &pvc); client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("failed to delete pvc %s/%s: %w", pvc.Namespace, pvc.Name, err)
			}
		}
	}

	interval := 500 * time.Millisecond
	return wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		pvcs := new(corev1.PersistentVolumeClaimList)
		if err := c.List(ctx, pvcs); client.IgnoreNotFound(err) != nil {
			return false, fmt.Errorf("failed to list PVCs during wait: %w", err)
		}

		for _, pvc := range pvcs.Items {
			if exclude != nil && exclude(&pvc) {
				continue
			}

			return false, nil
		}

		return true, nil
	})
}

// findPodsUsingPVC lists pods in namespace and returns pods that reference claimName.
func findPodsUsingPVC(ctx context.Context, c client.Client, namespace, claimName string) ([]corev1.Pod, error) {
	podList := new(corev1.PodList)
	if err := c.List(ctx, podList, client.InNamespace(namespace)); client.IgnoreNotFound(err) != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	var out []corev1.Pod
	for _, p := range podList.Items {
		for _, vol := range p.Spec.Volumes {
			if vol.PersistentVolumeClaim != nil && vol.PersistentVolumeClaim.ClaimName == claimName {
				out = append(out, p)
				break
			}
		}
	}

	return out, nil
}

// findTopLevelAllowedController walks ownerRefs from the given objectMeta (e.g. a Pod),
// but only considers "allowed" controller kinds (allowedOwnerKinds). It returns:
//
//   - an *unstructured.Unstructured for the top-most allowed controller (Deployment, StatefulSet, Job, ...)
//   - nil if there is no allowed owner (caller should delete the Pod directly)
//   - error if an unexpected error occurs when fetching owners
//
// If a non-allowed ownerRef appears (a CRD or external provider), we STOP the walk
// and return the last allowed controller found (if any). If none found, we return nil so caller will delete the pod.
func findTopLevelAllowedController(ctx context.Context, c client.Client, metaObj *metav1.ObjectMeta) (*unstructured.Unstructured, error) {
	const maxDepth = 10

	l := log.FromContext(ctx).WithName("owner-finder").WithValues("ancestor ns", metaObj.Namespace, "ancestor name", metaObj.Name)

	// find immediate controller ownerRef on the object
	var controllerRef *metav1.OwnerReference
	for _, or := range metaObj.OwnerReferences {
		if or.Controller != nil && *or.Controller {
			controllerRef = or.DeepCopy()
			break
		}
	}

	if controllerRef == nil {
		l.V(1).Info("no controller owner ref found")
		return nil, nil //nolint:nilnil // to avoid 3-return-param signature
	}

	depth := 0
	var lastAllowed *unstructured.Unstructured

	currentRef := controllerRef
	namespace := metaObj.Namespace

	for currentRef != nil {
		depth++
		if depth > maxDepth {
			return nil, fmt.Errorf("ownerRef chain too deep (> %d) for %s/%s", maxDepth, metaObj.Namespace, metaObj.Name)
		}

		// Decide whether this ownerRef is an allowed kind
		group := groupFromAPIVersion(currentRef.APIVersion)
		key := fmt.Sprintf("%s/%s", group, currentRef.Kind)
		allowed := false
		if _, ok := allowedOwnerKinds[key]; ok {
			allowed = true
		}

		if !allowed {
			// stop walking: we do not attempt to go above non-allowed owners.
			// return lastAllowed (may be nil)
			// WARN: removal of such an owner should collect ownees but the top controller still might resurrect the whole chain
			l.V(1).Info("found non-allowed owner, returning the previous", "non allowed group", key)
			return lastAllowed, nil
		}

		// allowed owner
		u := new(unstructured.Unstructured)
		u.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   group,
			Version: versionFromAPIVersion(currentRef.APIVersion),
			Kind:    currentRef.Kind,
		})

		if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: currentRef.Name}, u); err != nil {
			if apierrors.IsNotFound(err) {
				l.V(1).Info("current owner is not found, returning the previous", "owner name", currentRef.Name)
				return lastAllowed, nil
			}

			if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
				l.V(1).Info("not enough permissions to get current owner, returning the previous", "owner name", currentRef.Name)
				return lastAllowed, nil
			}

			return nil, fmt.Errorf("failed to get owner %s/%s (apiVersion=%s kind=%s): %w",
				namespace, currentRef.Name, currentRef.APIVersion, currentRef.Kind, err)
		}

		// this is a candidate allowed top-level controller
		// store it and see if it itself has a controller ownerRef
		lastAllowed = u

		ors := u.GetOwnerReferences()
		var nextController *metav1.OwnerReference
		for _, or := range ors {
			if or.Controller != nil && *or.Controller {
				nextController = or.DeepCopy()
				break
			}
		}

		if nextController == nil {
			// top-most controller
			l.V(1).Info("found top most owner ref", "owner name", lastAllowed.GetName())
			return lastAllowed, nil
		}

		// continue walking
		currentRef = nextController
	}

	l.V(1).Info("found last allowed owner ref", "owner name", lastAllowed.GetName())

	return lastAllowed, nil
}

// groupFromAPIVersion returns the group (left of /) from apiVersion, or "" if none.
func groupFromAPIVersion(apiVersion string) string {
	// apiVersion is either "group/version" or "version"
	parts := strings.SplitN(apiVersion, "/", 2)
	if len(parts) == 2 {
		return parts[0]
	}
	return ""
}

// versionFromAPIVersion returns the version (right of /) or the whole string if no slash.
func versionFromAPIVersion(apiVersion string) string {
	parts := strings.SplitN(apiVersion, "/", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return parts[0]
}

// removeFinalizers removes finalizers from the given client.Object and updates it.
// If the object already has no finalizers this is a no-op.
func removeFinalizers(ctx context.Context, c client.Client, obj client.Object) error {
	if len(obj.GetFinalizers()) == 0 {
		return nil
	}

	obj.SetFinalizers(nil)
	if err := c.Update(ctx, obj); err != nil { // question(zerospiel): should we patch the object instead?
		return fmt.Errorf("failed to update %s %s/%s to remove finalizers: %w",
			obj.GetObjectKind().GroupVersionKind().Kind, obj.GetNamespace(), obj.GetName(), err)
	}

	return nil
}
