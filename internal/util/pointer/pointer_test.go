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

package pointer

import "testing"

func TestDeref(t *testing.T) {
	t.Run("non-nil pointer returns dereferenced value", func(t *testing.T) {
		v := 42
		if got := Deref(&v, 0); got != 42 {
			t.Errorf("Deref() = %d, want 42", got)
		}
	})

	t.Run("nil pointer returns default", func(t *testing.T) {
		var v *int
		if got := Deref(v, 7); got != 7 {
			t.Errorf("Deref() = %d, want 7", got)
		}
	})

	t.Run("works with non-primitive types", func(t *testing.T) {
		type S struct{ Name string }
		def := S{Name: "default"}
		if got := Deref[S](nil, def); got != def {
			t.Errorf("Deref() = %+v, want %+v", got, def)
		}

		s := S{Name: "actual"}
		if got := Deref(&s, def); got != s {
			t.Errorf("Deref() = %+v, want %+v", got, s)
		}
	})
}
