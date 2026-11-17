// Copyright 2026
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

package validation

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"errors"
	"fmt"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

var (
	errCAKeysMissing = errors.New("secret must contain tls.crt and tls.key keys")

	// errEtcdCAKeyNotRSA: k0smotron derives apiserver-etcd-client/etcd-server/etcd-peer certs
	// from the "etcd" CA by hardcoding x509.SHA256WithRSA as the signer algorithm
	// (k0sproject/k0smotron internal/controller/k0smotron.io/k0smotroncluster_certs.go), regardless
	// of the supplied CA's actual key type. An ECDSA or Ed25519 etcd CA fails there with "x509:
	// requested SignatureAlgorithm does not match private key type" and the control plane never
	// comes up, so this must be caught here rather than surfacing as an opaque runtime failure.
	errEtcdCAKeyNotRSA = errors.New("etcd CA private key must be RSA: k0smotron signs etcd-derived certificates with SHA256WithRSA regardless of the supplied key's actual type")
)

// etcdK0smotronType is the k0smotron certificateRefs "type" value whose CA key must be RSA.
const etcdK0smotronType = "etcd"

// ValidateClusterCertificateAuthority checks that every configured ref in clCA resolves to
// something real: a secretRef names a Secret that exists in the same namespace and holds a
// tls.crt/tls.key keypair (the shape k0smotron's certificateRefs mounts into the control plane),
// and an issuerRef names an Issuer or ClusterIssuer that exists.
func ValidateClusterCertificateAuthority(ctx context.Context, mgmtClient client.Client, clCA *kcmv1.ClusterCertificateAuthority) error {
	for _, role := range clCA.Spec.Roles() {
		switch {
		case role.Ref.SecretRef != nil:
			if err := validateCASecretRef(ctx, mgmtClient, clCA.Namespace, role.Ref.SecretRef.Name, role.K0smotronType); err != nil {
				return fmt.Errorf("invalid secretRef for %s: %w", role.K0smotronType, err)
			}
		case role.Ref.IssuerRef != nil:
			if err := validateCAIssuerRef(ctx, mgmtClient, clCA.Namespace, role.Ref.IssuerRef); err != nil {
				return fmt.Errorf("invalid issuerRef for %s: %w", role.K0smotronType, err)
			}
		}
	}

	return nil
}

func validateCASecretRef(ctx context.Context, mgmtClient client.Client, namespace, name, k0smotronType string) error {
	secret := new(corev1.Secret)
	if err := mgmtClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, secret); err != nil {
		return fmt.Errorf("failed to get Secret %s/%s: %w", namespace, name, err)
	}

	certPEM, ok := secret.Data[corev1.TLSCertKey]
	if !ok {
		return errCAKeysMissing
	}
	keyPEM, ok := secret.Data[corev1.TLSPrivateKeyKey]
	if !ok {
		return errCAKeysMissing
	}

	if k0smotronType != etcdK0smotronType {
		return nil
	}

	keypair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return fmt.Errorf("failed to parse tls.crt/tls.key: %w", err)
	}
	if _, ok := keypair.PrivateKey.(*rsa.PrivateKey); !ok {
		return errEtcdCAKeyNotRSA
	}

	return nil
}

func validateCAIssuerRef(ctx context.Context, mgmtClient client.Client, namespace string, ref *cmmeta.IssuerReference) error {
	if ref.Kind == "ClusterIssuer" {
		issuer := new(certmanagerv1.ClusterIssuer)
		if err := mgmtClient.Get(ctx, client.ObjectKey{Name: ref.Name}, issuer); err != nil {
			return fmt.Errorf("failed to get ClusterIssuer %s: %w", ref.Name, err)
		}
		return nil
	}

	issuer := new(certmanagerv1.Issuer)
	if err := mgmtClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, issuer); err != nil {
		return fmt.Errorf("failed to get Issuer %s/%s: %w", namespace, ref.Name, err)
	}

	return nil
}

// ClusterCertificateAuthorityDeletionAllowed prevents deleting a ClusterCertificateAuthority that
// a ClusterDeployment still references.
func ClusterCertificateAuthorityDeletionAllowed(ctx context.Context, mgmtClient client.Client, clCA *kcmv1.ClusterCertificateAuthority) error {
	return deletionAllowedIfUnreferenced(ctx, mgmtClient, clCA, kcmv1.ClusterDeploymentCertificateAuthorityIndexKey, "ClusterCertificateAuthority")
}
