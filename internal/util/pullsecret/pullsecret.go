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

package pullsecret

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

type DockerConfigFile struct {
	AuthConfigs map[string]DockerAuthConfig `json:"auths"`
}

type DockerAuthConfig struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Auth     string `json:"auth,omitempty"`

	ServerAddress string `json:"serveraddress,omitempty"`

	// IdentityToken is used to authenticate the user and get
	// an access token for the registry.
	IdentityToken string `json:"identitytoken,omitempty"`

	// RegistryToken is a bearer token to be sent to a registry
	RegistryToken string `json:"registrytoken,omitempty"`
}

func GetRegistryCredsFromPullSecret(secret *corev1.Secret, registry string) (username, password string, err error) {
	if secret.Type != "kubernetes.io/dockerconfigjson" {
		return "", "",
			fmt.Errorf("wrong type for imagePullSecret %q, expected kubernetes.io/dockerconfigjson", secret.Type)
	}

	data := secret.Data
	configstr, ok := data[".dockerconfigjson"]
	if !ok {
		return "", "",
			errors.New("unable to get .dockerconfigjson from imagePullSecret data")
	}

	var config DockerConfigFile
	if err := json.Unmarshal(configstr, &config); err != nil {
		return "", "",
			fmt.Errorf("failed to unmarshal dockerconfig: %w", err)
	}

	auths := config.AuthConfigs

	registryHost := strings.Split(registry, "/")[0]

	authConfig, ok := auths[registryHost]
	if !ok {
		return "", "",
			fmt.Errorf("failed to extract auth config for the registry host %q", registryHost)
	}

	if authConfig.Auth != "" {
		auth, err := base64.StdEncoding.DecodeString(authConfig.Auth)
		if err != nil {
			return "", "",
				fmt.Errorf("unable to decode auth: %w", err)
		}

		username, password, found := strings.Cut(string(auth), ":")
		if !found {
			return "", "",
				errors.New("incorrect \":\" delimeted auth value")
		}

		return username, password, nil
	}

	if authConfig.Username != "" && authConfig.Password != "" {
		return authConfig.Username, authConfig.Password, nil
	}

	return "", "", errors.New("unable to identify auth parameters in the dockerconfig")
}
