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

package kube

import (
	"context"
	"errors"
	"fmt"
	"os"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	DefaultSystemNamespace         = "kcm-system"
	DefaultStateManagementProvider = "ksm-projectsveltos"

	DefaultStateManagementProviderSelectorKey   = "ksm.k0rdent.mirantis.com/adapter"
	DefaultStateManagementProviderSelectorValue = "kcm-controller-manager"
)

func EnsureDeleteAllOf(ctx context.Context, cl client.Client, gvk schema.GroupVersionKind, opts *client.ListOptions, exclude ...string) (requeue bool, err error) {
	l := ctrl.LoggerFrom(ctx).V(1).WithName("ensure-delete")
	itemsList := &metav1.PartialObjectMetadataList{}
	itemsList.SetGroupVersionKind(gvk)
	if err := cl.List(ctx, itemsList, opts); err != nil {
		return false, fmt.Errorf("failed to list %s: %w", gvk.String(), err)
	}

	excluded := make(map[string]struct{}, len(exclude))
	for _, name := range exclude {
		excluded[name] = struct{}{}
	}

	var errs error
	for _, item := range itemsList.Items {
		if _, skip := excluded[item.Name]; skip {
			continue
		}
		requeue = true
		if item.DeletionTimestamp.IsZero() {
			if err := cl.Delete(ctx, &item); client.IgnoreNotFound(err) != nil {
				errs = errors.Join(errs, fmt.Errorf("failed to delete %s %s/%s: %w", gvk.String(), item.Namespace, item.Name, err))
				continue
			}
		}

		l.Info("waiting for object removal", "gvk", gvk.String(), "object namespace", item.Namespace, "object name", item.Name)
	}

	if errs != nil {
		return false, errs
	}

	return requeue, nil
}

func CurrentNamespace() string {
	// Referencing https://github.com/kubernetes/kubernetes/blob/7353b6a93d5a1535787b87c87acfc178d6ea67e9/staging/src/k8s.io/client-go/tools/clientcmd/client_config.go#L646-L661
	// for simplicity

	ns, found := os.LookupEnv("POD_NAMESPACE")
	if found {
		return ns
	}

	const serviceAccountNs = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	nsb, err := os.ReadFile(serviceAccountNs)
	if err == nil && len(nsb) > 0 {
		return string(nsb)
	}

	return DefaultSystemNamespace
}

func AddOwnerReference(dependent, owner client.Object) (changed bool) {
	ownerRefs := dependent.GetOwnerReferences()
	if ownerRefs == nil {
		ownerRefs = []metav1.OwnerReference{}
	}
	for _, ref := range ownerRefs {
		if ref.UID == owner.GetUID() {
			return false
		}
	}
	apiVersion, kind := owner.GetObjectKind().GroupVersionKind().ToAPIVersionAndKind()
	ownerRefs = append(ownerRefs,
		metav1.OwnerReference{
			APIVersion: apiVersion,
			Kind:       kind,
			Name:       owner.GetName(),
			UID:        owner.GetUID(),
		},
	)
	dependent.SetOwnerReferences(ownerRefs)
	return true
}
