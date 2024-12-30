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

package webhook

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hmcv1alpha1 "github.com/K0rdent/kcm/api/v1alpha1"
	"github.com/K0rdent/kcm/test/objects/management"
	"github.com/K0rdent/kcm/test/scheme"
)

func TestManagementBackup_validateBackupEnabled(t *testing.T) {
	g := NewWithT(t)

	tests := []struct {
		name            string
		existingObjects []runtime.Object
		err             string
	}{
		{
			name:            "should fail if > 1 Management",
			existingObjects: []runtime.Object{management.NewManagement(), management.NewManagement(management.WithName("second"))},
			err:             "failed to get Management: expected 1 Management object, got 2",
		},
		{
			name: "should fail if no Management",
			err:  "failed to get Management: " + errManagementIsNotFound.Error(),
		},
		{
			name:            "should fail if backup is disabled",
			existingObjects: []runtime.Object{management.NewManagement()},
			err:             "management backup is disabled, create of ManagementBackup objects disabled",
		},
		{
			name:            "should succeed if backup is enabled",
			existingObjects: []runtime.Object{management.NewManagement(management.WithBackup(hmcv1alpha1.Backup{Enabled: true}))},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(_ *testing.T) {
			c := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(tt.existingObjects...).Build()
			validator := &ManagementBackupValidator{Client: c}

			ctx := context.Background()

			_, err := validator.validateBackupEnabled(ctx)
			if tt.err != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err).To(MatchError(tt.err))
			} else {
				g.Expect(err).To(Succeed())
			}
		})
	}
}
