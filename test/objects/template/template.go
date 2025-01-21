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

package template

import (
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/K0rdent/kcm/api/v1alpha1"
)

const (
	DefaultName      = "template"
	DefaultNamespace = metav1.NamespaceDefault
)

type (
	Opt func(template Template)

	Template interface {
		client.Object
		GetHelmSpec() *v1alpha1.HelmSpec
		GetCommonStatus() *v1alpha1.TemplateStatusCommon
	}
)

func NewClusterTemplate(opts ...Opt) *v1alpha1.ClusterTemplate {
	t := &v1alpha1.ClusterTemplate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.GroupVersion.String(),
			Kind:       v1alpha1.ClusterTemplateKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      DefaultName,
			Namespace: DefaultNamespace,
		},
	}

	for _, o := range opts {
		o(t)
	}

	return t
}

func NewServiceTemplate(opts ...Opt) *v1alpha1.ServiceTemplate {
	t := &v1alpha1.ServiceTemplate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.GroupVersion.String(),
			Kind:       v1alpha1.ServiceTemplateKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      DefaultName,
			Namespace: DefaultNamespace,
		},
	}

	for _, o := range opts {
		o(t)
	}

	return t
}

func NewProviderTemplate(opts ...Opt) *v1alpha1.ProviderTemplate {
	t := &v1alpha1.ProviderTemplate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.GroupVersion.String(),
			Kind:       v1alpha1.ProviderTemplateKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: DefaultName,
		},
	}

	for _, o := range opts {
		o(t)
	}

	return t
}

func WithName(name string) Opt {
	return func(t Template) {
		t.SetName(name)
	}
}

func WithNamespace(namespace string) Opt {
	return func(t Template) {
		t.SetNamespace(namespace)
	}
}

func WithLabels(labels map[string]string) Opt {
	return func(t Template) {
		t.SetLabels(labels)
	}
}

func WithOwnerReference(ownerRef []metav1.OwnerReference) Opt {
	return func(t Template) {
		t.SetOwnerReferences(ownerRef)
	}
}

func ManagedByKCM() Opt {
	return func(template Template) {
		labels := template.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		labels[v1alpha1.KCMManagedLabelKey] = v1alpha1.KCMManagedLabelValue

		template.SetLabels(labels)
	}
}

func WithHelmSpec(helmSpec v1alpha1.HelmSpec) Opt {
	return func(t Template) {
		spec := t.GetHelmSpec()
		spec.ChartSpec = helmSpec.ChartSpec
		spec.ChartRef = helmSpec.ChartRef
	}
}

func WithServiceK8sConstraint(v string) Opt {
	return func(template Template) {
		switch tt := template.(type) {
		case *v1alpha1.ServiceTemplate:
			tt.Status.KubernetesConstraint = v
		default:
			panic(fmt.Sprintf("unexpected obj typed %T, expected *ServiceTemplate", tt))
		}
	}
}

func WithValidationStatus(validationStatus v1alpha1.TemplateValidationStatus) Opt {
	return func(t Template) {
		status := t.GetCommonStatus()
		status.TemplateValidationStatus = validationStatus
	}
}

func WithProvidersStatus(providers ...string) Opt {
	return func(t Template) {
		switch v := t.(type) {
		case *v1alpha1.ClusterTemplate:
			v.Status.Providers = providers
		case *v1alpha1.ProviderTemplate:
			v.Status.Providers = providers
		case *v1alpha1.ServiceTemplate:
			v.Status.Providers = providers
		}
	}
}

func WithConfigStatus(config string) Opt {
	return func(t Template) {
		status := t.GetCommonStatus()
		status.Config = &apiextensionsv1.JSON{
			Raw: []byte(config),
		}
	}
}

func WithProviderStatusCAPIContracts(coreAndProvidersContracts ...string) Opt {
	if len(coreAndProvidersContracts)&1 != 0 {
		panic("non even number of arguments")
	}

	return func(template Template) {
		if len(coreAndProvidersContracts) == 0 {
			return
		}

		pt, ok := template.(*v1alpha1.ProviderTemplate)
		if !ok {
			panic(fmt.Sprintf("unexpected type %T, expected ProviderTemplate", template))
		}

		if pt.Status.CAPIContracts == nil {
			pt.Status.CAPIContracts = make(v1alpha1.CompatibilityContracts)
		}

		for i := range len(coreAndProvidersContracts) / 2 {
			pt.Status.CAPIContracts[coreAndProvidersContracts[i*2]] = coreAndProvidersContracts[i*2+1]
		}
	}
}

func WithClusterStatusK8sVersion(v string) Opt {
	return func(template Template) {
		ct, ok := template.(*v1alpha1.ClusterTemplate)
		if !ok {
			panic(fmt.Sprintf("unexpected type %T, expected ClusterTemplate", template))
		}
		ct.Status.KubernetesVersion = v
	}
}
