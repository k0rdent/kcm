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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcmv1 "github.com/K0rdent/kcm/api/v1beta1"
	pollerutil "github.com/K0rdent/kcm/internal/util/poller"
)

// EnqueueScheduledOrIncomplete returns a [pollerutil.EnqueueFunc] that lists
// every [kcmv1.ManagementBackup] which is either a schedule or has not yet
// completed. The helper is a no-op when the [kcmv1.Management] object is
// missing or being deleted.
func EnqueueScheduledOrIncomplete(cl client.Client) pollerutil.EnqueueFunc[*kcmv1.ManagementBackup] {
	return func(ctx context.Context) ([]*kcmv1.ManagementBackup, error) {
		management := new(kcmv1.Management)
		if err := cl.Get(ctx, client.ObjectKey{Name: kcmv1.ManagementName}, management); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, nil
			}

			return nil, fmt.Errorf("failed to get Management: %w", err)
		}

		if !management.DeletionTimestamp.IsZero() {
			return nil, nil
		}

		schedules := new(kcmv1.ManagementBackupList)
		if err := cl.List(ctx, schedules, client.MatchingFields{kcmv1.ManagementBackupIndexKey: "true"}); err != nil {
			return nil, fmt.Errorf("failed to list ManagementBackups: %w", err)
		}

		out := make([]*kcmv1.ManagementBackup, 0, len(schedules.Items))
		for i := range schedules.Items {
			out = append(out, &schedules.Items[i])
		}

		return out, nil
	}
}
