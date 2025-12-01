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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

var errInvalidTemplateChainSpec = errors.New("the template chain spec is invalid")

type ClusterTemplateChainValidator struct {
	client.Client
	SystemNamespace string
}

func (in *ClusterTemplateChainValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	in.Client = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr).
		For(&kcmv1.ClusterTemplateChain{}).
		WithValidator(in).
		Complete()
}

var _ webhook.CustomValidator = &ClusterTemplateChainValidator{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (in *ClusterTemplateChainValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	chain, ok := obj.(*kcmv1.ClusterTemplateChain)
	if !ok {
		return admission.Warnings{"Wrong object"}, apierrors.NewBadRequest(fmt.Sprintf("expected ClusterTemplateChain but got a %T", obj))
	}

	if warnings, ok := chain.Spec.IsValid(); !ok {
		return warnings, errInvalidTemplateChainSpec
	}

	if chain.Labels[kcmv1.KCMManagedLabelKey] != kcmv1.KCMManagedLabelValue || chain.Namespace == in.SystemNamespace { // validate only unmanged or system
		if errs := validateChainsTemplates(ctx, in.Client, chain.Namespace, chain.Spec, kcmv1.ClusterTemplateKind); len(errs) > 0 {
			return nil, apierrors.NewInvalid(chain.GroupVersionKind().GroupKind(), chain.Name, errs)
		}
	}

	return nil, nil
}

func validateChainsTemplates(ctx context.Context, cl client.Client, namespace string, chainSpec kcmv1.TemplateChainSpec, templatesKind string) field.ErrorList {
	var errs field.ErrorList
	for i, st := range chainSpec.SupportedTemplates {
		obj := new(metav1.PartialObjectMetadata)
		obj.SetGroupVersionKind(kcmv1.GroupVersion.WithKind(templatesKind))
		if err := cl.Get(ctx, client.ObjectKey{Name: st.Name, Namespace: namespace}, obj); err != nil {
			path := field.NewPath("spec", "supportedTemplates").Index(i).Child("name")

			if apierrors.IsNotFound(err) {
				errs = append(errs, field.NotFound(path, st.Name))
			} else {
				errs = append(errs, field.InternalError(path, err))
			}
		}
	}

	return errs
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (*ClusterTemplateChainValidator) ValidateUpdate(_ context.Context, _, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (*ClusterTemplateChainValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

type ServiceTemplateChainValidator struct {
	client.Client
	SystemNamespace string
}

func (in *ServiceTemplateChainValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	in.Client = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr).
		For(&kcmv1.ServiceTemplateChain{}).
		WithValidator(in).
		Complete()
}

var _ webhook.CustomValidator = &ServiceTemplateChainValidator{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (in *ServiceTemplateChainValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	chain, ok := obj.(*kcmv1.ServiceTemplateChain)
	if !ok {
		return admission.Warnings{"Wrong object"}, apierrors.NewBadRequest(fmt.Sprintf("expected ServiceTemplateChain but got a %T", obj))
	}

	if warnings, ok := chain.Spec.IsValid(); !ok {
		return warnings, errInvalidTemplateChainSpec
	}

	if chain.Labels[kcmv1.KCMManagedLabelKey] != kcmv1.KCMManagedLabelValue || chain.Namespace == in.SystemNamespace { // validate only unmanged or system
		if errs := validateChainsTemplates(ctx, in.Client, chain.Namespace, chain.Spec, kcmv1.ServiceTemplateKind); len(errs) > 0 {
			return nil, apierrors.NewInvalid(chain.GroupVersionKind().GroupKind(), chain.Name, errs)
		}
	}

	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (*ServiceTemplateChainValidator) ValidateUpdate(_ context.Context, _, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (*ServiceTemplateChainValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}
