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

package templates

import (
	"context"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/K0rdent/kcm/api/v1alpha1"
)

type Type string

const (
	TemplateAWSStandaloneCP     Type = "aws-standalone-cp"
	TemplateAWSHostedCP         Type = "aws-hosted-cp"
	TemplateAWSEKS              Type = "aws-eks"
	TemplateAzureStandaloneCP   Type = "azure-standalone-cp"
	TemplateAzureHostedCP       Type = "azure-hosted-cp"
	TemplateAzureAKS            Type = "azure-aks"
	TemplateVSphereStandaloneCP Type = "vsphere-standalone-cp"
	TemplateVSphereHostedCP     Type = "vsphere-hosted-cp"
	TemplateAdoptedCluster      Type = "adopted-cluster"
	TemplateRemoteCluster       Type = "remote-cluster"
)

// Types is an array of all the supported template types
var Types = []Type{
	TemplateAWSStandaloneCP,
	TemplateAWSHostedCP,
	TemplateAWSEKS,
	TemplateAzureStandaloneCP,
	TemplateAzureHostedCP,
	TemplateAzureAKS,
	TemplateVSphereStandaloneCP,
	TemplateVSphereHostedCP,
	TemplateAdoptedCluster,
	TemplateRemoteCluster,
}

func GetType(template string) Type {
	for _, t := range Types {
		if strings.HasPrefix(template, string(t)) {
			return t
		}
	}
	return ""
}

func GetSortedClusterTemplates(ctx context.Context, cl crclient.Client, namespace string) ([]string, error) {
	itemsList := &metav1.PartialObjectMetadataList{}
	itemsList.SetGroupVersionKind(v1alpha1.GroupVersion.WithKind(v1alpha1.ClusterTemplateKind))
	if err := cl.List(ctx, itemsList, crclient.InNamespace(namespace)); err != nil {
		return nil, err
	}
	clusterTemplates := make([]string, 0, len(itemsList.Items))
	for _, item := range itemsList.Items {
		clusterTemplates = append(clusterTemplates, item.Name)
	}

	slices.SortFunc(clusterTemplates, func(a, b string) int {
		return strings.Compare(b, a)
	})
	return clusterTemplates, nil
}

func FindLatestTemplatesWithType(clusterTemplates []string, templateType Type, n int) []string {
	var templates []string
	for _, template := range clusterTemplates {
		if strings.HasPrefix(template, string(templateType)) {
			templates = append(templates, template)
			if len(templates) == n {
				break
			}
		}
	}
	return templates
}
