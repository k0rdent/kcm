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
	SecretData           map[string]secretData
	Spec                 map[string]any
	Namespaced           bool
	CredentialName       string
}

type secretData struct {
	data     string
	optional bool
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
		secretStringData map[string]secretData
		spec             map[string]any
		namespaced       bool
	)

	secretName := fmt.Sprintf("%s-cluster-identity-secret", provider)
	identityName := fmt.Sprintf("%s-cluster-identity", provider)
	group := "infrastructure.cluster.x-k8s.io"

	switch provider {
	case clusterdeployment.ProviderAdopted:
		kubeCfgBytes, err := os.ReadFile(os.Getenv(clusterdeployment.EnvVarAdoptedKubeconfigPath))
		Expect(err).NotTo(HaveOccurred())

		kind = "Secret"
		version = "v1"
		group = ""
		identityName = secretName

		secretStringData = map[string]secretData{
			"Value": {
				data: string(kubeCfgBytes),
			},
		}

	case clusterdeployment.ProviderAWS:
		resource = "awsclusterstaticidentities"
		kind = "AWSClusterStaticIdentity"
		version = "v1beta2"
		secretStringData = map[string]secretData{
			"AccessKeyID": {
				data: os.Getenv(clusterdeployment.EnvVarAWSAccessKeyID),
			},
			"SecretAccessKey": {
				data: os.Getenv(clusterdeployment.EnvVarAWSSecretAccessKey),
			},
			"SessionToken": {
				data:     os.Getenv("AWS_SESSION_TOKEN"),
				optional: true,
			},
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
		secretStringData = map[string]secretData{
			"clientSecret": {
				data: os.Getenv(clusterdeployment.EnvVarAzureClientSecret),
			},
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
		secretStringData = map[string]secretData{
			"username": {
				data: os.Getenv(clusterdeployment.EnvVarVSphereUser),
			},
			"password": {
				data: os.Getenv(clusterdeployment.EnvVarVSpherePassword),
			},
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
			Group:    group,
			Version:  version,
			Resource: resource,
		},
		Kind:           kind,
		SecretName:     secretName,
		IdentityName:   identityName,
		SecretData:     secretStringData,
		Spec:           spec,
		Namespaced:     namespaced,
		CredentialName: fmt.Sprintf("%s-cred", identityName),
	}

	validateSecretDataPopulated(secretStringData)
	ci.createSecret(kc)

	if provider != clusterdeployment.ProviderAdopted {
		ci.waitForResourceCRD(kc)
		ci.createClusterIdentity(kc)
	}
	ci.createCredential(kc)

	return &ci
}

func validateSecretDataPopulated(secretData map[string]secretData) {
	for key, value := range secretData {
		if !value.optional {
			Expect(value.data).ToNot(BeEmpty(), fmt.Sprintf("Secret data key %s should not be empty", key))
		}
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

	secretData := make(map[string]string)
	for k, v := range ci.SecretData {
		secretData[k] = v.data
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ci.SecretName,
			Namespace: kc.Namespace,
		},
		StringData: secretData,
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

	By(fmt.Sprintf("creating Credential: %s", ci.CredentialName))

	cred := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "k0rdent.mirantis.com/v1beta1",
			"kind":       "Credential",
			"metadata": map[string]any{
				"name":      ci.CredentialName,
				"namespace": kc.Namespace,
			},
			"spec": map[string]any{
				"identityRef": map[string]any{
					"apiVersion": ci.GroupVersionResource.GroupVersion().String(),
					"kind":       ci.Kind,
					"name":       ci.IdentityName,
					"namespace":  kc.Namespace,
				},
			},
		},
	}

	kc.CreateOrUpdateUnstructuredObject(schema.GroupVersionResource{
		Group:    "k0rdent.mirantis.com",
		Version:  "v1beta1",
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

func (ci *ClusterIdentity) WaitForValidCredential(kc *kubeclient.KubeClient) {
	GinkgoHelper()

	By(fmt.Sprintf("waiting for %s credential to be ready", ci.CredentialName))

	ctx := context.Background()

	Eventually(func() error {
		cred, err := kc.GetCredential(ctx, ci.CredentialName)
		if err != nil {
			return fmt.Errorf("failed to get credntial: %w", err)
		}

		ready, found, err := unstructured.NestedBool(cred.Object, "status", "ready")
		if !found {
			return fmt.Errorf("failed to get ready status: %w", err)
		}
		if !ready {
			_, _ = fmt.Fprintf(GinkgoWriter, "credential is not ready, retrying...\n")
			return fmt.Errorf("credential is not ready: %s", ci.GroupVersionResource.String())
		}
		return nil
	}).WithTimeout(time.Minute).WithPolling(5 * time.Second).Should(Succeed())
}
