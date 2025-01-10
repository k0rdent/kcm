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

package clusteridentity

import (
	"context"
	"fmt"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/K0rdent/kcm/test/e2e/clusterdeployment"
	"github.com/K0rdent/kcm/test/e2e/kubeclient"
)

type ClusterIdentity struct {
	GroupVersionResource schema.GroupVersionResource
	Kind                 string
	SecretName           string
	IdentityName         string
	SecretData           map[string]string
	Spec                 map[string]any
	Namespaced           bool
}

// New creates a ClusterIdentity resource, credential and associated secret for
// the given provider using the provided KubeClient and returns details about
// the created ClusterIdentity.
func New(kc *kubeclient.KubeClient, provider clusterdeployment.ProviderType) *ClusterIdentity {
	GinkgoHelper()

	var (
		resource         string
		kind             string
		version          string
		secretStringData map[string]string
		spec             map[string]any
		namespaced       bool
	)

	secretName := fmt.Sprintf("%s-cluster-identity-secret", provider)
	identityName := fmt.Sprintf("%s-cluster-identity", provider)

	switch provider {
	case clusterdeployment.ProviderAWS:
		resource = "awsclusterstaticidentities"
		kind = "AWSClusterStaticIdentity"
		version = "v1beta2"
		secretStringData = map[string]string{
			"AccessKeyID":     os.Getenv(clusterdeployment.EnvVarAWSAccessKeyID),
			"SecretAccessKey": os.Getenv(clusterdeployment.EnvVarAWSSecretAccessKey),
		}
		spec = map[string]any{
			"secretRef": secretName,
			"allowedNamespaces": map[string]any{
				"selector": map[string]any{
					"matchLabels": make(map[string]any),
				},
			},
		}
	case clusterdeployment.ProviderAzure:
		resource = "azureclusteridentities"
		kind = "AzureClusterIdentity"
		version = "v1beta1"
		secretStringData = map[string]string{
			"clientSecret": os.Getenv(clusterdeployment.EnvVarAzureClientSecret),
		}
		spec = map[string]any{
			"allowedNamespaces": make(map[string]any),
			"clientID":          os.Getenv(clusterdeployment.EnvVarAzureClientID),
			"clientSecret": map[string]any{
				"name":      secretName,
				"namespace": kc.Namespace,
			},
			"tenantID": os.Getenv(clusterdeployment.EnvVarAzureTenantID),
			"type":     "ServicePrincipal",
		}
		namespaced = true
	case clusterdeployment.ProviderVSphere:
		resource = "vsphereclusteridentities"
		kind = "VSphereClusterIdentity"
		version = "v1beta1"
		secretStringData = map[string]string{
			"username": os.Getenv(clusterdeployment.EnvVarVSphereUser),
			"password": os.Getenv(clusterdeployment.EnvVarVSpherePassword),
		}
		spec = map[string]any{
			"secretName": secretName,
			"allowedNamespaces": map[string]any{
				"selector": map[string]any{
					"matchLabels": make(map[string]any),
				},
			},
		}
	default:
		Fail(fmt.Sprintf("Unsupported provider: %s", provider))
	}

	ci := ClusterIdentity{
		GroupVersionResource: schema.GroupVersionResource{
			Group:    "infrastructure.cluster.x-k8s.io",
			Version:  version,
			Resource: resource,
		},
		Kind:         kind,
		SecretName:   secretName,
		IdentityName: identityName,
		SecretData:   secretStringData,
		Spec:         spec,
		Namespaced:   namespaced,
	}

	validateSecretDataPopulated(secretStringData)
	ci.waitForResourceCRD(kc)
	ci.createSecret(kc)
	ci.createClusterIdentity(kc)
	ci.createCredential(kc)

	return &ci
}

func validateSecretDataPopulated(secretData map[string]string) {
	for key, value := range secretData {
		Expect(value).ToNot(BeEmpty(), fmt.Sprintf("Secret data key %s should not be empty", key))
	}
}

// waitForResourceCRD ensures the CRD for the given resource is present by
// trying to list the resources of the given type until it succeeds.
func (ci *ClusterIdentity) waitForResourceCRD(kc *kubeclient.KubeClient) {
	GinkgoHelper()

	By(fmt.Sprintf("waiting for %s CRD to be present", ci.Kind))

	ctx := context.Background()

	Eventually(func() error {
		crds, err := kc.ExtendedClient.ApiextensionsV1().CustomResourceDefinitions().List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("failed to list CRDs: %w", err)
		}

		for _, crd := range crds.Items {
			if crd.Spec.Names.Kind == ci.Kind {
				return nil
			}
		}

		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to find CRD, retrying...\n")
		return fmt.Errorf("failed to find CRD for resource: %s", ci.GroupVersionResource.String())
	}).WithTimeout(time.Minute).WithPolling(5 * time.Second).Should(Succeed())
}

// createSecret creates a secret affiliated with a ClusterIdentity.
func (ci *ClusterIdentity) createSecret(kc *kubeclient.KubeClient) {
	GinkgoHelper()

	By(fmt.Sprintf("creating ClusterIdentity secret: %s", ci.SecretName))

	ctx := context.Background()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ci.SecretName,
			Namespace: kc.Namespace,
		},
		StringData: ci.SecretData,
		Type:       corev1.SecretTypeOpaque,
	}

	_, err := kc.Client.CoreV1().Secrets(kc.Namespace).Create(ctx, secret, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		resp, err := kc.Client.CoreV1().Secrets(kc.Namespace).Get(ctx, ci.SecretName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "failed to get existing secret")

		secret.SetResourceVersion(resp.GetResourceVersion())
		_, err = kc.Client.CoreV1().Secrets(kc.Namespace).Update(ctx, secret, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred(), "failed to update existing secret")
	} else {
		Expect(err).NotTo(HaveOccurred(), "failed to create secret")
	}
}

func (ci *ClusterIdentity) createCredential(kc *kubeclient.KubeClient) {
	GinkgoHelper()

	credName := fmt.Sprintf("%s-cred", ci.IdentityName)
	By(fmt.Sprintf("creating Credential: %s", credName))

	cred := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "k0rdent.mirantis.com/v1alpha1",
			"kind":       "Credential",
			"metadata": map[string]any{
				"name":      credName,
				"namespace": kc.Namespace,
			},
			"spec": map[string]any{
				"identityRef": map[string]any{
					"apiVersion": ci.GroupVersionResource.Group + "/" + ci.GroupVersionResource.Version,
					"kind":       ci.Kind,
					"name":       ci.IdentityName,
					"namespace":  kc.Namespace,
				},
			},
		},
	}

	kc.CreateOrUpdateUnstructuredObject(schema.GroupVersionResource{
		Group:    "k0rdent.mirantis.com",
		Version:  "v1alpha1",
		Resource: "credentials",
	}, cred, true)
}

// createClusterIdentity creates a ClusterIdentity resource.
func (ci *ClusterIdentity) createClusterIdentity(kc *kubeclient.KubeClient) {
	GinkgoHelper()

	By(fmt.Sprintf("creating ClusterIdentity: %s", ci.IdentityName))

	id := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": ci.GroupVersionResource.Group + "/" + ci.GroupVersionResource.Version,
			"kind":       ci.Kind,
			"metadata": map[string]any{
				"name":      ci.IdentityName,
				"namespace": kc.Namespace,
			},
			"spec": ci.Spec,
		},
	}

	kc.CreateOrUpdateUnstructuredObject(ci.GroupVersionResource, id, ci.Namespaced)
}
