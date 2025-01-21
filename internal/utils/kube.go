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

package utils

import (
	"context"
	"errors"
	"fmt"
	"os"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	DefaultSystemNamespace = "kcm-system"
)

func EnsureDeleteAllOf(ctx context.Context, cl client.Client, gvk schema.GroupVersionKind, opts *client.ListOptions) error {
	itemsList := &metav1.PartialObjectMetadataList{}
	itemsList.SetGroupVersionKind(gvk)
	if err := cl.List(ctx, itemsList, opts); err != nil {
		return err
	}
	var errs error
	for _, item := range itemsList.Items {
		if item.DeletionTimestamp.IsZero() {
			if err := cl.Delete(ctx, &item); client.IgnoreNotFound(err) != nil {
				errs = errors.Join(errs, err)
				continue
			}
		}
		errs = errors.Join(errs, fmt.Errorf("waiting for %s %s/%s removal", gvk.Kind, item.Namespace, item.Name))
	}
	return errs
}

func CurrentNamespace() string {
	// Referencing https://github.com/kubernetes/kubernetes/blob/master/staging/src/k8s.io/client-go/tools/clientcmd/client_config.go#L631-L646
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
