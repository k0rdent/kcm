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

	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	accessmanagementutil "github.com/K0rdent/kcm/internal/util/accessmanagement"
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

	warnings, err := v.validateAccessRules(am.Spec.AccessRules)
	if err != nil {
		return warnings, err
	}

	removalWarnings, err := v.validateClusterTemplateRemovals(ctx, nil, am)
	return append(warnings, removalWarnings...), err
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (v *AccessManagementValidator) ValidateUpdate(ctx context.Context, oldAM, newAM *kcmv1.AccessManagement) (admission.Warnings, error) {
	warnings, err := v.validateAccessRules(newAM.Spec.AccessRules)
	if err != nil {
		return warnings, err
	}

	// rules left as they are remove nothing that was not already pending, so there is nothing to
	// plan: this also keeps metadata-only updates, the controller's own included, from failing on
	// stored rules that cannot be planned
	if accessRulesUnchanged(oldAM, newAM) {
		return warnings, nil
	}

	// the status is the controller's record of the Kinds it still handles, so it is read from the
	// stored object rather than from whatever the update carries
	updated := *newAM
	updated.Status = oldAM.Status

	removalWarnings, err := v.validateClusterTemplateRemovals(ctx, oldAM, &updated)
	return append(warnings, removalWarnings...), err
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
	am.MigrateAccessRules()
	return nil
}

// validateClusterTemplateRemovals runs the ClusterTemplate deletion validation against every
// ClusterTemplate that reconciling am would delete, worked out by the same plan the controller
// acts on. An update whose removals the controller could not carry out is rejected up front,
// rather than accepted and then left failing in the AccessManagement status for as long as, say,
// a ClusterDeployment still uses the ClusterTemplate.
//
// Only the removals am introduces over old, the stored object (nil on create), are validated: one
// old already had pending, such as a copy left behind by a namespace relabeled out of a selector,
// is not this update's doing, and must not get in the way of every unrelated update, the
// controller's own metadata updates included.
func (v *AccessManagementValidator) validateClusterTemplateRemovals(ctx context.Context, old, am *kcmv1.AccessManagement) (admission.Warnings, error) {
	toRemove, err := accessmanagementutil.ClusterTemplatesToRemove(ctx, v.Client, v.SystemNamespace, am)
	if err != nil {
		return nil, fmt.Errorf("failed to find the ClusterTemplates the AccessManagement would remove: %w", err)
	}

	if old != nil && len(toRemove) > 0 {
		// an old spec that cannot be planned, such as one with an invalid selector, has nothing
		// pending to go by: every removal is validated then, and the update fixing it still goes
		// through
		if pending, err := accessmanagementutil.ClusterTemplatesToRemove(ctx, v.Client, v.SystemNamespace, old); err == nil {
			pendingKeys := make(map[client.ObjectKey]struct{}, len(pending))
			for _, template := range pending {
				pendingKeys[client.ObjectKeyFromObject(template)] = struct{}{}
			}

			toRemove = slices.DeleteFunc(toRemove, func(template *kcmv1.ClusterTemplate) bool {
				_, ok := pendingKeys[client.ObjectKeyFromObject(template)]
				return ok
			})
		}
	}

	var (
		templateValidator = newClusterTemplateValidator(v.Client, v.SystemNamespace)
		warnings          admission.Warnings
		errs              error
	)
	for _, template := range toRemove {
		templateWarnings, err := templateValidator.ValidateDelete(ctx, template)
		switch {
		case err == nil:
			continue
		case errors.Is(err, errTemplateDeletionForbidden):
			for _, warning := range templateWarnings {
				warnings = append(warnings, fmt.Sprintf("%s/%s: %s", template.Namespace, template.Name, warning))
			}
			errs = errors.Join(errs, fmt.Errorf("the update would remove the ClusterTemplate %s/%s: %w", template.Namespace, template.Name, err))
		default:
			// the check itself failed, e.g. a lookup it makes: not a verdict on the removal
			errs = errors.Join(errs, fmt.Errorf("failed to check whether the ClusterTemplate %s/%s can be removed: %w", template.Namespace, template.Name, err))
		}
	}

	return warnings, errs
}

// accessRulesUnchanged reports whether oldAM and newAM carry the same access rules once the
// deprecated selectors of both are migrated and defaulted, the way the mutating webhook does on
// write: a stored object written while it was unavailable still matches its migrated update.
func accessRulesUnchanged(oldAM, newAM *kcmv1.AccessManagement) bool {
	oldMigrated, newMigrated := oldAM.DeepCopy(), newAM.DeepCopy()
	oldMigrated.MigrateAccessRules()
	newMigrated.MigrateAccessRules()

	return equality.Semantic.DeepEqual(oldMigrated.Spec.AccessRules, newMigrated.Spec.AccessRules)
}

// validateAccessRules validates the generic Resources entries of every AccessRule: the
// deprecated one-field-per-Kind selectors always reference one of six known, safe, namespaced
// built-in Kinds, so they require no such validation.
//
// Listing the system namespace as a target is warned about rather than rejected: the controller
// skips it, and rejecting it outright would break existing objects on upgrade.
func (v *AccessManagementValidator) validateAccessRules(rules []kcmv1.AccessRule) (admission.Warnings, error) {
	var (
		warnings admission.Warnings
		errs     error
	)

	for i, rule := range rules {
		if v.SystemNamespace != "" && slices.Contains(rule.TargetNamespaces.List, v.SystemNamespace) {
			warnings = append(warnings, fmt.Sprintf(
				"accessRules[%d].targetNamespaces.list: %s is the KCM system namespace and will be skipped; objects are never distributed into the namespace they are read from",
				i, v.SystemNamespace,
			))
		}

		for j, res := range rule.Resources {
			if err := v.validateResourceRule(res); err != nil {
				errs = errors.Join(errs, fmt.Errorf("accessRules[%d].resources[%d]: %w", i, j, err))
			}
		}
	}

	return warnings, errs
}

func (v *AccessManagementValidator) validateResourceRule(res kcmv1.ResourceRule) error {
	gk := res.GroupKind()

	if v.RESTMapper == nil {
		return nil
	}

	mapping, err := v.RESTMapper.RESTMapping(gk)
	if err != nil {
		return fmt.Errorf("failed to resolve %s (ensure the CRD is installed): %w", gk, err)
	}

	if mapping.Scope.Name() != apimeta.RESTScopeNameNamespace {
		return fmt.Errorf("%s is cluster-scoped and cannot be distributed into target namespaces by AccessManagement", gk)
	}

	return nil
}
