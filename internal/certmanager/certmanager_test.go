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

package certmanager

import (
	"context"
	"testing"

	"k8s.io/client-go/rest"
)

func TestVerifyAPI(t *testing.T) {
	t.Run("returns error when the client cannot be constructed", func(t *testing.T) {
		restcfg := &rest.Config{
			Host: "https://127.0.0.1:6443",
			TLSClientConfig: rest.TLSClientConfig{
				CAData: []byte("not-a-valid-pem-block"),
			},
		}

		err := VerifyAPI(context.Background(), restcfg, "default")
		if err == nil {
			t.Fatal("VerifyAPI() error = nil, want non-nil")
		}
	})

	t.Run("returns error when the API server is unreachable", func(t *testing.T) {
		restcfg := &rest.Config{
			Host: "http://127.0.0.1:1",
		}

		err := VerifyAPI(context.Background(), restcfg, "default")
		if err == nil {
			t.Fatal("VerifyAPI() error = nil, want non-nil")
		}
	})
}
