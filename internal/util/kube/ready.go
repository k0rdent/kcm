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

package kube

import (
	"context"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// IsAPIServerReady checks if /readyz of the given kubeconfig responds with OK
// within the given timeout.
func IsAPIServerReady(ctx context.Context, cfg *rest.Config, timeout time.Duration) bool {
	c := *cfg
	c.Timeout = timeout

	kc, err := kubernetes.NewForConfig(&c)
	if err != nil {
		return false
	}

	readyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return kc.Discovery().RESTClient().
		Get().
		AbsPath("/readyz").
		Do(readyCtx).
		Error() == nil
}
