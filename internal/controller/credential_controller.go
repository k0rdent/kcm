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
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hmc "github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/K0rdent/kcm/internal/utils"
)

// CredentialReconciler reconciles a Credential object
type CredentialReconciler struct {
	client.Client
	SystemNamespace string
	syncPeriod      time.Duration
}

func (r *CredentialReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, err error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Credential reconcile start")

	cred := &hmc.Credential{}
	if err := r.Client.Get(ctx, req.NamespacedName, cred); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if err := utils.AddHMCComponentLabel(ctx, r.Client, cred); err != nil {
		l.Error(err, "adding component label")
		return ctrl.Result{}, err
	}

	defer func() {
		err = errors.Join(err, r.updateStatus(ctx, cred))
	}()

	clIdty := &unstructured.Unstructured{}
	clIdty.SetAPIVersion(cred.Spec.IdentityRef.APIVersion)
	clIdty.SetKind(cred.Spec.IdentityRef.Kind)
	clIdty.SetName(cred.Spec.IdentityRef.Name)
	clIdty.SetNamespace(cred.Spec.IdentityRef.Namespace)

	if err := r.Client.Get(ctx, client.ObjectKey{
		Name:      cred.Spec.IdentityRef.Name,
		Namespace: cred.Spec.IdentityRef.Namespace,
	}, clIdty); err != nil {
		errMsg := fmt.Sprintf("Failed to get ClusterIdentity %s: %s", cred.Spec.IdentityRef.Name, err)
		if apierrors.IsNotFound(err) {
			errMsg = fmt.Sprintf("ClusterIdentity %s not found", cred.Spec.IdentityRef.Name)
		}

		apimeta.SetStatusCondition(cred.GetConditions(), metav1.Condition{
			Type:    hmc.CredentialReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  hmc.FailedReason,
			Message: errMsg,
		})

		return ctrl.Result{}, err
	}

	if err := utils.AddHMCComponentLabel(ctx, r.Client, clIdty); err != nil {
		l.Error(err, "adding component label to the ClusterIdentity")
		return ctrl.Result{}, err
	}

	if err := r.updateAWSReferencedSecret(ctx, cred.Spec.IdentityRef); err != nil {
		l.Error(err, "adding component label to the Secret from the Identity Reference")
		return ctrl.Result{}, err
	}

	apimeta.SetStatusCondition(cred.GetConditions(), metav1.Condition{
		Type:    hmc.CredentialReadyCondition,
		Status:  metav1.ConditionTrue,
		Reason:  hmc.SucceededReason,
		Message: "Credential is ready",
	})

	return ctrl.Result{RequeueAfter: r.syncPeriod}, nil
}

func (r *CredentialReconciler) updateStatus(ctx context.Context, cred *hmc.Credential) error {
	cred.Status.Ready = false
	for _, cond := range cred.Status.Conditions {
		if cond.Type == hmc.CredentialReadyCondition && cond.Status == metav1.ConditionTrue {
			cred.Status.Ready = true
			break
		}
	}

	if err := r.Client.Status().Update(ctx, cred); err != nil {
		return fmt.Errorf("failed to update Credential %s/%s status: %w", cred.Namespace, cred.Name, err)
	}

	return nil
}

// updateAWSReferencedSecret updates referenced AWS*Identity Secret with the component label.
// The component label on such Secret is required for a proper management backup.
func (r *CredentialReconciler) updateAWSReferencedSecret(ctx context.Context, idRef *corev1.ObjectReference) error {
	// avoid "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2" dependency
	const (
		awsClusterControllerIdentityKind = "AWSClusterControllerIdentity"
		awsClusterStaticIdentityKind     = "AWSClusterStaticIdentity"
		awsClusterRoleIdentityKind       = "AWSClusterRoleIdentity"
	)

	switch idRef.Kind {
	case awsClusterControllerIdentityKind: // no secret refs
		return nil
	case awsClusterRoleIdentityKind:
		return r.updateAWSSecretFromClusterRoleIdentity(ctx, idRef)
	case awsClusterStaticIdentityKind:
		return r.updateAWSSecretFromClusterStaticIdentity(ctx, idRef)
	}

	return nil
}

func (r *CredentialReconciler) updateAWSSecretFromClusterRoleIdentity(ctx context.Context, initialReference *corev1.ObjectReference) error {
	const (
		awsClusterControllerIdentityKind = "AWSClusterControllerIdentity"
		awsClusterStaticIdentityKind     = "AWSClusterStaticIdentity"
	)

	kind, name := initialReference.Kind, initialReference.Name

	const limitRefs = 3 // consider an error if more nested refs
	for range limitRefs {
		clRoleIdentity := new(unstructured.Unstructured)
		clRoleIdentity.SetAPIVersion(initialReference.APIVersion)

		clRoleIdentity.SetKind(kind)
		clRoleIdentity.SetName(name)

		if err := r.Client.Get(ctx, client.ObjectKey{Name: name}, clRoleIdentity); err != nil { // cluster-scoped
			return fmt.Errorf("failed to get %s identity reference: %w", kind, err)
		}

		srcIdentityRefKind, ok, err := unstructured.NestedString(clRoleIdentity.Object, "spec", "sourceIdentityRef", "kind")
		if err != nil {
			return fmt.Errorf("failed to extract .spec.sourceIdentityRef.kind from %s %s: %w", clRoleIdentity.GetKind(), clRoleIdentity.GetName(), err)
		}
		if !ok {
			return nil // sanity
		}

		srcIdentityRefName, ok, err := unstructured.NestedString(clRoleIdentity.Object, "spec", "sourceIdentityRef", "name")
		if err != nil {
			return fmt.Errorf("failed to extract .spec.sourceIdentityRef.name from %s %s", clRoleIdentity.GetKind(), clRoleIdentity.GetName())
		}
		if !ok {
			return nil // sanity
		}

		kind, name = srcIdentityRefKind, srcIdentityRefName

		switch srcIdentityRefKind {
		case awsClusterControllerIdentityKind: // no secret refs
			return nil
		case awsClusterStaticIdentityKind:
			newReference := &corev1.ObjectReference{
				APIVersion: initialReference.APIVersion,
				Kind:       kind,
				Name:       name,
			}
			return r.updateAWSSecretFromClusterStaticIdentity(ctx, newReference)
		}
		// nested refs case, continue
	}

	return fmt.Errorf("failed to determine the secrets data from the %s %s identity reference", initialReference.Kind, initialReference.Name)
}

func (r *CredentialReconciler) updateAWSSecretFromClusterStaticIdentity(ctx context.Context, reference *corev1.ObjectReference) error {
	clStaticIdentity := new(unstructured.Unstructured)
	clStaticIdentity.SetAPIVersion(reference.APIVersion)
	clStaticIdentity.SetKind(reference.Kind)
	clStaticIdentity.SetName(reference.Name)

	if err := r.Client.Get(ctx, client.ObjectKey{Name: reference.Name}, clStaticIdentity); err != nil { // cluster-scoped
		return fmt.Errorf("failed to get %s identity reference: %w", reference.Kind, err)
	}

	secretName, ok, err := unstructured.NestedString(clStaticIdentity.Object, "spec", "secretRef")
	if err != nil {
		return fmt.Errorf("failed to extract .spec.secretRef from %s %s", clStaticIdentity.GetKind(), clStaticIdentity.GetName())
	}
	if !ok {
		return nil // nothing to do
	}

	key := client.ObjectKey{Name: secretName, Namespace: r.SystemNamespace}
	secret := new(corev1.Secret)
	if err := r.Client.Get(ctx, key, secret); err != nil {
		return fmt.Errorf("failed to get Secret %s referenced in %s %s: %w", key, clStaticIdentity.GetKind(), clStaticIdentity.GetName(), err)
	}

	if err := utils.AddHMCComponentLabel(ctx, r.Client, secret); err != nil {
		return fmt.Errorf("failed to add component label: %w", err)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CredentialReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.syncPeriod = 15 * time.Minute

	return ctrl.NewControllerManagedBy(mgr).
		For(&hmc.Credential{}).
		Complete(r)
}
