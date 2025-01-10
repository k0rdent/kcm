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
	"fmt"

	velerov1api "github.com/zerospiel/velero/pkg/apis/velero/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1alpha1"
)

type Typ uint

const (
	TypeNone Typ = iota
	TypeSchedule
	TypeBackup
)

func (c *Config) GetBackupType(ctx context.Context, instance *kcmv1.Backup, reqName string) (Typ, error) {
	if instance.Status.Reference != nil {
		gv := velerov1api.SchemeGroupVersion
		switch instance.Status.Reference.GroupVersionKind() {
		case gv.WithKind("Schedule"):
			return TypeSchedule, nil
		case gv.WithKind("Backup"):
			return TypeBackup, nil
		default:
			return TypeNone, fmt.Errorf("unexpected kind %s in the backup reference", instance.Status.Reference.Kind)
		}
	}

	mgmts := new(kcmv1.ManagementList)
	if err := c.cl.List(ctx, mgmts, client.Limit(1)); err != nil {
		return TypeNone, fmt.Errorf("failed to list Management: %w", err)
	}

	if len(mgmts.Items) == 0 { // nothing to do in such case for both scheduled/non-scheduled backups
		return TypeNone, nil
	}

	if reqName == mgmts.Items[0].Name { // mgmt name == scheduled-backup
		return TypeSchedule, nil
	}

	return TypeBackup, nil
}
