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
	namespaces, err := listNamespaceNames(ctx, c)
	if err != nil {
		return fmt.Errorf("failed to list namespaces: %w", err)
	}

	newList := func() client.ObjectList {
		cp, ok := list.DeepCopyObject().(client.ObjectList)
		if !ok { // unreachable: a deep copy of an ObjectList is an ObjectList
			return list
		}

		return cp
	}

	for _, ns := range namespaces {
		if exclude == nil {
			opts := append([]client.DeleteAllOfOption{client.InNamespace(ns)}, extraOpts...)
			if err := c.DeleteAllOf(ctx, obj, opts...); client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("failed to delete collection: %w", err)
			}

			continue
		}

		err := EachPage(ctx, c, newList, func(page client.ObjectList) (bool, error) {
			items, err := apimeta.ExtractList(page)
			if err != nil {
				return false, fmt.Errorf("failed to extract list: %w", err)
			}

			for _, v := range items {
				o, ok := v.(T)
				if !ok {
					return false, fmt.Errorf("unexpected list item type %T", v)
				}

				if exclude(o) {
					continue
				}

				if err := c.Delete(ctx, o); client.IgnoreNotFound(err) != nil {
					return false, fmt.Errorf("failed to delete %s %s: %w", o.GetObjectKind().GroupVersionKind().String(), client.ObjectKeyFromObject(o), err)
				}
			}

			return true, nil
		}, client.InNamespace(ns))
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete in %q: %w", ns, err)
		}
	}

	const interval = 500 * time.Millisecond
	return wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		remaining := false
		// a single object left to delete is enough to keep waiting, hence the paging stops on the first one
		err := EachPage(ctx, c, newList, func(page client.ObjectList) (bool, error) {
			objs, err := apimeta.ExtractList(page)
			if err != nil {
				return false, fmt.Errorf("failed to extract list during wait: %w", err)
			}

			for _, v := range objs {
				o, ok := v.(T)
				if !ok {
					return false, fmt.Errorf("unexpected list item type %T", v)
				}

				if exclude != nil && exclude(o) {
					continue
				}

				remaining = true

				return false, nil
			}

			return true, nil
		})
		if client.IgnoreNotFound(err) != nil {
			return false, fmt.Errorf("failed to list during wait: %w", err)
		}

		return !remaining, nil
	})
}

// listNamespaceNames pages through the namespaces fetching only metadata, the names are
// all the deletion helpers need.
func listNamespaceNames(ctx context.Context, c client.Client) ([]string, error) {
	var names []string

	err := EachPage(ctx, c, func() *metav1.PartialObjectMetadataList {
		l := new(metav1.PartialObjectMetadataList)
		l.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Namespace"))
		return l
	}, func(page *metav1.PartialObjectMetadataList) (bool, error) {
		for _, ns := range page.Items {
			names = append(names, ns.Name)
		}
		return true, nil
	})

	return names, client.IgnoreNotFound(err)
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
// when necessary so PVCs can be released. This also removes finalizers from objects (pods/owners)
// after deleting them. It waits up to timeout for matching PersistentVolumes (not PVCs) to disappear.
func DeletePVCsAndOwnersAndWait(
	ctx context.Context,
	c client.Client,
	timeout time.Duration,
	exclude DeletionExcludeFn[*corev1.PersistentVolumeClaim],
) error {
	namespaces, err := listNamespaceNames(ctx, c)
	if err != nil {
		return fmt.Errorf("failed to list namespaces: %w", err)
	}

	ldebug := log.FromContext(ctx).V(1)

	// collects PVs that should be the subset of all of the existing PVCs
	pvWaitList, err := collectPersistentVolumeNames(ctx, c, exclude)
	if err != nil {
		return fmt.Errorf("failed to collect PVs: %w", err)
	}

	for _, ns := range namespaces {
		var podsByPVC map[string][]corev1.Pod // built lazily: namespaces without PVCs never list pods

		err := EachPage(ctx, c, func() *corev1.PersistentVolumeClaimList { return new(corev1.PersistentVolumeClaimList) },
			func(page *corev1.PersistentVolumeClaimList) (bool, error) {
				for i := range page.Items {
					pvc := &page.Items[i]
					if exclude != nil && exclude(pvc) {
						continue
					}

					if podsByPVC == nil {
						var err error
						if podsByPVC, err = findPodsUsingPVCs(ctx, c, ns); err != nil {
							return false, fmt.Errorf("failed to find pods using PVCs in %q: %w", ns, err)
						}
					}

					if err := deletePVCAndConsumers(ctx, c, pvc, podsByPVC[pvc.Name]); err != nil {
						return false, err
					}
				}

				return true, nil
			}, client.InNamespace(ns))
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete PVCs in %q: %w", ns, err)
		}
	}

	const interval = 500 * time.Millisecond

	for pvName, reclaim := range pvWaitList {
		ldebug.Info("Waiting for PV cleanup", "pv name", pvName, "reclaim policy", reclaim)

		waitErr := wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
			if reclaim == corev1.PersistentVolumeReclaimRecycle {
				return true, nil // if the deprecated Recycle policy, then consider the resource has been released
			}

			pv := new(corev1.PersistentVolume)
			err := c.Get(ctx, client.ObjectKey{Name: pvName}, pv)

			if apierrors.IsNotFound(err) {
				return true, nil // deleted
			}

			if err != nil {
				return false, fmt.Errorf("failed to get PV %s: %w", pvName, err)
			}

			// if Delete policy, PV should be deleted and it is not
			if reclaim == corev1.PersistentVolumeReclaimDelete {
				return false, nil
			}

			// otherwise (Retain) PV should become Released or claimRef cleared
			if pv.Status.Phase == corev1.VolumeReleased || pv.Spec.ClaimRef == nil {
				return true, nil
			}

			return false, nil
		})

		if waitErr != nil {
			return fmt.Errorf("failed to wait for PV %s cleanup with reclaimPolicy %s: %w", pvName, reclaim, waitErr)
		}
	}

	return nil
}

func collectPersistentVolumeNames(ctx context.Context, c client.Client, exclude DeletionExcludeFn[*corev1.PersistentVolumeClaim]) (map[string]corev1.PersistentVolumeReclaimPolicy, error) {
	pvWaitList := make(map[string]corev1.PersistentVolumeReclaimPolicy)

	err := EachPage(ctx, c, func() *corev1.PersistentVolumeList { return new(corev1.PersistentVolumeList) },
		func(page *corev1.PersistentVolumeList) (bool, error) {
			for _, pv := range page.Items {
				claim := pv.Spec.ClaimRef
				if claim == nil {
					continue // released
				}

				// check if the pvc exists and is not excluded
				pvc := new(corev1.PersistentVolumeClaim)
				err := c.Get(ctx, client.ObjectKey{Namespace: claim.Namespace, Name: claim.Name}, pvc)
				switch {
				case apierrors.IsNotFound(err): // pvc deleted but the pv is still there thus wait for it
					pvWaitList[pv.Name] = pv.Spec.PersistentVolumeReclaimPolicy

				case err != nil:
					return false, fmt.Errorf("failed to get PVC %s/%s for PV %s: %w", claim.Namespace, claim.Name, pv.Name, err)

				default:
					if exclude != nil && exclude(pvc) {
						continue
					}

					pvWaitList[pv.Name] = pv.Spec.PersistentVolumeReclaimPolicy
				}
			}

			return true, nil
		})
	if err != nil {
		return nil, err // either a list or a get error, both already descriptive
	}

	return pvWaitList, nil
}

// deletePVCAndConsumers removes the pods holding the given PVC, preferring their top-level
// controller so the pods are not recreated, and then the PVC itself. Finalizer removal
// failures are logged, not propagated: the object is already being deleted.
func deletePVCAndConsumers(ctx context.Context, c client.Client, pvc *corev1.PersistentVolumeClaim, pods []corev1.Pod) error {
	ldebug := log.FromContext(ctx).V(1)

	ldebug.Info("Processing PVC", "pvc namespace", pvc.Namespace, "pvc name", pvc.Name)

	for _, pod := range pods {
		ldebug.Info("Processing Pod", "pod namespace", pod.Namespace, "pod name", pod.Name)

		topOwner, err := findTopLevelAllowedController(ctx, c, &pod.ObjectMeta)
		if err != nil {
			return fmt.Errorf("failed to resolve owners for pod %s/%s: %w", pod.Namespace, pod.Name, err)
		}

		if topOwner == nil {
			ldebug.Info("Deleting Pod", "pod namespace", pod.Namespace, "pod name", pod.Name)

			// delete plain pod
			if err := c.Delete(ctx, &pod); client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("failed to delete pod %s/%s: %w", pod.Namespace, pod.Name, err)
			}

			if err := removeFinalizers(ctx, c, &pod); err != nil {
				ldebug.Error(err, "failed to remove finalizers from pod", "pod ns", pod.Namespace, "pod name", pod.Name)
			}

			continue
		}

		ldebug.Info("Deleting top owner", "owner namespace", topOwner.GetNamespace(), "owner name", topOwner.GetName(), "owner kind", topOwner.GetKind())
		// delete top owner of the pod (pod should be gc-ed)
		if err := c.Delete(ctx, topOwner); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete owner %s/%s (kind=%s): %w",
				topOwner.GetNamespace(), topOwner.GetName(), topOwner.GetKind(), err)
		}

		if err := removeFinalizers(ctx, c, topOwner); err != nil {
			ldebug.Error(err, "failed to remove finalizers from owner", "owner ns", topOwner.GetNamespace(), "owner name", topOwner.GetName(), "owner kind", topOwner.GetKind())
		}
	}

	ldebug.Info("Deleting PVC", "pvc namespace", pvc.Namespace, "pvc name", pvc.Name)
	if err := c.Delete(ctx, pvc); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to delete pvc %s/%s: %w", pvc.Namespace, pvc.Name, err)
	}

	return nil
}

// findPodsUsingPVCs pages through the pods of the namespace and indexes by claim name the
// pods referencing it. Only metadata is kept, that is all the callers need (owner walk and
// deletion), and a single pass replaces re-listing the whole namespace for every single PVC.
func findPodsUsingPVCs(ctx context.Context, c client.Client, namespace string) (map[string][]corev1.Pod, error) {
	out := make(map[string][]corev1.Pod)

	err := EachPage(ctx, c, func() *corev1.PodList { return new(corev1.PodList) },
		func(page *corev1.PodList) (bool, error) {
			for i := range page.Items {
				pod := &page.Items[i]

				claims := make(map[string]struct{}, len(pod.Spec.Volumes)) // a pod may mount the very same claim twice
				for _, vol := range pod.Spec.Volumes {
					if vol.PersistentVolumeClaim != nil {
						claims[vol.PersistentVolumeClaim.ClaimName] = struct{}{}
					}
				}

				for claim := range claims {
					out[claim] = append(out[claim], corev1.Pod{ObjectMeta: pod.ObjectMeta})
				}
			}

			return true, nil
		}, client.InNamespace(namespace))
	if client.IgnoreNotFound(err) != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
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

	ldebug := log.FromContext(ctx).WithValues("ancestor ns", metaObj.Namespace, "ancestor name", metaObj.Name).V(1)

	// find immediate controller ownerRef on the object
	var controllerRef *metav1.OwnerReference
	for _, or := range metaObj.OwnerReferences {
		if or.Controller != nil && *or.Controller {
			controllerRef = or.DeepCopy()
			break
		}
	}

	if controllerRef == nil {
		ldebug.Info("no controller owner ref found")
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
			ldebug.Info("found non-allowed owner, returning the previous", "non allowed group", key)
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
				ldebug.Info("current owner is not found, returning the previous", "owner group key", key, "owner name", currentRef.Name)
				return lastAllowed, nil
			}

			if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
				ldebug.Info("not enough permissions to get current owner, returning the previous", "owner group key", key, "owner name", currentRef.Name)
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
			ldebug.Info("found top most owner ref", "owner kind", lastAllowed.GetKind(), "owner name", lastAllowed.GetName())
			return lastAllowed, nil
		}

		// continue walking
		currentRef = nextController
	}

	ldebug.Info("found last allowed owner ref", "owner kind", lastAllowed.GetKind(), "owner name", lastAllowed.GetName())

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

	if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to get %s %s/%s to remove finalizers: %w",
			obj.GetObjectKind().GroupVersionKind().Kind, obj.GetNamespace(), obj.GetName(), err)
	}

	obj.SetFinalizers(nil)
	if err := c.Update(ctx, obj); client.IgnoreNotFound(err) != nil { // question(zerospiel): should we patch the object instead?
		return fmt.Errorf("failed to update %s %s/%s to remove finalizers: %w",
			obj.GetObjectKind().GroupVersionKind().Kind, obj.GetNamespace(), obj.GetName(), err)
	}

	return nil
}
