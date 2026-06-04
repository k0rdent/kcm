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

package clusterdeployment

import "time"

// ThresholdError is an error that wraps an underlying error and carries a
// threshold duration.  When returned from a resourceValidationFunc, the
// ProviderValidator will fail the test immediately if the same error keeps
// occurring beyond the specified threshold, instead of waiting for the full
// Eventually timeout.
type ThresholdError struct {
	err       error
	Threshold time.Duration
}

func (t *ThresholdError) Error() string {
	return t.err.Error()
}

func (t *ThresholdError) Unwrap() error {
	return t.err
}

// NewThresholdError creates a ThresholdError wrapping err with the given
// threshold duration.
func NewThresholdError(err error, threshold time.Duration) *ThresholdError {
	return &ThresholdError{err: err, Threshold: threshold}
}
