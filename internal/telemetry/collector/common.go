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

	addoncontrollerv1beta1 "github.com/projectsveltos/addon-controller/api/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// copied from "github.com/k0sproject/k0smotron/api/controlplane/v1beta1" to avoid dependency
const k0sClusterIDAnnotation = "k0sproject.io/cluster-id"

func getK0sClusterID(partialCapiClusters *[]metav1.PartialObjectMetadata, cldKey client.ObjectKey) string {
	if partialCapiClusters == nil { // sanity check
		return ""
	}
	for _, v := range *partialCapiClusters {
		if cldKey.Namespace == v.Namespace &&
			cldKey.Name == v.Name {
			return v.Annotations[k0sClusterIDAnnotation]
		}
	}

	return ""
}

// countChildUserServices counts only child user services, excluding mgmt cluster.
//
// To count mgmt cluster services, [github.com/projectsveltos/addon-controller/api/v1beta1.ClusterProfile]
// selector should be used instead of [github.com/projectsveltos/addon-controller/api/v1beta1.Profile]
// to match [github.com/projectsveltos/libsveltos/api/v1beta1.SveltosCluster] labels instead of
// [sigs.k8s.io/cluster-api/api/core/v1beta2.Cluster].
func countChildUserServices(profiles *addoncontrollerv1beta1.ProfileList, capiClusters *[]metav1.PartialObjectMetadata) int {
	if profiles == nil || capiClusters == nil { // sanity check
		return 0
	}

	uniqCharts, uniqKustomize := make(map[string]struct{}), make(map[string]struct{})

	for _, profile := range profiles.Items {
		sel, err := metav1.LabelSelectorAsSelector(&profile.Spec.ClusterSelector.LabelSelector)
		if err != nil {
			continue // NOTE: better continue than drop
		}

		for _, cl := range *capiClusters {
			if sel.Matches(labels.Set(cl.Labels)) {
				for _, hc := range profile.Spec.HelmCharts {
					uniqCharts[hc.ReleaseNamespace+"/"+hc.ReleaseName] = struct{}{}
				}
				for _, kr := range profile.Spec.KustomizationRefs {
					uniqKustomize[kr.TargetNamespace+"/"+kr.Name] = struct{}{}
				}
			}
		}
	}

	return len(uniqCharts) + len(uniqKustomize)
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

// defaultPartialListLimit is the page size used by [listAsPartial], matching kubectl's default list chunk size.
const defaultPartialListLimit = 500

// listAsPartial lists all objects with the given GVK, paging through the results via
// the List continuation token so that clusters with more objects than fit in a single
// API response (e.g. CAPI Clusters, DaemonSets, VirtualMachineInstances) are not truncated.
func listAsPartial(ctx context.Context, c client.Client, gvk schema.GroupVersionKind) ([]metav1.PartialObjectMetadata, error) {
	return listAsPartialWithLimit(ctx, c, gvk, defaultPartialListLimit)
}

func listAsPartialWithLimit(ctx context.Context, c client.Client, gvk schema.GroupVersionKind, limit int64) ([]metav1.PartialObjectMetadata, error) {
	var items []metav1.PartialObjectMetadata

	opts := &client.ListOptions{Limit: limit}
	for {
		ll := new(metav1.PartialObjectMetadataList)
		ll.SetGroupVersionKind(gvk)

		if err := c.List(ctx, ll, opts); err != nil {
			return nil, fmt.Errorf("failed to list %s: %w", gvk.String(), err)
		}

		items = append(items, ll.Items...)

		opts.Continue = ll.Continue
		if opts.Continue == "" {
			break
		}
	}

	return items, nil
}

func getChildScheme() (*runtime.Scheme, error) {
	s := runtime.NewScheme()

	for _, f := range []func(*runtime.Scheme) error{
		corev1.AddToScheme,
		metav1.AddMetaToScheme,
		kubevirtv1.AddToScheme,
		appsv1.AddToScheme,
	} {
		if err := f(s); err != nil {
			return nil, fmt.Errorf("failed to add to scheme: %w", err)
		}
	}

	return s, nil
}
