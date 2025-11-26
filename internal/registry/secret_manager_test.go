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
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	schemeutil "github.com/K0rdent/kcm/internal/util/scheme"
)

func TestSecretManager_GetCredentialsFromSpec(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = kcmv1.AddToScheme(scheme)

	tests := []struct {
		name        string
		credentials *kcmv1.RegistryCredentials
		secret      *corev1.Secret
		wantUser    string
		wantPass    string
		wantErr     bool
	}{
		{
			name: "inline credentials",
			credentials: &kcmv1.RegistryCredentials{
				Username: "testuser",
				Password: "testpass",
			},
			wantUser: "testuser",
			wantPass: "testpass",
			wantErr:  false,
		},
		{
			name: "secret reference with username and password",
			credentials: &kcmv1.RegistryCredentials{
				SecretRef: &corev1.LocalObjectReference{
					Name: "test-secret",
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"username": []byte("secretuser"),
					"password": []byte("secretpass"),
				},
			},
			wantUser: "secretuser",
			wantPass: "secretpass",
			wantErr:  false,
		},
		{
			name: "secret reference with token",
			credentials: &kcmv1.RegistryCredentials{
				SecretRef: &corev1.LocalObjectReference{
					Name: "token-secret",
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "token-secret",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"token": []byte("mytoken123"),
				},
			},
			wantUser: "",
			wantPass: "mytoken123",
			wantErr:  false,
		},
		{
			name:        "nil credentials",
			credentials: nil,
			wantErr:     true,
		},
		{
			name: "no credentials provided",
			credentials: &kcmv1.RegistryCredentials{},
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objects []client.Object
			if tt.secret != nil {
				objects = append(objects, tt.secret)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()

			sm := NewSecretManager(fakeClient)
			user, pass, err := sm.GetCredentialsFromSpec(context.Background(), tt.credentials, "default")

			if (err != nil) != tt.wantErr {
				t.Errorf("GetCredentialsFromSpec() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if user != tt.wantUser {
					t.Errorf("GetCredentialsFromSpec() username = %v, want %v", user, tt.wantUser)
				}
				if pass != tt.wantPass {
					t.Errorf("GetCredentialsFromSpec() password = %v, want %v", pass, tt.wantPass)
				}
			}
		})
	}
}

func TestSecretManager_CreateOrUpdateDockerConfigSecret(t *testing.T) {
	scheme := schemeutil.MustGetManagementScheme()

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	sm := NewSecretManager(fakeClient)

	ctx := context.Background()
	name := "test-registry-secret"
	namespace := "default"
	registry := "registry.example.com"
	username := "testuser"
	password := "testpass"

	err := sm.CreateOrUpdateDockerConfigSecret(ctx, name, namespace, registry, username, password, nil, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdateDockerConfigSecret() error = %v", err)
	}

	// Verify secret was created
	secret := &corev1.Secret{}
	err = fakeClient.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, secret)
	if err != nil {
		t.Fatalf("Failed to get created secret: %v", err)
	}

	// Verify secret type
	if secret.Type != corev1.SecretTypeDockerConfigJson {
		t.Errorf("Secret type = %v, want %v", secret.Type, corev1.SecretTypeDockerConfigJson)
	}

	// Verify docker config json
	dockerConfigJSON, ok := secret.Data[corev1.DockerConfigJsonKey]
	if !ok {
		t.Fatal("Secret does not contain .dockerconfigjson key")
	}

	var dockerConfig DockerConfigJSON
	err = json.Unmarshal(dockerConfigJSON, &dockerConfig)
	if err != nil {
		t.Fatalf("Failed to unmarshal docker config: %v", err)
	}

	// Verify registry entry exists
	entry, ok := dockerConfig.Auths["registry.example.com"]
	if !ok {
		t.Fatal("Docker config does not contain registry entry")
	}

	// Verify credentials
	if entry.Username != username {
		t.Errorf("Username = %v, want %v", entry.Username, username)
	}
	if entry.Password != password {
		t.Errorf("Password = %v, want %v", entry.Password, password)
	}

	// Verify auth field
	expectedAuth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	if entry.Auth != expectedAuth {
		t.Errorf("Auth = %v, want %v", entry.Auth, expectedAuth)
	}

	// Verify labels
	if secret.Labels[RegistrySecretLabel] != RegistrySecretLabelValue {
		t.Errorf("Registry secret label not set correctly")
	}
}

func TestGenerateSecretName(t *testing.T) {
	tests := []struct {
		name     string
		registry string
		want     string
	}{
		{
			name:     "simple registry",
			registry: "registry.example.com",
			want:     "registry-example-com-registry-creds",
		},
		{
			name:     "registry with port",
			registry: "registry.example.com:5000",
			want:     "registry-example-com-registry-creds",
		},
		{
			name:     "registry with https prefix",
			registry: "https://registry.example.com",
			want:     "registry-example-com-registry-creds",
		},
		{
			name:     "registry with oci prefix",
			registry: "oci://registry.example.com",
			want:     "registry-example-com-registry-creds",
		},
		{
			name:     "empty registry",
			registry: "",
			want:     "default-registry-creds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateSecretName(tt.registry)
			if got != tt.want {
				t.Errorf("GenerateSecretName() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSecretManager_NormalizeRegistryURL(t *testing.T) {
	sm := &SecretManager{}

	tests := []struct {
		name     string
		registry string
		want     string
	}{
		{
			name:     "simple host",
			registry: "registry.example.com",
			want:     "registry.example.com",
		},
		{
			name:     "with https prefix",
			registry: "https://registry.example.com",
			want:     "registry.example.com",
		},
		{
			name:     "with http prefix",
			registry: "http://registry.example.com",
			want:     "registry.example.com",
		},
		{
			name:     "with oci prefix",
			registry: "oci://registry.example.com",
			want:     "registry.example.com",
		},
		{
			name:     "with port",
			registry: "registry.example.com:5000",
			want:     "registry.example.com:5000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sm.normalizeRegistryURL(tt.registry)
			if got != tt.want {
				t.Errorf("normalizeRegistryURL() = %v, want %v", got, tt.want)
			}
		})
	}
}

