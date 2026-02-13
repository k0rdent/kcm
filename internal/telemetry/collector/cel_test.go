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

package collector

import (
	"errors"
	"maps"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestCEL_newCELExtractor(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		wantErr    bool
		errCheck   func(error) bool
	}{
		{
			name:       "Empty expression",
			expression: "",
			wantErr:    true,
			errCheck:   func(err error) bool { return errors.Is(err, errNothingToExtract) },
		},
		{
			name:       "Invalid syntax",
			expression: "this is not cel code",
			wantErr:    true,
		},
		{
			name:       "Valid simple expression",
			expression: "{'count': items.size()}",
			wantErr:    false,
		},
		{
			name:       "Valid complex expression with standard macros",
			expression: "items.filter(i, i.metadata.name == 'foo').size() > 0 ? {'found': true} : {}",
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newCELExtractor(tt.expression)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errCheck != nil {
					require.True(t, tt.errCheck(err), "Error did not match expected value")
				}
				require.Nil(t, got)
			} else {
				require.NoError(t, err)
				require.NotNil(t, got)
			}
		})
	}
}

func TestCEL_extract(t *testing.T) {
	obj := func(name string, fields map[string]any) unstructured.Unstructured {
		u := unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "Pod",
				"metadata": map[string]any{
					"name": name,
				},
			},
		}
		maps.Copy(u.Object, fields)
		return u
	}

	tests := []struct {
		name       string
		expression string
		items      []unstructured.Unstructured
		want       map[string]any
		wantErr    bool
	}{
		{
			name:       "Nil extractor input",
			expression: "{'a': 1}", // valid expression but nil items passed manually
			items:      nil,
			wantErr:    true,
		},
		{
			name:       "Count items",
			expression: "{'count': items.size()}",
			items: []unstructured.Unstructured{
				obj("pod-1", nil),
				obj("pod-2", nil),
			},
			want: map[string]any{"count": int64(2)},
		},
		{
			name:       "Condition extract specific field if exists",
			expression: "items.exists(i, i.metadata.name == 'special') ? {'found': 'yes'} : {'found': 'no'}",
			items: []unstructured.Unstructured{
				obj("normal", nil),
				obj("special", nil),
			},
			want: map[string]any{"found": "yes"},
		},
		{
			name:       "Condition fallback when missing",
			expression: "items.exists(i, i.metadata.name == 'special') ? {'found': 'yes'} : {'found': 'no'}",
			items: []unstructured.Unstructured{
				obj("normal", nil),
			},
			want: map[string]any{"found": "no"},
		},
		{
			name: "Complex expression positive",
			expression: `items.filter(i, !has(i.status.expired) || !i.status.expired).size() > 0 ? 
                {"client_id": 
                  items.filter(c, !has(c.status.expired) || !c.status.expired)
                       .filter(c, items.filter(o, !has(o.status.expired) || !o.status.expired)
                                       .all(o, c.status.issuedAt >= o.status.issuedAt)
                       )[0].status.subject
                } 
                : {}`,
			items: []unstructured.Unstructured{
				// old valid license
				obj("license-old", map[string]any{
					"status": map[string]any{
						"expired":  false,
						"issuedAt": "2023-01-01T00:00:00Z",
						"subject":  "OLD_ID",
					},
				}),
				// newer but expired license (should be ignored)
				obj("license-expired", map[string]any{
					"status": map[string]any{
						"expired":  true,
						"issuedAt": "2025-01-01T00:00:00Z",
						"subject":  "EXPIRED_ID",
					},
				}),
				// newest valid license (should be picked)
				obj("license-new", map[string]any{
					"status": map[string]any{
						"issuedAt": "2024-01-01T00:00:00Z",
						"subject":  "TARGET_ID",
					},
				}),
			},
			want: map[string]any{"client_id": "TARGET_ID"},
		},
		{
			name:       "No valid licenses",
			expression: `items.filter(i, !has(i.status.expired) || !i.status.expired).size() > 0 ? {'client_id': 'found'} : {}`,
			items: []unstructured.Unstructured{
				obj("license-expired-1", map[string]any{
					"status": map[string]any{"expired": true},
				}),
			},
			want: map[string]any{},
		},
		{
			name:       "Error: Output is not a map",
			expression: "items.size()",
			items:      []unstructured.Unstructured{obj("p1", nil)},
			wantErr:    true,
		},
		{
			name:       "Error: Runtime error (accessing missing field without check)",
			expression: "{'val': items[0].status.missingField}",
			items:      []unstructured.Unstructured{obj("p1", nil)},
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, err := newCELExtractor(tt.expression)
			if tt.items == nil {
				// special case where creation is valid but usage is invalid
				e = &CELExtractor{}
				_, err := e.extract(tt.items)
				require.ErrorIs(t, err, errNothingToExtract)
				return
			}

			require.NoError(t, err, "Failed to compile expression")

			got, err := e.extract(tt.items)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, got)
			}
		})
	}
}
