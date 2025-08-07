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
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

// copied from "github.com/k0sproject/k0smotron/api/controlplane/v1beta1" to avoid dependency
const k0sClusterIDAnnotation = "k0sproject.io/cluster-id"

func getK0sClusterID(partialCapiClusters []metav1.PartialObjectMetadata, cldKey client.ObjectKey) string {
	for _, v := range partialCapiClusters {
		if cldKey.Namespace == v.Namespace &&
			cldKey.Name == v.Name {
			return v.Annotations[k0sClusterIDAnnotation]
		}
	}

	return ""
}

func countUserServices(cld *kcmv1.ClusterDeployment, mcsList *kcmv1.MultiClusterServiceList, partialClusters []metav1.PartialObjectMetadata) int {
	svcCnt := len(cld.Spec.ServiceSpec.Services)

	for _, mcs := range mcsList.Items {
		sel, err := metav1.LabelSelectorAsSelector(&mcs.Spec.ClusterSelector)
		if err != nil {
			continue // NOTE: better continue than drop
		}

		for _, cl := range partialClusters {
			if sel.Matches(labels.Set(cl.Labels)) {
				svcCnt += len(mcs.Spec.ServiceSpec.Services)
			}
		}
	}

	return svcCnt
}

func getGpuOperatorPresence(dsList []metav1.PartialObjectMetadata) (nvidiaPresent, amdPresent bool) {
	const (
		nvidiaKey                = "nvidia"
		nvidiaOperatorNamePrefix = "gpu-operator"
		amdKey                   = "amd"
		amdOperatorNamePrefix    = "amd-gpu-operator"
	)
	gpuOperators := make(map[string]bool, 2)

	for _, ds := range dsList {
		if gpuOperators[nvidiaKey] && gpuOperators[amdKey] {
			break
		}

		// TODO: NOTE: how likely both of the operators will be installed?
		name := strings.ToLower(ds.Name)
		if strings.Contains(name, nvidiaOperatorNamePrefix) && !strings.Contains(name, amdOperatorNamePrefix) {
			gpuOperators[nvidiaKey] = true
		}
		if strings.Contains(name, amdOperatorNamePrefix) {
			gpuOperators[amdKey] = true
		}
	}

	return gpuOperators[nvidiaKey], gpuOperators[amdKey]
}

func streamPaginatedPods(ctx context.Context, cl client.Client, limit int64, handle func(pods []*corev1.Pod)) error {
	opts := &client.ListOptions{Limit: limit}

	for {
		var list corev1.PodList
		if err := cl.List(ctx, &list, opts); err != nil {
			return fmt.Errorf("failed to list pods: %w", err)
		}

		out := make([]*corev1.Pod, len(list.Items))
		for i := range list.Items {
			out[i] = &list.Items[i]
		}
		handle(out)

		opts.Continue = list.Continue
		if opts.Continue == "" {
			break
		}
	}
	return nil
}

func streamPaginatedNodes(ctx context.Context, cl client.Client, limit int64, handle func(nodes []*corev1.Node)) error {
	opts := &client.ListOptions{Limit: limit}

	for {
		var list corev1.NodeList
		if err := cl.List(ctx, &list, opts); err != nil {
			return fmt.Errorf("failed to list nodes: %w", err)
		}

		items := make([]*corev1.Node, len(list.Items))
		for i := range list.Items {
			items[i] = &list.Items[i]
		}

		handle(items)

		opts.Continue = list.Continue
		if opts.Continue == "" {
			break
		}
	}

	return nil
}

// listAsPartial lists objects with the given GVK. WARN: Does NOT paginate results.
func listAsPartial(ctx context.Context, c client.Client, gvk schema.GroupVersionKind) ([]metav1.PartialObjectMetadata, error) {
	ll := new(metav1.PartialObjectMetadataList)
	ll.SetGroupVersionKind(gvk)

	// NOTE: PartialObjectMetadata does not support Continue until it explicitly set in the cached client to bypass the cache for this Kind.
	// Using the PartialObjectMetadata is already an optimization so we let the cached client to do his job without limiting results.
	// Suggestion: split the function into 2 (mgmt and child), because we have the control over the latter.
	if err := c.List(ctx, ll); err != nil {
		return nil, fmt.Errorf("failed to list %s: %w", gvk.String(), err)
	}

	return ll.Items, nil
}
