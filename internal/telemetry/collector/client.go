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

func getChildClient(ctx context.Context, cl client.Client, clusterName, systemNamespace string, scheme *runtime.Scheme) (client.Client, error) {
	secret := new(corev1.Secret)
	if err := cl.Get(ctx, client.ObjectKey{Name: clusterName + "-kubeconfig", Namespace: systemNamespace}, secret); err != nil {
		return nil, fmt.Errorf("failed to get Secret with kubeconfig: %w", err)
	}

	data, ok := secret.Data["value"]
	if !ok { // sanity check
		return nil, fmt.Errorf("kubeconfig from Secret %s is empty", client.ObjectKeyFromObject(secret))
	}

	apicfg, err := clientcmd.Load(data)
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig from %s Secret: %w", client.ObjectKeyFromObject(secret), err)
	}
	restcfg, err := clientcmd.NewDefaultClientConfig(*apicfg, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get client REST config from %s Secret: %w", client.ObjectKeyFromObject(secret), err)
	}

	childCl, err := client.New(restcfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create client config from %s Secret: %w", client.ObjectKeyFromObject(secret), err)
	}

	return childCl, nil
}
