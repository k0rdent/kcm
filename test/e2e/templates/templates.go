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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

func CreateServiceTemplate(ctx context.Context, client crclient.Client, namespace, name string, spec kcmv1.ServiceTemplateSpec) {
	st := &kcmv1.ServiceTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: spec,
	}
	err := client.Create(ctx, st)
	Expect(crclient.IgnoreAlreadyExists(err)).NotTo(HaveOccurred(), "failed to create ServiceTemplate")

	Eventually(func() error {
		if err = client.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: name}, st); err != nil {
			return fmt.Errorf("failed to get ServiceTemplate %s/%s: %w", namespace, name, err)
		}
		if !st.Status.Valid {
			return fmt.Errorf("ServiceTemplate %s/%s is not yet valid: %s", namespace, name, st.Status.ValidationError)
		}
		return nil
	}).WithTimeout(10 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())

	_, _ = fmt.Fprintf(GinkgoWriter, "Created ServiceTemplate %s\n", crclient.ObjectKeyFromObject(st))
}

func CreateServiceTemplateWithDelete(ctx context.Context, client crclient.Client, namespace, name string, spec kcmv1.ServiceTemplateSpec) func() error {
	CreateServiceTemplate(ctx, client, namespace, name, spec)

	st := &kcmv1.ServiceTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: spec,
	}

	stKey := crclient.ObjectKeyFromObject(st)
	return func() error {
		if err := client.Delete(ctx, st); crclient.IgnoreNotFound(err) != nil {
			return err
		}
		Eventually(func() bool {
			err := client.Get(ctx, stKey, &kcmv1.ServiceTemplate{})
			return apierrors.IsNotFound(err)
		}).WithTimeout(30 * time.Minute).WithPolling(5 * time.Minute).Should(BeTrue())
		_, _ = fmt.Fprintf(GinkgoWriter, "Deleted ServiceTemplate %s\n", stKey)
		return nil
	}
}
