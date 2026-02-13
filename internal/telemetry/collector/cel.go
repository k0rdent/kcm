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
	"fmt"
	"reflect"

	"github.com/google/cel-go/cel"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var errNothingToExtract = errors.New("nothing to extract with CEL")

// CELExtractor compiles a CEL program once and runs it against a list of objects.
type CELExtractor struct {
	program cel.Program
}

func newCELExtractor(expression string) (*CELExtractor, error) {
	if expression == "" {
		return nil, errNothingToExtract
	}

	env, err := cel.NewEnv(cel.StdLib(), cel.Variable("items", cel.ListType(cel.AnyType)))
	if err != nil {
		return nil, fmt.Errorf("creating CEL env: %w", err)
	}

	ast, issues := env.Compile(expression)
	if issues.Err() != nil {
		return nil, fmt.Errorf("compiling CEL expression: %w", issues.Err())
	}

	prg, err := env.Program(ast, cel.EvalOptions(cel.OptOptimize))
	if err != nil {
		return nil, fmt.Errorf("creating CEL program: %w", err)
	}

	return &CELExtractor{program: prg}, nil
}

// extract runs the CEL expression against the list of unstructured objects.
func (c *CELExtractor) extract(items []unstructured.Unstructured) (map[string]any, error) {
	if c == nil || len(items) == 0 {
		return nil, errNothingToExtract
	}

	inputList := make([]map[string]any, len(items))
	for i, u := range items {
		inputList[i] = u.UnstructuredContent()
	}

	out, _, err := c.program.Eval(map[string]any{
		"items": inputList,
	})
	if err != nil {
		return nil, fmt.Errorf("evaluating CEL against given items: %w", err)
	}

	val, err := out.ConvertToNative(reflect.TypeFor[map[string]any]())
	if err != nil {
		return nil, fmt.Errorf("converting CEL output to native a struct: %w", err)
	}

	// sanity check, we could've been just use "github.com/google/cel-go/common/types/ref".Val.Value() method
	if m, ok := val.(map[string]any); ok {
		return m, nil
	}

	return nil, fmt.Errorf("unexpected output type %T", val)
}
