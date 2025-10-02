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

package helm

import (
	"fmt"
	"net/url"
)

const (
	RegistryTypeOCI     = "oci"
	RegistryTypeDefault = "default"
)

func DetermineDefaultRepositoryType(templatesRepoURL string) (string, error) {
	parsedRegistryURL, err := url.Parse(templatesRepoURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse default registry URL: %w", err)
	}

	switch parsedRegistryURL.Scheme {
	case "oci":
		return RegistryTypeOCI, nil
	case "http", "https":
		return RegistryTypeDefault, nil
	default:
		return "", fmt.Errorf("invalid default registry URL scheme: %s must be 'oci://', 'http://', or 'https://'", parsedRegistryURL.Scheme)
	}
}
