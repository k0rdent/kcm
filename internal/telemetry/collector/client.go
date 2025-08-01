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

package collector

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func getChildClient(ctx context.Context, mgmtCl client.Client, cldKey client.ObjectKey, scheme *runtime.Scheme, clientFactory func([]byte, *runtime.Scheme) (client.Client, error)) (client.Client, error) {
	secret, secretKey := new(corev1.Secret), getKubeconfigSecretKey(cldKey)
	if err := mgmtCl.Get(ctx, secretKey, secret); err != nil {
		return nil, fmt.Errorf("failed to get Secret with kubeconfig: %w", err)
	}

	kubeconfigBytes, ok := secret.Data["value"]
	if !ok { // sanity check
		return nil, fmt.Errorf("kubeconfig from Secret %s is empty", secretKey)
	}

	childCl, err := clientFactory(kubeconfigBytes, scheme)
	if err != nil {
		return nil, fmt.Errorf("failed to create client config from %s Secret: %w", secretKey, err)
	}

	return childCl, nil
}

func getKubeconfigSecretKey(cldKey client.ObjectKey) client.ObjectKey {
	return client.ObjectKey{Name: cldKey.Name + "-kubeconfig", Namespace: cldKey.Namespace}
}

func defaultClientFactory(kubeconfig []byte, scheme *runtime.Scheme) (client.Client, error) {
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
