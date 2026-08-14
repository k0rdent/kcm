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

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

var errAccessManagementDeletionForbidden = errors.New("AccessManagement deletion is forbidden")

// AccessManagementValidator validates and defaults AccessManagement objects.
//
// Its Default method also carries the automatic migration of the deprecated
// one-field-per-Kind AccessRule selectors (ClusterTemplateChains, ServiceTemplateChains,
// Credentials, ClusterAuthentications, DataSources, ClusterAuditPolicies) into the generic
// Resources field, so existing manifests keep working unmodified while the controller only
// ever has to deal with the new shape.
type AccessManagementValidator struct {
	client.Client

	// RESTMapper is used to resolve the scope (namespaced vs cluster-scoped) of a
	// ResourceRule's Kind at admission time. Populated by SetupWebhookWithManager; left unset
	// only in tests that don't exercise the scope check.
	RESTMapper apimeta.RESTMapper

	SystemNamespace string
}

func (v *AccessManagementValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	v.Client = mgr.GetClient()
	v.RESTMapper = mgr.GetRESTMapper()
	return ctrl.NewWebhookManagedBy(mgr, &kcmv1.AccessManagement{}).
		WithValidator(v).
		WithDefaulter(v).
		Complete()
}

var (
	_ admission.Validator[*kcmv1.AccessManagement] = &AccessManagementValidator{}
	_ admission.Defaulter[*kcmv1.AccessManagement] = &AccessManagementValidator{}
)

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (v *AccessManagementValidator) ValidateCreate(ctx context.Context, am *kcmv1.AccessManagement) (admission.Warnings, error) {
	itemsList := &metav1.PartialObjectMetadataList{}
	itemsList.SetGroupVersionKind(kcmv1.GroupVersion.WithKind(kcmv1.AccessManagementKind))

	if err := v.List(ctx, itemsList, client.Limit(1)); err != nil {
		return nil, err
	}

	if len(itemsList.Items) > 0 {
		return nil, errors.New("AccessManagement object already exists")
	}

	return nil, v.validateAccessRules(am.Spec.AccessRules)
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (v *AccessManagementValidator) ValidateUpdate(_ context.Context, _, newAM *kcmv1.AccessManagement) (admission.Warnings, error) {
	return nil, v.validateAccessRules(newAM.Spec.AccessRules)
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (v *AccessManagementValidator) ValidateDelete(ctx context.Context, _ *kcmv1.AccessManagement) (admission.Warnings, error) {
	partialList := &metav1.PartialObjectMetadataList{}
	partialList.SetGroupVersionKind(kcmv1.GroupVersion.WithKind(kcmv1.ManagementKind))

	if err := v.List(ctx, partialList, client.Limit(1)); err != nil {
		return nil, fmt.Errorf("failed to list Management objects: %w", err)
	}

	if len(partialList.Items) > 0 {
		mgmt := partialList.Items[0]
		if mgmt.DeletionTimestamp == nil {
			return nil, errAccessManagementDeletionForbidden
		}
	}

	return nil, nil
}

// Default implements webhook.Defaulter so a webhook will be registered for the type.
//
// It migrates any deprecated one-field-per-Kind AccessRule selectors still populated into
// equivalent Resources entries, and defaults the APIVersion of Resources entries that omit it.
// Re-running against an already-migrated object is a no-op.
func (*AccessManagementValidator) Default(_ context.Context, am *kcmv1.AccessManagement) error {
	migrated, changed := kcmv1.MigrateAccessRules(am.Spec.AccessRules)
	if changed {
		am.Spec.AccessRules = migrated
	}

	return nil
}

// validateAccessRules validates the generic Resources entries of every AccessRule: the
// deprecated one-field-per-Kind selectors always reference one of six known, safe, namespaced
// built-in Kinds, so they require no such validation.
func (v *AccessManagementValidator) validateAccessRules(rules []kcmv1.AccessRule) error {
	var errs error
	for i, rule := range rules {
		for j, res := range rule.Resources {
			if err := v.validateResourceRule(res); err != nil {
				errs = errors.Join(errs, fmt.Errorf("accessRules[%d].resources[%d]: %w", i, j, err))
			}
		}
	}

	return errs
}

func (v *AccessManagementValidator) validateResourceRule(res kcmv1.ResourceRule) error {
	gvk, err := res.GroupVersionKind()
	if err != nil {
		return err
	}

	if v.RESTMapper == nil {
		return nil
	}

	mapping, err := v.RESTMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return fmt.Errorf("failed to resolve %s (ensure the CRD is installed): %w", gvk, err)
	}

	if mapping.Scope.Name() != apimeta.RESTScopeNameNamespace {
		return fmt.Errorf("%s is cluster-scoped and cannot be distributed into target namespaces by AccessManagement", gvk)
	}

	return nil
}
