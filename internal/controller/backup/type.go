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

package backup

import (
	"context"
	"errors"
	"fmt"

	velerov1api "github.com/zerospiel/velero/pkg/apis/velero/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hmcv1alpha1 "github.com/K0rdent/kcm/api/v1alpha1"
)

// Typ indicates type of a ManagementBackup object.
type Typ uint

const (
	// TypeNone indicates unknown type.
	TypeNone Typ = iota
	// TypeSchedule indicates Schedule type.
	TypeSchedule
	// TypeSchedule indicates Backup oneshot type.
	TypeBackup
)

// GetType returns type of the ManagementBackup, returns TypeNone if undefined.
func GetType(instance *hmcv1alpha1.ManagementBackup) Typ {
	if instance.Status.Reference == nil {
		return TypeNone
	}

	gv := velerov1api.SchemeGroupVersion
	switch instance.Status.Reference.GroupVersionKind() {
	case gv.WithKind("Schedule"):
		return TypeSchedule
	case gv.WithKind("Backup"):
		return TypeBackup
	default:
		return TypeNone
	}
}

// ErrNoManagementExists is a sentinel error indicating no Management object exists.
var ErrNoManagementExists = errors.New("no Management object exists")

// GetManagement fetches a Management object.
func (c *Config) GetManagement(ctx context.Context) (*hmcv1alpha1.Management, error) {
	mgmts := new(hmcv1alpha1.ManagementList)
	if err := c.cl.List(ctx, mgmts, client.Limit(1)); err != nil {
		return nil, fmt.Errorf("failed to list Management: %w", err)
	}

	if len(mgmts.Items) == 0 {
		return nil, ErrNoManagementExists
	}

	return &mgmts.Items[0], nil
}
