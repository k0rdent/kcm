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
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ValidationResult represents the result of a registry credential validation.
type ValidationResult struct {
	Valid   bool
	Message string
	Error   error
}

// Validator handles validation of registry credentials.
type Validator struct {
	cache      map[string]*ValidationResult
	cacheMutex sync.RWMutex
	cacheTTL   time.Duration
}

// NewValidator creates a new Validator instance.
func NewValidator() *Validator {
	return &Validator{
		cache:    make(map[string]*ValidationResult),
		cacheTTL: 5 * time.Minute,
	}
}

// ValidateCredentials validates registry credentials by attempting to authenticate.
func (v *Validator) ValidateCredentials(ctx context.Context, registry, username, password string, insecure bool) ValidationResult {
	cacheKey := fmt.Sprintf("%s:%s", registry, username)

	// Check cache first
	v.cacheMutex.RLock()
	if cached, ok := v.cache[cacheKey]; ok {
		v.cacheMutex.RUnlock()
		return *cached
	}
	v.cacheMutex.RUnlock()

	// Perform validation
	result := v.validateCredentials(ctx, registry, username, password, insecure)

	// Cache the result
	v.cacheMutex.Lock()
	v.cache[cacheKey] = &result
	v.cacheMutex.Unlock()

	// Schedule cache cleanup
	go func() {
		time.Sleep(v.cacheTTL)
		v.cacheMutex.Lock()
		delete(v.cache, cacheKey)
		v.cacheMutex.Unlock()
	}()

	return result
}

// validateCredentials performs the actual validation.
func (v *Validator) validateCredentials(ctx context.Context, registry, username, password string, insecure bool) ValidationResult {
	if registry == "" {
		return ValidationResult{
			Valid:   false,
			Message: "registry URL is empty",
			Error:   fmt.Errorf("registry URL is empty"),
		}
	}

	// Normalize registry URL
	registryURL := normalizeRegistryURLForValidation(registry)

	// Try to access the registry v2 API
	// This is a standard endpoint for Docker registries
	apiURL := fmt.Sprintf("%s/v2/", registryURL)

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: insecure, //nolint:gosec // This is controlled by user configuration
			},
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return ValidationResult{
			Valid:   false,
			Message: fmt.Sprintf("failed to create request: %v", err),
			Error:   err,
		}
	}

	// Add basic auth if credentials provided
	if username != "" || password != "" {
		req.SetBasicAuth(username, password)
	}

	resp, err := client.Do(req)
	if err != nil {
		return ValidationResult{
			Valid:   false,
			Message: fmt.Sprintf("failed to connect to registry: %v", err),
			Error:   err,
		}
	}
	defer resp.Body.Close()

	// Check response status
	// 200 = authenticated successfully
	// 401 = authentication required (credentials invalid or missing)
	// 404 = not a valid registry endpoint
	switch resp.StatusCode {
	case http.StatusOK:
		return ValidationResult{
			Valid:   true,
			Message: "registry credentials validated successfully",
		}
	case http.StatusUnauthorized:
		return ValidationResult{
			Valid:   false,
			Message: "authentication failed: invalid credentials",
			Error:   fmt.Errorf("authentication failed with status 401"),
		}
	case http.StatusNotFound:
		return ValidationResult{
			Valid:   false,
			Message: "registry endpoint not found: ensure the registry URL is correct",
			Error:   fmt.Errorf("registry endpoint returned 404"),
		}
	default:
		return ValidationResult{
			Valid:   false,
			Message: fmt.Sprintf("unexpected response from registry: %d %s", resp.StatusCode, resp.Status),
			Error:   fmt.Errorf("unexpected status code: %d", resp.StatusCode),
		}
	}
}

// normalizeRegistryURLForValidation normalizes a registry URL for validation purposes.
func normalizeRegistryURLForValidation(registry string) string {
	// Remove oci:// prefix as it's not an HTTP scheme
	registry = strings.TrimPrefix(registry, "oci://")

	// Add https:// if no scheme is present
	if !strings.HasPrefix(registry, "http://") && !strings.HasPrefix(registry, "https://") {
		registry = "https://" + registry
	}

	// Remove trailing slashes
	registry = strings.TrimSuffix(registry, "/")

	return registry
}

// ClearCache clears the validation cache.
func (v *Validator) ClearCache() {
	v.cacheMutex.Lock()
	defer v.cacheMutex.Unlock()
	v.cache = make(map[string]*ValidationResult)
}

