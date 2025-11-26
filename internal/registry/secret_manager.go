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

package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
)

const (
	// RegistrySecretLabel is the label applied to all registry secrets managed by KCM.
	RegistrySecretLabel = "k0rdent.mirantis.com/registry-secret"
	// RegistrySecretLabelValue is the value for the registry secret label.
	RegistrySecretLabelValue = "true"

	// Default keys for Opaque secrets containing registry credentials
	UsernameKey = "username"
	PasswordKey = "password"
	TokenKey    = "token"
)

// DockerConfigJSON represents the structure of a Docker config.json file
type DockerConfigJSON struct {
	Auths map[string]DockerConfigEntry `json:"auths"`
}

// DockerConfigEntry represents an authentication entry for a single registry
type DockerConfigEntry struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Auth     string `json:"auth,omitempty"`
}

// SecretManager handles creation, validation, and conversion of registry credential secrets.
type SecretManager struct {
	client client.Client
}

// NewSecretManager creates a new SecretManager instance.
func NewSecretManager(cl client.Client) *SecretManager {
	return &SecretManager{client: cl}
}

// GetCredentialsFromSpec extracts registry credentials from either inline values or a Secret reference.
func (sm *SecretManager) GetCredentialsFromSpec(ctx context.Context, credentials *kcmv1.RegistryCredentials, namespace string) (username, password string, err error) {
	if credentials == nil {
		return "", "", fmt.Errorf("credentials is nil")
	}

	// If SecretRef is specified, read from the Secret
	if credentials.SecretRef != nil {
		return sm.getCredentialsFromSecret(ctx, credentials.SecretRef.Name, namespace)
	}

	// Otherwise, use inline credentials
	if credentials.Username == "" && credentials.Password == "" {
		return "", "", fmt.Errorf("neither secretRef nor inline credentials provided")
	}

	return credentials.Username, credentials.Password, nil
}

// getCredentialsFromSecret reads credentials from an Opaque Secret.
func (sm *SecretManager) getCredentialsFromSecret(ctx context.Context, secretName, namespace string) (username, password string, err error) {
	secret := &corev1.Secret{}
	err = sm.client.Get(ctx, client.ObjectKey{Name: secretName, Namespace: namespace}, secret)
	if err != nil {
		return "", "", fmt.Errorf("failed to get secret %s/%s: %w", namespace, secretName, err)
	}

	// Check if it's a dockerconfigjson secret
	if secret.Type == corev1.SecretTypeDockerConfigJson {
		return sm.extractCredentialsFromDockerConfig(secret.Data[corev1.DockerConfigJsonKey])
	}

	// Otherwise, expect Opaque secret with username/password or token
	usernameBytes, hasUsername := secret.Data[UsernameKey]
	passwordBytes, hasPassword := secret.Data[PasswordKey]
	tokenBytes, hasToken := secret.Data[TokenKey]

	if hasToken {
		// Token-based auth: use empty username or a placeholder
		return "", string(tokenBytes), nil
	}

	if hasUsername && hasPassword {
		return string(usernameBytes), string(passwordBytes), nil
	}

	return "", "", fmt.Errorf("secret %s/%s does not contain valid credentials (expected username/password or token keys)", namespace, secretName)
}

// extractCredentialsFromDockerConfig parses a dockerconfigjson and extracts credentials.
func (sm *SecretManager) extractCredentialsFromDockerConfig(data []byte) (username, password string, err error) {
	var dockerConfig DockerConfigJSON
	if err := json.Unmarshal(data, &dockerConfig); err != nil {
		return "", "", fmt.Errorf("failed to parse dockerconfigjson: %w", err)
	}

	// Return credentials from the first registry entry
	for _, entry := range dockerConfig.Auths {
		if entry.Auth != "" {
			// Decode base64 auth string
			decoded, err := base64.StdEncoding.DecodeString(entry.Auth)
			if err != nil {
				return "", "", fmt.Errorf("failed to decode auth string: %w", err)
			}
			parts := strings.SplitN(string(decoded), ":", 2)
			if len(parts) == 2 {
				return parts[0], parts[1], nil
			}
		}
		if entry.Username != "" || entry.Password != "" {
			return entry.Username, entry.Password, nil
		}
	}

	return "", "", fmt.Errorf("no valid credentials found in dockerconfigjson")
}

// CreateOrUpdateDockerConfigSecret creates or updates a kubernetes.io/dockerconfigjson Secret.
func (sm *SecretManager) CreateOrUpdateDockerConfigSecret(
	ctx context.Context,
	name, namespace, registry, username, password string,
	owner client.Object,
	labels map[string]string,
) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, sm.client, secret, func() error {
		if secret.Labels == nil {
			secret.Labels = make(map[string]string)
		}
		secret.Labels[RegistrySecretLabel] = RegistrySecretLabelValue
		for k, v := range labels {
			secret.Labels[k] = v
		}

		if owner != nil {
			if err := controllerutil.SetControllerReference(owner, secret, sm.client.Scheme()); err != nil {
				return fmt.Errorf("failed to set owner reference: %w", err)
			}
		}

		secret.Type = corev1.SecretTypeDockerConfigJson

		dockerConfig := sm.createDockerConfig(registry, username, password)
		configJSON, err := json.Marshal(dockerConfig)
		if err != nil {
			return fmt.Errorf("failed to marshal docker config: %w", err)
		}

		if secret.Data == nil {
			secret.Data = make(map[string][]byte)
		}
		secret.Data[corev1.DockerConfigJsonKey] = configJSON

		return nil
	})

	return err
}

// createDockerConfig creates a DockerConfigJSON structure.
func (sm *SecretManager) createDockerConfig(registry, username, password string) DockerConfigJSON {
	// Normalize registry URL
	registryHost := sm.normalizeRegistryURL(registry)

	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))

	return DockerConfigJSON{
		Auths: map[string]DockerConfigEntry{
			registryHost: {
				Username: username,
				Password: password,
				Auth:     auth,
			},
		},
	}
}

// normalizeRegistryURL normalizes a registry URL to the format expected in docker config.
func (sm *SecretManager) normalizeRegistryURL(registry string) string {
	if registry == "" {
		return ""
	}

	// Remove scheme if present
	registry = strings.TrimPrefix(registry, "http://")
	registry = strings.TrimPrefix(registry, "https://")
	registry = strings.TrimPrefix(registry, "oci://")

	// Parse to ensure it's valid
	if !strings.Contains(registry, "://") {
		registry = "https://" + registry
	}

	parsed, err := url.Parse(registry)
	if err != nil {
		return registry
	}

	return parsed.Host
}

// GenerateSecretName generates a consistent name for a registry secret based on the registry host.
func GenerateSecretName(registry string) string {
	if registry == "" {
		return "default-registry-creds"
	}

	// Extract host from registry URL
	registry = strings.TrimPrefix(registry, "http://")
	registry = strings.TrimPrefix(registry, "https://")
	registry = strings.TrimPrefix(registry, "oci://")

	// Remove port if present
	host := strings.Split(registry, ":")[0]

	// Replace dots and slashes with dashes for valid k8s name
	host = strings.ReplaceAll(host, ".", "-")
	host = strings.ReplaceAll(host, "/", "-")
	host = strings.ToLower(host)

	return fmt.Sprintf("%s-registry-creds", host)
}

// DeleteSecret deletes a registry secret.
func (sm *SecretManager) DeleteSecret(ctx context.Context, name, namespace string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	err := sm.client.Delete(ctx, secret)
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// SecretExists checks if a secret exists.
func (sm *SecretManager) SecretExists(ctx context.Context, name, namespace string) (bool, error) {
	secret := &corev1.Secret{}
	err := sm.client.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, secret)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

