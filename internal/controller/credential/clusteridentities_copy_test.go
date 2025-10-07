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

package credential

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/test/objects/clusteridentity"
	"github.com/K0rdent/kcm/test/objects/credential"
	"github.com/K0rdent/kcm/test/objects/providerinterface"
	"github.com/K0rdent/kcm/test/scheme"
)

const (
	systemNamespace = "test-system"

	infrastructureGroup = "infrastructure.cluster.x-k8s.io"
	v1beta2Version      = "v1beta2"

	clusterScopedClusterIdentityKind   = "ClusterScopedClusterIdentity"
	namespaceScopedClusterIdentityKind = "NamespaceScopedClusterIdentity"

	clusterIdentityName          = "testIdentityName"
	clusterIdentitySecretRefName = "testIdentitySecretName"
	testNamespace                = "test1"
)

var (
	infraAPIVersion = infrastructureGroup + "/" + v1beta2Version

	kcmManagedLabels = map[string]string{
		kcmv1.KCMManagedLabelKey: kcmv1.KCMManagedLabelValue,
	}

	secretIdentityInSystemNamespace = clusteridentity.New(
		clusteridentity.WithName(clusterIdentitySecretRefName),
		clusteridentity.WithNamespace(systemNamespace),
	)

	clusterScopedIdentityWithSecretRef = clusteridentity.New(
		clusteridentity.WithAPIVersion(infraAPIVersion),
		clusteridentity.WithKind(clusterScopedClusterIdentityKind),
		clusteridentity.WithName(clusterIdentityName),
		clusteridentity.WithData(map[string]any{
			"spec": map[string]any{
				"secretRef": clusterIdentitySecretRefName,
			},
		}),
	)

	namespaceScopedIdentityInSystemNamespace = clusteridentity.New(
		clusteridentity.WithAPIVersion(infraAPIVersion),
		clusteridentity.WithKind(namespaceScopedClusterIdentityKind),
		clusteridentity.WithName(clusterIdentityName),
		clusteridentity.WithNamespace(systemNamespace),
		clusteridentity.WithData(map[string]any{
			"spec": map[string]any{
				"clientSecret": map[string]any{
					"name":      clusterIdentitySecretRefName,
					"namespace": systemNamespace,
				},
			},
		}),
	)

	namespaceScopedIdentity = clusteridentity.New(
		clusteridentity.WithAPIVersion(infraAPIVersion),
		clusteridentity.WithKind(namespaceScopedClusterIdentityKind),
		clusteridentity.WithName(clusterIdentityName),
		clusteridentity.WithNamespace(testNamespace),
		clusteridentity.WithData(map[string]any{
			"spec": map[string]any{
				"clientSecret": map[string]any{
					"name":      clusterIdentitySecretRefName,
					"namespace": testNamespace,
				},
			},
		}),
	)

	someProviderInterface = providerinterface.NewProviderInterface(
		providerinterface.WithName("some-provider-interface-for-testing"),
		providerinterface.WithClusterIdentities([]kcmv1.ClusterIdentity{
			// cluster-scoped ClusterIdentity with Secret reference (namespaceFieldPath is empty, meaning
			// we expect the Identity reference to exist in the system namespace
			{
				GroupVersionKind: kcmv1.GroupVersionKind{
					Group:   infrastructureGroup,
					Version: v1beta2Version,
					Kind:    clusterScopedClusterIdentityKind,
				},
				References: []kcmv1.ClusterIdentityReference{
					{
						GroupVersionKind: kcmv1.GroupVersionKind{
							Group:   "",
							Version: "v1",
							Kind:    "Secret",
						},
						NameFieldPath: "spec.secretRef",
					},
				},
			},
			// namespace-scoped ClusterIdentity with Secret reference with custom namespace
			{
				GroupVersionKind: kcmv1.GroupVersionKind{
					Group:   infrastructureGroup,
					Version: v1beta2Version,
					Kind:    namespaceScopedClusterIdentityKind,
				},
				References: []kcmv1.ClusterIdentityReference{
					{
						GroupVersionKind: kcmv1.GroupVersionKind{
							Group:   "",
							Version: "v1",
							Kind:    "Secret",
						},
						NameFieldPath:      "spec.clientSecret.name",
						NamespaceFieldPath: "spec.clientSecret.namespace",
					},
				},
			},
			// cluster identity without any references
			{
				GroupVersionKind: kcmv1.GroupVersionKind{
					Group:   "",
					Version: "v1",
					Kind:    "Secret",
				},
			},
		}),
	)
)

type clusterIdentity struct {
	corev1.ObjectReference
	labels      map[string]string
	shouldExist bool
}

func Test_CopyClusterIdentities(t *testing.T) {
	g := NewWithT(t)

	ctx := t.Context()

	tests := []struct {
		name                   string
		targetNamespace        string
		existingManagementObjs []runtime.Object
		existingRegionalObjs   []runtime.Object
		cred                   *kcmv1.Credential
		objsToCheck            []clusterIdentity
		err                    string
	}{
		{
			name: "Credential is not-regional and is not managed by KCM: should create nothing but succeed",
			cred: credential.NewCredential(credential.WithIdentityRef(&corev1.ObjectReference{
				APIVersion: infraAPIVersion,
				Kind:       clusterScopedClusterIdentityKind,
				Name:       clusterIdentityName,
			})),
		},
		{
			name: "kind is not defined in the ProviderInterface: should create nothing but succeed",
			cred: credential.NewCredential(
				credential.WithLabels(kcmManagedLabels),
				credential.WithIdentityRef(&corev1.ObjectReference{
					APIVersion: infraAPIVersion,
					Kind:       "UnknownKind",
					Name:       clusterIdentityName,
				})),
		},
		{
			name:                   "Credential is in system namespace of the management cluster, cluster-scoped identity, nothing to copy, should succeed",
			existingManagementObjs: []runtime.Object{clusterScopedIdentityWithSecretRef, secretIdentityInSystemNamespace},
			cred: credential.NewCredential(
				credential.WithNamespace(systemNamespace),
				credential.WithLabels(kcmManagedLabels),
				credential.WithIdentityRef(&corev1.ObjectReference{
					APIVersion: infraAPIVersion,
					Kind:       clusterScopedClusterIdentityKind,
					Name:       clusterIdentityName,
				})),
			objsToCheck: []clusterIdentity{
				{
					ObjectReference: corev1.ObjectReference{
						APIVersion: infraAPIVersion,
						Kind:       clusterScopedClusterIdentityKind,
						Name:       clusterIdentityName,
					},
					shouldExist: true,
				},
				{
					ObjectReference: corev1.ObjectReference{
						APIVersion: "v1",
						Kind:       "Secret",
						Name:       clusterIdentitySecretRefName,
						Namespace:  systemNamespace,
					},
					shouldExist: true,
				},
			},
		},
		{
			name:                   "Credential belongs to the management cluster, cluster-scoped identity, the identity does not exist, should fail",
			existingManagementObjs: []runtime.Object{},
			cred: credential.NewCredential(
				credential.WithLabels(kcmManagedLabels),
				credential.WithIdentityRef(&corev1.ObjectReference{
					APIVersion: infraAPIVersion,
					Kind:       clusterScopedClusterIdentityKind,
					Name:       clusterIdentityName,
				})),
			err: fmt.Sprintf("failed to collect all Cluster Identities for default/credential Credential: failed to get ClusterIdentity object of Kind=%s %s: clusterscopedclusteridentities.infrastructure.cluster.x-k8s.io %q not found", clusterScopedClusterIdentityKind, clusterIdentityName, clusterIdentityName),
		},
		{
			name: "Credential is in custom namespace of the management cluster, cluster-scoped identity, the secret does not exist in the system namespace, should fail",
			existingManagementObjs: []runtime.Object{
				clusterScopedIdentityWithSecretRef,
				clusteridentity.New(
					clusteridentity.WithName(clusterIdentitySecretRefName),
					clusteridentity.WithNamespace(testNamespace),
				),
			},
			cred: credential.NewCredential(
				credential.WithNamespace(testNamespace),
				credential.WithLabels(kcmManagedLabels),
				credential.WithIdentityRef(&corev1.ObjectReference{
					APIVersion: infraAPIVersion,
					Kind:       clusterScopedClusterIdentityKind,
					Name:       clusterIdentityName,
				})),
			err: fmt.Sprintf("failed to collect all Cluster Identities for %s/credential Credential: failed to get ClusterIdentity reference object of Kind=%s %s/%s: secrets %q not found", testNamespace, clusterScopedClusterIdentityKind, systemNamespace, clusterIdentitySecretRefName, clusterIdentitySecretRefName),
		},
		{
			name: "Credential is in custom namespace of the management cluster, namespace-scoped identity which does not exist in the system namespace, should fail",
			existingManagementObjs: []runtime.Object{
				namespaceScopedIdentity, // identity exists but in the test namespace
				secretIdentityInSystemNamespace,
			},
			cred: credential.NewCredential(
				credential.WithNamespace(testNamespace),
				credential.WithLabels(kcmManagedLabels),
				credential.WithIdentityRef(&corev1.ObjectReference{
					APIVersion: infraAPIVersion,
					Kind:       namespaceScopedClusterIdentityKind,
					Name:       clusterIdentityName,
					Namespace:  systemNamespace,
				})),
			err: fmt.Sprintf("failed to collect all Cluster Identities for %s/credential Credential: failed to get ClusterIdentity object of Kind=%s %s/%s: namespacescopedclusteridentities.infrastructure.cluster.x-k8s.io %q not found", testNamespace, namespaceScopedClusterIdentityKind, systemNamespace, clusterIdentityName, clusterIdentityName),
		},
		{
			name:                   "Credential is in custom namespace of the management cluster, cluster-scoped identity, nothing to copy, should succeed",
			existingManagementObjs: []runtime.Object{clusterScopedIdentityWithSecretRef, secretIdentityInSystemNamespace},
			cred: credential.NewCredential(
				credential.WithNamespace(testNamespace),
				credential.WithLabels(kcmManagedLabels),
				credential.WithIdentityRef(&corev1.ObjectReference{
					APIVersion: infraAPIVersion,
					Kind:       clusterScopedClusterIdentityKind,
					Name:       clusterIdentityName,
				})),
			objsToCheck: []clusterIdentity{
				{
					ObjectReference: corev1.ObjectReference{
						APIVersion: infraAPIVersion,
						Kind:       clusterScopedClusterIdentityKind,
						Name:       clusterIdentityName,
					},
					shouldExist: true,
				},
				{
					ObjectReference: corev1.ObjectReference{
						APIVersion: "v1",
						Kind:       "Secret",
						Name:       clusterIdentitySecretRefName,
						Namespace:  systemNamespace,
					},
					shouldExist: true,
				},
			},
		},
		{
			name: "Credential is in custom namespace of the management cluster, namespaced-scoped identities exist " +
				"in the system namespace, should copy the identity and ref to the Credential namespace",
			existingManagementObjs: []runtime.Object{
				namespaceScopedIdentityInSystemNamespace,
				clusteridentity.New(
					clusteridentity.WithName(clusterIdentitySecretRefName),
					clusteridentity.WithNamespace(systemNamespace),
				),
			},
			cred: credential.NewCredential(
				credential.WithNamespace("test2"),
				credential.WithLabels(kcmManagedLabels),
				credential.WithIdentityRef(&corev1.ObjectReference{
					APIVersion: infraAPIVersion,
					Kind:       namespaceScopedClusterIdentityKind,
					Name:       clusterIdentityName,
					Namespace:  "test2",
				})),
			objsToCheck: []clusterIdentity{
				{
					ObjectReference: corev1.ObjectReference{
						APIVersion: infraAPIVersion,
						Kind:       namespaceScopedClusterIdentityKind,
						Name:       clusterIdentityName,
						Namespace:  "test2",
					},
					labels:      kcmManagedLabels,
					shouldExist: true,
				},
				{
					ObjectReference: corev1.ObjectReference{
						APIVersion: "v1",
						Kind:       "Secret",
						Name:       clusterIdentitySecretRefName,
						Namespace:  "test2",
					},
					labels:      kcmManagedLabels,
					shouldExist: true,
				},
			},
		},
		{
			name: "Credential is in custom namespace of the management cluster, identity is a secret without any references " +
				"should copy the secret from the referenced namespace to the Credential namespace",
			existingManagementObjs: []runtime.Object{
				clusteridentity.New(
					clusteridentity.WithName(clusterIdentitySecretRefName),
					clusteridentity.WithNamespace(systemNamespace),
				),
			},
			cred: credential.NewCredential(
				credential.WithNamespace("test2"),
				credential.WithLabels(kcmManagedLabels),
				credential.WithIdentityRef(&corev1.ObjectReference{
					APIVersion: "v1",
					Kind:       "Secret",
					Name:       clusterIdentitySecretRefName,
					Namespace:  "test2",
				})),
			objsToCheck: []clusterIdentity{
				{
					ObjectReference: corev1.ObjectReference{
						APIVersion: "v1",
						Kind:       "Secret",
						Name:       clusterIdentitySecretRefName,
						Namespace:  "test2",
					},
					labels:      kcmManagedLabels,
					shouldExist: true,
				},
			},
		},
		{
			name:                   "Credential belongs to the regional cluster, cluster-scoped identity, the identity does not exist, should fail",
			existingManagementObjs: []runtime.Object{},
			cred: credential.NewCredential(
				credential.WithNamespace(testNamespace),
				credential.WithRegion("region1"),
				credential.WithIdentityRef(&corev1.ObjectReference{
					APIVersion: infraAPIVersion,
					Kind:       clusterScopedClusterIdentityKind,
					Name:       clusterIdentityName,
				})),
			err: fmt.Sprintf("failed to collect all Cluster Identities for %s/credential Credential: failed to get ClusterIdentity object of Kind=%s %s: clusterscopedclusteridentities.infrastructure.cluster.x-k8s.io %q not found", testNamespace, clusterScopedClusterIdentityKind, clusterIdentityName, clusterIdentityName),
		},
		{
			name: "Credential is in custom namespace of the regional cluster, cluster-scoped identity, the secret does not exist in the system namespace, should fail",
			existingManagementObjs: []runtime.Object{
				clusterScopedIdentityWithSecretRef,
				clusteridentity.New(
					clusteridentity.WithName(clusterIdentitySecretRefName),
					clusteridentity.WithNamespace(testNamespace),
				),
			},
			cred: credential.NewCredential(
				credential.WithNamespace(testNamespace),
				credential.WithRegion("region1"),
				credential.WithIdentityRef(&corev1.ObjectReference{
					APIVersion: infraAPIVersion,
					Kind:       clusterScopedClusterIdentityKind,
					Name:       clusterIdentityName,
				})),
			err: fmt.Sprintf("failed to collect all Cluster Identities for %s/credential Credential: failed to get ClusterIdentity reference object of Kind=%s %s/%s: secrets %q not found", testNamespace, clusterScopedClusterIdentityKind, systemNamespace, clusterIdentitySecretRefName, clusterIdentitySecretRefName),
		},
		{
			name: "Credential is in custom namespace of the regional cluster, namespace-scoped identity which does not exist in the system namespace, should fail",
			existingManagementObjs: []runtime.Object{
				namespaceScopedIdentity, // identity exists but in the test namespace
				secretIdentityInSystemNamespace,
			},
			cred: credential.NewCredential(
				credential.WithNamespace(testNamespace),
				credential.WithRegion("region1"),
				credential.WithIdentityRef(&corev1.ObjectReference{
					APIVersion: infraAPIVersion,
					Kind:       namespaceScopedClusterIdentityKind,
					Name:       clusterIdentityName,
					Namespace:  testNamespace,
				})),
			err: fmt.Sprintf("failed to collect all Cluster Identities for %s/credential Credential: failed to get ClusterIdentity object of Kind=%s %s/%s: namespacescopedclusteridentities.infrastructure.cluster.x-k8s.io %q not found", testNamespace, namespaceScopedClusterIdentityKind, testNamespace, clusterIdentityName, clusterIdentityName),
		},
		{
			name: "Credential is in custom namespace of the regional cluster, cluster-scoped identity " +
				"should copy identities to the system namespace of the regional cluster",
			existingManagementObjs: []runtime.Object{clusterScopedIdentityWithSecretRef, secretIdentityInSystemNamespace},
			cred: credential.NewCredential(
				credential.WithNamespace(testNamespace),
				credential.WithRegion("region1"),
				credential.WithIdentityRef(&corev1.ObjectReference{
					APIVersion: infraAPIVersion,
					Kind:       clusterScopedClusterIdentityKind,
					Name:       clusterIdentityName,
				})),
			objsToCheck: []clusterIdentity{
				{
					ObjectReference: corev1.ObjectReference{
						APIVersion: infraAPIVersion,
						Kind:       clusterScopedClusterIdentityKind,
						Name:       clusterIdentityName,
					},
					labels:      kcmManagedLabels,
					shouldExist: true,
				},
				{
					ObjectReference: corev1.ObjectReference{
						APIVersion: "v1",
						Kind:       "Secret",
						Name:       clusterIdentitySecretRefName,
						Namespace:  systemNamespace,
					},
					labels:      kcmManagedLabels,
					shouldExist: true,
				},
			},
		},
		{
			name: "Credential is in custom namespace of the regional cluster, namespaced-scoped identities exist " +
				"in the system namespace, should copy the identity and ref to the Credential namespace",
			existingManagementObjs: []runtime.Object{
				namespaceScopedIdentityInSystemNamespace,
				clusteridentity.New(
					clusteridentity.WithName(clusterIdentitySecretRefName),
					clusteridentity.WithNamespace(systemNamespace),
				),
			},
			cred: credential.NewCredential(
				credential.WithNamespace("test2"),
				credential.WithRegion("region1"),
				credential.WithIdentityRef(&corev1.ObjectReference{
					APIVersion: infraAPIVersion,
					Kind:       namespaceScopedClusterIdentityKind,
					Name:       clusterIdentityName,
					Namespace:  "test2",
				})),
			objsToCheck: []clusterIdentity{
				{
					ObjectReference: corev1.ObjectReference{
						APIVersion: infraAPIVersion,
						Kind:       namespaceScopedClusterIdentityKind,
						Name:       clusterIdentityName,
						Namespace:  "test2",
					},
					labels:      kcmManagedLabels,
					shouldExist: true,
				},
				{
					ObjectReference: corev1.ObjectReference{
						APIVersion: "v1",
						Kind:       "Secret",
						Name:       clusterIdentitySecretRefName,
						Namespace:  "test2",
					},
					labels:      kcmManagedLabels,
					shouldExist: true,
				},
			},
		},
		{
			name: "Credential is in custom namespace of the regional cluster, namespaced-scoped identities exist " +
				"in the system namespace, the identity already exists and is not managed by KCM, should not update it," +
				"should copy the secret to the Credential namespace",
			existingManagementObjs: []runtime.Object{
				namespaceScopedIdentityInSystemNamespace,
				clusteridentity.New(
					clusteridentity.WithName(clusterIdentitySecretRefName),
					clusteridentity.WithNamespace(systemNamespace),
				),
			},
			existingRegionalObjs: []runtime.Object{
				clusteridentity.New(
					clusteridentity.WithAPIVersion(infraAPIVersion),
					clusteridentity.WithKind(namespaceScopedClusterIdentityKind),
					clusteridentity.WithName(clusterIdentityName),
					clusteridentity.WithNamespace("test2"),
					clusteridentity.WithData(map[string]any{
						"spec": map[string]any{
							"clientSecret": map[string]any{
								"name":      clusterIdentitySecretRefName,
								"namespace": systemNamespace,
							},
						},
					}),
				),
			},
			cred: credential.NewCredential(
				credential.WithNamespace("test2"),
				credential.WithRegion("region1"),
				credential.WithIdentityRef(&corev1.ObjectReference{
					APIVersion: infraAPIVersion,
					Kind:       namespaceScopedClusterIdentityKind,
					Name:       clusterIdentityName,
					Namespace:  systemNamespace,
				})),
			objsToCheck: []clusterIdentity{
				{
					ObjectReference: corev1.ObjectReference{
						APIVersion: infraAPIVersion,
						Kind:       namespaceScopedClusterIdentityKind,
						Name:       clusterIdentityName,
						Namespace:  "test2",
					},
					shouldExist: true,
				},
				{
					ObjectReference: corev1.ObjectReference{
						APIVersion: "v1",
						Kind:       "Secret",
						Name:       clusterIdentitySecretRefName,
						Namespace:  "test2",
					},
					labels:      kcmManagedLabels,
					shouldExist: true,
				},
			},
		},
		{
			name: "Credential is in custom namespace of the regional cluster, identity is a secret without any references " +
				"should copy the secret from the system namespace to the Credential namespace",
			existingManagementObjs: []runtime.Object{
				clusteridentity.New(
					clusteridentity.WithName(clusterIdentitySecretRefName),
					clusteridentity.WithNamespace(systemNamespace),
				),
			},
			cred: credential.NewCredential(
				credential.WithNamespace("test3"),
				credential.WithRegion("region1"),
				credential.WithIdentityRef(&corev1.ObjectReference{
					APIVersion: "v1",
					Kind:       "Secret",
					Name:       clusterIdentitySecretRefName,
					Namespace:  "test3",
				})),
			objsToCheck: []clusterIdentity{
				{
					ObjectReference: corev1.ObjectReference{
						APIVersion: "v1",
						Kind:       "Secret",
						Name:       clusterIdentitySecretRefName,
						Namespace:  "test3",
					},
					labels:      kcmManagedLabels,
					shouldExist: true,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgmtClient, rgnClient := setupClients(tt.existingManagementObjs, tt.existingRegionalObjs, tt.cred.Spec.Region)
			err := CopyClusterIdentities(ctx, mgmtClient, rgnClient, tt.cred, systemNamespace)
			if tt.err != "" {
				g.Expect(err).To(HaveOccurred())
				if err.Error() != tt.err {
					t.Fatalf("expected error '%s', got error: %s", tt.err, err.Error())
				}
			} else {
				g.Expect(err).To(Succeed())
			}
			checkObjectsExistence(t, ctx, rgnClient, tt.objsToCheck)
		})
	}
}

func setupClients(existingManagementObjs, existingRegionalObjs []runtime.Object, region string) (mgmtClient, rgnClient client.Client) {
	existingRegionalObjs = append(existingRegionalObjs, someProviderInterface)

	// when the region is empty, the regional cluster is the management cluster. Expecting all the regional
	// objects exist in it.
	if region == "" {
		existingManagementObjs = append(existingManagementObjs, existingRegionalObjs...)
	}
	mgmtClient = fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithRuntimeObjects(existingManagementObjs...).
		Build()

	if region == "" {
		return mgmtClient, mgmtClient
	}
	return mgmtClient, fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithRuntimeObjects(existingRegionalObjs...).
		Build()
}

func checkObjectsExistence(t *testing.T, ctx context.Context, rgnClient client.Client, expectedObjs []clusterIdentity) {
	t.Helper()

	g := NewWithT(t)
	for _, obj := range expectedObjs {
		ci := &unstructured.Unstructured{}
		ci.SetAPIVersion(obj.APIVersion)
		ci.SetKind(obj.Kind)

		key := client.ObjectKey{Name: obj.Name}
		if obj.Namespace != "" {
			key = client.ObjectKey{Namespace: obj.Namespace, Name: obj.Name}
		}
		err := rgnClient.Get(ctx, key, ci)
		if obj.shouldExist {
			g.Expect(err).To(Succeed())
			g.Expect(ci.GetLabels()).To(Equal(obj.labels))
		} else {
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}
	}
}
