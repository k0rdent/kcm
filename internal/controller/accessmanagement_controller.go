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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/record"
	labelsutil "github.com/K0rdent/kcm/internal/util/labels"
	ratelimitutil "github.com/K0rdent/kcm/internal/util/ratelimit"
)

// AccessManagementReconciler reconciles an AccessManagement object
type AccessManagementReconciler struct {
	client.Client
	SystemNamespace string
}

func (r *AccessManagementReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := ctrl.LoggerFrom(ctx)
	l.Info("Reconciling AccessManagement")

	management := &kcmv1.Management{}
	if err := r.Get(ctx, client.ObjectKey{Name: kcmv1.ManagementName}, management); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Management: %w", err)
	}
	if !management.DeletionTimestamp.IsZero() {
		l.Info("Management is being deleted, skipping AccessManagement reconciliation")
		return ctrl.Result{}, nil
	}

	accessMgmt := &kcmv1.AccessManagement{}
	if err := r.Get(ctx, req.NamespacedName, accessMgmt); err != nil {
		l.Error(err, "unable to fetch AccessManagement")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if updated, err := labelsutil.AddKCMComponentLabel(ctx, r.Client, accessMgmt); updated || err != nil {
		if err != nil {
			l.Error(err, "adding component label")
		}
		return ctrl.Result{}, err
	}

	errMsg := ""
	err := r.reconcileObj(ctx, accessMgmt)
	if err != nil {
		errMsg = err.Error()
	}
	accessMgmt.Status.ObservedGeneration = accessMgmt.Generation
	accessMgmt.Status.Error = errMsg

	return ctrl.Result{}, errors.Join(err, r.updateStatus(ctx, accessMgmt))
}

func (r *AccessManagementReconciler) reconcileObj(ctx context.Context, accessMgmt *kcmv1.AccessManagement) error {
	systemCtChains, managedCtChains, err := r.getCurrentTemplateChains(ctx, kcmv1.ClusterTemplateChainKind)
	if err != nil {
		return err
	}

	systemStChains, managedStChains, err := r.getCurrentTemplateChains(ctx, kcmv1.ServiceTemplateChainKind)
	if err != nil {
		return err
	}

	systemCredentials, managedCredentials, err := r.getCredentials(ctx)
	if err != nil {
		return err
	}

	keepCtChains := make(map[string]bool)
	keepStChains := make(map[string]bool)
	keepCredentials := make(map[string]bool)

	var errs error
	for _, rule := range accessMgmt.Spec.AccessRules {
		namespaces, err := getTargetNamespaces(ctx, r.Client, rule.TargetNamespaces)
		if err != nil {
			return err
		}

		for _, namespace := range namespaces {
			for _, ctChain := range rule.ClusterTemplateChains {
				keepCtChains[getNamespacedName(namespace, ctChain)] = true
				if systemCtChains[ctChain] == nil {
					errs = errors.Join(errs, fmt.Errorf("ClusterTemplateChain %s/%s is not found", r.SystemNamespace, ctChain))
					continue
				}

				created, err := r.createTemplateChain(ctx, systemCtChains[ctChain], namespace)
				if err != nil {
					r.warnf(accessMgmt, "ClusterTemplateChainCreationFailed", "Failed to create ClusterTemplateChain %s/%s: %v", namespace, ctChain, err)
					errs = errors.Join(errs, err)
					continue
				}
				if created {
					r.eventf(accessMgmt, "ClusterTemplateChainCreated", "Successfully created ClusterTemplateChain %s/%s", namespace, ctChain)
				}
			}
			for _, stChain := range rule.ServiceTemplateChains {
				keepStChains[getNamespacedName(namespace, stChain)] = true
				if systemStChains[stChain] == nil {
					errs = errors.Join(errs, fmt.Errorf("ServiceTemplateChain %s/%s is not found", r.SystemNamespace, stChain))
					continue
				}

				created, err := r.createTemplateChain(ctx, systemStChains[stChain], namespace)
				if err != nil {
					r.warnf(accessMgmt, "ServiceTemplateChainCreationFailed", "Failed to create ServiceTemplateChain %s/%s: %v", namespace, stChain, err)
					errs = errors.Join(errs, err)
					continue
				}
				if created {
					r.eventf(accessMgmt, "ServiceTemplateChainCreated", "Successfully created ServiceTemplateChain %s/%s", namespace, stChain)
				}
			}
			for _, credentialName := range rule.Credentials {
				keepCredentials[getNamespacedName(namespace, credentialName)] = true
				if systemCredentials[credentialName] == nil {
					errs = errors.Join(errs, fmt.Errorf("credential %s/%s is not found", r.SystemNamespace, credentialName))
					continue
				}

				created, err := r.createCredential(ctx, namespace, credentialName, systemCredentials[credentialName])
				if err != nil {
					r.warnf(accessMgmt, "CredentialCreationFailed", "Failed to create Credential %s/%s: %v", namespace, credentialName, err)
					errs = errors.Join(errs, err)
					continue
				}
				if created {
					r.eventf(accessMgmt, "CredentialCreated", "Successfully created Credential %s/%s", namespace, credentialName)
				}
			}
		}
	}

	managedObjects := append(append(managedCtChains, managedStChains...), managedCredentials...)
	for _, managedObject := range managedObjects {
		keep := false
		kind := managedObject.GetObjectKind().GroupVersionKind().Kind
		namespacedName := getNamespacedName(managedObject.GetNamespace(), managedObject.GetName())
		switch managedObject.GetObjectKind().GroupVersionKind().Kind {
		case kcmv1.ClusterTemplateChainKind:
			keep = keepCtChains[namespacedName]
		case kcmv1.ServiceTemplateChainKind:
			keep = keepStChains[namespacedName]
		case kcmv1.CredentialKind:
			keep = keepCredentials[namespacedName]
		default:
			errs = errors.Join(errs, fmt.Errorf("invalid kind. Supported kinds are %s, %s and %s", kcmv1.ClusterTemplateChainKind, kcmv1.ServiceTemplateChainKind, kcmv1.CredentialKind))
		}

		if !keep {
			deleted, err := r.deleteManagedObject(ctx, managedObject)
			if err != nil {
				r.warnf(accessMgmt, kind+"DeletionFailed", "Failed to delete %s %s: %v", kind, namespacedName, err)
				errs = errors.Join(errs, err)
				continue
			}
			if deleted {
				r.eventf(accessMgmt, kind+"Deleted", "Successfully deleted %s %s", kind, namespacedName)
			}
		}
	}

	if errs != nil {
		return errs
	}

	accessMgmt.Status.Current = accessMgmt.Spec.AccessRules
	return nil
}

func getNamespacedName(namespace, name string) string {
	return namespace + "/" + name
}

func (r *AccessManagementReconciler) getCurrentTemplateChains(ctx context.Context, templateChainKind string) (map[string]templateChain, []client.Object, error) {
	var templateChains []templateChain
	switch templateChainKind {
	case kcmv1.ClusterTemplateChainKind:
		ctChainList := &kcmv1.ClusterTemplateChainList{}
		err := r.List(ctx, ctChainList)
		if err != nil {
			return nil, nil, err
		}
		for _, chain := range ctChainList.Items {
			templateChains = append(templateChains, &chain)
		}
	case kcmv1.ServiceTemplateChainKind:
		stChainList := &kcmv1.ServiceTemplateChainList{}
		err := r.List(ctx, stChainList)
		if err != nil {
			return nil, nil, err
		}
		for _, chain := range stChainList.Items {
			templateChains = append(templateChains, &chain)
		}
	default:
		return nil, nil, fmt.Errorf("invalid TemplateChain kind. Supported kinds are %s and %s", kcmv1.ClusterTemplateChainKind, kcmv1.ServiceTemplateChainKind)
	}

	var (
		systemTemplateChains  = make(map[string]templateChain, len(templateChains))
		managedTemplateChains = make([]client.Object, 0, len(templateChains))
	)
	for _, chain := range templateChains {
		if chain.GetNamespace() == r.SystemNamespace {
			systemTemplateChains[chain.GetName()] = chain
			continue
		}

		if chain.GetLabels()[kcmv1.KCMManagedLabelKey] == kcmv1.KCMManagedLabelValue {
			managedTemplateChains = append(managedTemplateChains, chain)
		}
	}

	return systemTemplateChains, managedTemplateChains, nil
}

func (r *AccessManagementReconciler) getCredentials(ctx context.Context) (map[string]*kcmv1.CredentialSpec, []client.Object, error) {
	credentialList := &kcmv1.CredentialList{}
	err := r.List(ctx, credentialList)
	if err != nil {
		return nil, nil, err
	}
	var (
		systemCredentials  = make(map[string]*kcmv1.CredentialSpec, len(credentialList.Items))
		managedCredentials = make([]client.Object, 0, len(credentialList.Items))
	)
	for _, cred := range credentialList.Items {
		if cred.Namespace == r.SystemNamespace {
			systemCredentials[cred.Name] = &cred.Spec
			continue
		}

		if cred.GetLabels()[kcmv1.KCMManagedLabelKey] == kcmv1.KCMManagedLabelValue {
			managedCredentials = append(managedCredentials, &cred)
		}
	}
	return systemCredentials, managedCredentials, nil
}

func getTargetNamespaces(ctx context.Context, cl client.Client, targetNamespaces kcmv1.TargetNamespaces) ([]string, error) {
	if len(targetNamespaces.List) > 0 {
		return targetNamespaces.List, nil
	}
	var selector labels.Selector
	var err error
	if targetNamespaces.StringSelector != "" {
		selector, err = labels.Parse(targetNamespaces.StringSelector)
		if err != nil {
			return nil, err
		}
	} else {
		selector, err = metav1.LabelSelectorAsSelector(targetNamespaces.Selector)
		if err != nil {
			return nil, fmt.Errorf("failed to construct selector from the namespaces selector %s: %w", targetNamespaces.Selector, err)
		}
	}

	var (
		namespaces = new(corev1.NamespaceList)
		listOpts   = new(client.ListOptions)
	)
	if !selector.Empty() {
		listOpts.LabelSelector = selector
	}

	if err := cl.List(ctx, namespaces, listOpts); err != nil {
		return nil, err
	}

	result := make([]string, len(namespaces.Items))
	for i, ns := range namespaces.Items {
		result[i] = ns.Name
	}

	return result, nil
}

func (r *AccessManagementReconciler) createTemplateChain(ctx context.Context, source templateChain, targetNamespace string) (created bool, _ error) {
	l := ctrl.LoggerFrom(ctx)

	meta := metav1.ObjectMeta{
		Name:      source.GetName(),
		Namespace: targetNamespace,
		Labels: map[string]string{
			kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue,
		},
	}
	var target templateChain
	kind := source.GetObjectKind().GroupVersionKind().Kind
	switch kind {
	case kcmv1.ClusterTemplateChainKind:
		target = &kcmv1.ClusterTemplateChain{ObjectMeta: meta, Spec: *source.GetSpec()}
	case kcmv1.ServiceTemplateChainKind:
		target = &kcmv1.ServiceTemplateChain{ObjectMeta: meta, Spec: *source.GetSpec()}
	}

	if err := r.Create(ctx, target); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return false, nil
		}
		return false, err
	}
	l.Info(kind+" was successfully created", "target namespace", targetNamespace, "source name", source.GetName())
	return true, nil
}

func (r *AccessManagementReconciler) createCredential(ctx context.Context, namespace, name string, spec *kcmv1.CredentialSpec) (created bool, _ error) {
	l := ctrl.LoggerFrom(ctx)

	target := &kcmv1.Credential{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue,
			},
		},
		Spec: *spec,
	}
	if err := r.Create(ctx, target); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return false, nil
		}
		return false, err
	}
	l.Info("Credential was successfully created", "namespace", namespace, "name", name)
	return true, nil
}

func (r *AccessManagementReconciler) deleteManagedObject(ctx context.Context, obj client.Object) (deleted bool, _ error) {
	l := ctrl.LoggerFrom(ctx)

	if err := r.Delete(ctx, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	l.Info(obj.GetObjectKind().GroupVersionKind().Kind+" was successfully deleted", "namespace", obj.GetNamespace(), "name", obj.GetName())
	return true, nil
}

func (r *AccessManagementReconciler) updateStatus(ctx context.Context, accessMgmt *kcmv1.AccessManagement) error {
	if err := r.Status().Update(ctx, accessMgmt); err != nil {
		return fmt.Errorf("failed to update status for AccessManagement %s: %w", accessMgmt.Name, err)
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AccessManagementReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.TypedOptions[ctrl.Request]{
			RateLimiter: ratelimitutil.DefaultFastSlow(),
		}).
		For(&kcmv1.AccessManagement{}).
		Complete(r)
}

func (*AccessManagementReconciler) eventf(am *kcmv1.AccessManagement, reason, message string, args ...any) {
	record.Eventf(am, am.Generation, reason, message, args...)
}

func (*AccessManagementReconciler) warnf(am *kcmv1.AccessManagement, reason, message string, args ...any) {
	record.Warnf(am, am.Generation, reason, message, args...)
}
