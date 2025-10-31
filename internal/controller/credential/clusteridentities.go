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

package credential

import (
	"context"
	"errors"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/providerinterface"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
)

func CopyClusterIdentities(ctx context.Context, mgmtClient, rgnClient client.Client, cred *kcmv1.Credential, systemNamespace string) error {
	// Copy Cluster Identities only for regional Credential objects or Credentials created by the Access Management system
	// (with `k0rdent.mirantis.com/managed: true` label).
	if cred.Labels[kcmv1.KCMManagedLabelKey] != kcmv1.KCMManagedLabelValue && cred.Spec.Region == "" {
		return nil
	}
	cis, err := collectClusterIdentities(ctx, mgmtClient, rgnClient, cred, systemNamespace)
	if err != nil {
		return fmt.Errorf("failed to collect all Cluster Identities for %s Credential: %w", client.ObjectKeyFromObject(cred), err)
	}
	for _, ci := range cis {
		if err = ensureClusterIdentityObject(ctx, rgnClient, cred, ci); err != nil {
			return fmt.Errorf("error creating Cluster Identities for Credential %s: %w", client.ObjectKeyFromObject(cred), err)
		}
	}
	return nil
}

// ReleaseClusterIdentities removes orphaned ClusterIdentity objects associated with this Credential.
// It deletes Cluster Identities that have no referencing Credentials, or removes the
// "k0rdent.mirantis.com/credential.<namespace>.<name>" label if this Credential is no longer a parent.
func ReleaseClusterIdentities(ctx context.Context, rgnClient client.Client, cred *kcmv1.Credential) error {
	// Release Cluster Identities only for regional Credential objects or Credentials created by the Access Management system
	// (with `k0rdent.mirantis.com/managed: true` label).
	if cred.Labels[kcmv1.KCMManagedLabelKey] != kcmv1.KCMManagedLabelValue && cred.Spec.Region == "" {
		return nil
	}

	l := ctrl.LoggerFrom(ctx)

	ci, err := providerinterface.FindClusterIdentity(ctx, rgnClient, cred.Spec.IdentityRef)
	if err != nil {
		if errors.Is(err, providerinterface.ErrMissingClusterIdentityRef) {
			l.Info("No ProviderInterface has Cluster Identity defined. Skipping cluster identities deletion", "cluster identity", cred.Spec.IdentityRef.String())
			return nil
		}
		return fmt.Errorf("failed to get Cluster Identity definition for %s: %w", cred.Spec.IdentityRef.String(), err)
	}

	clIdties := &unstructured.UnstructuredList{}
	clIdties.SetAPIVersion(cred.Spec.IdentityRef.APIVersion)
	clIdties.SetKind(cred.Spec.IdentityRef.Kind + "List")

	if err := rgnClient.List(ctx, clIdties, client.MatchingLabels{
		kcmv1.KCMManagedLabelKey:           kcmv1.KCMManagedLabelValue,
		buildClusterIdentityLabelKey(cred): "true",
	}); err != nil {
		return fmt.Errorf("failed to list Cluster Identities for %s Credential of kind %s: %w", client.ObjectKeyFromObject(cred), cred.Spec.IdentityRef.Kind, err)
	}

	var errs error
	for _, clIdty := range clIdties.Items {
		for _, reference := range ci.References {
			clIdtyRefs := &unstructured.UnstructuredList{}
			clIdtyRefs.SetAPIVersion(reference.Version)
			clIdtyRefs.SetKind(reference.Kind + "List")

			if err := rgnClient.List(ctx, clIdtyRefs, client.MatchingLabels{
				kcmv1.KCMManagedLabelKey:           kcmv1.KCMManagedLabelValue,
				buildClusterIdentityLabelKey(cred): "true",
			}); err != nil {
				errs = errors.Join(errs, fmt.Errorf("failed to list Cluster Identity references for %s Credential of kind %s: %w", client.ObjectKeyFromObject(cred), reference.Kind, err))
				continue
			}
			for _, ref := range clIdtyRefs.Items {
				if err := releaseClusterIdentity(ctx, rgnClient, cred, &ref); err != nil {
					errs = errors.Join(errs, fmt.Errorf("failed to release Cluster Identity object reference of kind %s %s: %w", ref.GetKind(), objectKeyUnstructured(&ref), err))
					continue
				}
			}
		}
		if err := releaseClusterIdentity(ctx, rgnClient, cred, &clIdty); err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to release Cluster Identity object of kind %s %s: %w", clIdty.GetKind(), objectKeyUnstructured(&clIdty), err))
			continue
		}
	}

	return errs
}

func collectClusterIdentities(ctx context.Context, mgmtClient, rgnClient client.Client, cred *kcmv1.Credential, systemNamespace string) ([]*unstructured.Unstructured, error) {
	l := ctrl.LoggerFrom(ctx)

	ci, err := providerinterface.FindClusterIdentity(ctx, rgnClient, cred.Spec.IdentityRef)
	if err != nil {
		if errors.Is(err, providerinterface.ErrMissingClusterIdentityRef) {
			l.Info("No ProviderInterface has Cluster Identity defined. Skipping cluster identities distribution", "cluster identity", cred.Spec.IdentityRef.String())
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get Cluster Identity definition for %s: %w", cred.Spec.IdentityRef.String(), err)
	}

	clIdty := &unstructured.Unstructured{}
	clIdty.SetAPIVersion(cred.Spec.IdentityRef.APIVersion)
	clIdty.SetKind(cred.Spec.IdentityRef.Kind)
	clIdty.SetName(cred.Spec.IdentityRef.Name)

	objKey := client.ObjectKey{Name: cred.Spec.IdentityRef.Name}
	if cred.Spec.IdentityRef.Namespace != "" {
		// we expect the original identity to exist in the system namespace
		objKey = client.ObjectKey{Namespace: systemNamespace, Name: cred.Spec.IdentityRef.Name}
	}

	if err := mgmtClient.Get(ctx, objKey, clIdty); err != nil {
		return nil, fmt.Errorf("failed to get ClusterIdentity object of Kind=%s %s: %w",
			cred.Spec.IdentityRef.Kind, objectKey(cred.Spec.IdentityRef.Namespace, cred.Spec.IdentityRef.Name), err)
	}

	// set the new namespace to the namespace-scoped identities only
	if clIdty.GetNamespace() != "" {
		clIdty.SetNamespace(cred.Namespace)
	}

	var (
		errs error

		result = make([]*unstructured.Unstructured, 0, len(ci.References)+1)

		sourceNamespace = systemNamespace
		targetNamespace = cred.Namespace
	)

	for _, reference := range ci.References {
		name, nameFound, err := unstructured.NestedString(clIdty.Object, strings.Split(reference.NameFieldPath, ".")...)
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to get name reference from ClusterIdentity %s %s by path %s: %w",
				cred.Spec.IdentityRef.Kind, objectKey(cred.Spec.IdentityRef.Namespace, clIdty.GetName()), reference.NameFieldPath, err))
			continue
		}
		if !nameFound {
			errs = errors.Join(errs, fmt.Errorf("name reference from ClusterIdentity %s %s by path %s is not found",
				cred.Spec.IdentityRef.Kind, objectKey(cred.Spec.IdentityRef.Namespace, clIdty.GetName()), reference.NameFieldPath))
			continue
		}

		// when the namespaceFieldPath is unspecified, the provider requires the identity reference object
		// to exist in the system namespace
		var namespacePath []string

		if reference.NamespaceFieldPath != "" {
			namespacePath = strings.Split(reference.NamespaceFieldPath, ".")
			sourceNamespace, _, err = unstructured.NestedString(clIdty.Object, namespacePath...)
			if err != nil {
				errs = errors.Join(errs, fmt.Errorf("failed to get namespace reference from ClusterIdentity %s %s by path %s: %w",
					cred.Spec.IdentityRef.Kind, objectKey(cred.Spec.IdentityRef.Namespace, clIdty.GetName()), reference.NameFieldPath, err))
				continue
			}
		}
		clIdtyRef := &unstructured.Unstructured{}
		clIdtyRef.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   reference.Group,
			Version: reference.Version,
			Kind:    reference.Kind,
		})

		clIdtyRef.SetName(name)
		clIdtyRef.SetNamespace(sourceNamespace)

		if err := mgmtClient.Get(ctx, client.ObjectKey{Name: name, Namespace: sourceNamespace}, clIdtyRef); err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to get ClusterIdentity reference object of Kind=%s %s: %w",
				cred.Spec.IdentityRef.Kind, objectKey(sourceNamespace, name), err))
			continue
		}

		// set the namespace of Credential on the distributed identity object
		if reference.NamespaceFieldPath != "" {
			if err = unstructured.SetNestedField(clIdty.Object, targetNamespace, namespacePath...); err != nil {
				errs = errors.Join(fmt.Errorf("failed to set the %s field to %s of %s object: %w",
					reference.NamespaceFieldPath, targetNamespace, objectKeyUnstructured(clIdty), err))
				continue
			}
			clIdtyRef.SetNamespace(targetNamespace)
		}

		result = append(result, clIdtyRef)
	}
	result = append(result, clIdty)

	return result, errs
}

func ensureClusterIdentityObject(ctx context.Context, rgnClient client.Client, cred *kcmv1.Credential, obj *unstructured.Unstructured) error {
	l := ctrl.LoggerFrom(ctx)

	existingObj := &unstructured.Unstructured{}
	existingObj.SetGroupVersionKind(obj.GroupVersionKind())

	objKey := objectKeyUnstructured(obj)

	if err := rgnClient.Get(ctx, objKey, existingObj); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get Cluster Identity object %s %s: %w", obj.GetKind(), objectKeyUnstructured(obj), err)
		}
		// Cluster Identity resource does not exist - creating it
		if ns := obj.GetNamespace(); ns != "" {
			if err := kubeutil.EnsureNamespace(ctx, rgnClient, ns); err != nil {
				return fmt.Errorf("failed to ensure namespace %s: %w", ns, err)
			}
		}

		clIdty := copyClusterIdentity(cred, obj)
		if err := rgnClient.Create(ctx, clIdty); client.IgnoreAlreadyExists(err) != nil {
			return fmt.Errorf("failed to create Cluster Identity object %s %s: %w", obj.GetKind(), objKey, err)
		}
		l.Info("Cluster Identity object was successfully created", "kind", obj.GetKind(), "name", objKey)

		return nil
	}

	if existingObj.GetLabels()[kcmv1.KCMManagedLabelKey] != kcmv1.KCMManagedLabelValue {
		l.Info("Cluster Identity object is not managed by KCM, skipping update", "kind", existingObj.GetKind(), "name", objKey)
		return nil
	}

	clIdty := copyClusterIdentity(cred, obj)
	clIdty.SetResourceVersion(existingObj.GetResourceVersion())
	labels := existingObj.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[buildClusterIdentityLabelKey(cred)] = "true"
	clIdty.SetLabels(labels)

	if err := rgnClient.Update(ctx, clIdty); err != nil {
		return fmt.Errorf("failed to update Cluster Identity object %s %s: %w", obj.GetKind(), objKey, err)
	}

	l.Info("Cluster Identity object was successfully updated", "kind", obj.GetKind(), "name", objKey)
	return nil
}

func copyClusterIdentity(cred *kcmv1.Credential, source *unstructured.Unstructured) *unstructured.Unstructured {
	result := source.DeepCopy()
	if source.GetNamespace() != "" {
		result.SetNamespace(source.GetNamespace())
	}

	result.SetCreationTimestamp(metav1.Time{})
	result.SetFinalizers(nil)
	result.SetManagedFields(nil)
	result.SetOwnerReferences(nil)
	result.SetResourceVersion("")
	result.SetSelfLink("")
	result.SetUID("")

	result.SetLabels(map[string]string{
		kcmv1.KCMManagedLabelKey:           kcmv1.KCMManagedLabelValue,
		buildClusterIdentityLabelKey(cred): "true",
	})

	return result
}

// releaseClusterIdentity either removes the Cluster Identity object if no parent Credential object found or
// updates labels by removing k0rdent.mirantis.com/credential.<namespace>.<name> label.
func releaseClusterIdentity(ctx context.Context, rgnClient client.Client, cred *kcmv1.Credential, clIdty *unstructured.Unstructured) error {
	labels := clIdty.GetLabels()
	// delete the Credential label from this Cluster Identity
	delete(labels, buildClusterIdentityLabelKey(cred))

	shouldDelete := true
	for k := range labels {
		// if Cluster Identity is managed by another Credential, skip the deletion, only update labels
		if strings.HasPrefix(k, kcmv1.CredentialLabelKeyPrefix) {
			shouldDelete = false
			break
		}
	}
	if shouldDelete {
		if err := rgnClient.Delete(ctx, clIdty); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete Cluster Identity object of Kind %s: %s: %w", clIdty.GetKind(), objectKeyUnstructured(clIdty), err)
		}
		return nil
	}
	original := clIdty.DeepCopy()
	clIdty.SetLabels(labels)

	if err := rgnClient.Patch(ctx, clIdty, client.MergeFrom(original)); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to patch Cluster Identity object of Kind %s: %s: %w",
			clIdty.GetKind(), objectKeyUnstructured(clIdty), err)
	}
	return nil
}

func buildClusterIdentityLabelKey(cred *kcmv1.Credential) string {
	return strings.Join([]string{kcmv1.CredentialLabelKeyPrefix, cred.Namespace, cred.Name}, ".")
}

func objectKeyUnstructured(obj *unstructured.Unstructured) client.ObjectKey {
	if obj.GetNamespace() != "" {
		return client.ObjectKey{Namespace: obj.GetNamespace(), Name: obj.GetName()}
	}
	return client.ObjectKey{Name: obj.GetName()}
}

func objectKey(namespace, name string) string {
	if namespace != "" {
		return namespace + "/" + name
	}
	return name
}
