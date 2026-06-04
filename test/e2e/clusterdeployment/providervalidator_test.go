// Copyright 2026
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package clusterdeployment

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onsi/gomega"
)

func Test_failFastIfStuck(t *testing.T) {
	t.Parallel()

	now := time.Unix(0, 0)
	validator := &ProviderValidator{now: func() time.Time { return now }}

	other := errors.New("transient")
	if got := validator.failFastIfStuck(other); got != other {
		t.Fatalf("unrelated error must pass through; got %v", got)
	}
	if !validator.stuckSince.IsZero() {
		t.Fatal("unrelated error must not arm the timer")
	}

	stuck := fmt.Errorf("wrap: %w", ErrClusterNotDeleting)
	if got := validator.failFastIfStuck(stuck); got != stuck {
		t.Fatalf("first hit must return original; got %v", got)
	}

	now = now.Add(deletionStuckGrace - time.Second)
	if got := validator.failFastIfStuck(stuck); got != stuck {
		t.Fatalf("within grace must return original; got %v", got)
	}

	now = now.Add(time.Second)
	got := validator.failFastIfStuck(stuck)
	if _, ok := errors.AsType[gomega.PollingSignalError](got); !ok {
		t.Fatalf("at threshold must return StopTrying; got %T: %v", got, got)
	}
	if !errors.Is(got, ErrClusterNotDeleting) {
		t.Fatalf("StopTrying must wrap the sentinel; got %v", got)
	}
}
