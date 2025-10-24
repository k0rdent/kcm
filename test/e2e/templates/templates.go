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
	"fmt"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

type Type string

const (
	TemplateAWSStandaloneCP     Type = "aws-standalone-cp"
	TemplateAWSHostedCP         Type = "aws-hosted-cp"
	TemplateAWSEKS              Type = "aws-eks"
	TemplateAzureStandaloneCP   Type = "azure-standalone-cp"
	TemplateAzureHostedCP       Type = "azure-hosted-cp"
	TemplateAzureAKS            Type = "azure-aks"
	TemplateGCPStandaloneCP     Type = "gcp-standalone-cp"
	TemplateGCPHostedCP         Type = "gcp-hosted-cp"
	TemplateGCPGKE              Type = "gcp-gke"
	TemplateVSphereStandaloneCP Type = "vsphere-standalone-cp"
	TemplateVSphereHostedCP     Type = "vsphere-hosted-cp"
	TemplateAdoptedCluster      Type = "adopted-cluster"
	TemplateRemoteCluster       Type = "remote-cluster"
	TemplateDockerCluster       Type = "docker-hosted-cp"
)

// Types is an array of all the supported template types
var Types = []Type{
	TemplateAWSStandaloneCP,
	TemplateAWSHostedCP,
	TemplateAWSEKS,
	TemplateAzureStandaloneCP,
	TemplateAzureHostedCP,
	TemplateAzureAKS,
	TemplateGCPStandaloneCP,
	TemplateGCPHostedCP,
	TemplateGCPGKE,
	TemplateVSphereStandaloneCP,
	TemplateVSphereHostedCP,
	TemplateAdoptedCluster,
	TemplateRemoteCluster,
	TemplateDockerCluster,
}

func GetType(template string) Type {
	for _, t := range Types {
		if strings.HasPrefix(template, string(t)) {
			return t
		}
	}
	return ""
}

func (t Type) IsHosted() bool {
	return strings.Contains(string(t), "-hosted-")
}

func GetSortedClusterTemplates(ctx context.Context, cl crclient.Client, namespace string) ([]string, error) {
	itemsList := &metav1.PartialObjectMetadataList{}
	itemsList.SetGroupVersionKind(kcmv1.GroupVersion.WithKind(kcmv1.ClusterTemplateKind))
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

func createAndWaitForValid(ctx context.Context, client crclient.Client, obj crclient.Object, kind string) {
	err := client.Create(ctx, obj)
	Expect(crclient.IgnoreAlreadyExists(err)).NotTo(HaveOccurred(), fmt.Sprintf("failed to create %s", kind))

	Eventually(func() error {
		key := crclient.ObjectKeyFromObject(obj)
		if err := client.Get(ctx, key, obj); err != nil {
			return fmt.Errorf("failed to get %s %s/%s: %w", kind, key.Namespace, key.Name, err)
		}

		switch o := obj.(type) {
		case *kcmv1.ServiceTemplate:
			if !o.Status.Valid {
				return fmt.Errorf("%s %s/%s is not yet valid: %s", kind, key.Namespace, key.Name, o.Status.ValidationError)
			}
		case *kcmv1.ServiceTemplateChain:
			if !o.Status.Valid {
				return fmt.Errorf("%s %s/%s is not yet valid: %s", kind, key.Namespace, key.Name, o.Status.ValidationError)
			}
		default:
			return fmt.Errorf("unsupported type %T", obj)
		}
		return nil
	}).WithTimeout(10 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())
}

// CreateServiceTemplate creates a ServiceTemplate and waits for it to become valid
func CreateServiceTemplate(ctx context.Context, client crclient.Client, namespace, name string, spec kcmv1.ServiceTemplateSpec) {
	obj := &kcmv1.ServiceTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: spec,
	}
	createAndWaitForValid(ctx, client, obj, "ServiceTemplate")
}

// CreateTemplateChain creates a ServiceTemplateChain and waits for it to become valid
func CreateTemplateChain(ctx context.Context, client crclient.Client, namespace, name string, spec kcmv1.TemplateChainSpec) {
	obj := &kcmv1.ServiceTemplateChain{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: spec,
	}
	createAndWaitForValid(ctx, client, obj, "ServiceTemplateChain")
}
