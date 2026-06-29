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

package backup_test

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/K0rdent/kcm/internal/controller/backup"
)

func enqueueTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := kcmv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

func TestEnqueueScheduledOrIncomplete(t *testing.T) {
	t.Parallel()

	scheme := enqueueTestScheme(t)
	management := func() *kcmv1.Management {
		return &kcmv1.Management{ObjectMeta: metav1.ObjectMeta{Name: kcmv1.ManagementName}}
	}

	tests := map[string]struct {
		builder   func(t *testing.T) client.Client
		wantNames []string
	}{
		"management not found": {
			builder: func(*testing.T) client.Client {
				return clientfake.NewClientBuilder().WithScheme(scheme).Build()
			},
		},
		"management being deleted": {
			builder: func(*testing.T) client.Client {
				return clientfake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(&kcmv1.Management{
						ObjectMeta: metav1.ObjectMeta{
							Name:              kcmv1.ManagementName,
							Finalizers:        []string{"foo-finalizer"},
							DeletionTimestamp: &metav1.Time{Time: time.Now()},
						},
					}).
					Build()
			},
		},
		"no backups": {
			builder: func(*testing.T) client.Client {
				return clientfake.NewClientBuilder().
					WithScheme(scheme).
					WithIndex(&kcmv1.ManagementBackup{}, kcmv1.ManagementBackupIndexKey, kcmv1.ExtractScheduledOrIncompleteBackups).
					WithObjects(management()).
					Build()
			},
		},
		"backups found": {
			builder: func(*testing.T) client.Client {
				return clientfake.NewClientBuilder().
					WithScheme(scheme).
					WithIndex(&kcmv1.ManagementBackup{}, kcmv1.ManagementBackupIndexKey, kcmv1.ExtractScheduledOrIncompleteBackups).
					WithObjects(
						management(),
						&kcmv1.ManagementBackup{ObjectMeta: metav1.ObjectMeta{Name: "test-backup"}},
					).
					Build()
			},
			wantNames: []string{"test-backup"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cl := tc.builder(t)
			got, err := backup.EnqueueScheduledOrIncomplete(cl)(t.Context())
			if err != nil {
				t.Fatalf("EnqueueScheduledOrIncomplete: unexpected error %v", err)
			}
			if len(got) != len(tc.wantNames) {
				t.Fatalf("got %d items, want %d (%v)", len(got), len(tc.wantNames), tc.wantNames)
			}
			for i, want := range tc.wantNames {
				if got[i].Name != want {
					t.Fatalf("got[%d].Name = %q, want %q", i, got[i].Name, want)
				}
			}
		})
	}
}
