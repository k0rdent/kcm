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

package webhook

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	hmcv1alpha1 "github.com/K0rdent/kcm/api/v1alpha1"
)

type ManagementValidator struct {
	client.Client
}

var errManagementDeletionForbidden = errors.New("management deletion is forbidden")

func (v *ManagementValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	v.Client = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr).
		For(&hmcv1alpha1.Management{}).
		WithValidator(v).
		WithDefaulter(v).
		Complete()
}

var (
	_ webhook.CustomValidator = &ManagementValidator{}
	_ webhook.CustomDefaulter = &ManagementValidator{}
)

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (v *ManagementValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	mgmt, ok := obj.(*hmcv1alpha1.Management)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected Management but got a %T", obj))
	}
	if err := validateRelease(ctx, v.Client, mgmt.Spec.Release); err != nil {
		return nil,
			apierrors.NewInvalid(mgmt.GroupVersionKind().GroupKind(), mgmt.Name, field.ErrorList{
				field.Forbidden(field.NewPath("spec", "release"), err.Error()),
			})
	}
	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (v *ManagementValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	const invalidMgmtMsg = "the Management is invalid"

	newMgmt, ok := newObj.(*hmcv1alpha1.Management)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected Management but got a %T", newObj))
	}

	oldMgmt, ok := oldObj.(*hmcv1alpha1.Management)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected Management but got a %T", oldObj))
	}

	if oldMgmt.Spec.Release != newMgmt.Spec.Release {
		if err := validateRelease(ctx, v.Client, newMgmt.Spec.Release); err != nil {
			return nil,
				apierrors.NewInvalid(newMgmt.GroupVersionKind().GroupKind(), newMgmt.Name, field.ErrorList{
					field.Forbidden(field.NewPath("spec", "release"), err.Error()),
				})
		}
	}

	if err := checkComponentsRemoval(ctx, v.Client, oldMgmt, newMgmt); err != nil {
		return admission.Warnings{"Some of the providers cannot be removed"},
			apierrors.NewInvalid(newMgmt.GroupVersionKind().GroupKind(), newMgmt.Name, field.ErrorList{
				field.Forbidden(field.NewPath("spec", "providers"), err.Error()),
			})
	}

	incompatibleContracts, err := getIncompatibleContracts(ctx, v, newMgmt)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", invalidMgmtMsg, err)
	}

	if incompatibleContracts != "" {
		return admission.Warnings{"The Management object has incompatible CAPI contract versions in ProviderTemplates"}, fmt.Errorf("%s: %s", invalidMgmtMsg, incompatibleContracts)
	}

	return nil, nil
}

func checkComponentsRemoval(ctx context.Context, cl client.Client, oldMgmt, newMgmt *hmcv1alpha1.Management) error {
	removedComponents := []hmcv1alpha1.Provider{}
	for _, oldComp := range oldMgmt.Spec.Providers {
		if !slices.ContainsFunc(newMgmt.Spec.Providers, func(newComp hmcv1alpha1.Provider) bool { return oldComp.Name == newComp.Name }) {
			removedComponents = append(removedComponents, oldComp)
		}
	}

	if len(removedComponents) == 0 {
		return nil
	}

	release := new(hmcv1alpha1.Release)
	if err := cl.Get(ctx, client.ObjectKey{Name: newMgmt.Spec.Release}, release); err != nil {
		return fmt.Errorf("failed to get Release %s: %w", newMgmt.Spec.Release, err)
	}

	removedProvidersSet := make(map[string]struct{})
	for _, m := range removedComponents {
		tplRef := m.Template
		if tplRef == "" {
			tplRef = release.ProviderTemplate(m.Name)
		}

		// it does not matter if component has been successfully installed
		prTpl := new(hmcv1alpha1.ProviderTemplate)
		if err := cl.Get(ctx, client.ObjectKey{Name: tplRef}, prTpl); err != nil {
			return fmt.Errorf("failed to get ProviderTemplate %s: %w", tplRef, err)
		}

		for _, pn := range prTpl.Status.Providers {
			removedProvidersSet[pn] = struct{}{}
		}
	}

	if len(removedProvidersSet) == 0 { // sanity
		return nil
	}

	for providerName := range removedProvidersSet {
		clusterTemplates := new(hmcv1alpha1.ClusterTemplateList)
		if err := cl.List(ctx, clusterTemplates, client.MatchingFields{hmcv1alpha1.ClusterTemplateProvidersIndexKey: providerName}); err != nil {
			return fmt.Errorf("failed to list ClusterTemplates: %w", err)
		}

		if len(clusterTemplates.Items) == 0 {
			continue
		}

		for _, cltpl := range clusterTemplates.Items {
			mcls := new(hmcv1alpha1.ClusterDeploymentList)
			if err := cl.List(ctx, mcls,
				client.MatchingFields{hmcv1alpha1.ClusterDeploymentTemplateIndexKey: cltpl.Name},
				client.Limit(1)); err != nil {
				return fmt.Errorf("failed to list ClusterDeployments: %w", err)
			}

			if len(mcls.Items) == 0 {
				continue
			}

			return fmt.Errorf("provider %s is required by at least one ClusterDeployment (%s) and cannot be removed from the Management %s", providerName, client.ObjectKeyFromObject(&mcls.Items[0]), newMgmt.Name)
		}
	}

	return nil
}

func getIncompatibleContracts(ctx context.Context, cl client.Client, mgmt *hmcv1alpha1.Management) (string, error) {
	release := new(hmcv1alpha1.Release)
	if err := cl.Get(ctx, client.ObjectKey{Name: mgmt.Spec.Release}, release); err != nil {
		// TODO: probably we do not want this skip if extra checks will be introduced
		if apierrors.IsNotFound(err) && (mgmt.Spec.Core == nil || mgmt.Spec.Core.CAPI.Template == "") {
			return "", nil // nothing to do
		}

		return "", fmt.Errorf("failed to get Release %s: %w", mgmt.Spec.Release, err)
	}

	capiTplName := release.Spec.CAPI.Template
	if mgmt.Spec.Core != nil && mgmt.Spec.Core.CAPI.Template != "" {
		capiTplName = mgmt.Spec.Core.CAPI.Template
	}

	capiTpl := new(hmcv1alpha1.ProviderTemplate)
	if err := cl.Get(ctx, client.ObjectKey{Name: capiTplName}, capiTpl); err != nil {
		return "", fmt.Errorf("failed to get ProviderTemplate %s: %w", capiTplName, err)
	}

	if len(capiTpl.Status.CAPIContracts) == 0 {
		return "", nil // nothing to validate against
	}

	if !capiTpl.Status.Valid {
		return "", fmt.Errorf("not valid ProviderTemplate %s", capiTpl.Name)
	}

	incompatibleContracts := strings.Builder{}
	for _, p := range mgmt.Spec.Providers {
		tplName := p.Template
		if tplName == "" {
			tplName = release.ProviderTemplate(p.Name)
		}

		if tplName == capiTpl.Name { // skip capi itself
			continue
		}

		pTpl := new(hmcv1alpha1.ProviderTemplate)
		if err := cl.Get(ctx, client.ObjectKey{Name: tplName}, pTpl); err != nil {
			return "", fmt.Errorf("failed to get ProviderTemplate %s: %w", tplName, err)
		}

		if len(pTpl.Status.CAPIContracts) == 0 {
			continue
		}

		if !pTpl.Status.Valid {
			return "", fmt.Errorf("not valid ProviderTemplate %s", tplName)
		}

		for capiVersion := range pTpl.Status.CAPIContracts {
			if _, ok := capiTpl.Status.CAPIContracts[capiVersion]; !ok {
				_, _ = incompatibleContracts.WriteString(fmt.Sprintf("core CAPI contract versions does not support %s version in the ProviderTemplate %s, ", capiVersion, pTpl.Name))
			}
		}
	}

	return strings.TrimSuffix(incompatibleContracts.String(), ", "), nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (v *ManagementValidator) ValidateDelete(ctx context.Context, _ runtime.Object) (admission.Warnings, error) {
	clusterDeployments := &hmcv1alpha1.ClusterDeploymentList{}
	err := v.Client.List(ctx, clusterDeployments, client.Limit(1))
	if err != nil {
		return nil, err
	}
	if len(clusterDeployments.Items) > 0 {
		return admission.Warnings{"The Management object can't be removed if ClusterDeployment objects still exist"}, errManagementDeletionForbidden
	}
	return nil, nil
}

func validateRelease(ctx context.Context, cl client.Client, releaseName string) error {
	release := &hmcv1alpha1.Release{}
	if err := cl.Get(ctx, client.ObjectKey{Name: releaseName}, release); err != nil {
		return err
	}
	if !release.Status.Ready {
		return fmt.Errorf("release \"%s\" status is not ready", releaseName)
	}
	return nil
}

// Default implements webhook.Defaulter so a webhook will be registered for the type.
func (*ManagementValidator) Default(_ context.Context, obj runtime.Object) error {
	mgmt, ok := obj.(*hmcv1alpha1.Management)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected Management but got a %T", obj))
	}

	if mgmt.Spec.Backup.Enabled && mgmt.Spec.Backup.Schedule == "" {
		mgmt.Spec.Backup.Schedule = "0 */6 * * *" // every 6h
	}

	return nil
}
