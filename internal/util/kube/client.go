// Copyright 2025
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

// Package kube provides useful functions to manipulate with kube objects.
package kube

import (
	"context"
	"errors"
	"fmt"

	fluxmeta "github.com/fluxcd/pkg/apis/meta"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// GetChildClient fetches a child cluster's client.
func GetChildClient(ctx context.Context, mgmtCl client.Client, kubeconfigSecretRef client.ObjectKey, kubeconfigSecretKey string, scheme *runtime.Scheme, clientFactory func([]byte, *runtime.Scheme) (client.Client, error)) (client.Client, error) {
	secret := new(corev1.Secret)
	if err := mgmtCl.Get(ctx, kubeconfigSecretRef, secret); err != nil {
		return nil, fmt.Errorf("failed to get Secret with kubeconfig: %w", err)
	}

	kubeconfigBytes, ok := secret.Data[kubeconfigSecretKey]
	if !ok { // sanity check
		return nil, fmt.Errorf("kubeconfig from Secret %s is empty", kubeconfigSecretRef)
	}

	childCl, err := clientFactory(kubeconfigBytes, scheme)
	if err != nil {
		return nil, fmt.Errorf("failed to create client config from %s Secret: %w", kubeconfigSecretRef, err)
	}

	return childCl, nil
}

// GetKubeconfigSecretKey forms an [sigs.k8s.io/controller-runtime/pkg/client.ObjectKey]
// for a Secret containing kubeconfig bytes.
func GetKubeconfigSecretKey(clusterKey client.ObjectKey) client.ObjectKey {
	return client.ObjectKey{Name: clusterKey.Name + "-kubeconfig", Namespace: clusterKey.Namespace}
}

// DefaultClientFactory constructs a [sigs.k8s.io/controller-runtime/pkg/client.Client] from the
// given kubeconfig bytes and scheme.
func DefaultClientFactory(kubeconfig []byte, scheme *runtime.Scheme) (client.Client, error) {
	restCfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build rest config from the given kubeconfig data: %w", err)
	}

	cl, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	return cl, nil
}

// DefaultClientFactoryWithRestConfig returns a lazy factory function that builds a
// [sigs.k8s.io/controller-runtime/pkg/client.Client] from kubeconfig bytes and a scheme.
//
// The associated [k8s.io/client-go/rest.Config] is initialized only when the factory is first invoked.
//
// WARN: The returned callback is NOT concurrency safe.
func DefaultClientFactoryWithRestConfig() (func([]byte, *runtime.Scheme) (client.Client, error), *rest.Config) {
	restCfg := new(rest.Config)
	return func(kubeconfig []byte, scheme *runtime.Scheme) (client.Client, error) {
		cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to build rest config from the given kubeconfig data: %w", err)
		}

		*restCfg = *cfg

		cl, err := client.New(cfg, client.Options{Scheme: scheme})
		if err != nil {
			return nil, fmt.Errorf("failed to create client: %w", err)
		}
		return cl, nil
	}, restCfg
}

// GetRegionalKubeconfigSecretRef retrieves the kubeconfig secret reference from the given
// [github.com/k0rdent/kcm/api/v1beta1.Region] in [github.com/fluxcd/pkg/apis/meta.SecretKeyReference] format.
func GetRegionalKubeconfigSecretRef(region *kcmv1.Region) (*fluxmeta.SecretKeyReference, error) {
	if region == nil {
		return nil, errors.New("region is nil")
	}

	if region.Spec.KubeConfig == nil && region.Spec.ClusterDeployment == nil {
		return nil, errors.New("either spec.kubeConfig or spec.clusterDeployment must be set")
	}

	if region.Spec.KubeConfig != nil && region.Spec.ClusterDeployment != nil {
		return nil, errors.New("only one of spec.kubeConfig and spec.clusterDeployment is allowed")
	}

	if region.Spec.KubeConfig != nil {
		return region.Spec.KubeConfig, nil
	}
	if region.Spec.ClusterDeployment != nil {
		const kubeConfigSecretDataKey = "value"

		kubeconfigSecretKey := GetKubeconfigSecretKey(client.ObjectKey{
			Namespace: region.Spec.ClusterDeployment.Namespace,
			Name:      region.Spec.ClusterDeployment.Name,
		})
		return &fluxmeta.SecretKeyReference{
			Name: kubeconfigSecretKey.Name,
			Key:  kubeConfigSecretDataKey,
		}, nil
	}

	return nil, errors.New("kubeConfig is unset")
}

// GetRegionalClientByRegionName returns the [sigs.k8s.io/controller-runtime/pkg/client.Client] for the given region name.
// If region is empty, returns the client of the management cluster.
func GetRegionalClientByRegionName(ctx context.Context, mgmtClient client.Client, systemNamespace, region string, getSchemeFunc func() (*runtime.Scheme, error)) (client.Client, error) {
	if region == "" {
		return mgmtClient, nil
	}

	rgn := new(kcmv1.Region)
	if err := mgmtClient.Get(ctx, client.ObjectKey{Name: region}, rgn); err != nil {
		return nil, fmt.Errorf("failed to get %s region: %w", region, err)
	}

	rgnClient, _, err := GetRegionalClient(ctx, mgmtClient, systemNamespace, rgn, getSchemeFunc)
	if err != nil {
		return nil, fmt.Errorf("failed to get client for %s region: %w", region, err)
	}

	return rgnClient, nil
}

// GetRegionalClient returns the [sigs.k8s.io/controller-runtime/pkg/client.Client] for the given [github.com/k0rdent/kcm/api/v1beta1.Region] object.
func GetRegionalClient(
	ctx context.Context,
	mgmtClient client.Client,
	systemNamespace string,
	region *kcmv1.Region,
	getSchemeFunc func() (*runtime.Scheme, error),
) (client.Client, *rest.Config, error) {
	scheme, err := getSchemeFunc()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get regional scheme for the %s region: %w", region.Name, err)
	}

	kubeConfigSecretRef, err := GetRegionalKubeconfigSecretRef(region)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get kubeconfig secret reference: %w", err)
	}

	namespace := systemNamespace
	if region.Spec.ClusterDeployment != nil && region.Spec.ClusterDeployment.Namespace != "" {
		namespace = region.Spec.ClusterDeployment.Namespace
	}

	secretRef := client.ObjectKey{Namespace: namespace, Name: kubeConfigSecretRef.Name}
	factory, restCfg := DefaultClientFactoryWithRestConfig()
	rgnlClient, err := GetChildClient(ctx, mgmtClient, secretRef, kubeConfigSecretRef.Key, scheme, factory)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get rest config for the %s region: %w", region.Name, err)
	}

	return rgnlClient, restCfg, nil
}
