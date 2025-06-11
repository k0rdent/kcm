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

package sveltos

import (
	"errors"
	"fmt"
	"testing"

	fluxcdmeta "github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/stretchr/testify/require"
)

func Test_priorityToTier(t *testing.T) {
	for _, tc := range []struct {
		err      error
		priority int32
		tier     int32
	}{
		{priority: 1, tier: 2147483646},
		{priority: 2147483646, tier: 1},
		{priority: 0, err: errors.New("priority has to be between 1 and 2147483646")},
		{priority: 2147483647, err: errors.New("priority has to be between 1 and 2147483646")},
	} {
		t.Run(fmt.Sprintf("priority=%d", tc.priority), func(t *testing.T) {
			tier, err := priorityToTier(tc.priority)
			if tc.err != nil {
				require.ErrorContains(t, err, tc.err.Error())
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.tier, tier)
		})
	}
}

func Test_nonEmptyRegistryCredentialsConfig(t *testing.T) {
	testNamespace := "default"
	testSecretName := "secret"
	for _, tc := range []struct {
		tcName string
		repo   *sourcev1.HelmRepository
	}{
		{tcName: "with insecure registry", repo: &sourcev1.HelmRepository{Spec: sourcev1.HelmRepositorySpec{Insecure: true}}},
		{tcName: "with secret ref", repo: &sourcev1.HelmRepository{Spec: sourcev1.HelmRepositorySpec{
			SecretRef: &fluxcdmeta.LocalObjectReference{
				Name: testSecretName,
			},
		}}},
		{tcName: "with insecure registry and secret ref", repo: &sourcev1.HelmRepository{Spec: sourcev1.HelmRepositorySpec{
			Insecure: true,
			SecretRef: &fluxcdmeta.LocalObjectReference{
				Name: testSecretName,
			},
		}}},
	} {
		t.Run(tc.tcName, func(t *testing.T) {
			config := generateRegistryCredentialsConfig(testNamespace, tc.repo.Spec.Insecure, tc.repo.Spec.SecretRef)
			require.NotNil(t, config)
			require.Equal(t, tc.repo.Spec.Insecure, config.PlainHTTP)

			if tc.repo.Spec.Insecure {
				require.False(t, config.InsecureSkipTLSVerify)
			}

			if tc.repo.Spec.SecretRef != nil {
				require.Equal(t, testSecretName, config.CredentialsSecretRef.Name)
				require.Equal(t, testNamespace, config.CredentialsSecretRef.Namespace)
			} else {
				require.Nil(t, config.CredentialsSecretRef)
			}
		})
	}
}
