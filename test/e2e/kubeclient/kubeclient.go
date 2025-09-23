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

package kubeclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clusterapiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	statusutil "github.com/K0rdent/kcm/internal/util/status"
	"github.com/K0rdent/kcm/test/scheme"
)

type KubeClient struct {
	Client         kubernetes.Interface
	CrClient       crclient.Client
	ExtendedClient apiextensionsclientset.Interface
	Config         *rest.Config

	Namespace string
}

// NewFromLocal creates a new instance of KubeClient from a given namespace
// using the locally found kubeconfig.
func NewFromLocal(namespace string) *KubeClient {
	GinkgoHelper()
	return newKubeClient(getLocalKubeConfig(), namespace)
}

// NewFromCluster creates a new KubeClient using the kubeconfig stored in the
// secret affiliated with the given clusterName.  Since it relies on fetching
// the kubeconfig from secret it needs an existing kubeclient.
func (kc *KubeClient) NewFromCluster(ctx context.Context, namespace, clusterName string) *KubeClient {
	GinkgoHelper()

	secretData, err := kc.GetKubeconfigSecretData(ctx, clusterName)
	Expect(err).To(Succeed())
	return newKubeClient(secretData, namespace)
}

// WriteKubeconfig writes the kubeconfig for the given clusterName to the
// test/e2e directory returning the path to the file and a function to delete
// it later.
func (kc *KubeClient) WriteKubeconfig(ctx context.Context, clusterName string) (path, b64Raw string, cleanup func() error, err error) {
	GinkgoHelper()

	secretData, err := kc.GetKubeconfigSecretData(ctx, clusterName)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to get kubeconfig secret data: %w", err)
	}

	kcFile, err := os.CreateTemp("", clusterName+"-kubeconfig")
	if err != nil {
		return "", "", nil, err
	}
	defer kcFile.Close() //nolint:errcheck // no need

	_, err = kcFile.Write(secretData)
	if err != nil {
		return "", "", nil, err
	}

	cleanup = func() error {
		if err := os.Remove(kcFile.Name()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}

	return kcFile.Name(), base64.StdEncoding.EncodeToString(bytes.TrimSpace(secretData)), cleanup, nil
}

func (kc *KubeClient) GetKubeconfigSecretData(ctx context.Context, clusterName string) ([]byte, error) {
	GinkgoHelper()

	kubeconfigSecretName := clusterName + "-kubeconfig"
	secret, err := kc.Client.CoreV1().Secrets(kc.Namespace).Get(ctx, kubeconfigSecretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster: %q kubeconfig secret: %w", clusterName, err)
	}
	Expect(err).NotTo(HaveOccurred(), "failed to get cluster: %q kubeconfig secret", clusterName)

	secretData, ok := secret.Data["value"]
	if !ok {
		return nil, fmt.Errorf("kubeconfig secret %q has no 'value' key", kubeconfigSecretName)
	}
	return secretData, nil
}

// getLocalKubeConfig returns the kubeconfig file content.
func getLocalKubeConfig() []byte {
	GinkgoHelper()

	// Use the KUBECONFIG environment variable if it is set, otherwise use the
	// default path.
	kubeConfig, ok := os.LookupEnv("KUBECONFIG")
	if !ok {
		homeDir, err := os.UserHomeDir()
		Expect(err).NotTo(HaveOccurred(), "failed to get user home directory")

		kubeConfig = filepath.Join(homeDir, ".kube", "config")
	}

	configBytes, err := os.ReadFile(kubeConfig)
	Expect(err).NotTo(HaveOccurred(), "failed to read %q", kubeConfig)

	return configBytes
}

// newKubeClient creates a new instance of KubeClient from a given namespace using
// the local kubeconfig.
func newKubeClient(configBytes []byte, namespace string) *KubeClient {
	GinkgoHelper()

	config, err := clientcmd.RESTConfigFromKubeConfig(configBytes)
	Expect(err).NotTo(HaveOccurred(), "failed to parse kubeconfig")

	clientSet, err := kubernetes.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred(), "failed to initialize kubernetes client")

	extendedClientSet, err := apiextensionsclientset.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred(), "failed to initialize apiextensions clientset")

	crClient, err := crclient.New(config, crclient.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred(), "failed to create controller runtime client")

	return &KubeClient{
		Namespace:      namespace,
		Client:         clientSet,
		CrClient:       crClient,
		ExtendedClient: extendedClientSet,
		Config:         config,
	}
}

// GetDynamicClient returns a dynamic client for the given GroupVersionResource.
func (kc *KubeClient) GetDynamicClient(gvr schema.GroupVersionResource, namespaced bool) dynamic.ResourceInterface { //nolint:revive
	GinkgoHelper()

	client, err := dynamic.NewForConfig(kc.Config)
	Expect(err).NotTo(HaveOccurred(), "failed to create dynamic client for resource: %s", gvr.String())

	if !namespaced {
		return client.Resource(gvr)
	}

	return client.Resource(gvr).Namespace(kc.Namespace)
}

func (kc *KubeClient) CreateOrUpdateUnstructuredObject(gvr schema.GroupVersionResource, obj *unstructured.Unstructured, namespaced bool) {
	GinkgoHelper()

	client := kc.GetDynamicClient(gvr, namespaced)

	kind, name := statusutil.ObjKindName(obj)

	resp, err := client.Get(context.Background(), name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = client.Create(context.Background(), obj, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), "failed to create %s: %s", kind, name)
	} else {
		Expect(err).NotTo(HaveOccurred(), "failed to get existing %s: %s", kind, name)

		obj.SetResourceVersion(resp.GetResourceVersion())
		_, err = client.Update(context.Background(), obj, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred(), "failed to update existing %s: %s", kind, name)
	}
}

// CreateClusterDeployment creates a clusterdeployment.k0rdent.mirantis.com in the given
// namespace and returns a DeleteFunc to clean up the deployment.
// The DeleteFunc is a no-op if the deployment has already been deleted.
func (kc *KubeClient) CreateClusterDeployment(
	ctx context.Context, clusterDeployment *unstructured.Unstructured,
) func() error {
	GinkgoHelper()

	kind := clusterDeployment.GetKind()
	Expect(kind).To(Equal("ClusterDeployment"))

	client := kc.GetDynamicClient(kcmv1.GroupVersion.WithResource("clusterdeployments"), true)

	_, err := client.Create(ctx, clusterDeployment, metav1.CreateOptions{})
	if !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred(), "failed to create %s", kind)
	}

	return func() error {
		name := clusterDeployment.GetName()
		if err := client.Delete(ctx, name, metav1.DeleteOptions{}); crclient.IgnoreNotFound(err) != nil {
			return err
		}
		Eventually(func() bool {
			_, err := client.Get(ctx, name, metav1.GetOptions{})
			return apierrors.IsNotFound(err)
		}, 30*time.Minute, 1*time.Minute).Should(BeTrue())
		return nil
	}
}

// GetClusterDeployment returns a ClusterDeployment resource.
func (kc *KubeClient) GetClusterDeployment(ctx context.Context, name string) (*unstructured.Unstructured, error) {
	return kc.getResource(ctx, kcmv1.GroupVersion.WithResource("clusterdeployments"), name)
}

// GetCluster returns a Cluster resource by name.
func (kc *KubeClient) GetCluster(ctx context.Context, clusterName string) (*unstructured.Unstructured, error) {
	return kc.getResource(ctx, schema.GroupVersionResource{
		Group:    clusterapiv1.GroupVersion.Group,
		Version:  clusterapiv1.GroupVersion.Version,
		Resource: "clusters",
	}, clusterName)
}

// GetAzureASOManagedCluster returns an AzureASOManagedCluster resource by name.
func (kc *KubeClient) GetAzureASOManagedCluster(ctx context.Context, name string) (*unstructured.Unstructured, error) {
	return kc.getResource(ctx, schema.GroupVersionResource{
		Group:    "infrastructure.cluster.x-k8s.io",
		Version:  "v1alpha1",
		Resource: "azureasomanagedclusters",
	}, name)
}

// GetAzureASOManagedControlPlane returns an AzureASOManagedControlPlane resource by name.
func (kc *KubeClient) GetAzureASOManagedControlPlane(ctx context.Context, name string) (*unstructured.Unstructured, error) {
	return kc.getResource(ctx, schema.GroupVersionResource{
		Group:    "infrastructure.cluster.x-k8s.io",
		Version:  "v1alpha1",
		Resource: "azureasomanagedcontrolplanes",
	}, name)
}

// GetGCPManagedCluster returns GCPManagedCluster resource by name.
func (kc *KubeClient) GetGCPManagedCluster(ctx context.Context, name string) (*unstructured.Unstructured, error) {
	return kc.getResource(ctx, schema.GroupVersionResource{
		Group:    "infrastructure.cluster.x-k8s.io",
		Version:  "v1beta1",
		Resource: "gcpmanagedclusters",
	}, name)
}

// GetGCPManagedControlPlanes returns an GCPManagedControlPlane resource by name.
func (kc *KubeClient) GetGCPManagedControlPlanes(ctx context.Context, name string) ([]unstructured.Unstructured, error) {
	return kc.listResource(ctx, schema.GroupVersionResource{
		Group:    "infrastructure.cluster.x-k8s.io",
		Version:  "v1beta1",
		Resource: "gcpmanagedcontrolplanes",
	}, name)
}

func (kc *KubeClient) GetCredential(ctx context.Context, name string) (*unstructured.Unstructured, error) {
	return kc.getResource(ctx, kcmv1.GroupVersion.WithResource("credentials"), name)
}

func (kc *KubeClient) GetSveltosCluster(ctx context.Context, name string) (*unstructured.Unstructured, error) {
	return kc.getResource(ctx, schema.GroupVersionResource{
		Group:    libsveltosv1beta1.GroupVersion.Group,
		Version:  libsveltosv1beta1.GroupVersion.Version,
		Resource: "sveltosclusters",
	}, name)
}

func (kc *KubeClient) GetMultiClusterService(ctx context.Context, name string) (*unstructured.Unstructured, error) {
	gvr := kcmv1.GroupVersion.WithResource("multiclusterservices")
	client := kc.GetDynamicClient(gvr, false)

	resource, err := client.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get %s %s: %w", gvr.Resource, name, err)
	}

	return resource, err
}

// getResource returns a resource for the given GroupVersionResource
func (kc *KubeClient) getResource(
	ctx context.Context, gvr schema.GroupVersionResource, name string,
) (*unstructured.Unstructured, error) {
	client := kc.GetDynamicClient(gvr, true)

	resource, err := client.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get %s %s: %w", gvr.Resource, name, err)
	}

	return resource, nil
}

// listResource returns a list of resources for the given GroupVersionResource
// affiliated with the given clusterName.
func (kc *KubeClient) listResource(
	ctx context.Context, gvr schema.GroupVersionResource, clusterName string,
) ([]unstructured.Unstructured, error) {
	client := kc.GetDynamicClient(gvr, true)

	resources, err := client.List(ctx, metav1.ListOptions{
		LabelSelector: "cluster.x-k8s.io/cluster-name=" + clusterName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list %s", gvr.Resource)
	}

	return resources.Items, nil
}

// patchResource patches a specified resource.
func (kc *KubeClient) patchResource(
	ctx context.Context,
	gvr schema.GroupVersionResource,
	name string,
	pt types.PatchType,
	data []byte,
) (*unstructured.Unstructured, error) {
	client := kc.GetDynamicClient(gvr, true)

	resource, err := client.Patch(ctx, name, pt, data, metav1.PatchOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to patch %s %s", gvr.Resource, name)
	}
	return resource, nil
}

// ListMachines returns a list of Machine resources for the given cluster.
func (kc *KubeClient) ListMachines(ctx context.Context, clusterName string) ([]unstructured.Unstructured, error) {
	GinkgoHelper()

	return kc.listResource(ctx, schema.GroupVersionResource{
		Group:    clusterapiv1.GroupVersion.Group,
		Version:  clusterapiv1.GroupVersion.Version,
		Resource: "machines",
	}, clusterName)
}

// ListRemoteMachines returns a list of RemoteMachine resources for the given cluster.
func (kc *KubeClient) ListRemoteMachines(ctx context.Context, clusterName string) ([]unstructured.Unstructured, error) {
	GinkgoHelper()

	return kc.listResource(ctx, schema.GroupVersionResource{
		Group:    "infrastructure.cluster.x-k8s.io",
		Version:  "v1beta1",
		Resource: "remotemachines",
	}, clusterName)
}

// ListMachineDeployments returns a list of MachineDeployment resources for the
// given cluster.
func (kc *KubeClient) ListMachineDeployments(
	ctx context.Context, clusterName string,
) ([]unstructured.Unstructured, error) {
	GinkgoHelper()

	return kc.listResource(ctx, schema.GroupVersionResource{
		Group:    clusterapiv1.GroupVersion.Group,
		Version:  clusterapiv1.GroupVersion.Version,
		Resource: "machinedeployments",
	}, clusterName)
}

// PatchMachineDeployment patches a MachineDeployment resource with the given data.
func (kc *KubeClient) PatchMachineDeployment(
	ctx context.Context,
	name string,
	pt types.PatchType,
	data []byte,
) (*unstructured.Unstructured, error) {
	GinkgoHelper()

	return kc.patchResource(ctx, schema.GroupVersionResource{
		Group:    clusterapiv1.GroupVersion.Group,
		Version:  clusterapiv1.GroupVersion.Version,
		Resource: "machinedeployments",
	}, name, pt, data)
}

func (kc *KubeClient) ListK0sControlPlanes(
	ctx context.Context, clusterName string,
) ([]unstructured.Unstructured, error) {
	GinkgoHelper()

	return kc.listResource(ctx, schema.GroupVersionResource{
		Group:    "controlplane.cluster.x-k8s.io",
		Version:  "v1beta1",
		Resource: "k0scontrolplanes",
	}, clusterName)
}

func (kc *KubeClient) ListK0smotronControlPlanes(
	ctx context.Context, clusterName string,
) ([]unstructured.Unstructured, error) {
	GinkgoHelper()

	return kc.listResource(ctx, schema.GroupVersionResource{
		Group:    "controlplane.cluster.x-k8s.io",
		Version:  "v1beta1",
		Resource: "k0smotroncontrolplanes",
	}, clusterName)
}

func (kc *KubeClient) ListAWSManagedControlPlanes(
	ctx context.Context, clusterName string,
) ([]unstructured.Unstructured, error) {
	GinkgoHelper()

	return kc.listResource(ctx, schema.GroupVersionResource{
		Group:    "controlplane.cluster.x-k8s.io",
		Version:  "v1beta2",
		Resource: "awsmanagedcontrolplanes",
	}, clusterName)
}

func (kc *KubeClient) ListAzureASOManagedMachinePools(
	ctx context.Context, clusterName string,
) ([]unstructured.Unstructured, error) {
	GinkgoHelper()

	return kc.listResource(ctx, schema.GroupVersionResource{
		Group:    "infrastructure.cluster.x-k8s.io",
		Version:  "v1alpha1",
		Resource: "azureasomanagedmachinepools",
	}, clusterName)
}

func (kc *KubeClient) ListGCPManagedMachinePools(
	ctx context.Context, clusterName string,
) ([]unstructured.Unstructured, error) {
	GinkgoHelper()

	return kc.listResource(ctx, schema.GroupVersionResource{
		Group:    "infrastructure.cluster.x-k8s.io",
		Version:  "v1beta1",
		Resource: "gcpmanagedmachinepools",
	}, clusterName)
}

func (kc *KubeClient) ListClusterTemplates(ctx context.Context) ([]unstructured.Unstructured, error) {
	client := kc.GetDynamicClient(schema.GroupVersionResource{
		Group:    kcmv1.GroupVersion.Group,
		Version:  kcmv1.GroupVersion.Version,
		Resource: "clustertemplates",
	}, true)

	resources, err := client.List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster templates")
	}

	return resources.Items, nil
}

func (kc *KubeClient) CreateMultiClusterService(
	ctx context.Context,
	multiClusterService *unstructured.Unstructured,
) func() error {
	GinkgoHelper()

	kind := multiClusterService.GetKind()
	Expect(kind).To(Equal("MultiClusterService"))

	client := kc.GetDynamicClient(schema.GroupVersionResource{
		Group:    "k0rdent.mirantis.com",
		Version:  "v1beta1",
		Resource: "multiclusterservices",
	}, false)

	_, err := client.Create(ctx, multiClusterService, metav1.CreateOptions{})
	if !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred(), "failed to create %s", kind)
	}

	return func() error {
		err := client.Delete(ctx, multiClusterService.GetName(), metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
}
