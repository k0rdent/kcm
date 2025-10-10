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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/controller/credential"
	"github.com/K0rdent/kcm/internal/record"
	kubeutil "github.com/K0rdent/kcm/internal/util/kube"
	labelsutil "github.com/K0rdent/kcm/internal/util/labels"
	ratelimitutil "github.com/K0rdent/kcm/internal/util/ratelimit"
	schemeutil "github.com/K0rdent/kcm/internal/util/scheme"
)

// CredentialReconciler reconciles a Credential object
type CredentialReconciler struct {
	MgmtClient      client.Client
	SystemNamespace string
	syncPeriod      time.Duration
}

func (r *CredentialReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, err error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Credential reconcile start")

	management := &kcmv1.Management{}
	if err := r.MgmtClient.Get(ctx, client.ObjectKey{Name: kcmv1.ManagementName}, management); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Management: %w", err)
	}
	if !management.DeletionTimestamp.IsZero() {
		l.Info("Management is being deleted, skipping Credential reconciliation")
		return ctrl.Result{}, nil
	}

	cred := &kcmv1.Credential{}
	if err := r.MgmtClient.Get(ctx, req.NamespacedName, cred); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if updated, err := labelsutil.AddKCMComponentLabel(ctx, r.MgmtClient, cred); updated || err != nil {
		if err != nil {
			l.Error(err, "adding component label")
		}
		return ctrl.Result{}, err
	}

	defer func() {
		if err != nil {
			r.setReadyCondition(cred, err.Error())
		}
		err = errors.Join(err, r.updateStatus(ctx, cred))
	}()

	rgnClient := r.MgmtClient
	if cred.Spec.Region != "" {
		var failedMsg string
		rgn := &kcmv1.Region{}
		if err = r.MgmtClient.Get(ctx, client.ObjectKey{Name: cred.Spec.Region}, rgn); err != nil {
			failedMsg = fmt.Sprintf("Failed to get Region %s: %v", cred.Spec.Region, err)
		}
		if !rgn.DeletionTimestamp.IsZero() {
			failedMsg = fmt.Sprintf("Region %s is deleting", rgn.Name)
		}
		if failedMsg != "" {
			apimeta.SetStatusCondition(cred.GetConditions(), metav1.Condition{
				Type:               kcmv1.CredentialReadyCondition,
				Status:             metav1.ConditionFalse,
				Reason:             kcmv1.FailedReason,
				ObservedGeneration: cred.Generation,
				Message:            failedMsg,
			})
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}

		rgnClient, _, err = kubeutil.GetRegionalClient(ctx, r.MgmtClient, r.SystemNamespace, rgn, schemeutil.GetRegionalScheme)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	err = credential.CopyClusterIdentities(ctx, r.MgmtClient, rgnClient, cred, r.SystemNamespace)
	if err != nil {
		return ctrl.Result{}, err
	}

	clIdty := &unstructured.Unstructured{}
	clIdty.SetAPIVersion(cred.Spec.IdentityRef.APIVersion)
	clIdty.SetKind(cred.Spec.IdentityRef.Kind)
	clIdty.SetName(cred.Spec.IdentityRef.Name)
	clIdty.SetNamespace(cred.Spec.IdentityRef.Namespace)

	if err := rgnClient.Get(ctx, client.ObjectKey{
		Name:      cred.Spec.IdentityRef.Name,
		Namespace: cred.Spec.IdentityRef.Namespace,
	}, clIdty); err != nil {
		errMsg := fmt.Sprintf("Failed to get ClusterIdentity object of Kind=%s %s/%s: %s",
			cred.Spec.IdentityRef.Kind, cred.Spec.IdentityRef.Namespace, cred.Spec.IdentityRef.Name, err)
		if apierrors.IsNotFound(err) {
			errMsg = fmt.Sprintf("ClusterIdentity object of Kind=%s %s/%s not found",
				cred.Spec.IdentityRef.Kind, cred.Spec.IdentityRef.Namespace, cred.Spec.IdentityRef.Name)
		}

		if apimeta.SetStatusCondition(cred.GetConditions(), metav1.Condition{
			Type:               kcmv1.CredentialReadyCondition,
			Status:             metav1.ConditionFalse,
			Reason:             kcmv1.FailedReason,
			ObservedGeneration: cred.Generation,
			Message:            errMsg,
		}) {
			record.Warn(cred, cred.Generation, "MissingClusterIdentity", errMsg)
		}

		return ctrl.Result{}, err
	}

	apimeta.SetStatusCondition(cred.GetConditions(), metav1.Condition{
		Type:               kcmv1.CredentialReadyCondition,
		Status:             metav1.ConditionTrue,
		Reason:             kcmv1.SucceededReason,
		ObservedGeneration: cred.Generation,
		Message:            "Credential is ready",
	})

	return ctrl.Result{RequeueAfter: r.syncPeriod}, nil
}

// setReadyCondition updates the Credential resource's "Ready" condition
func (*CredentialReconciler) setReadyCondition(cred *kcmv1.Credential, errMsg string) {
	readyCond := metav1.Condition{
		Type:               kcmv1.CredentialReadyCondition,
		ObservedGeneration: cred.Generation,
		Status:             metav1.ConditionTrue,
		Reason:             kcmv1.SucceededReason,
		Message:            "Credential is ready",
	}
	if errMsg != "" {
		readyCond.Status = metav1.ConditionFalse
		readyCond.Reason = kcmv1.FailedReason
		readyCond.Message = errMsg
	}
	apimeta.SetStatusCondition(&cred.Status.Conditions, readyCond)
}

func (r *CredentialReconciler) updateStatus(ctx context.Context, cred *kcmv1.Credential) error {
	cred.Status.Ready = false
	for _, cond := range cred.Status.Conditions {
		if cond.Type == kcmv1.CredentialReadyCondition && cond.Status == metav1.ConditionTrue {
			cred.Status.Ready = true
			break
		}
	}

	if err := r.MgmtClient.Status().Update(ctx, cred); err != nil {
		return fmt.Errorf("failed to update Credential %s/%s status: %w", cred.Namespace, cred.Name, err)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CredentialReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.syncPeriod = 15 * time.Minute

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimitutil.DefaultFastSlow(),
		}).
		For(&kcmv1.Credential{}).
		Watches(&kcmv1.Region{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []ctrl.Request {
			creds := new(kcmv1.CredentialList)
			if err := r.MgmtClient.List(ctx, creds, client.MatchingFields{kcmv1.CredentialRegionIndexKey: o.GetName()}); err != nil {
				return nil
			}

			requests := make([]ctrl.Request, 0, len(creds.Items))
			for _, cred := range creds.Items {
				requests = append(requests, ctrl.Request{
					NamespacedName: client.ObjectKeyFromObject(&cred),
				})
			}

			return requests
		}), builder.WithPredicates(predicate.Funcs{
			GenericFunc: func(event.TypedGenericEvent[client.Object]) bool { return false },
			UpdateFunc: func(tue event.TypedUpdateEvent[client.Object]) bool {
				oldRegion, ok := tue.ObjectOld.(*kcmv1.Region)
				if !ok {
					return false
				}

				newRegion, ok := tue.ObjectNew.(*kcmv1.Region)
				if !ok {
					return false
				}

				oldReady := apimeta.FindStatusCondition(oldRegion.Status.Conditions, kcmv1.ReadyCondition)
				newReady := apimeta.FindStatusCondition(newRegion.Status.Conditions, kcmv1.ReadyCondition)
				return oldReady == nil || newReady == nil || oldReady.Status != newReady.Status
			},
		})).
		Complete(r)
}
