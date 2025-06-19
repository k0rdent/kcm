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

package v1alpha1

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	helmcontrollerv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	chartAnnoCAPIPrefix = "cluster.x-k8s.io/"

	DefaultRepoName = "kcm-templates"
)

var DefaultSourceRef = sourcev1.LocalHelmChartSourceReference{
	Kind: sourcev1.HelmRepositoryKind,
	Name: DefaultRepoName,
}

// +kubebuilder:validation:XValidation:rule="(has(self.chartSpec) ? (!has(self.chartSource) && !has(self.chartRef)): true)",message="chartSpec, chartSource and chartRef are mutually exclusive"
// +kubebuilder:validation:XValidation:rule="(has(self.chartSource) ? (!has(self.chartSpec) && !has(self.chartRef)): true)",message="chartSpec, chartSource and chartRef are mutually exclusive"
// +kubebuilder:validation:XValidation:rule="(has(self.chartRef) ? (!has(self.chartSpec) && !has(self.chartSource)): true)",message="chartSpec, chartSource and chartRef are mutually exclusive"
// +kubebuilder:validation:XValidation:rule="has(self.chartSpec) || has(self.chartRef) || has(self.chartSource)",message="one of chartSpec, chartRef or chartSource must be set"

// HelmSpec references a Helm chart representing the KCM template
type HelmSpec struct {
	// ChartSpec defines the desired state of the HelmChart to be created by the controller
	ChartSpec *sourcev1.HelmChartSpec `json:"chartSpec,omitempty"`

	// ChartRef is a reference to a source controller resource containing the
	// Helm chart representing the template.
	ChartRef *helmcontrollerv2.CrossNamespaceSourceReference `json:"chartRef,omitempty"`

	// +kubebuilder:validation:XValidation:rule="has(self.localSourceRef) ? (self.localSourceRef.kind != 'Secret' && self.localSourceRef.kind != 'ConfigMap'): true",message="Secret and ConfigMap are not supported as Helm chart sources"

	// ChartSource is a source of a Helm chart representing the template.
	ChartSource *SourceSpec `json:"chartSource,omitempty"`
}

func (s *HelmSpec) String() string {
	if s.ChartRef != nil {
		if s.ChartRef.Namespace != "" {
			return s.ChartRef.Namespace + "/" + s.ChartRef.Name + ", Kind=" + s.ChartRef.Kind
		}

		return s.ChartRef.Name + ", Kind=" + s.ChartRef.Kind
	}

	if s.ChartSpec.Version != "" {
		return s.ChartSpec.Chart + ": " + s.ChartSpec.Version
	}

	return s.ChartSpec.Chart
}

// TemplateStatusCommon defines the observed state of Template common for all Template types
type TemplateStatusCommon struct {
	// Config demonstrates available parameters for template customization,
	// that can be used when creating ClusterDeployment objects.
	Config *apiextv1.JSON `json:"config,omitempty"`
	// ChartRef is a reference to a source controller resource containing the
	// Helm chart representing the template.
	ChartRef *helmcontrollerv2.CrossNamespaceSourceReference `json:"chartRef,omitempty"`
	// ChartVersion represents the version of the Helm Chart associated with this template.
	ChartVersion string `json:"chartVersion,omitempty"`
	// Description contains information about the template.
	Description string `json:"description,omitempty"`

	TemplateValidationStatus `json:",inline"`

	// ObservedGeneration is the last observed generation.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

type TemplateValidationStatus struct {
	// ValidationError provides information regarding issues encountered during template validation.
	ValidationError string `json:"validationError,omitempty"`
	// Valid indicates whether the template passed validation or not.
	Valid bool `json:"valid"`
}

func getProvidersList(providers Providers, annotations map[string]string) Providers {
	const multiProviderSeparator = ","

	if len(providers) > 0 {
		res := slices.Clone(providers)
		slices.Sort(res)
		return slices.Compact(res)
	}

	providersFromAnno := annotations[clusterapiv1.ProviderNameLabel]
	if len(providersFromAnno) == 0 {
		return Providers{}
	}

	var (
		splitted = strings.Split(providersFromAnno, multiProviderSeparator)
		pstatus  = make([]string, 0, len(splitted))
	)
	for _, v := range splitted {
		if c := strings.TrimSpace(v); c != "" {
			pstatus = append(pstatus, c)
		}
	}

	slices.Sort(pstatus)
	return slices.Compact(pstatus)
}

func getCAPIContracts(kind string, contracts CompatibilityContracts, annotations map[string]string) (_ CompatibilityContracts, merr error) {
	contractsStatus := make(map[string]string)

	// spec preceding the annos
	if len(contracts) > 0 {
		for key, providerContract := range contracts { // key is either CAPI contract version or the name of a provider
			// for provider templates the key must be contract version
			// for cluster template the key must be the name of a provider
			if kind == ProviderTemplateKind && !isCAPIContractSingleVersion(key) {
				merr = errors.Join(merr, fmt.Errorf("incorrect CAPI contract version %s in the spec", key))
				continue
			}

			// for provider templates it is allowed to have a list of contract versions, or be empty for the core CAPI case
			// for cluster templates the contract versions should be single
			if kind == ProviderTemplateKind && providerContract != "" && !isCAPIContractVersion(providerContract) {
				merr = errors.Join(merr, fmt.Errorf("incorrect provider contract version %s in the spec for the %s CAPI contract version", providerContract, key))
				continue
			} else if kind == ClusterTemplateKind && !isCAPIContractSingleVersion(providerContract) {
				merr = errors.Join(merr, fmt.Errorf("incorrect provider contract version %s in the spec for the %s provider name", providerContract, key))
				continue
			}

			contractsStatus[key] = providerContract
		}

		return contractsStatus, merr
	}

	for k, providerContract := range annotations {
		idx := strings.Index(k, chartAnnoCAPIPrefix)
		if idx < 0 {
			continue
		}

		capiContractOrProviderName := k[idx+len(chartAnnoCAPIPrefix):]
		if (kind == ProviderTemplateKind && isCAPIContractSingleVersion(capiContractOrProviderName)) ||
			(kind == ClusterTemplateKind && (strings.HasPrefix(capiContractOrProviderName, "bootstrap-") ||
				strings.HasPrefix(capiContractOrProviderName, "control-plane-") ||
				strings.HasPrefix(capiContractOrProviderName, "infrastructure-"))) {
			if kind == ProviderTemplateKind && providerContract == "" { // special case for the core CAPI
				contractsStatus[capiContractOrProviderName] = ""
				continue
			}

			if (kind == ProviderTemplateKind && isCAPIContractVersion(providerContract)) ||
				(kind == ClusterTemplateKind && isCAPIContractSingleVersion(providerContract)) {
				contractsStatus[capiContractOrProviderName] = providerContract
			} else {
				// since we parsed capi contract version,
				// then treat the provider's invalid version as an error
				merr = errors.Join(merr, fmt.Errorf("incorrect provider contract version %s given for the %s annotation", providerContract, k))
			}
		}
	}

	return contractsStatus, merr
}
