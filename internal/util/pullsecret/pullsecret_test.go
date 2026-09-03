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

package pullsecret

import (
	"encoding/base64"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func dockerConfigSecret(t *testing.T, json string) *corev1.Secret {
	t.Helper()
	return &corev1.Secret{
		Type: "kubernetes.io/dockerconfigjson",
		Data: map[string][]byte{".dockerconfigjson": []byte(json)},
	}
}

func TestGetRegistryCredsFromPullSecret(t *testing.T) {
	t.Run("wrong secret type returns error", func(t *testing.T) {
		secret := &corev1.Secret{Type: "Opaque"}

		_, _, err := GetRegistryCredsFromPullSecret(secret, "registry.example.com/repo")
		if err == nil || !strings.Contains(err.Error(), "wrong type for imagePullSecret") {
			t.Fatalf("err = %v, want wrong type error", err)
		}
	})

	t.Run("missing .dockerconfigjson key returns error", func(t *testing.T) {
		secret := &corev1.Secret{Type: "kubernetes.io/dockerconfigjson", Data: map[string][]byte{}}

		_, _, err := GetRegistryCredsFromPullSecret(secret, "registry.example.com/repo")
		if err == nil || !strings.Contains(err.Error(), "unable to get .dockerconfigjson") {
			t.Fatalf("err = %v, want missing key error", err)
		}
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		secret := dockerConfigSecret(t, "{not-json")

		_, _, err := GetRegistryCredsFromPullSecret(secret, "registry.example.com/repo")
		if err == nil || !strings.Contains(err.Error(), "failed to unmarshal dockerconfig") {
			t.Fatalf("err = %v, want unmarshal error", err)
		}
	})

	t.Run("no auth entry for the registry host returns error", func(t *testing.T) {
		secret := dockerConfigSecret(t, `{"auths":{"other.example.com":{"username":"u","password":"p"}}}`)

		_, _, err := GetRegistryCredsFromPullSecret(secret, "registry.example.com/repo")
		if err == nil || !strings.Contains(err.Error(), "failed to extract auth config") {
			t.Fatalf("err = %v, want extract auth error", err)
		}
	})

	t.Run("invalid base64 in auth field returns error", func(t *testing.T) {
		secret := dockerConfigSecret(t, `{"auths":{"registry.example.com":{"auth":"not-valid-base64!!"}}}`)

		_, _, err := GetRegistryCredsFromPullSecret(secret, "registry.example.com/repo")
		if err == nil || !strings.Contains(err.Error(), "unable to decode auth") {
			t.Fatalf("err = %v, want decode error", err)
		}
	})

	t.Run("auth field missing the colon delimiter returns error", func(t *testing.T) {
		encoded := base64.StdEncoding.EncodeToString([]byte("no-colon-here"))
		secret := dockerConfigSecret(t, `{"auths":{"registry.example.com":{"auth":"`+encoded+`"}}}`)

		_, _, err := GetRegistryCredsFromPullSecret(secret, "registry.example.com/repo")
		if err == nil || !strings.Contains(err.Error(), "delimeted auth value") {
			t.Fatalf("err = %v, want delimiter error", err)
		}
	})

	t.Run("valid base64 auth field is decoded into username and password", func(t *testing.T) {
		encoded := base64.StdEncoding.EncodeToString([]byte("myuser:mypass"))
		secret := dockerConfigSecret(t, `{"auths":{"registry.example.com":{"auth":"`+encoded+`"}}}`)

		user, pass, err := GetRegistryCredsFromPullSecret(secret, "registry.example.com/repo/image")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if user != "myuser" || pass != "mypass" {
			t.Errorf("got (%q, %q), want (myuser, mypass)", user, pass)
		}
	})

	t.Run("falls back to username/password fields when auth is empty", func(t *testing.T) {
		secret := dockerConfigSecret(t, `{"auths":{"registry.example.com":{"username":"u2","password":"p2"}}}`)

		user, pass, err := GetRegistryCredsFromPullSecret(secret, "registry.example.com")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if user != "u2" || pass != "p2" {
			t.Errorf("got (%q, %q), want (u2, p2)", user, pass)
		}
	})

	t.Run("no usable auth parameters returns error", func(t *testing.T) {
		secret := dockerConfigSecret(t, `{"auths":{"registry.example.com":{}}}`)

		_, _, err := GetRegistryCredsFromPullSecret(secret, "registry.example.com")
		if err == nil || !strings.Contains(err.Error(), "unable to identify auth parameters") {
			t.Fatalf("err = %v, want identify auth error", err)
		}
	})

	t.Run("only username set (no password) returns error", func(t *testing.T) {
		secret := dockerConfigSecret(t, `{"auths":{"registry.example.com":{"username":"u"}}}`)

		_, _, err := GetRegistryCredsFromPullSecret(secret, "registry.example.com")
		if err == nil || !strings.Contains(err.Error(), "unable to identify auth parameters") {
			t.Fatalf("err = %v, want identify auth error", err)
		}
	})
}
