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

package projectroot

import (
	"path/filepath"
	"runtime"
)

// WARNING: This path resolution is dependent on the source file's location
// in the project structure. Moving this file to a different directory will
// change the resolved path. When refactoring or restructuring the project,
// ensure to update this path resolution accordingly.
var (
	_, b, _, _ = runtime.Caller(0)

	// Path is root folder of this project.
	Path = filepath.Join(filepath.Dir(b), "../..")
)
